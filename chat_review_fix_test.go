package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

// C1 + I6: Live incremental appends to an already-mounted RoleToolGroup and RoleAssistant card
// MUST render without reopening the session, and header/code-block layouts keep shrink bounds.
func TestReviewFixLiveStreamingUpdatesMountedToolGroupAndAssistantCard(t *testing.T) {
	claudeKey := TermKey{InstanceID: "mac", Addr: "t1"}
	claudePath := "/Users/dev/.claude/projects/-Volumes-ExternalSSD-workspace-herdr-nsgo/live.jsonl"

	var fb *fakeTranscriptBackend
	var uiRef *uiController

	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Foreground = &Foreground{
			PID:  34488,
			Name: "2.1.330",
			Cwd:  "/Volumes/ExternalSSD/workspace/herdr-nsgo",
			Argv: []string{"claude"},
		}
		a.SeedSnapshots(snaps)

		fb = &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				claudeKey: {Key: claudeKey, Terminal: snaps[1].Terminals[0]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {
					`{"pid":34488,"sessionId":"live","cwd":"/Volumes/ExternalSSD/workspace/herdr-nsgo"}`,
				},
				"::" + claudePath: {
					`{"type":"user","uuid":"u1","message":{"role":"user","content":"开始排查"}}`,
					`{"type":"assistant","uuid":"a1","message":{"id":"msg_stream","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/repo/first.go"}}]}}`,
					`{"type":"assistant","uuid":"a2","message":{"id":"msg_stream","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"第一段结论"}]}}`,
				},
			},
		}
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)

		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	h.Press("id:session-row-mac::t1")
	chat := uiRef.activeChat.Get()
	_ = chat.RefreshSync(t.Context())
	h.Find("text:first.go")
	h.Find("text:/第一段结论/")
	h.Find("id:chat-source-pill")

	// Append a SECOND tool into the SAME RoleToolGroup and a SECOND text chunk into the SAME RoleAssistant!
	// First test appending tool #2 to an active RoleToolGroup:
	fb.mu.Lock()
	fb.files["::"+claudePath] = []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"开始排查"}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"msg_stream","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/repo/first.go"}}]}}`,
		`{"type":"assistant","uuid":"a1b","message":{"id":"msg_stream","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"tu_2","name":"Edit","input":{"file_path":"/repo/second.go"}}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"id":"msg_stream","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"第一段结论"}]}}`,
		`{"type":"assistant","uuid":"a3","message":{"id":"msg_stream","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"第二段流式补充"}]}}`,
	}
	fb.mu.Unlock()

	_ = chat.RefreshSync(t.Context())
	// Drive a benign UI interaction/tick so posted signal updates flush into the tree.
	h.Press("id:chat-source-pill")
	h.Find("text:second.go")
	h.Find("text:/第二段流式补充/")
}

// C2 + I4: >512 KiB JSONL lines (e.g. Base64 screenshot + user prompt or huge tool_result)
// must NOT be silently dropped, and >16 KiB multi-byte UTF-8 CJK tool output must stay valid UTF-8.
func TestReviewFixOversizedBase64LineAndUTF8SafeTruncation(t *testing.T) {
	bigB64 := strings.Repeat("QUJD", 150000) // 600 KB Base64 payload (> 512 KiB)
	lines := []string{
		fmt.Sprintf(`{"type":"user","uuid":"u-big","message":{"role":"user","content":[{"type":"text","text":"请看这张报错截图"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}}`, bigB64),
		`{"type":"assistant","uuid":"a-big","message":{"id":"m-big","role":"assistant","content":[{"type":"tool_use","id":"t-big","name":"Bash","input":{"command":"cat huge.log"}}]}}`,
		fmt.Sprintf(`{"type":"user","uuid":"u-tr","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t-big","content":%q}]}}`, "HEAD-MARKER\n"+strings.Repeat("中文日志内容测试行ABC\n", 45000)+"TAIL-MARKER"),
	}
	items := ParseClaudeJSONL(lines)
	if len(items) != 2 {
		t.Fatalf("expected user prompt + tool group preserved on >512KiB lines, got %d: %+v", len(items), items)
	}
	if !strings.Contains(items[0].Text, "请看这张报错截图") || !strings.Contains(items[0].Text, "[图片附件]") {
		t.Fatalf("expected user text and [图片附件] preserved, got %q", items[0].Text)
	}
	out := items[1].Tools[0].Output
	if !strings.Contains(out, "HEAD-MARKER") || !strings.Contains(out, "TAIL-MARKER") {
		t.Fatalf("expected oversized tool_result head & tail preserved, got %q", out)
	}
	if !utf8.ValidString(out) || strings.ContainsRune(out, '\ufffd') {
		t.Fatalf("expected valid UTF-8 without replacement char after CJK truncation, got invalid UTF-8")
	}
}

