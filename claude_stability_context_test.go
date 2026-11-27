package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestAntigravityContextUsageAndHelpers(t *testing.T) {
	lines := []string{
		`{"step_index":1,"source":"USER","type":"USER_INPUT","content":"hello"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","model":"gemini-4-neon-high","input_tokens":24220,"cache_read_tokens":0,"output_tokens":507,"content":"hi"}`,
		`{"step_index":6,"source":"MODEL","type":"PLANNER_RESPONSE","model":"gemini-4-neon-high","input_tokens":6780,"cache_read_tokens":24221,"output_tokens":268,"content":"done"}`,
	}
	u := ParseTranscriptUsage(FormatAntigravity, lines)
	if !u.Valid() {
		t.Fatal("expected valid ContextUsage for Antigravity")
	}
	if u.ContextTokens != 31001 {
		t.Fatalf("expected 31001 context tokens (input+cache_read), got %d", u.ContextTokens)
	}
	if u.WindowTokens != 1_000_000 {
		t.Fatalf("expected 1M window for gemini, got %d", u.WindowTokens)
	}
	if u.Model != "gemini-4-neon" {
		t.Fatalf("expected clean model gemini-4-neon, got %q", u.Model)
	}
	if got := u.DetailLabel(); got != "上下文 31k / 1.0M · 3%" {
		t.Fatalf("unexpected DetailLabel: %q", got)
	}
	if win := estimateContextWindow("claude-opus-4-6[1m]", 120_000); win != 1_000_000 {
		t.Fatalf("expected 1M window for [1m] Claude model, got %d", win)
	}
}

type flakyClaudeBackend struct {
	mu       sync.Mutex
	term     AggregatedTerminal
	files    map[string][]string
	offline  bool
	execHook func(ExecParams) (ExecResult, error)
}

func (b *flakyClaudeBackend) FindTerminal(key TermKey) (AggregatedTerminal, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.term.Key == key {
		return b.term, true
	}
	return AggregatedTerminal{}, false
}

func makeRPCError(code int, kind, msg string) *RPCError {
	e := &RPCError{Code: code, Message: msg}
	e.Data.Kind = kind
	return e
}

func (b *flakyClaudeBackend) FileRead(_ context.Context, _ string, p FileReadParams) (FileReadResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offline {
		return FileReadResult{}, errors.New("instance offline")
	}
	lines, ok := b.files[p.Path]
	if !ok {
		return FileReadResult{}, makeRPCError(-32004, "not_found", "not found")
	}
	start := p.Offset
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return FileReadResult{}, makeRPCError(-32602, "invalid_params", "offset exceeds the file has 1 lines")
	}
	end := start + p.Limit - 1
	if p.Limit <= 0 || end > len(lines) {
		end = len(lines)
	}
	return FileReadResult{
		Text:  strings.Join(lines[start-1:end], "\n"),
		First: start,
		Last:  end,
		Lines: len(lines),
	}, nil
}

func (b *flakyClaudeBackend) MachineExec(_ context.Context, _ string, p ExecParams) (ExecResult, error) {
	b.mu.Lock()
	offline := b.offline
	hook := b.execHook
	b.mu.Unlock()
	if offline {
		return ExecResult{}, errors.New("instance offline")
	}
	if hook != nil {
		return hook(p)
	}
	return ExecResult{}, nil
}

func (b *flakyClaudeBackend) ReadTerminal(_ context.Context, _ TermKey, _ int) (ReadResult, error) {
	return ReadResult{Text: "❯ raw plaintext screen\n● raw plaintext output"}, nil
}

