package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

const chatTailWindowLines = 250

// TranscriptSource describes where ChatSessionController's current items came from.
type TranscriptSource string

const (
	SourceLoading    TranscriptSource = "loading"
	SourceJSONL      TranscriptSource = "jsonl"
	SourceVTFallback TranscriptSource = "vt-fallback"
)

// Label returns a concise UI badge label for the active data source.
func (s TranscriptSource) Label() string {
	switch s {
	case SourceJSONL:
		return "JSONL"
	case SourceVTFallback:
		return "终端回退"
	default:
		return "同步中"
	}
}

// ResolvedTranscript points to a discovered remote harness JSONL log file.
type ResolvedTranscript struct {
	Path   string
	Format TranscriptFormat
	On     string
}

// TranscriptBackend abstracts read-only RPC operations needed by TranscriptResolver and ChatSessionController.
type TranscriptBackend interface {
	FindTerminal(key TermKey) (AggregatedTerminal, bool)
	FileRead(ctx context.Context, instanceID string, p FileReadParams) (FileReadResult, error)
	MachineExec(ctx context.Context, instanceID string, p ExecParams) (ExecResult, error)
	ReadTerminal(ctx context.Context, key TermKey, lines int) (ReadResult, error)
}

// appTranscriptBackend adapts *App to TranscriptBackend over its pooled framed Client connections.
type appTranscriptBackend struct {
	app *App
}

func NewAppTranscriptBackend(app *App) TranscriptBackend {
	return &appTranscriptBackend{app: app}
}

func (b *appTranscriptBackend) FindTerminal(key TermKey) (AggregatedTerminal, bool) {
	if b.app == nil {
		return AggregatedTerminal{}, false
	}
	return b.app.FindTerminal(key)
}

func (b *appTranscriptBackend) resolveClientAndOn(instanceID, requestedOn string) (*Client, string) {
	if b.app == nil {
		return nil, requestedOn
	}
	if instanceID != "" {
		if c := b.app.pool.ClientFor(instanceID); c != nil {
			return c, requestedOn
		}
		for _, it := range b.app.aggTerms.Get() {
			if it.Relayed && it.Key.InstanceID != "" {
				if c := b.app.pool.ClientFor(it.Key.InstanceID); c != nil {
					relayOn, _ := SplitMachineAddr(it.Key.Addr)
					if requestedOn == "" {
						return c, relayOn
					}
					return c, requestedOn
				}
			}
		}
	}
	return b.app.clientForKey(TermKey{InstanceID: instanceID}), requestedOn
}

func (b *appTranscriptBackend) FileRead(ctx context.Context, instanceID string, p FileReadParams) (FileReadResult, error) {
	c, effOn := b.resolveClientAndOn(instanceID, p.On)
	if c == nil {
		return FileReadResult{}, errors.New("instance offline")
	}
	p.On = effOn
	return c.FileRead(ctx, p)
}

func (b *appTranscriptBackend) MachineExec(ctx context.Context, instanceID string, p ExecParams) (ExecResult, error) {
	c, effOn := b.resolveClientAndOn(instanceID, p.On)
	if c == nil {
		return ExecResult{}, errors.New("instance offline")
	}
	p.On = effOn
	return c.MachineExec(ctx, p)
}

func (b *appTranscriptBackend) ReadTerminal(ctx context.Context, key TermKey, lines int) (ReadResult, error) {
	if b.app == nil {
		return ReadResult{}, errors.New("app unavailable")
	}
	c := b.app.clientForKey(key)
	if c == nil {
		return ReadResult{}, errors.New("instance offline")
	}
	return c.Read(ctx, key.Addr, lines)
}

type cachedTranscriptEntry struct {
	resolved ResolvedTranscript
	fgPID    uint32
}

// TranscriptResolver locates remote Claude Code, OMP/Pi, Codex, and Antigravity JSONL logs
// and caches resolved paths per TermKey so periodic tailing never spawns shell processes.
type TranscriptResolver struct {
	mu       sync.Mutex
	byKey    map[TermKey]cachedTranscriptEntry
	missAt   map[TermKey]time.Time
	homeDirs map[string]string // instanceID+"::"+on -> remote $HOME
}

