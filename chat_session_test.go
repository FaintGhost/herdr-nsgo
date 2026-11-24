package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTranscriptBackend struct {
	mu          sync.Mutex
	terms       map[TermKey]AggregatedTerminal
	files       map[string][]string // key: on+"::"+path -> 1-indexed lines slice
	execCalls   int
	fileReads   []FileReadParams
	execHandler func(instanceID string, p ExecParams) (ExecResult, error)
}

func (f *fakeTranscriptBackend) FindTerminal(key TermKey) (AggregatedTerminal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.terms[key]
	return t, ok
}

func (f *fakeTranscriptBackend) FileRead(_ context.Context, _ string, p FileReadParams) (FileReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fileReads = append(f.fileReads, p)
	lines, ok := f.files[p.On+"::"+p.Path]
	if !ok {
		e := &RPCError{Message: "not_found"}
		e.Data.Kind = "not_found"
		return FileReadResult{}, e
	}
	total := len(lines)
	off := p.Offset
	if off <= 0 {
		off = 1
	}
	if off > total {
		e := &RPCError{Message: fmt.Sprintf("offset %d: the file has %d lines", off, total)}
		e.Data.Kind = "invalid_params"
		return FileReadResult{}, e
	}
	end := total
	if p.Limit > 0 && off+p.Limit-1 < end {
		end = off + p.Limit - 1
	}
	chunk := strings.Join(lines[off-1:end], "\n") + "\n"
	return FileReadResult{
		Text:  chunk,
		First: off,
		Last:  end,
		Lines: total,
	}, nil
}

func (f *fakeTranscriptBackend) MachineExec(_ context.Context, instanceID string, p ExecParams) (ExecResult, error) {
	f.mu.Lock()
	f.execCalls++
	h := f.execHandler
	f.mu.Unlock()
	if h != nil {
		return h(instanceID, p)
	}
	code := int32(0)
	return ExecResult{Code: &code}, nil
}

func (f *fakeTranscriptBackend) ReadTerminal(_ context.Context, _ TermKey, _ int) (ReadResult, error) {
	return ReadResult{}, errors.New("unused")
}

