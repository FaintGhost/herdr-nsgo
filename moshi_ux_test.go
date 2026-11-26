package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestStripHerdrScrollbar(t *testing.T) {
	herdrPane := strings.Join([]string{
		"func main() {                  │",
		"    println(\"hello herdr\")     █",
		"}                              ░",
	}, "\n")
	got := StripHerdrScrollbar(herdrPane)
	for _, bar := range []string{"│", "█", "░"} {
		if strings.Contains(got, bar) {
			t.Fatalf("expected scrollbar %q stripped, got:\n%s", bar, got)
		}
	}
	if !strings.Contains(got, `println("hello herdr")`) {
		t.Fatalf("unexpected stripped output:\n%s", got)
	}

	// Normal Markdown ASCII table must NOT be stripped!
	mdTable := "| Name | Value |\n| --- | --- |\n| fps | 120 |"
	if StripHerdrScrollbar(mdTable) != mdTable {
		t.Fatalf("expected Markdown ASCII table preserved, got:\n%s", StripHerdrScrollbar(mdTable))
	}
}

func TestToolDiffExtractionAcrossHarnesses(t *testing.T) {
	// 1. Claude Edit
	claudeItems := ParseClaudeJSONL([]string{
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Edit","input":{"file_path":"/repo/app.go","old_string":"oldVal := 1","new_string":"newVal := 2"}}]}}`,
	})
	if len(claudeItems) != 1 || len(claudeItems[0].Tools) != 1 {
		t.Fatalf("unexpected claude items: %+v", claudeItems)
	}
	tc := claudeItems[0].Tools[0]
	if !tc.HasDiff() || tc.DiffFile != "/repo/app.go" || tc.DiffOld != "oldVal := 1" || tc.DiffNew != "newVal := 2" {
		t.Fatalf("expected Claude Edit diff extracted, got %+v", tc)
	}

	// 2. Antigravity edit_file
	agyItems := ParseAntigravityJSONL([]string{
		`{"step_index":4,"type":"PLANNER_RESPONSE","model":"gemini-3-pro","tool_calls":[{"name":"edit_file","args":{"Path":"/repo/theme.go","Target":"Bg: old","Replacement":"Bg: new"}}]}`,
	})
	if len(agyItems) != 1 || len(agyItems[0].Tools) != 1 || !agyItems[0].Tools[0].HasDiff() {
		t.Fatalf("expected Antigravity edit_file diff extracted, got %+v", agyItems)
	}

	// 3. Codex apply_patch
	codexPatch := "*** Begin Patch\n*** Update File: main.go\n@@\n-foo()\n+bar()\n*** End Patch"
	codexLine := fmt.Sprintf(`{"type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":%q}}`, codexPatch)
	codexItems := ParseCodexJSONL([]string{codexLine})
	if len(codexItems) != 1 || len(codexItems[0].Tools) != 1 || !codexItems[0].Tools[0].HasDiff() {
		t.Fatalf("expected Codex apply_patch diff extracted, got %+v", codexItems)
	}
	if codexItems[0].Tools[0].DiffFile != "main.go" || codexItems[0].Tools[0].DiffOld != "foo()" || codexItems[0].Tools[0].DiffNew != "bar()" {
		t.Fatalf("unexpected Codex apply_patch diff fields: %+v", codexItems[0].Tools[0])
	}
}

func TestSnapshotStoreInstantOpenAndLRU(t *testing.T) {
	store := NewSnapshotStore(nil)
	targetKey := TermKey{InstanceID: "mac", Addr: "t19"}
	store.SaveChat(targetKey, ChatSnapshot{
		Source:       SourceJSONL,
		Format:       FormatAntigravity,
		ResolvedPath: "/Users/dev/.gemini/antigravity-cli/brain/abc/.system_generated/logs/transcript.jsonl",
		Items: []ChatTurnItem{
			{ID: "u-cached", Role: RoleUser, Text: "缓存秒开测试"},
			{ID: "a-cached", Role: RoleAssistant, Text: "Frame 0 立即上屏！", Model: "gemini-3.1-pro"},
		},
	})

	// Seed a brand-new ChatSessionController BEFORE any RefreshSync runs -> 0ms Frame-0 items!
	ctrl := NewChatSessionController(nil, targetKey, nil, nil, nil)
	if online := NewChatSessionController(nil, targetKey, nil, nil, nil); online.SeedFromSnapshot(store, &Terminal{Status: &Status{State: "working"}}) || len(online.Items()) > 0 {
		t.Fatal("working session opened on old cached messages")
	}
	if !ctrl.SeedFromSnapshot(store, nil) {
		t.Fatal("expected SeedFromSnapshot to hit cached snapshot")
	}
	if !ctrl.IsCachedSnapshot() {
		t.Fatal("expected IsCachedSnapshot() == true before live RefreshSync")
	}
	items := ctrl.Items()
	if len(items) != 2 || items[1].Text != "Frame 0 立即上屏！" {
		t.Fatalf("expected cached items immediately available in 0ms, got %+v", items)
	}

	// Verify LRU cap (max 24 keys).
	for i := 0; i < 30; i++ {
		k := TermKey{InstanceID: "inst", Addr: fmt.Sprintf("t-%d", i)}
		store.SaveVT(k, []byte(fmt.Sprintf("vt-%d", i)))
	}
	if _, ok := store.LoadChat(targetKey); ok {
		t.Fatal("expected oldest key evicted past 24-entry LRU cap")
	}
	if raw, ok := store.LoadVT(TermKey{InstanceID: "inst", Addr: "t-29"}); !ok || string(raw) != "vt-29" {
		t.Fatalf("expected newest VT snapshot retained, got ok=%v raw=%q", ok, string(raw))
	}
}