// C3: Optimistic echo must NOT be eaten by an earlier identical user prompt ("继续"),
// and failed send must roll back the optimistic item & restore empty composer draft.
func TestReviewFixOptimisticRepeatPromptAndRollbackOnError(t *testing.T) {
	ctx := context.Background()
	key := TermKey{InstanceID: "mac", Addr: "t9"}
	path := "/Users/dev/.claude/projects/-app/s1.jsonl"
	resolver := NewTranscriptResolver()
	resolver.SetHome("mac", "", "/Users/dev")

	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			key: {
				Key: key,
				Terminal: Terminal{
					Address:    "t9",
					Cwd:        "/app",
					State:      "live",
					Foreground: &Foreground{PID: 12, Cwd: "/app", Argv: []string{"claude"}},
					Status:     &Status{App: "claude-code", State: "idle"},
				},
			},
		},
		files: map[string][]string{
			"::/Users/dev/.claude/sessions/12.json": {`{"pid":12,"sessionId":"s1","cwd":"/app"}`},
			"::" + path: {
				`{"type":"user","uuid":"u1","message":{"role":"user","content":"继续"}}`,
				`{"type":"assistant","uuid":"a1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"好的，已完成第一步"}]}}`,
			},
		},
	}

	ctrl := NewChatSessionController(nil, key, fb, resolver, nil)
	defer ctrl.Close()
	_ = ctrl.RefreshSync(ctx)

	// Send "继续" AGAIN: must NOT match u1 from before AddOptimisticUser!
	optID := ctrl.AddOptimisticUser("继续")
	items := ctrl.Items()
	if len(items) != 3 || !items[2].Optimistic || items[2].Text != "继续" {
		t.Fatalf("expected repeat '继续' optimistic bubble preserved as 3rd item, got %+v", items)
	}

	// Simulate send failure -> RemoveOptimistic removes the unsent bubble cleanly!
	ctrl.RemoveOptimistic(optID)
	if len(ctrl.Items()) != 2 {
		t.Fatalf("expected failed optimistic bubble rolled back, got %+v", ctrl.Items())
	}

	// Send "继续" a third time and then append the new user turn to JSONL -> deduplicates cleanly!
	ctrl.AddOptimisticUser("继续")
	fb.mu.Lock()
	fb.files["::"+path] = append(fb.files["::"+path], `{"type":"user","uuid":"u2","message":{"role":"user","content":"继续"}}`)
	fb.mu.Unlock()
	_ = ctrl.RefreshSync(ctx)
	items = ctrl.Items()
	if len(items) != 3 || items[2].Optimistic {
		t.Fatalf("expected new JSONL '继续' turn to deduplicate optimistic bubble into 3 items, got %+v", items)
	}
}