func TestChatSessionResolverPaginationTailAndOptimisticDedup(t *testing.T) {
	ctx := context.Background()
	resolver := NewTranscriptResolver()

	// 1. Claude Code direct PID file resolution (0 MachineExec calls once ~ or home known) + 600-line JSONL pagination & tailing.
	claudeLines := make([]string, 0, 600)
	for i := 1; i <= 600; i++ {
		if i%2 == 1 {
			claudeLines = append(claudeLines, fmt.Sprintf(`{"type":"user","uuid":"u-%d","message":{"role":"user","content":"user-msg-%03d"}}`, i, i))
		} else {
			claudeLines = append(claudeLines, fmt.Sprintf(`{"type":"assistant","uuid":"a-%d","message":{"id":"m-%d","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"reply-%03d"}]}}`, i, i, i))
		}
	}

	claudeKey := TermKey{InstanceID: "mac", Addr: "t9"}
	ompKey := TermKey{InstanceID: "mac", Addr: "kr/t12"}
	shellKey := TermKey{InstanceID: "mac", Addr: "t5"}

	claudePath := "/Users/dev/.claude/projects/-Volumes-ExternalSSD-workspace-app/sess-uuid-99.jsonl"
	ompPath := "/root/.omp/agent/sessions/-root-pp-comic/2026-11-24_01a16527-21ee-725b-9549-ee5a9106c718.jsonl"

	backend := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			claudeKey: {
				Key: claudeKey,
				Terminal: Terminal{
					Address:    "t9",
					Cwd:        "/Volumes/ExternalSSD/workspace/app",
					State:      "live",
					Foreground: &Foreground{PID: 34488, Name: "2.1.330", Cwd: "/Volumes/ExternalSSD/workspace/app", Argv: []string{"claude"}},
					Status:     &Status{App: "claude-code", State: "working"},
				},
			},
			ompKey: {
				Key: ompKey,
				Terminal: Terminal{
					Address: "kr/t12",
					Cwd:     "/root/pp/comic",
					State:   "live",
					Labels:  map[string]string{"alma.session": "omp:id:01a16527-21ee-725b-9549-ee5a9106c718"},
					Status:  &Status{App: "omp", State: "done"},
				},
			},
			shellKey: {
				Key: shellKey,
				Terminal: Terminal{
					Address:    "t5",
					Cwd:        "/",
					State:      "live",
					Foreground: &Foreground{PID: 100, Name: "zsh"},
				},
			},
		},
		files: map[string][]string{
			"::/Users/dev/.claude/sessions/34488.json": {`{"pid":34488,"sessionId":"sess-uuid-99","cwd":"/Volumes/ExternalSSD/workspace/app"}`},
			"::" + claudePath:                          claudeLines,
			"kr::" + ompPath: {
				`{"type":"message","id":"o1","message":{"role":"user","content":[{"type":"text","text":"hello from kr omp"}]}}`,
				`{"type":"message","id":"o2","message":{"role":"assistant","model":"gpt-5.4","content":[{"type":"text","text":"omp reply on kr"}]}}`,
			},
		},
	}
	backend.execHandler = func(_ string, p ExecParams) (ExecResult, error) {
		code := int32(0)
		if strings.Contains(p.Line, "$HOME") && p.On == "" {
			return ExecResult{Code: &code, Stdout: base64.StdEncoding.EncodeToString([]byte("/Users/dev"))}, nil
		}
		if p.On == "kr" && p.Env["HC_SID"] == "01a16527-21ee-725b-9549-ee5a9106c718" {
			return ExecResult{Code: &code, Stdout: base64.StdEncoding.EncodeToString([]byte(ompPath + "\n"))}, nil
		}
		return ExecResult{Code: &code}, nil
	}

	// Pre-seed cached home for local mac to prove zero MachineExec needed for Claude PID file hit!
	resolver.SetHome("mac", "", "/Users/dev")

	ctrl := NewChatSessionController(nil, claudeKey, backend, resolver, func() string { return "❯ fallback" })
	defer ctrl.Close()
	if err := ctrl.RefreshSync(ctx); err != nil {
		t.Fatalf("initial RefreshSync: %v", err)
	}
	if backend.execCalls != 0 {
		t.Fatalf("expected 0 MachineExec calls when Claude PID file hits directly, got %d", backend.execCalls)
	}
	if ctrl.Source() != SourceJSONL {
		t.Fatalf("expected SourceJSONL, got %v", ctrl.Source())
	}
	items := ctrl.Items()
	if len(items) != 250 {
		t.Fatalf("expected initial tail window of 250 items, got %d", len(items))
	}
	if !strings.Contains(items[0].Text, "351") || !strings.Contains(items[len(items)-1].Text, "600") {
		t.Fatalf("expected tail window lines 351..600, got first=%q last=%q", items[0].Text, items[len(items)-1].Text)
	}
	if !ctrl.HasOlder() || ctrl.OlderRemaining() != 350 {
		t.Fatalf("expected HasOlder=true remaining=350, got %v / %d", ctrl.HasOlder(), ctrl.OlderRemaining())
	}

	// 2. LoadOlder prepends 250 earlier lines (lines 101..350 -> total 500 items).
	if err := ctrl.LoadOlderSync(ctx); err != nil {
		t.Fatalf("LoadOlderSync: %v", err)
	}
	items = ctrl.Items()
	if len(items) != 500 || !strings.Contains(items[0].Text, "101") || ctrl.OlderRemaining() != 100 {
		t.Fatalf("expected 500 items starting at 101 with 100 remaining, got len=%d first=%q rem=%d", len(items), items[0].Text, ctrl.OlderRemaining())
	}

	// 3. Idle EOF tailing MUST query offset=600 (never offset=601 > lines!) and NOT fail or drop to VT fallback.
	if err := ctrl.RefreshSync(ctx); err != nil {
		t.Fatalf("EOF RefreshSync failed: %v", err)
	}
	if ctrl.Source() != SourceJSONL || len(ctrl.Items()) != 500 {
		t.Fatalf("EOF RefreshSync corrupted state: source=%v len=%d", ctrl.Source(), len(ctrl.Items()))
	}

	// 4. Optimistic echo & automatic deduplication once JSONL appends the user turn + reply!
	ctrl.AddOptimisticUser("fix bug now")
	items = ctrl.Items()
	if len(items) != 501 || !items[len(items)-1].Optimistic || items[len(items)-1].Text != "fix bug now" {
		t.Fatalf("expected optimistic user message at tail, got %+v", items[len(items)-1])
	}

	backend.mu.Lock()
	backend.files["::"+claudePath] = append(backend.files["::"+claudePath],
		`{"type":"user","uuid":"u-601","message":{"role":"user","content":"fix bug now"}}`,
		`{"type":"assistant","uuid":"a-602","message":{"id":"m-602","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"bug fixed!"}]}}`,
	)
	backend.mu.Unlock()

	if err := ctrl.RefreshSync(ctx); err != nil {
		t.Fatalf("tail RefreshSync after append: %v", err)
	}
	items = ctrl.Items()
	// 500 + 1 deduplicated user + 1 assistant = 502 (optimistic echo merged cleanly!).
	if len(items) != 502 || items[len(items)-2].Optimistic || items[len(items)-1].Text != "bug fixed!" {
		t.Fatalf("expected optimistic echo deduplicated into 502 items, got len=%d tail=%+v", len(items), items[len(items)-2:])
	}

	// 5. Federated kr/t12 OMP session resolves with On="kr" via HC_SID env and caches path on second refresh!
	ompCtrl := NewChatSessionController(nil, ompKey, backend, resolver, func() string { return "" })
	defer ompCtrl.Close()
	if err := ompCtrl.RefreshSync(ctx); err != nil {
		t.Fatalf("omp RefreshSync: %v", err)
	}
	execAfterFirst := backend.execCalls
	if execAfterFirst != 1 || ompCtrl.Source() != SourceJSONL || len(ompCtrl.Items()) != 2 {
		t.Fatalf("expected 1 MachineExec and 2 OMP items, got exec=%d source=%v len=%d", execAfterFirst, ompCtrl.Source(), len(ompCtrl.Items()))
	}
	_ = ompCtrl.RefreshSync(ctx)
	if backend.execCalls != execAfterFirst {
		t.Fatalf("expected cached transcript path (0 extra MachineExec), got %d", backend.execCalls)
	}

	// 6. Missing JSONL seamlessly falls back to VT transcript parser!
	vtCtrl := NewChatSessionController(nil, shellKey, backend, resolver, func() string {
		return "❯ ls -la\n● total 8"
	})
	defer vtCtrl.Close()
	if err := vtCtrl.RefreshSync(ctx); err != nil {
		t.Fatalf("vt fallback RefreshSync: %v", err)
	}
	if vtCtrl.Source() != SourceVTFallback || len(vtCtrl.Items()) != 2 {
		t.Fatalf("expected SourceVTFallback with 2 items, got source=%v items=%+v", vtCtrl.Source(), vtCtrl.Items())
	}
}