func TestClaudeNeverRegressesToVTFallbackOnTransientErrOrFileRotation(t *testing.T) {
	key := TermKey{InstanceID: "mac", Addr: "t1"}
	oldPath := "/Users/dev/.claude/projects/-repo/sess-1.jsonl"
	newPath := "/Users/dev/.claude/projects/-repo/sess-2.jsonl"
	fb := &flakyClaudeBackend{
		term: AggregatedTerminal{
			Key: key,
			Terminal: Terminal{
				Address: "t1",
				Cwd:     "/repo",
				Argv:    []string{"claude"},
				Foreground: &Foreground{
					PID:  501,
					Name: "bash",
					Cwd:  "/repo/subdir",
				},
			},
		},
		files: map[string][]string{
			oldPath: {
				`{"type":"user","uuid":"u1","message":{"role":"user","content":"检查 bug"}}`,
				`{"type":"assistant","uuid":"a1","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":40000,"cache_read_input_tokens":24000,"output_tokens":300},"content":[{"type":"text","text":"已完成排查"}]}}`,
			},
		},
	}
	fb.execHook = func(p ExecParams) (ExecResult, error) {
		if strings.Contains(p.Line, "$HOME") && strings.Contains(p.Line, "printf %s") {
			return ExecResult{Stdout: base64.StdEncoding.EncodeToString([]byte("/Users/dev"))}, nil
		}
		// Verify launch cwd slug (-repo) is provided even when Foreground.Cwd is /repo/subdir!
		if p.Env["HC_LAUNCH_SLUG"] != "-repo" {
			t.Errorf("expected HC_LAUNCH_SLUG=-repo, got %+v", p.Env)
		}
		fb.mu.Lock()
		_, hasNew := fb.files[newPath]
		fb.mu.Unlock()
		chosen := oldPath
		if hasNew {
			chosen = newPath
		}
		return ExecResult{Stdout: base64.StdEncoding.EncodeToString([]byte(chosen + "\n"))}, nil
	}

	resolver := NewTranscriptResolver()
	ctrl := NewChatSessionController(nil, key, fb, resolver, func() string { return "❯ vt fallback" })
	defer ctrl.Close()

	// 1. Initial sync resolves via launch cwd slug even though Foreground.Cwd is /repo/subdir.
	if err := ctrl.RefreshSync(context.Background()); err != nil {
		t.Fatalf("initial RefreshSync failed: %v", err)
	}
	if ctrl.Source() != SourceJSONL || len(ctrl.Items()) != 2 {
		t.Fatalf("expected SourceJSONL with 2 items, got source=%v items=%d", ctrl.Source(), len(ctrl.Items()))
	}

	// 2. Transient offline error must NEVER degrade an active JSONL session to SourceVTFallback plaintext!
	fb.mu.Lock()
	fb.offline = true
	fb.mu.Unlock()
	_ = ctrl.RefreshSync(context.Background())
	if ctrl.Source() != SourceJSONL || len(ctrl.Items()) != 2 {
		t.Fatalf("transient offline degraded Claude to %v (items=%d)", ctrl.Source(), len(ctrl.Items()))
	}

	// 3. File rotation (oldPath removed -> newPath created) must self-heal directly to new JSONL without plaintext fallback!
	fb.mu.Lock()
	fb.offline = false
	delete(fb.files, oldPath)
	fb.files[newPath] = []string{
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"新会话消息"}}`,
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"新会话已恢复"}]}}`,
	}
	fb.mu.Unlock()
	if err := ctrl.RefreshSync(context.Background()); err != nil {
		t.Fatalf("rotation RefreshSync failed: %v", err)
	}
	if ctrl.Source() != SourceJSONL {
		t.Fatalf("expected SourceJSONL after rotation, got %v", ctrl.Source())
	}
	items := ctrl.Items()
	if len(items) != 2 || items[1].Text != "新会话已恢复" {
		t.Fatalf("expected rotated JSONL content, got %+v", items)
	}
}