// I1 + I5: Relayed Claude PID lookup must fall back to $HOME when relative .claude/... misses,
// false-positive "resume" inside prompt text must be ignored, and Template-only terminals detect harness!
func TestReviewFixRelayedClaudeHomeFallbackAndPromptResumeGuard(t *testing.T) {
	ctx := context.Background()
	resolver := NewTranscriptResolver()
	relayedClaudeKey := TermKey{InstanceID: "mac", Addr: "kr/t9"}
	expectedPath := "/root/.claude/projects/-srv-api/real-uuid-77.jsonl"

	tpl := "claude"
	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			relayedClaudeKey: {
				Key: relayedClaudeKey,
				Terminal: Terminal{
					Address:    "kr/t9",
					Cwd:        "/srv/api",
					State:      "live",
					Template:   &tpl,
					Argv:       []string{"claude", "please resume the deployment audit"},
					Foreground: &Foreground{PID: 5501, Name: "node", Cwd: "/srv/api"},
				},
			},
		},
		files: map[string][]string{
			// Note: relative ".claude/sessions/5501.json" is NOT present (simulating core CWD != $HOME),
			// only absolute "/root/.claude/sessions/5501.json" exists!
			"kr::/root/.claude/sessions/5501.json": {`{"pid":5501,"sessionId":"real-uuid-77","cwd":"/srv/api"}`},
			"kr::" + expectedPath:                  {`{"type":"user","uuid":"u1","message":{"role":"user","content":"audit"}}`},
		},
	}
	fb.execHandler = func(_ string, p ExecParams) (ExecResult, error) {
		code := int32(0)
		if p.On == "kr" && strings.Contains(p.Line, "$HOME") && p.Env["HC_SID"] == "" {
			return ExecResult{Code: &code, Stdout: base64.StdEncoding.EncodeToString([]byte("/root"))}, nil
		}
		if p.Env["HC_SID"] == "the" {
			t.Fatalf("false-positive session ID 'the' extracted from prompt 'please resume the...'!")
		}
		return ExecResult{Code: &code}, nil
	}

	res, ok := resolver.Resolve(ctx, relayedClaudeKey, fb.terms[relayedClaudeKey].Terminal, fb)
	if !ok || res.Path != expectedPath || res.On != "kr" {
		t.Fatalf("expected relayed Claude resolved via $HOME fallback to %q, got ok=%v res=%+v", expectedPath, ok, res)
	}
}

// I2 + I3: >200-line bursts must be tailed completely in one RefreshSync, file truncation (offset > lines)
// must reload fresh instead of freezing, and LoadOlderSync must keep item IDs & expanded state stable!
func TestReviewFixLargeBurstTailFileTruncationAndStableIDsOnLoadOlder(t *testing.T) {
	ctx := context.Background()
	key := TermKey{InstanceID: "mac", Addr: "t9"}
	path := "/Users/dev/.claude/projects/-app/burst.jsonl"
	resolver := NewTranscriptResolver()
	resolver.SetHome("mac", "", "/Users/dev")

	initial := make([]string, 0, 300)
	for i := 1; i <= 299; i++ {
		initial = append(initial, fmt.Sprintf(`{"type":"user","uuid":"u-%d","message":{"role":"user","content":"msg-%d"}}`, i, i))
	}
	// Line 300 has thinking so we can expand it and verify LoadOlderSync does NOT collapse it!
	initial = append(initial, `{"type":"assistant","uuid":"a-300","message":{"id":"m-300","role":"assistant","content":[{"type":"thinking","thinking":"keep-me-expanded"}]}}`)

	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			key: {
				Key: key,
				Terminal: Terminal{
					Address:    "t9",
					Cwd:        "/app",
					State:      "live",
					Foreground: &Foreground{PID: 88, Cwd: "/app", Argv: []string{"claude"}},
					Status:     &Status{App: "claude-code", State: "working"},
				},
			},
		},
		files: map[string][]string{
			"::/Users/dev/.claude/sessions/88.json": {`{"pid":88,"sessionId":"burst","cwd":"/app"}`},
			"::" + path:                             initial,
		},
	}

	ctrl := NewChatSessionController(nil, key, fb, resolver, nil)
	defer ctrl.Close()
	_ = ctrl.RefreshSync(ctx)

	itemsBefore := ctrl.Items()
	thinkID := itemsBefore[len(itemsBefore)-1].ID
	ctrl.ToggleExpanded(thinkID)
	if !ctrl.IsExpanded(thinkID) {
		t.Fatal("expected thinking item expanded")
	}

	// Prepend earlier 50 lines via LoadOlderSync -> thinkID MUST remain identical and expanded!
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = ctrl.LoadOlderSync(ctx) }()
	go func() { defer wg.Done(); _ = ctrl.RefreshSync(ctx) }()
	wg.Wait()

	itemsAfterOlder := ctrl.Items()
	if itemsAfterOlder[len(itemsAfterOlder)-1].ID != thinkID || !ctrl.IsExpanded(thinkID) {
		t.Fatalf("LoadOlderSync shifted item ID (%q -> %q) and lost expanded state!", thinkID, itemsAfterOlder[len(itemsAfterOlder)-1].ID)
	}

	// Append 320 lines in a single burst (> 201 limit) -> single RefreshSync must catch all the way to line 620!
	fb.mu.Lock()
	for i := 301; i <= 620; i++ {
		fb.files["::"+path] = append(fb.files["::"+path], fmt.Sprintf(`{"type":"user","uuid":"u-%d","message":{"role":"user","content":"burst-%d"}}`, i, i))
	}
	fb.mu.Unlock()
	_ = ctrl.RefreshSync(ctx)
	tailItems := ctrl.Items()
	if tailItems[len(tailItems)-1].Text != "burst-620" {
		t.Fatalf("expected burst tail to reach burst-620 in one RefreshSync, stopped at %q", tailItems[len(tailItems)-1].Text)
	}

	// Truncate/rotate file down to 2 lines -> RefreshSync hits offset>lines and MUST reload fresh 2 lines!
	fb.mu.Lock()
	fb.files["::"+path] = []string{
		`{"type":"user","uuid":"rot-1","message":{"role":"user","content":"after-rotation-1"}}`,
		`{"type":"assistant","uuid":"rot-2","message":{"id":"m-rot","role":"assistant","content":[{"type":"text","text":"after-rotation-2"}]}}`,
	}
	fb.mu.Unlock()
	_ = ctrl.RefreshSync(ctx)
	rotItems := ctrl.Items()
	if len(rotItems) != 2 || rotItems[1].Text != "after-rotation-2" {
		t.Fatalf("expected truncated file to reload fresh 2 items, got %d: %+v", len(rotItems), rotItems)
	}
}