func NewTranscriptResolver() *TranscriptResolver {
	return &TranscriptResolver{
		byKey:    make(map[TermKey]cachedTranscriptEntry),
		missAt:   make(map[TermKey]time.Time),
		homeDirs: make(map[string]string),
	}
}

// SetHome pre-seeds or updates the cached remote home directory for (instanceID, on).
func (r *TranscriptResolver) SetHome(instanceID, on, home string) {
	r.mu.Lock()
	r.homeDirs[instanceID+"::"+on] = strings.TrimRight(strings.TrimSpace(home), "/")
	r.mu.Unlock()
}

// Invalidate clears any cached path for key (e.g. when a file is rotated or deleted).
func (r *TranscriptResolver) Invalidate(key TermKey) {
	r.mu.Lock()
	delete(r.byKey, key)
	delete(r.missAt, key)
	r.mu.Unlock()
}

func (r *TranscriptResolver) resolveHome(ctx context.Context, backend TranscriptBackend, instanceID, on string) string {
	cacheKey := instanceID + "::" + on
	r.mu.Lock()
	if h := r.homeDirs[cacheKey]; h != "" {
		r.mu.Unlock()
		return h
	}
	r.mu.Unlock()

	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		Line:    `printf %s "$HOME"`,
		On:      on,
		Timeout: 4.0,
	})
	if err != nil {
		return ""
	}
	h := strings.TrimRight(strings.TrimSpace(er.StdoutText()), "/")
	if h != "" {
		r.SetHome(instanceID, on, h)
	}
	return h
}

var reNonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func claudeProjectSlug(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	// Claude Code replaces every non-alphanumeric character with '-' (preserving leading '-').
	var b strings.Builder
	for _, ch := range cwd {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func extractSessionIDFromTerm(t Terminal) string {
	if t.Labels != nil {
		if alma := strings.TrimSpace(t.Labels["alma.session"]); alma != "" {
			// Formats like "omp:id:01a16527-..." or "pi:id:01a1e743-..."
			parts := strings.Split(alma, ":")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}
	var allArgs []string
	if t.Foreground != nil {
		allArgs = append(allArgs, t.Foreground.Argv...)
	}
	allArgs = append(allArgs, t.Argv...)
	for i := 0; i < len(allArgs); i++ {
		tok := strings.TrimSpace(allArgs[i])
		if tok == "-r" || tok == "--resume" || tok == "resume" || tok == "--session" {
			if i+1 < len(allArgs) {
				return strings.TrimSpace(allArgs[i+1])
			}
		}
		// Handle shell command strings like `omp -r 01a16527-...`
		fields := strings.Fields(tok)
		for j := 0; j < len(fields)-1; j++ {
			if fields[j] == "-r" || fields[j] == "--resume" || fields[j] == "resume" {
				return strings.Trim(fields[j+1], `"'`)
			}
		}
	}
	return ""
}

func terminalPID(t Terminal) uint32 {
	if t.Foreground != nil && t.Foreground.PID > 0 {
		return t.Foreground.PID
	}
	if t.PID != nil {
		return *t.PID
	}
	return 0
}

// Resolve attempts to locate the native JSONL transcript for key/term.
func (r *TranscriptResolver) Resolve(ctx context.Context, key TermKey, term Terminal, backend TranscriptBackend) (ResolvedTranscript, bool) {
	kind := DetectHarnessKind(term)
	if kind == "" || kind == "other-agent" {
		return ResolvedTranscript{}, false
	}
	curPID := terminalPID(term)

	r.mu.Lock()
	if hit, ok := r.byKey[key]; ok {
		if curPID == 0 || hit.fgPID == 0 || hit.fgPID == curPID {
			r.mu.Unlock()
			return hit.resolved, true
		}
	}
	if lastMiss, ok := r.missAt[key]; ok && time.Since(lastMiss) < 4*time.Second {
		r.mu.Unlock()
		return ResolvedTranscript{}, false
	}
	r.mu.Unlock()

	on, _ := SplitMachineAddr(key.Addr)
	var resolved ResolvedTranscript
	var ok bool

	switch kind {
	case "claude":
		resolved, ok = r.resolveClaude(ctx, key.InstanceID, on, curPID, term, backend)
	case "omp", "pi":
		resolved, ok = r.resolvePiOmp(ctx, key.InstanceID, on, term, backend)
	case "codex":
		resolved, ok = r.resolveCodex(ctx, key.InstanceID, on, term, backend)
	case "antigravity":
		resolved, ok = r.resolveAntigravity(ctx, key.InstanceID, on, term, backend)
	}

	r.mu.Lock()
	if ok && resolved.Path != "" {
		r.byKey[key] = cachedTranscriptEntry{resolved: resolved, fgPID: curPID}
		delete(r.missAt, key)
	} else {
		r.missAt[key] = time.Now()
	}
	r.mu.Unlock()
	return resolved, ok
}

func (r *TranscriptResolver) fileExists(ctx context.Context, backend TranscriptBackend, instanceID, on, path string) bool {
	if path == "" {
		return false
	}
	_, err := backend.FileRead(ctx, instanceID, FileReadParams{
		Path:   path,
		Offset: 1,
		Limit:  1,
		On:     on,
	})
	if err == nil {
		return true
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && rpcErr.Kind() == "invalid_params" && strings.Contains(rpcErr.Message, "the file has 0 lines") {
		return true
	}
	return false
}

func (r *TranscriptResolver) resolveClaude(ctx context.Context, instanceID, on string, pid uint32, term Terminal, backend TranscriptBackend) (ResolvedTranscript, bool) {
	home := ""
	r.mu.Lock()
	home = r.homeDirs[instanceID+"::"+on]
	r.mu.Unlock()

	// 1. Direct PID session metadata lookup (~/.claude/sessions/<pid>.json) — zero MachineExec when home known or relative on remote!
	if pid > 0 {
		var candidates []string
		if home != "" {
			candidates = append(candidates, fmt.Sprintf("%s/.claude/sessions/%d.json", home, pid))
		}
		if on != "" {
			candidates = append(candidates, fmt.Sprintf(".claude/sessions/%d.json", pid))
		}
		if len(candidates) == 0 {
			if h := r.resolveHome(ctx, backend, instanceID, on); h != "" {
				home = h
				candidates = append(candidates, fmt.Sprintf("%s/.claude/sessions/%d.json", home, pid))
			}
		}
		for _, pidMetaPath := range candidates {
			fr, err := backend.FileRead(ctx, instanceID, FileReadParams{
				Path:   pidMetaPath,
				Offset: 1,
				Limit:  20,
				On:     on,
			})
			if err != nil {
				continue
			}
			var meta struct {
				SessionID string `json:"sessionId"`
				Cwd       string `json:"cwd"`
			}
			if json.Unmarshal([]byte(fr.Text), &meta) == nil && meta.SessionID != "" {
				cwd := nonEmpty(meta.Cwd, term.CurrentDir())
				slug := claudeProjectSlug(cwd)
				prefix := home
				if prefix == "" && strings.HasPrefix(pidMetaPath, "/") {
					if idx := strings.Index(pidMetaPath, "/.claude/"); idx > 0 {
						prefix = pidMetaPath[:idx]
					}
				}
				var jsonlPath string
				if prefix != "" {
					jsonlPath = fmt.Sprintf("%s/.claude/projects/%s/%s.jsonl", prefix, slug, meta.SessionID)
				} else {
					jsonlPath = fmt.Sprintf(".claude/projects/%s/%s.jsonl", slug, meta.SessionID)
				}
				if r.fileExists(ctx, backend, instanceID, on, jsonlPath) {
					return ResolvedTranscript{Path: jsonlPath, Format: FormatClaude, On: on}, true
				}
			}
		}
	}

	// 2. Explicit session ID (--resume / alma.session) + known cwd.
	sid := extractSessionIDFromTerm(term)
	slug := claudeProjectSlug(term.CurrentDir())
	if home == "" {
		home = r.resolveHome(ctx, backend, instanceID, on)
	}
	if sid != "" && slug != "" && home != "" {
		direct := fmt.Sprintf("%s/.claude/projects/%s/%s.jsonl", home, slug, sid)
		if r.fileExists(ctx, backend, instanceID, on, direct) {
			return ResolvedTranscript{Path: direct, Format: FormatClaude, On: on}, true
		}
	}

	// 3. Fallback single read-only MachineExec using Env vars (injection-safe).
	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		On:      on,
		Timeout: 5.0,
		Env: map[string]string{
			"HC_SID":  sid,
			"HC_SLUG": slug,
		},
		Line: `if [ -n "$HC_SID" ]; then ls -1t "$HOME/.claude/projects"/*/*"$HC_SID"*.jsonl 2>/dev/null | head -n 1; elif [ -n "$HC_SLUG" ]; then ls -1t "$HOME/.claude/projects/$HC_SLUG"/*.jsonl 2>/dev/null | head -n 1; fi`,
	})
	if err != nil {
		return ResolvedTranscript{}, false
	}
	found := firstOutputLine(er.StdoutText())
	if found != "" {
		return ResolvedTranscript{Path: found, Format: FormatClaude, On: on}, true
	}
	return ResolvedTranscript{}, false
}

func (r *TranscriptResolver) resolvePiOmp(ctx context.Context, instanceID, on string, term Terminal, backend TranscriptBackend) (ResolvedTranscript, bool) {
	sid := extractSessionIDFromTerm(term)
	if strings.HasSuffix(sid, ".jsonl") && r.fileExists(ctx, backend, instanceID, on, sid) {
		return ResolvedTranscript{Path: sid, Format: FormatPiOmp, On: on}, true
	}
	slug := strings.Trim(reNonAlnum.ReplaceAllString(term.CurrentDir(), "-"), "-")
	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		On:      on,
		Timeout: 5.0,
		Env: map[string]string{
			"HC_SID":  sid,
			"HC_SLUG": slug,
		},
		Line: `if [ -n "$HC_SID" ]; then ls -1t "$HOME/.omp/agent/sessions"/*/*"$HC_SID"*.jsonl "$HOME/.pi/agent/sessions"/*/*"$HC_SID"*.jsonl 2>/dev/null | head -n 1; elif [ -n "$HC_SLUG" ]; then ls -1t "$HOME/.omp/agent/sessions"/*"$HC_SLUG"*/*.jsonl "$HOME/.pi/agent/sessions"/*"$HC_SLUG"*/*.jsonl 2>/dev/null | head -n 1; else ls -1t "$HOME/.omp/agent/sessions"/*/*.jsonl "$HOME/.pi/agent/sessions"/*/*.jsonl 2>/dev/null | head -n 1; fi`,
	})
	if err != nil {
		return ResolvedTranscript{}, false
	}
	if found := firstOutputLine(er.StdoutText()); found != "" {
		return ResolvedTranscript{Path: found, Format: FormatPiOmp, On: on}, true
	}
	return ResolvedTranscript{}, false
}

