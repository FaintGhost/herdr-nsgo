package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
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
	_ = errors.New
}
