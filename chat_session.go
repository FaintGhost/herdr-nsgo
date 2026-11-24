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

const (
	chatTailWindowLines      = 250
	chatFastInitialTailLines = 80
)

func initialTailWindowLines(format TranscriptFormat, on string) int {
	if on != "" || format == FormatAntigravity {
		return chatFastInitialTailLines
	}
	return chatTailWindowLines
}

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
	return b.app.FindTerminalSafe(key)
}

func (b *appTranscriptBackend) resolveClientAndOn(instanceID, requestedOn string) (*Client, string) {
	if b.app == nil {
		return nil, requestedOn
	}
	if instanceID != "" {
		if c := b.app.pool.ClientFor(instanceID); c != nil {
			return c, requestedOn
		}
		snaps, agg := b.app.snapshotsSafe()
		offlineCoreID := ""
		for _, s := range snaps {
			if s.InstanceID == instanceID {
				offlineCoreID = s.CoreID
				break
			}
		}
		for _, it := range agg {
			if !it.Relayed || it.Key.InstanceID == "" {
				continue
			}
			relayOn, _ := SplitMachineAddr(it.Key.Addr)
			matchesTarget := (offlineCoreID != "" && it.CoreID == offlineCoreID) || relayOn == instanceID
			if !matchesTarget {
				continue
			}
			if c := b.app.pool.ClientFor(it.Key.InstanceID); c != nil {
				if requestedOn == "" {
					return c, relayOn
				}
				return c, requestedOn
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

func isLikelySessionID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 6 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	if strings.HasSuffix(s, ".jsonl") {
		return true
	}
	hasDigitOrSep := false
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '-' || r == '_' {
			hasDigitOrSep = true
			break
		}
	}
	return hasDigitOrSep
}

func extractSessionIDFromTerm(t Terminal) string {
	if t.Labels != nil {
		if alma := strings.TrimSpace(t.Labels["alma.session"]); alma != "" {
			parts := strings.Split(alma, ":")
			if last := strings.TrimSpace(parts[len(parts)-1]); isLikelySessionID(last) {
				return last
			}
		}
	}
	var argSlices [][]string
	if t.Foreground != nil && len(t.Foreground.Argv) > 0 {
		argSlices = append(argSlices, t.Foreground.Argv)
	}
	if len(t.Argv) > 0 {
		argSlices = append(argSlices, t.Argv)
	}
	for _, args := range argSlices {
		for i := 0; i < len(args); i++ {
			tok := strings.TrimSpace(args[i])
			if tok == "-r" || tok == "--resume" || tok == "--session" || (tok == "resume" && i == 1) {
				if i+1 < len(args) {
					cand := strings.Trim(strings.TrimSpace(args[i+1]), `"'`)
					if isLikelySessionID(cand) {
						return cand
					}
				}
			}
			// Only split shell command strings after `-c` (e.g. `sh -c "omp -r 01a16527..."`), never free-text prompts!
			if i > 0 && args[i-1] == "-c" {
				fields := strings.Fields(tok)
				for j := 0; j < len(fields)-1; j++ {
					if fields[j] == "-r" || fields[j] == "--resume" || fields[j] == "--session" {
						cand := strings.Trim(fields[j+1], `"'`)
						if isLikelySessionID(cand) {
							return cand
						}
					}
				}
			}
		}
	}
	return ""
}

func terminalPIDs(t Terminal) []uint32 {
	var pids []uint32
	if t.Foreground != nil && t.Foreground.PID > 0 {
		pids = append(pids, t.Foreground.PID)
	}
	if t.PID != nil && *t.PID > 0 {
		if len(pids) == 0 || pids[0] != *t.PID {
			pids = append(pids, *t.PID)
		}
	}
	return pids
}

func terminalPID(t Terminal) uint32 {
	pids := terminalPIDs(t)
	if len(pids) > 0 {
		return pids[0]
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
		resolved, ok = r.resolveClaude(ctx, key.InstanceID, on, terminalPIDs(term), term, backend)
	case "omp", "pi":
		resolved, ok = r.resolvePiOmp(ctx, key.InstanceID, on, kind, term, backend)
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

func (r *TranscriptResolver) resolveClaude(ctx context.Context, instanceID, on string, pids []uint32, term Terminal, backend TranscriptBackend) (ResolvedTranscript, bool) {
	r.mu.Lock()
	home := r.homeDirs[instanceID+"::"+on]
	r.mu.Unlock()

	tryPIDFile := func(pidMetaPath, homePrefix string) (ResolvedTranscript, bool) {
		fr, err := backend.FileRead(ctx, instanceID, FileReadParams{
			Path:   pidMetaPath,
			Offset: 1,
			Limit:  20,
			On:     on,
		})
		if err != nil {
			return ResolvedTranscript{}, false
		}
		var meta struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		if json.Unmarshal([]byte(fr.Text), &meta) != nil || meta.SessionID == "" {
			return ResolvedTranscript{}, false
		}
		cwd := nonEmpty(meta.Cwd, term.CurrentDir())
		slug := claudeProjectSlug(cwd)
		prefix := homePrefix
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
		return ResolvedTranscript{}, false
	}

	// 1. Direct PID session metadata lookup (~/.claude/sessions/<pid>.json), falling back to $HOME if relative misses!
	for _, pid := range pids {
		if home != "" {
			if res, ok := tryPIDFile(fmt.Sprintf("%s/.claude/sessions/%d.json", home, pid), home); ok {
				return res, true
			}
		}
		if on != "" {
			if res, ok := tryPIDFile(fmt.Sprintf(".claude/sessions/%d.json", pid), ""); ok {
				return res, true
			}
		}
	}
	if len(pids) > 0 && home == "" {
		if h := r.resolveHome(ctx, backend, instanceID, on); h != "" {
			home = h
			for _, pid := range pids {
				if res, ok := tryPIDFile(fmt.Sprintf("%s/.claude/sessions/%d.json", home, pid), home); ok {
					return res, true
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

	// 3. Fallback single read-only MachineExec using Env vars (falls through to HC_SLUG if HC_SID misses).
	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		On:      on,
		Timeout: 5.0,
		Env: map[string]string{
			"HC_SID":  sid,
			"HC_SLUG": slug,
		},
		Line: `found=""; if [ -n "$HC_SID" ]; then found=$(ls -1t "$HOME/.claude/projects"/*/*"$HC_SID"*.jsonl 2>/dev/null | head -n 1); fi; if [ -z "$found" ] && [ -n "$HC_SLUG" ]; then found=$(ls -1t "$HOME/.claude/projects/$HC_SLUG"/*.jsonl 2>/dev/null | head -n 1); fi; printf "%s\n" "$found"`,
	})
	if err != nil {
		return ResolvedTranscript{}, false
	}
	if found := firstOutputLine(er.StdoutText()); found != "" {
		return ResolvedTranscript{Path: found, Format: FormatClaude, On: on}, true
	}
	return ResolvedTranscript{}, false
}

func (r *TranscriptResolver) resolvePiOmp(ctx context.Context, instanceID, on, kind string, term Terminal, backend TranscriptBackend) (ResolvedTranscript, bool) {
	sid := extractSessionIDFromTerm(term)
	if strings.HasSuffix(sid, ".jsonl") {
		cand := sid
		if strings.HasPrefix(cand, "~/") {
			if h := r.resolveHome(ctx, backend, instanceID, on); h != "" {
				cand = h + cand[1:]
			}
		}
		if r.fileExists(ctx, backend, instanceID, on, cand) {
			return ResolvedTranscript{Path: cand, Format: FormatPiOmp, On: on}, true
		}
	}
	slug := strings.Trim(reNonAlnum.ReplaceAllString(term.CurrentDir(), "-"), "-")
	primaryDir := ".omp"
	secondaryDir := ".pi"
	if kind == "pi" {
		primaryDir = ".pi"
		secondaryDir = ".omp"
	}
	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		On:      on,
		Timeout: 5.0,
		Env: map[string]string{
			"HC_SID":  sid,
			"HC_SLUG": slug,
			"HC_P":    primaryDir,
			"HC_S":    secondaryDir,
		},
		Line: `found=""; if [ -n "$HC_SID" ]; then found=$(ls -1t "$HOME/$HC_P/agent/sessions"/*/*"$HC_SID"*.jsonl "$HOME/$HC_S/agent/sessions"/*/*"$HC_SID"*.jsonl 2>/dev/null | head -n 1); fi; if [ -z "$found" ] && [ -n "$HC_SLUG" ]; then found=$(ls -1t "$HOME/$HC_P/agent/sessions"/*"$HC_SLUG"*/*.jsonl "$HOME/$HC_S/agent/sessions"/*"$HC_SLUG"*/*.jsonl 2>/dev/null | head -n 1); fi; if [ -z "$found" ]; then found=$(ls -1t "$HOME/$HC_P/agent/sessions"/*/*.jsonl "$HOME/$HC_S/agent/sessions"/*/*.jsonl 2>/dev/null | head -n 1); fi; printf "%s\n" "$found"`,
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
		Line: `found=""; if [ -n "$HC_SID" ]; then found=$(ls -1t "$HOME/.codex/sessions"/*/*/*/*"$HC_SID"*.jsonl 2>/dev/null | head -n 1); fi; if [ -z "$found" ]; then for f in $(ls -1t "$HOME/.codex/sessions"/*/*/*/*.jsonl 2>/dev/null | head -n 15); do head -n 1 "$f" | grep -Fq "\"cwd\":\"$HC_CWD\"" && { found="$f"; break; }; done; fi; printf "%s\n" "$found"`,
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
	launchCwd := strings.TrimSpace(term.Cwd)
	pidStr := ""
	if p := terminalPID(term); p > 0 {
		pidStr = fmt.Sprintf("%d", p)
	}
	er, err := backend.MachineExec(ctx, instanceID, ExecParams{
		On:      on,
		Timeout: 6.0,
		Env: map[string]string{
			"HC_SID":        sid,
			"HC_CWD":        cwd,
			"HC_LAUNCH_CWD": launchCwd,
			"HC_PID":        pidStr,
		},
		Line: `brain="$HOME/.gemini/antigravity-cli/brain"; found=""; ` +
			`if [ -n "$HC_SID" ] && [ -f "$brain/$HC_SID/.system_generated/logs/transcript.jsonl" ]; then found="$brain/$HC_SID/.system_generated/logs/transcript.jsonl"; fi; ` +
			`if [ -z "$found" ] && [ -n "$HC_PID" ] && command -v lsof >/dev/null 2>&1; then ` +
			`  cands=""; for cid in $(lsof -Fn -p "$HC_PID" 2>/dev/null | sed -En 's#^n.*/(presence|conversations|brain)/([0-9a-fA-F-]{8,}).*#\2#p' | sort -u); do ` +
			`    tf="$brain/$cid/.system_generated/logs/transcript.jsonl"; [ -f "$tf" ] && cands="$cands $tf"; ` +
			`  done; ` +
			`  if [ -n "$cands" ]; then ` +
			`    for f in $(ls -1t $cands 2>/dev/null); do head -n 1 "$f" | grep -Fq '"USER_EXPLICIT"' && { found="$f"; break; }; done; ` +
			`    [ -z "$found" ] && found=$(ls -1t $cands 2>/dev/null | head -n 1); ` +
			`  fi; ` +
			`fi; ` +
			`if [ -z "$found" ]; then ` +
			`  list=$(ls -1t "$brain"/*/.system_generated/logs/transcript.jsonl 2>/dev/null | head -n 20); ` +
			`  if [ -n "$HC_CWD" ]; then for f in $list; do head -n 1 "$f" | grep -Fq '"USER_EXPLICIT"' && grep -Fq "$HC_CWD" "$f" && { found="$f"; break; }; done; fi; ` +
			`  if [ -z "$found" ] && [ -n "$HC_LAUNCH_CWD" ]; then for f in $list; do head -n 1 "$f" | grep -Fq '"USER_EXPLICIT"' && grep -Fq "$HC_LAUNCH_CWD" "$f" && { found="$f"; break; }; done; fi; ` +
			`  if [ -z "$found" ]; then for f in $list; do head -n 1 "$f" | grep -Fq '"USER_EXPLICIT"' && { found="$f"; break; }; done; fi; ` +
			`  if [ -z "$found" ]; then for f in $list; do if [ -z "$HC_CWD" ] || grep -Fq "$HC_CWD" "$f"; then found="$f"; break; fi; done; fi; ` +
			`fi; ` +
			`[ -n "$found" ] && printf "%s\n" "$found"`,
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
	id        string
	text      string
	afterLine int // only reconciles against JSONL items with SourceLine > afterLine
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

	syncMu     sync.Mutex
	loopCtx    context.Context
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
	lctx, lcancel := context.WithCancel(context.Background())
	c := &ChatSessionController{
		cx:         cx,
		key:        key,
		backend:    backend,
		resolver:   resolver,
		vtFallback: vtFallback,
		loopCtx:    lctx,
		cancel:     lcancel,
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
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.loopCtx = ctx
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

func (c *ChatSessionController) isClosed() bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	return closed
}

func (c *ChatSessionController) bumpRev(scrollToEnd bool) {
	if c.isClosed() {
		return
	}
	if c.cx != nil && c.rev != nil {
		c.cx.Post(func() {
			if c.isClosed() {
				return
			}
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
// (recording its baseline file line so earlier identical prompts never eat it),
// returning an ID that RemoveOptimistic can roll back if terminal.send fails.
func (c *ChatSessionController) AddOptimisticUser(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	seq := c.optSeq.Add(1)
	id := fmt.Sprintf("opt-%d", seq)
	c.mu.Lock()
	c.optimistic = append(c.optimistic, optimisticMsg{
		id:        id,
		text:      trimmed,
		afterLine: c.lastLine,
	})
	c.rebuildMergedLocked()
	c.mu.Unlock()
	c.bumpRev(true)
	return id
}

// RemoveOptimistic removes a pending optimistic message (used when terminal.send returns an error).
func (c *ChatSessionController) RemoveOptimistic(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	kept := c.optimistic[:0]
	changed := false
	for _, opt := range c.optimistic {
		if opt.id == id {
			changed = true
			continue
		}
		kept = append(kept, opt)
	}
	c.optimistic = kept
	if changed {
		c.rebuildMergedLocked()
	}
	c.mu.Unlock()
	if changed {
		c.bumpRev(false)
	}
}

func (c *ChatSessionController) rebuildMergedLocked() {
	// Reconcile optimistic user messages ONLY against parsed items strictly newer than opt.afterLine!
	if len(c.optimistic) > 0 && len(c.parsedItems) > 0 {
		usedIdx := make(map[int]bool)
		var remaining []optimisticMsg
		for _, opt := range c.optimistic {
			matched := false
			for i := 0; i < len(c.parsedItems); i++ {
				if usedIdx[i] {
					continue
				}
				it := c.parsedItems[i]
				if it.Role != RoleUser || strings.TrimSpace(it.Text) != opt.text {
					continue
				}
				if c.source == SourceJSONL && it.SourceLine <= opt.afterLine {
					continue
				}
				usedIdx[i] = true
				matched = true
				break
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

// RefreshAsync triggers a non-overlapping background refresh tied to the controller's lifecycle context.
func (c *ChatSessionController) RefreshAsync() {
	if c.isClosed() || !c.refreshing.CompareAndSwap(false, true) {
		return
	}
	c.mu.Lock()
	parentCtx := c.loopCtx
	c.mu.Unlock()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	go func() {
		defer c.refreshing.Store(false)
		ctx, cancel := context.WithTimeout(parentCtx, 25*time.Second)
		defer cancel()
		_ = c.RefreshSync(ctx)
	}()
}

// RefreshSync performs one serialized transcript discovery / tail step.
func (c *ChatSessionController) RefreshSync(ctx context.Context) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()

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
				c.mu.Lock()
				c.resolvedPath = curPath
				c.format = curFmt
				c.onMachine = curOn
				c.mu.Unlock()
			}
		}
	}
	if c.isClosed() {
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if !hasResolved {
		c.applyVTFallback()
		return nil
	}

	if lastLine <= 0 {
		return c.loadInitialJSONLWindow(ctx, curPath, curFmt, curOn)
	}
	return c.tailJSONLFromLastLine(ctx, curPath, curFmt, curOn, lastLine)
}

// readWindowAdaptive halves Limit if oversized lines hit herdr-core's 8 MiB file.read response cap.
func (c *ChatSessionController) readWindowAdaptive(ctx context.Context, path, on string, endLine, wantLines int) (FileReadResult, error) {
	limit := wantLines
	for {
		if ctx.Err() != nil {
			return FileReadResult{}, ctx.Err()
		}
		start := endLine - limit + 1
		if start < 1 {
			start = 1
		}
		count := endLine - start + 1
		win, err := c.backend.FileRead(ctx, c.key.InstanceID, FileReadParams{
			Path:   path,
			Offset: start,
			Limit:  count,
			On:     on,
		})
		if err == nil {
			return win, nil
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return FileReadResult{}, err
		}
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) && (rpcErr.Kind() == "not_found" || rpcErr.Kind() == "invalid_params") {
			return FileReadResult{}, err
		}
		if limit <= 10 {
			return FileReadResult{}, err
		}
		limit /= 2
	}
}

func isTransientReadErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func (c *ChatSessionController) invalidateResolvedOnHardErr(err error) {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && rpcErr.Kind() == "not_found" {
		c.resolver.Invalidate(c.key)
		c.mu.Lock()
		c.resolvedPath = ""
		c.mu.Unlock()
	}
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
		c.invalidateResolvedOnHardErr(err)
		if !isTransientReadErr(err) {
			c.applyVTFallback()
		}
		return err
	}

	total := probe.Lines
	if total <= 0 {
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

	win, err := c.readWindowAdaptive(ctx, path, on, total, initialTailWindowLines(format, on))
	if err != nil {
		c.invalidateResolvedOnHardErr(err)
		if !isTransientReadErr(err) {
			c.applyVTFallback()
		}
		return err
	}

	chunkLines := splitJSONLLines(win.Text)
	parsed := ParseTranscriptLinesAt(format, chunkLines, win.First)

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
	curOffset := lastLine
	anyChanged := false
	grew := false

	// Loop up to 8 batches (up to ~1600 lines) so large multi-tool bursts finish catching up in one sync!
	for batch := 0; batch < 8; batch++ {
		win, err := c.backend.FileRead(ctx, c.key.InstanceID, FileReadParams{
			Path:   path,
			Offset: curOffset,
			Limit:  201,
			On:     on,
		})
		if err != nil {
			// Because curOffset == lastLine >= 1, "offset N: the file has M lines" means M < curOffset (file truncated/rotated)!
			if isOffsetPastEOF(err, curOffset) {
				c.mu.Lock()
				c.lastLine = 0
				c.mu.Unlock()
				return c.loadInitialJSONLWindow(ctx, path, format, on)
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
			break
		}

		if win.Lines < curOffset {
			c.mu.Lock()
			c.lastLine = 0
			c.mu.Unlock()
			return c.loadInitialJSONLWindow(ctx, path, format, on)
		}

		chunk := splitJSONLLines(win.Text)
		if len(chunk) == 0 {
			break
		}

		c.mu.Lock()
		prevCount := len(c.mergedItems)
		if len(c.rawLines) > 0 && win.First == c.lastLine {
			sameTail := len(chunk) == 1 && c.rawLines[len(c.rawLines)-1] == chunk[0] && win.Lines == c.totalLines
			if sameTail {
				c.mu.Unlock()
				break
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
		c.parsedItems = ParseTranscriptLinesAt(format, c.rawLines, c.firstLine)
		c.rebuildMergedLocked()
		if len(c.mergedItems) > prevCount {
			grew = true
		}
		anyChanged = true
		c.mu.Unlock()

		if win.Last >= win.Lines {
			break
		}
		curOffset = win.Last
	}

	if anyChanged {
		c.bumpRev(grew)
	}
	return nil
}

// LoadOlder triggers an asynchronous fetch of the previous 250 JSONL lines.
func (c *ChatSessionController) LoadOlder() {
	c.mu.Lock()
	if c.closed || c.loadingOlder || c.source != SourceJSONL || c.firstLine <= 1 {
		c.mu.Unlock()
		return
	}
	c.loadingOlder = true
	parentCtx := c.loopCtx
	c.mu.Unlock()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	c.bumpRev(false)

	go func() {
		ctx, cancel := context.WithTimeout(parentCtx, 12*time.Second)
		defer cancel()
		_ = c.LoadOlderSync(ctx)
	}()
}

// LoadOlderSync synchronously prepends up to 250 earlier JSONL lines before firstLine.
func (c *ChatSessionController) LoadOlderSync(ctx context.Context) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()

	c.mu.Lock()
	if c.closed || c.source != SourceJSONL || c.firstLine <= 1 || c.resolvedPath == "" {
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

	win, err := c.readWindowAdaptive(ctx, path, on, prevEnd, chatTailWindowLines)
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
	c.parsedItems = ParseTranscriptLinesAt(format, c.rawLines, c.firstLine)
	c.rebuildMergedLocked()
	c.mu.Unlock()
	return nil
}

func (c *ChatSessionController) applyVTFallback() {
	if c.isClosed() {
		return
	}
	raw := ""
	if c.vtFallback != nil {
		raw = c.vtFallback()
	}
	if strings.TrimSpace(raw) == "" && c.backend != nil && !c.isClosed() {
		c.mu.Lock()
		parentCtx := c.loopCtx
		c.mu.Unlock()
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		rctx, cancel := context.WithTimeout(parentCtx, 4*time.Second)
		if rr, err := c.backend.ReadTerminal(rctx, c.key, 400); err == nil {
			raw = rr.Text
		}
		cancel()
	}
	if c.isClosed() {
		return
	}
	parsed := ParseVTTranscript(raw)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
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