func TestUIMoshiGlassComposerDiffSheetQuickSwitcherAndSubagents(t *testing.T) {
	parentKey := TermKey{InstanceID: "mac", Addr: "t1"}
	childKey := TermKey{InstanceID: "mac", Addr: "t9"}
	claudePath := "/Users/dev/.claude/projects/-Volumes-ExternalSSD-workspace-herdr-nsgo/sess-moshi.jsonl"
	parentAddr := "t1"

	var uiRef *uiController

	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		snaps := a.instances.Get()
		for i := range snaps {
			if snaps[i].InstanceID == "mac" {
				snaps[i].Terminals[0].Foreground = &Foreground{
					PID:  34488,
					Name: "2.1.330",
					Cwd:  "/Volumes/ExternalSSD/workspace/herdr-nsgo",
					Argv: []string{"claude"},
				}
				snaps[i].Terminals = append(snaps[i].Terminals, Terminal{
					Address:    "t9",
					Name:       "subagent-reviewer",
					Cwd:        "/Volumes/ExternalSSD/workspace/herdr-nsgo",
					State:      "working",
					Parent:     &parentAddr,
					Status:     &Status{State: "working", App: "claude", Msg: "Reviewing diff"},
					Foreground: &Foreground{PID: 34499, Name: "claude", Argv: []string{"claude"}},
				})
			}
		}
		a.SeedSnapshots(snaps)

		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				parentKey: {Key: parentKey, Terminal: snaps[1].Terminals[0]},
				childKey:  {Key: childKey, Terminal: snaps[1].Terminals[1]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {
					`{"pid":34488,"sessionId":"sess-moshi","cwd":"/Volumes/ExternalSSD/workspace/herdr-nsgo"}`,
				},
				"::" + claudePath: {
					`{"type":"user","uuid":"u1","message":{"role":"user","content":"重构聊天界面"}}`,
					`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Edit","input":{"file_path":"/repo/screen_chat.go","old_string":"oldUI()","new_string":"moshiGlassUI()"}}]}}`,
					`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Updated screen_chat.go"}]}}`,
					`{"type":"assistant","uuid":"a2","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"已完成悬浮毛玻璃与 Diff 视图升级。"}]}}`,
				},
			},
		}
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)

		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	// 1. Open parent session -> verify Floating Glass Composer & Subagent Strip!
	h.Press("id:session-row-mac::t1")
	_ = uiRef.activeChat.Get().RefreshSync(t.Context())
	h.Find("id:chat-glass-composer")
	h.Find("id:chat-subagent-strip")
	h.Find("text:DIFF")

	// 2. Expand Edit tool -> open swipeable Fullscreen Diff Sheet -> close it!
	h.Press("id:chat-tool-m1-tg-1-0")
	h.Press("id:chat-open-diff-m1-tg-1-0")
	h.Find("id:sheet-diff-viewer")
	h.Press("id:sheet-diff-close")

	// 3. Tap Quote action on assistant card -> quote preview appears in floating composer!
	h.Press("id:chat-quote-m1-txt-3-0")
	h.Find("id:composer-quote-banner")
	h.Press("id:composer-quote-clear")

	// 4. Tap Subagent chip -> jumps directly to child session mac::t9 -> tap "返回父会话" -> back to mac::t1!
	h.Press("id:subagent-chip-mac::t9")
	h.Find("id:chat-back-parent-btn")
	h.Press("id:chat-back-parent-btn")
	if uiRef.activeSession.Get() != parentKey {
		t.Fatalf("expected back at parent %v, got %v", parentKey, uiRef.activeSession.Get())
	}

	// The mode switch in the session header opens the terminal.
	h.Press("id:session-mode-vt")
	h.Find("id:vt-surface")
	h.Find("id:keycap-rightclick")

	// 6. Open Quick Session Switcher in header -> jump directly to kr::t7 without going back to Home!
	h.Press("id:session-switcher-btn")
	h.Find("id:sheet-session-switcher")
	h.Press("id:switcher-item-kr::t7")
	if uiRef.activeSession.Get() != (TermKey{InstanceID: "kr", Addr: "t7"}) {
		t.Fatalf("expected Quick Switcher to open kr::t7, got %v", uiRef.activeSession.Get())
	}
}