func (r *TranscriptResolver) resolveCodex(ctx context.Context, instanceID, on string, term Terminal, backend TranscriptBackend) (ResolvedTranscript, bool) {
	sid := extractSessionIDFromTerm(term)
	cwd := term.CurrentDir()
	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		On:      on,
		Timeout: 5.0,
		Env: map[string]string{
			"HC_SID": sid,
			"HC_CWD": cwd,
		},
		Line: `if [ -n "$HC_SID" ]; then ls -1t "$HOME/.codex/sessions"/*/*/*/*"$HC_SID"*.jsonl 2>/dev/null | head -n 1; else for f in $(ls -1t "$HOME/.codex/sessions"/*/*/*/*.jsonl 2>/dev/null | head -n 15); do head -n 1 "$f" | grep -Fq "\"cwd\":\"$HC_CWD\"" && { printf "%s\n" "$f"; break; }; done; fi`,
	})
	if err != nil {
		return ResolvedTranscript{}, false
	}
	if found := firstOutputLine(er.StdoutText()); found != "" {
		return ResolvedTranscript{Path: found, Format: FormatCodex, On: on}, true
	}
	return ResolvedTranscript{}, false
}

func (r *TranscriptResolver) resolveAntigravity(ctx context.Context, instanceID, on string, term Terminal, backend TranscriptBackend) (ResolvedTranscript, bool) {
	sid := extractSessionIDFromTerm(term)
	cwd := term.CurrentDir()
	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		On:      on,
		Timeout: 5.0,
		Env: map[string]string{
			"HC_SID": sid,
			"HC_CWD": cwd,
		},
		Line: `if [ -n "$HC_SID" ] && [ -f "$HOME/.gemini/antigravity-cli/brain/$HC_SID/.system_generated/logs/transcript.jsonl" ]; then printf "%s\n" "$HOME/.gemini/antigravity-cli/brain/$HC_SID/.system_generated/logs/transcript.jsonl"; else for f in $(ls -1t "$HOME/.gemini/antigravity-cli/brain"/*/.system_generated/logs/transcript.jsonl 2>/dev/null | head -n 12); do if [ -z "$HC_CWD" ] || head -n 5 "$f" | grep -Fq "$HC_CWD"; then printf "%s\n" "$f"; break; fi; done; fi`,
	})
	if err != nil {
		return ResolvedTranscript{}, false
	}
	if found := firstOutputLine(er.StdoutText()); found != "" {
		return ResolvedTranscript{Path: found, Format: FormatAntigravity, On: on}, true
	}
	return ResolvedTranscript{}, false
}