// Dogfood Fix 1: When a session finishes initial sync with 0 structured items (e.g. empty file or VT fallback
// on a blank screen), EmptyStateCard MUST transition out of "正在同步会话记录…" instead of freezing inside core.Show!
func TestDogfoodFixEmptyStateTransitionsOutOfLoadingWhenZeroItems(t *testing.T) {
	shellKey := TermKey{InstanceID: "mac", Addr: "t1"}
	var uiRef *uiController
	blockCh := make(chan struct{})

	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				shellKey: {
					Key: shellKey,
					Terminal: Terminal{
						Address:    "t1",
						Cwd:        "/app",
						State:      "live",
						Foreground: &Foreground{PID: 99, Name: "agy", Cwd: "/app", Argv: []string{"agy"}},
						Status:     &Status{App: "antigravity", State: "working"},
					},
				},
			},
		}
		fb.execHandler = func(_ string, _ ExecParams) (ExecResult, error) {
			<-blockCh
			code := int32(0)
			return ExecResult{Code: &code}, nil
		}
		a.SetTranscriptBackend(fb)
		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	h.Press("id:session-row-mac::t1")
	// While initial sync is still blocked, "正在同步会话记录…" is displayed.
	h.Find("text:正在同步会话记录…")

	// Unblock sync -> resolves 0 items (SourceVTFallback with empty screen).
	close(blockCh)
	_ = uiRef.activeChat.Get().RefreshSync(t.Context())
	h.Press("id:chat-source-pill")

	if len(h.Query("text:正在同步会话记录…")) != 0 {
		t.Fatal("Chat View stayed stuck on '正在同步会话记录…' after Source left SourceLoading with 0 items!")
	}
	h.Find("text:当前暂无结构化对话记录")
}