type liveClientBackend struct {
	client *Client
	terms  map[string]Terminal
}

func (l *liveClientBackend) FindTerminal(key TermKey) (AggregatedTerminal, bool) {
	t, ok := l.terms[key.Addr]
	if !ok {
		return AggregatedTerminal{}, false
	}
	return AggregatedTerminal{Key: key, Terminal: t}, true
}

func (l *liveClientBackend) FileRead(ctx context.Context, _ string, p FileReadParams) (FileReadResult, error) {
	return l.client.FileRead(ctx, p)
}

func (l *liveClientBackend) MachineExec(ctx context.Context, _ string, p ExecParams) (ExecResult, error) {
	return l.client.MachineExec(ctx, p)
}

func (l *liveClientBackend) ReadTerminal(ctx context.Context, key TermKey, lines int) (ReadResult, error) {
	return l.client.Read(ctx, key.Addr, lines)
}

// TestLiveCoreChatSessionEndToEnd exercises real framed RPC + TranscriptResolver + ChatSessionController
// against the live local herdr-core (and its relayed remote machines) when hc is on PATH.
func TestLiveCoreChatSessionEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("hc"); err != nil {
		t.Skip("hc is not on PATH")
	}
	rw, err := dialLocal()
	if err != nil {
		t.Fatalf("dialLocal: %v", err)
	}
	c := NewClient(rw)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if _, err := c.HelloFramed(ctx); err != nil {
		t.Fatalf("HelloFramed: %v", err)
	}
	ls, err := c.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	termMap := make(map[string]Terminal, len(ls.Terminals))
	byKind := make(map[string]Terminal)
	for _, tm := range ls.Terminals {
		if tm.Finished() {
			continue
		}
		k := DetectHarnessKind(tm)
		if k == "" {
			// Try fetching full record only for local terminals where foreground argv might reveal the harness.
			if on, _ := SplitMachineAddr(tm.Address); on == "" {
				if full, gerr := c.Get(ctx, tm.Address); gerr == nil {
					tm = full
					k = DetectHarnessKind(tm)
				}
			}
		}
		if k != "" {
			if on, _ := SplitMachineAddr(tm.Address); on == "" {
				if full, gerr := c.Get(ctx, tm.Address); gerr == nil {
					tm = full
				}
			}
			if _, exists := byKind[k]; !exists {
				byKind[k] = tm
			}
		}
		termMap[tm.Address] = tm
	}

	// Also explicitly exercise t19 if present (live dogfood Antigravity session).
	if t19, ok := termMap["t19"]; ok && DetectHarnessKind(t19) == "antigravity" {
		byKind["antigravity-t19"] = t19
	}

	lb := &liveClientBackend{client: c, terms: termMap}
	resolver := NewTranscriptResolver()
	resolvedJSONLCount := 0

	for kind, tm := range byKind {
		key := TermKey{InstanceID: "local", Addr: tm.Address}
		ctrl := NewChatSessionController(nil, key, lb, resolver, nil)
		if err := ctrl.RefreshSync(ctx); err != nil {
			ctrl.Close()
			t.Fatalf("live RefreshSync %s (%s): %v", tm.Address, kind, err)
		}
		src := ctrl.Source()
		items := ctrl.Items()
		ctrl.mu.Lock()
		rpath := ctrl.resolvedPath
		ctrl.mu.Unlock()
		t.Logf("live_session addr=%s harness=%s source=%s items=%d older_remaining=%d path=%s", tm.Address, kind, src, len(items), ctrl.OlderRemaining(), rpath)
		if src == SourceJSONL {
			resolvedJSONLCount++
		}
		ctrl.Close()
	}
	if len(byKind) > 0 && resolvedJSONLCount == 0 {
		t.Fatalf("expected at least 1 live agent session to resolve native JSONL, got 0 out of %d", len(byKind))
	}
	t.Logf("live_e2e_summary agent_kinds=%d jsonl_resolved=%d", len(byKind), resolvedJSONLCount)
}