func firstOutputLine(s string) string {
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// ChatSessionController (Root-Scope Reactive Session Transcript State)

type optimisticMsg struct {
	id   string
	text string
}

// ChatSessionController manages paginated JSONL loading, error-free incremental tailing,
// optimistic send echo deduplication, collapsible card expansion state, and VT fallback.
// All NSGo signals are bound strictly to the root app scope passed to NewChatSessionController.
type ChatSessionController struct {
	cx         *core.Scope
	key        TermKey
	backend    TranscriptBackend
	resolver   *TranscriptResolver
	vtFallback func() string

	cancel     context.CancelFunc
	refreshing atomic.Bool
	optSeq     atomic.Uint64

	mu           sync.Mutex
	closed       bool
	source       TranscriptSource
	resolvedPath string
	format       TranscriptFormat
	onMachine    string
	firstLine    int
	lastLine     int
	totalLines   int
	rawLines     []string
	parsedItems  []ChatTurnItem
	optimistic   []optimisticMsg
	mergedItems  []ChatTurnItem
	expanded     map[string]bool
	loadingOlder bool

	// Root-scope reactive signals (non-nil when cx != nil).
	rev         *core.Signal[uint64]
	searchQuery *core.Signal[string]
	scrolledUp  *core.Signal[bool]
	scrollRef   core.Ref
}

func NewChatSessionController(
	cx *core.Scope,
	key TermKey,
	backend TranscriptBackend,
	resolver *TranscriptResolver,
	vtFallback func() string,
) *ChatSessionController {
	if resolver == nil {
		resolver = NewTranscriptResolver()
	}
	c := &ChatSessionController{
		cx:         cx,
		key:        key,
		backend:    backend,
		resolver:   resolver,
		vtFallback: vtFallback,
		source:     SourceLoading,
		expanded:   make(map[string]bool),
	}
	if cx != nil {
		c.rev = core.NewSignal[uint64](cx, 0)
		c.searchQuery = core.NewSignal[string](cx, "")
		c.scrolledUp = core.NewSignal[bool](cx, false)
	}
	return c
}

// StartLoop launches the initial sync and lightweight 1.5s working-only tail ticker.
func (c *ChatSessionController) StartLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.cancel = cancel
	c.mu.Unlock()

	c.RefreshAsync()
	go func() {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if c.shouldPollWorking() {
					c.RefreshAsync()
				}
			}
		}
	}()
}

func (c *ChatSessionController) shouldPollWorking() bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	hasOpt := len(c.optimistic) > 0
	src := c.source
	c.mu.Unlock()

	if hasOpt || src == SourceLoading {
		return true
	}
	if c.backend != nil {
		if at, ok := c.backend.FindTerminal(c.key); ok {
			st := at.Terminal.StateWord()
			return st == "working" || st == "blocked"
		}
	}
	return false
}