func TestOpenSessionNeverTrapsClaudeInVTAndContextRingToast(t *testing.T) {
	key := TermKey{InstanceID: "mac", Addr: "t1"}
	path := "/Users/dev/.claude/projects/-repo/s1.jsonl"
	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			key: {
				Key:          key,
				InstanceName: "Mac",
				Terminal: Terminal{
					Address:    "t1",
					Title:      "Claude Code",
					Cwd:        "/repo",
					Argv:       []string{"claude"},
					Foreground: &Foreground{PID: 900, Name: "claude", Cwd: "/repo", Argv: []string{"claude"}},
					State:      "idle",
				},
			},
		},
		files: map[string][]string{
			"::/Users/dev/.claude/sessions/900.json": {`{"sessionId":"s1","cwd":"/repo"}`},
			"::" + path: {
				`{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`,
				`{"type":"assistant","uuid":"a1","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":40000,"cache_read_input_tokens":24000,"output_tokens":500},"content":[{"type":"text","text":"你好"}]}}`,
			},
		},
	}

	var uiRef *uiController
	var appRef *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		appRef = a
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)
		// Start with a snapshot where Claude metadata hasn't populated yet (transient shell fg).
		snaps := a.instances.Get()
		snaps[1].Terminals = []Terminal{{
			Address:    "t1",
			Title:      "zsh",
			Cwd:        "/repo",
			Argv:       []string{"zsh"},
			Foreground: &Foreground{PID: 900, Name: "bash", Cwd: "/repo"},
			State:      "idle",
		}}
		a.SeedSnapshots(snaps)
		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	// Open before Claude metadata is visible -> falls back to VT temporarily, MUST NOT poison u.sessionModes[key].
	uiRef.openSession(appRef.cx, key)
	h.Settle()
	if uiRef.sessionMode.Get() != "vt" {
		t.Fatalf("expected temporary vt mode before Claude metadata arrives, got %q", uiRef.sessionMode.Get())
	}
	if _, pinned := uiRef.sessionModes[key]; pinned {
		t.Fatalf("auto-detected vt default must NOT be permanently pinned in sessionModes")
	}

	// Now pool sync arrives with Claude metadata -> must auto-upgrade to Chat mode!
	snaps := appRef.instances.Get()
	snaps[1].Terminals = []Terminal{{
		Address:    "t1",
		Title:      "Claude Code",
		Cwd:        "/repo",
		Argv:       []string{"claude"},
		Foreground: &Foreground{PID: 900, Name: "claude", Cwd: "/repo", Argv: []string{"claude"}},
		State:      "idle",
	}}
	appRef.SeedSnapshots(snaps)
	appRef.mu.Lock()
	syncFn := appRef.onPoolSync
	appRef.mu.Unlock()
	if syncFn != nil {
		syncFn()
	}
	h.Settle()
	if uiRef.sessionMode.Get() != "chat" {
		t.Fatalf("expected auto-upgrade to chat once Claude metadata arrived, got %q", uiRef.sessionMode.Get())
	}

	// Sync transcript -> verify compact Context Ring renders and tapping shows detail toast!
	_ = uiRef.activeChat.Get().RefreshSync(context.Background())
	h.Settle()
	h.Find("id:chat-context-ring")
	h.Press("id:chat-context-ring")
	h.Settle()
	h.Find("text:上下文 64k / 200k · 32%")

	// If user explicitly chooses VT mode, OnPoolSync must respect their manual choice!
	h.Press("id:session-mode-vt")
	h.Settle()
	if syncFn != nil {
		syncFn()
	}
	h.Settle()
	if uiRef.sessionMode.Get() != "vt" {
		t.Fatalf("expected explicit user choice of vt mode to stay vt, got %q", uiRef.sessionMode.Get())
	}
}

func TestVTPlaintextFallbackReplacedCleanlyByLiveANSI(t *testing.T) {
	st := NewSessionTerminal(nil, TermKey{InstanceID: "mac", Addr: "t1"}, 80, 24, true, nil, false)
	defer st.Close()

	st.FeedPlaintextFallback([]byte("stale plaintext line\r\n"))
	if !strings.Contains(st.Screen().PlainText(), "stale plaintext line") {
		t.Fatalf("expected plaintext fallback rendered, got %q", st.Screen().PlainText())
	}
	// Live ANSI frame arrives -> must reset plaintext fallback and render ANSI cleanly.
	st.Feed([]byte("\x1b[32mLIVE ANSI\x1b[0m\r\n"))
	plain := st.Screen().PlainText()
	if strings.Contains(plain, "stale plaintext line") {
		t.Fatalf("stale plaintext should have been cleared when live ANSI arrived: %q", plain)
	}
	if !strings.Contains(plain, "LIVE ANSI") {
		t.Fatalf("expected live ANSI content, got %q", plain)
	}
}