// Dogfood Fix 2: Transient timeout during initial relayed/Antigravity window read MUST NOT invalidate
// the already-resolved transcript path, and relayed/Antigravity sessions load a fast 80-line first paint.
func TestDogfoodFixRelayTimeoutPreservesResolvedPathAndUsesFastFirstPaint(t *testing.T) {
	key := TermKey{InstanceID: "kr", Addr: "mac-mini/t19"}
	agyPath := "/Users/dev/.gemini/antigravity-cli/brain/69a3f59e/.system_generated/logs/transcript.jsonl"
	lines := make([]string, 0, 300)
	for i := 1; i <= 300; i++ {
		lines = append(lines, fmt.Sprintf(`{"step_index":%d,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"msg-%d"}`, i, i))
	}

	var failNextWindow bool = true
	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			key: {
				Key:     key,
				Relayed: true,
				Terminal: Terminal{
					Address:    "mac-mini/t19",
					Cwd:        "/Users/dev",
					State:      "live",
					Foreground: &Foreground{PID: 72025, Name: "agy", Cwd: "/workspace/herdr-nsgo", Argv: []string{"agy"}},
					Status:     &Status{App: "antigravity", State: "working"},
				},
			},
		},
		files: map[string][]string{
			"mac-mini::" + agyPath: lines,
		},
	}
	fb.execHandler = func(_ string, p ExecParams) (ExecResult, error) {
		code := int32(0)
		if p.On == "mac-mini" {
			return ExecResult{Code: &code, Stdout: base64.StdEncoding.EncodeToString([]byte(agyPath + "\n"))}, nil
		}
		return ExecResult{Code: &code}, nil
	}

	// Wrap fb to fail the multi-line window read once with DeadlineExceeded (simulating slow 5G DERP hop).
	timeoutBackend := &flakyWindowBackend{TranscriptBackend: fb, failOnce: &failNextWindow}
	resolver := NewTranscriptResolver()
	ctrl := NewChatSessionController(nil, key, timeoutBackend, resolver, nil)
	defer ctrl.Close()

	// 1st sync: Resolve succeeds (1 MachineExec), probe succeeds, 80-line window read times out!
	_ = ctrl.RefreshSync(context.Background())
	if fb.execCalls != 1 {
		t.Fatalf("expected 1 MachineExec on first sync, got %d", fb.execCalls)
	}

	// 2nd sync: MUST reuse resolved path (0 extra MachineExec!) and load fast 80-line initial tail!
	if err := ctrl.RefreshSync(context.Background()); err != nil {
		t.Fatalf("second RefreshSync failed: %v", err)
	}
	if fb.execCalls != 1 {
		t.Fatalf("timeout wiped resolved path and caused redundant MachineExec (got %d calls, want 1)!", fb.execCalls)
	}
	if ctrl.Source() != SourceJSONL || len(ctrl.Items()) != 80 || ctrl.OlderRemaining() != 220 {
		t.Fatalf("expected SourceJSONL with 80 fast first-paint items and 220 older remaining, got source=%v len=%d older=%d",
			ctrl.Source(), len(ctrl.Items()), ctrl.OlderRemaining())
	}
}

type flakyWindowBackend struct {
	TranscriptBackend
	mu       sync.Mutex
	failOnce *bool
}

func (f *flakyWindowBackend) FileRead(ctx context.Context, instanceID string, p FileReadParams) (FileReadResult, error) {
	f.mu.Lock()
	shouldFail := p.Limit > 1 && f.failOnce != nil && *f.failOnce
	if shouldFail {
		*f.failOnce = false
	}
	f.mu.Unlock()
	if shouldFail {
		return FileReadResult{}, context.DeadlineExceeded
	}
	return f.TranscriptBackend.FileRead(ctx, instanceID, p)
}