// Close stops background polling cleanly when leaving or switching sessions.
func (c *ChatSessionController) Close() {
	c.mu.Lock()
	c.closed = true
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *ChatSessionController) bumpRev(scrollToEnd bool) {
	if c.cx != nil && c.rev != nil {
		c.cx.Post(func() {
			c.rev.Set(c.rev.Get() + 1)
			if scrollToEnd && (c.scrolledUp == nil || !c.scrolledUp.Get()) {
				c.scrollRef.ScrollToEnd(false)
			}
		})
	}
}

// ScrollToBottom scrolls the chat viewport to the latest item.
func (c *ChatSessionController) ScrollToBottom() {
	if c.scrolledUp != nil {
		c.scrolledUp.Set(false)
	}
	c.scrollRef.ScrollToEnd(true)
}

// Items returns a snapshot of the normalized, optimistic-merged conversation items.
func (c *ChatSessionController) Items() []ChatTurnItem {
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ChatTurnItem, len(c.mergedItems))
	copy(out, c.mergedItems)
	return out
}

// Source returns the current transcript source (JSONL, VT fallback, or loading).
func (c *ChatSessionController) Source() TranscriptSource {
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.source
}

// HasOlder reports whether earlier JSONL lines exist before the loaded window.
func (c *ChatSessionController) HasOlder() bool {
	return c.OlderRemaining() > 0
}

// OlderRemaining returns how many earlier JSONL lines remain unloaded above firstLine.
func (c *ChatSessionController) OlderRemaining() int {
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.source != SourceJSONL || c.firstLine <= 1 {
		return 0
	}
	return c.firstLine - 1
}

// IsLoadingOlder reports whether an older-page fetch is currently in flight.
func (c *ChatSessionController) IsLoadingOlder() bool {
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadingOlder
}

// IsExpanded reports whether a thinking/tool item is expanded.
func (c *ChatSessionController) IsExpanded(id string) bool {
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expanded[id]
}

// ToggleExpanded flips expansion state for a collapsible item.
func (c *ChatSessionController) ToggleExpanded(id string) {
	c.mu.Lock()
	c.expanded[id] = !c.expanded[id]
	c.mu.Unlock()
	c.bumpRev(false)
}

// AddOptimisticUser immediately appends a sent user message to the chat tail
// and automatically deduplicates it once the underlying transcript records it.
func (c *ChatSessionController) AddOptimisticUser(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	seq := c.optSeq.Add(1)
	c.mu.Lock()
	c.optimistic = append(c.optimistic, optimisticMsg{
		id:   fmt.Sprintf("opt-%d", seq),
		text: trimmed,
	})
	c.rebuildMergedLocked()
	c.mu.Unlock()
	c.bumpRev(true)
}

func (c *ChatSessionController) rebuildMergedLocked() {
	// Reconcile optimistic user messages against the tail of parsedItems (last 20 items).
	if len(c.optimistic) > 0 && len(c.parsedItems) > 0 {
		tailStart := len(c.parsedItems) - 20
		if tailStart < 0 {
			tailStart = 0
		}
		usedIdx := make(map[int]bool)
		var remaining []optimisticMsg
		for _, opt := range c.optimistic {
			matched := false
			for i := tailStart; i < len(c.parsedItems); i++ {
				if usedIdx[i] {
					continue
				}
				if c.parsedItems[i].Role == RoleUser && strings.TrimSpace(c.parsedItems[i].Text) == opt.text {
					usedIdx[i] = true
					matched = true
					break
				}
			}
			if !matched {
				remaining = append(remaining, opt)
			}
		}
		c.optimistic = remaining
	}

	merged := make([]ChatTurnItem, 0, len(c.parsedItems)+len(c.optimistic))
	merged = append(merged, c.parsedItems...)
	for _, opt := range c.optimistic {
		merged = append(merged, ChatTurnItem{
			ID:         opt.id,
			Role:       RoleUser,
			Text:       opt.text,
			Optimistic: true,
		})
	}
	c.mergedItems = merged
}

// RefreshAsync triggers a non-overlapping background refresh.
func (c *ChatSessionController) RefreshAsync() {
	if !c.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.refreshing.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.RefreshSync(ctx)
	}()
}

// RefreshSync performs one synchronous transcript discovery / tail step.
func (c *ChatSessionController) RefreshSync(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	hasResolved := c.resolvedPath != ""
	curPath := c.resolvedPath
	curFmt := c.format
	curOn := c.onMachine
	lastLine := c.lastLine
	c.mu.Unlock()

	if c.backend == nil {
		c.applyVTFallback()
		return nil
	}

	if !hasResolved {
		at, ok := c.backend.FindTerminal(c.key)
		if ok {
			if res, found := c.resolver.Resolve(ctx, c.key, at.Terminal, c.backend); found {
				curPath = res.Path
				curFmt = res.Format
				curOn = res.On
				hasResolved = true
			}
		}
	}

	if !hasResolved {
		c.applyVTFallback()
		return nil
	}

	// Initial tail window load vs error-free incremental tailing from max(1, lastLine).
	if lastLine <= 0 {
		return c.loadInitialJSONLWindow(ctx, curPath, curFmt, curOn)
	}
	return c.tailJSONLFromLastLine(ctx, curPath, curFmt, curOn, lastLine)
}

func (c *ChatSessionController) loadInitialJSONLWindow(ctx context.Context, path string, format TranscriptFormat, on string) error {
	probe, err := c.backend.FileRead(ctx, c.key.InstanceID, FileReadParams{
		Path:   path,
		Offset: 1,
		Limit:  1,
		On:     on,
	})
	if err != nil {
		if isEmptyFileOffsetErr(err) {
			c.mu.Lock()
			c.resolvedPath = path
			c.format = format
			c.onMachine = on
			c.source = SourceJSONL
			c.firstLine = 0
			c.lastLine = 0
			c.totalLines = 0
			c.rawLines = nil
			c.parsedItems = nil
			c.rebuildMergedLocked()
			c.mu.Unlock()
			c.bumpRev(false)
			return nil
		}
		c.resolver.Invalidate(c.key)
		c.applyVTFallback()
		return nil
	}

	total := probe.Lines
	if total <= 0 {
		c.mu.Lock()
		c.resolvedPath = path
		c.format = format
		c.onMachine = on
		c.source = SourceJSONL
		c.rebuildMergedLocked()
		c.mu.Unlock()
		c.bumpRev(false)
		return nil
	}

	start := total - chatTailWindowLines + 1
	if start < 1 {
		start = 1
	}
	win, err := c.backend.FileRead(ctx, c.key.InstanceID, FileReadParams{
		Path:   path,
		Offset: start,
		Limit:  chatTailWindowLines,
		On:     on,
	})
	if err != nil {
		c.resolver.Invalidate(c.key)
		c.applyVTFallback()
		return nil
	}

	chunkLines := splitJSONLLines(win.Text)
	parsed := ParseTranscriptLines(format, chunkLines)

	c.mu.Lock()
	c.resolvedPath = path
	c.format = format
	c.onMachine = on
	c.source = SourceJSONL
	c.firstLine = win.First
	c.lastLine = win.Last
	c.totalLines = win.Lines
	c.rawLines = chunkLines
	c.parsedItems = parsed
	c.rebuildMergedLocked()
	c.mu.Unlock()
	c.bumpRev(true)
	return nil
}

func (c *ChatSessionController) tailJSONLFromLastLine(ctx context.Context, path string, format TranscriptFormat, on string, lastLine int) error {
	// Always query from known-valid lastLine (>= 1) so EOF never triggers invalid_params!
	win, err := c.backend.FileRead(ctx, c.key.InstanceID, FileReadParams{
		Path:   path,
		Offset: lastLine,
		Limit:  201,
		On:     on,
	})
	if err != nil {
		// Guard: if offset > lines (e.g. file truncated or empty), handle gracefully without dropping loaded JSONL on normal EOF.
		if isOffsetPastEOF(err, lastLine) {
			return nil
		}
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) && rpcErr.Kind() == "not_found" {
			c.resolver.Invalidate(c.key)
			c.mu.Lock()
			c.resolvedPath = ""
			c.lastLine = 0
			c.mu.Unlock()
			c.applyVTFallback()
		}
		return nil
	}

	// File rotated/shrunk: reload fresh window.
	if win.Lines < lastLine {
		c.mu.Lock()
		c.lastLine = 0
		c.mu.Unlock()
		return c.loadInitialJSONLWindow(ctx, path, format, on)
	}

	chunk := splitJSONLLines(win.Text)
	if len(chunk) == 0 {
		return nil
	}

	c.mu.Lock()
	prevCount := len(c.mergedItems)
	if len(c.rawLines) > 0 && win.First == c.lastLine {
		// Replace last line (in case previous read saw a partial flush) and append new lines.
		sameTail := len(chunk) == 1 && c.rawLines[len(c.rawLines)-1] == chunk[0] && win.Lines == c.totalLines
		if sameTail {
			c.mu.Unlock()
			return nil
		}
		c.rawLines[len(c.rawLines)-1] = chunk[0]
		if len(chunk) > 1 {
			c.rawLines = append(c.rawLines, chunk[1:]...)
		}
	} else if win.First > c.lastLine {
		c.rawLines = append(c.rawLines, chunk...)
	}
	c.lastLine = win.Last
	c.totalLines = win.Lines
	c.source = SourceJSONL
	c.parsedItems = ParseTranscriptLines(format, c.rawLines)
	c.rebuildMergedLocked()
	grew := len(c.mergedItems) > prevCount
	c.mu.Unlock()

	c.bumpRev(grew)
	return nil
}