// Dogfood Fix 3: Antigravity resolver MUST skip SYSTEM_SDK subagent transcripts and match USER_EXPLICIT
// root sessions even when HC_CWD only appears past line 5 (after cd from $HOME).
func TestDogfoodFixAntigravityResolverSkipsSubagentsAndMatchesLateCwd(t *testing.T) {
	tmpHome := t.TempDir()
	rootTranscript := tmpHome + "/.gemini/antigravity-cli/brain/69a3f59e/.system_generated/logs/transcript.jsonl"
	subTranscript := tmpHome + "/.gemini/antigravity-cli/brain/9d0de77e/.system_generated/logs/transcript.jsonl"

	writeTestLines(t, rootTranscript, []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"hello from home"}`,
		`{"step_index":1,"source":"SYSTEM_SDK","type":"SYSTEM_MESSAGE","content":"line 2"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","content":"line 3"}`,
		`{"step_index":3,"source":"MODEL","type":"GENERIC","content":"line 4"}`,
		`{"step_index":4,"source":"MODEL","type":"GENERIC","content":"line 5"}`,
		`{"step_index":5,"source":"MODEL","type":"GENERIC","content":"line 6"}`,
		`{"step_index":6,"source":"MODEL","type":"GENERIC","content":"cd /Volumes/ExternalSSD/workspace/herdr-nsgo"}`,
	})
	// Subagent transcript is modified MORE RECENTLY and mentions cwd on line 1, but is SYSTEM_SDK!
	writeTestLines(t, subTranscript, []string{
		`{"step_index":0,"source":"SYSTEM_SDK","type":"SYSTEM_MESSAGE","content":"Subagent in /Volumes/ExternalSSD/workspace/herdr-nsgo"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","content":"reviewing"}`,
	})

	key := TermKey{InstanceID: "mac", Addr: "t19"}
	term := Terminal{
		Address:    "t19",
		Cwd:        tmpHome,
		State:      "live",
		Foreground: &Foreground{PID: 999999, Name: "agy", Cwd: "/Volumes/ExternalSSD/workspace/herdr-nsgo", Argv: []string{"agy"}},
		Status:     &Status{App: "antigravity", State: "working"},
	}
	shellBackend := &localShellExecBackend{home: tmpHome, term: term}
	resolver := NewTranscriptResolver()
	res, ok := resolver.Resolve(context.Background(), key, term, shellBackend)
	if !ok || res.Path != rootTranscript {
		t.Fatalf("expected USER_EXPLICIT root session %q, got ok=%v path=%q (picked subagent!)", rootTranscript, ok, res.Path)
	}
}

// Dogfood Fix 4: Non-login SSH remote command MUST locate hc / herdr-core inside ~/.local/bin
// even when sshd's minimal PATH is only /usr/bin:/bin (macOS Remote Login default).
func TestDogfoodFixSSHRemoteCommandFindsLocalBinHC(t *testing.T) {
	tmpHome := t.TempDir()
	localBin := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeHC := filepath.Join(localBin, "hc")
	if err := os.WriteFile(fakeHC, []byte("#!/bin/sh\nprintf 'FAKE_HC_ARGS=%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", "-c", sshConnectCommand())
	cmd.Env = []string{
		"HOME=" + tmpHome,
		"PATH=/usr/bin:/bin",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sshConnectCommand failed under minimal macOS sshd PATH: %v (%s)", err, string(out))
	}
	if !strings.Contains(string(out), "FAKE_HC_ARGS=connect") {
		t.Fatalf("expected ~/.local/bin/hc connect invoked, got %q", string(out))
	}
}

func writeTestLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure deterministic mtime gap when multiple files are created back-to-back.
	time.Sleep(15 * time.Millisecond)
}

type localShellExecBackend struct {
	home string
	term Terminal
}

func (l *localShellExecBackend) FindTerminal(key TermKey) (AggregatedTerminal, bool) {
	return AggregatedTerminal{Key: key, Terminal: l.term}, true
}

func (l *localShellExecBackend) FileRead(_ context.Context, _ string, p FileReadParams) (FileReadResult, error) {
	raw, err := os.ReadFile(p.Path)
	if err != nil {
		e := &RPCError{Message: "not_found"}
		e.Data.Kind = "not_found"
		return FileReadResult{}, e
	}
	lines := splitJSONLLines(string(raw))
	return FileReadResult{Text: string(raw), First: 1, Last: len(lines), Lines: len(lines)}, nil
}

func (l *localShellExecBackend) MachineExec(ctx context.Context, _ string, p ExecParams) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", p.Line)
	env := append(os.Environ(), "HOME="+l.home)
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	out, _ := cmd.Output()
	code := int32(0)
	return ExecResult{
		Code:   &code,
		Stdout: base64.StdEncoding.EncodeToString(out),
	}, nil
}

func (l *localShellExecBackend) ReadTerminal(_ context.Context, _ TermKey, _ int) (ReadResult, error) {
	return ReadResult{}, nil
}