// LoadOlder triggers an asynchronous fetch of the previous 250 JSONL lines.
func (c *ChatSessionController) LoadOlder() {
	c.mu.Lock()
	if c.loadingOlder || c.source != SourceJSONL || c.firstLine <= 1 {
		c.mu.Unlock()
		return
	}
	c.loadingOlder = true
	c.mu.Unlock()
	c.bumpRev(false)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		_ = c.LoadOlderSync(ctx)
	}()
}

// LoadOlderSync synchronously prepends up to 250 earlier JSONL lines before firstLine.
func (c *ChatSessionController) LoadOlderSync(ctx context.Context) error {
	c.mu.Lock()
	if c.source != SourceJSONL || c.firstLine <= 1 || c.resolvedPath == "" {
		c.loadingOlder = false
		c.mu.Unlock()
		return nil
	}
	prevEnd := c.firstLine - 1
	path := c.resolvedPath
	format := c.format
	on := c.onMachine
	c.loadingOlder = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.loadingOlder = false
		c.mu.Unlock()
		c.bumpRev(false)
	}()

	prevStart := prevEnd - chatTailWindowLines + 1
	if prevStart < 1 {
		prevStart = 1
	}
	count := prevEnd - prevStart + 1

	win, err := c.backend.FileRead(ctx, c.key.InstanceID, FileReadParams{
		Path:   path,
		Offset: prevStart,
		Limit:  count,
		On:     on,
	})
	if err != nil {
		return err
	}
	olderChunk := splitJSONLLines(win.Text)

	c.mu.Lock()
	combined := make([]string, 0, len(olderChunk)+len(c.rawLines))
	combined = append(combined, olderChunk...)
	combined = append(combined, c.rawLines...)
	c.rawLines = combined
	c.firstLine = win.First
	if win.Lines > c.totalLines {
		c.totalLines = win.Lines
	}
	c.parsedItems = ParseTranscriptLines(format, c.rawLines)
	c.rebuildMergedLocked()
	c.mu.Unlock()
	return nil
}

func (c *ChatSessionController) applyVTFallback() {
	raw := ""
	if c.vtFallback != nil {
		raw = c.vtFallback()
	}
	if strings.TrimSpace(raw) == "" && c.backend != nil {
		rctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		if rr, err := c.backend.ReadTerminal(rctx, c.key, 400); err == nil {
			raw = rr.Text
		}
		cancel()
	}
	parsed := ParseVTTranscript(raw)
	c.mu.Lock()
	c.source = SourceVTFallback
	c.parsedItems = parsed
	c.rebuildMergedLocked()
	c.mu.Unlock()
	c.bumpRev(false)
}

func splitJSONLLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func isEmptyFileOffsetErr(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Kind() == "invalid_params" && strings.Contains(rpcErr.Message, "the file has 0 lines")
}

func isOffsetPastEOF(err error, requestedOffset int) bool {
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Kind() != "invalid_params" {
		return false
	}
	return strings.Contains(rpcErr.Message, "the file has") && requestedOffset > 0
}
