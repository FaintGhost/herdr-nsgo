package main

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestUIChatViewDefaultSwitchExpandAndObserveSafeSend(t *testing.T) {
	claudeKey := TermKey{InstanceID: "mac", Addr: "t1"}
	shellKey := TermKey{InstanceID: "mac", Addr: "t2"}
	claudePath := "/Users/dev/.claude/projects/-Volumes-ExternalSSD-workspace-herdr-nsgo/sess-1.jsonl"

	var appRef *App
	var uiRef *uiController

	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		appRef = a

		// Add a plain zsh shell terminal alongside the claude agent terminal on mac.
		snaps := a.instances.Get()
		for i := range snaps {
			if snaps[i].InstanceID == "mac" {
				snaps[i].Terminals = append(snaps[i].Terminals, Terminal{
					Address:    "t2",
					Name:       "ops-shell",
					Cwd:        "/tmp",
					State:      "live",
					Foreground: &Foreground{PID: 901, Name: "zsh", Cwd: "/tmp"},
				})
				snaps[i].Terminals[0].Foreground = &Foreground{
					PID:  34488,
					Name: "2.1.330",
					Cwd:  "/Volumes/ExternalSSD/workspace/herdr-nsgo",
					Argv: []string{"claude"},
				}
			}
		}
		a.SeedSnapshots(snaps)

		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				claudeKey: {Key: claudeKey, Terminal: snaps[1].Terminals[0]},
				shellKey:  {Key: shellKey, Terminal: snaps[1].Terminals[1]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {
					`{"pid":34488,"sessionId":"sess-1","cwd":"/Volumes/ExternalSSD/workspace/herdr-nsgo"}`,
				},
				"::" + claudePath: {
					`{"type":"user","uuid":"u1","message":{"role":"user","content":"请帮我实现 Chat View"}}`,
					`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"thinking","thinking":"详细思维链：先设计四合一解析器"}]}}`,
					`{"type":"assistant","uuid":"a2","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Read","input":{"file_path":"/repo/screen_session.go"}}]}}`,
					`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"1: package main\n2: // session view"}]}}`,
					`{"type":"assistant","uuid":"a3","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"已完成分析，核心示例：\n` + "```go\\nfunc Chat() {}\\n```" + `"}]}}`,
				},
			},
		}
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)

		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	// 1. Open Claude session mac::t1 -> must default to Chat View!
	h.Press("id:session-row-mac::t1")
	if uiRef != nil && uiRef.activeChat.Get() != nil {
		_ = uiRef.activeChat.Get().RefreshSync(t.Context())
	}
	h.Find("id:screen-session")
	h.Find("id:session-mode-chat")
	h.Find("id:session-mode-vt")
	h.Find("id:chat-surface")

	// KeycapBar must be hidden in Chat mode, and empty composer while "working" shows Interrupt button!
	if len(h.Query("id:keycap-esc")) != 0 {
		t.Fatal("expected KeycapBar hidden in Chat View")
	}
	h.Find("id:session-interrupt-btn")

	// User bubble, assistant reply, model label, and code block render cleanly.
	h.Find("text:请帮我实现 Chat View")
	h.Find("text:claude-opus-4-7")
	h.Find("text:/已完成分析/")
	h.Find("text:/func Chat/")

	// 2. Thinking & Tool chips start collapsed, and expand on press!
	if len(h.Query("text:/详细思维链/")) != 0 {
		t.Fatal("expected thinking body collapsed initially")
	}
	h.Press("id:chat-thinking-m1-th-1-0")
	h.Find("text:/详细思维链/")

	if len(h.Query("text:/session view/")) != 0 {
		t.Fatal("expected tool output collapsed initially")
	}
	h.Press("id:chat-tool-m1-tg-2-0")
	h.Find("text:/session view/")

	// 3. Typing in composer swaps Interrupt -> Send, and sending in Chat mode preserves SessionTerminal.Observe() == true!
	st := uiRef.activeTerm.Get()
	if st == nil || !st.Observe() {
		t.Fatal("expected background SessionTerminal attached in observe=true mode")
	}
	h.Type("id:session-composer", "继续完成测试")
	h.Press("id:session-send-btn")
	h.Find("text:继续完成测试")
	if !st.Observe() {
		t.Fatal("sending from Chat View MUST NOT exit observe mode or resize desktop PTY")
	}

	// 4. Switch to Terminal mode -> id:vt-surface & KeycapBar appear, remembered per session!
	h.Press("id:session-mode-vt")
	h.Find("id:vt-surface")
	h.Find("id:keycap-esc")

	// Go back to Home and open plain zsh shell mac::t2 -> defaults to Terminal View!
	h.Press("id:session-back-btn")
	h.Press("id:session-row-mac::t2")
	h.Find("id:vt-surface")

	// Go back and re-open mac::t1 -> remembers user's manual switch to Terminal View!
	h.Press("id:session-back-btn")
	h.Press("id:session-row-mac::t1")
	h.Find("id:vt-surface")

	// Switch back to Chat View and test blocked question card ("进入终端处理" switches to VT!).
	h.Press("id:session-mode-chat")
	h.Find("id:chat-surface")
	h.Press("id:session-back-btn")

	h.Press("id:session-row-kr::t8")
	h.Find("id:chat-surface")
	h.Press("id:attention-open-kr::t8")
	h.Find("id:vt-surface")
	_ = appRef
}

func TestUISessionZeroRemountSwitchAndStableHeader(t *testing.T) {
	claudeKey := TermKey{InstanceID: "mac", Addr: "t1"}
	claudePath := "/Users/dev/.claude/projects/-Volumes-ExternalSSD-workspace-herdr-nsgo/perf.jsonl"

	var uiRef *uiController

	// Build 160 JSONL turns so we also verify viewport windowing keeps mounted cards bounded!
	manyLines := make([]string, 0, 160)
	for i := 1; i <= 160; i++ {
		if i%2 == 1 {
			manyLines = append(manyLines, `{"type":"user","uuid":"pu-`+string(rune('0'+i%10))+`-`+itoaTest(i)+`","message":{"role":"user","content":"turn-msg-`+itoaTest(i)+`"}}`)
		} else {
			manyLines = append(manyLines, `{"type":"assistant","uuid":"pa-`+itoaTest(i)+`","message":{"id":"pm-`+itoaTest(i)+`","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"reply-msg-`+itoaTest(i)+`"}]}}`)
		}
	}

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

		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				claudeKey: {Key: claudeKey, Terminal: snaps[1].Terminals[0]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {
					`{"pid":34488,"sessionId":"perf","cwd":"/Volumes/ExternalSSD/workspace/herdr-nsgo"}`,
				},
				"::" + claudePath: manyLines,
			},
		}
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)

		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	h.Press("id:session-row-mac::t1")
	_ = uiRef.activeChat.Get().RefreshSync(t.Context())
	h.Press("id:chat-source-pill")

	// 1. Both chat-surface and vt-surface MUST stay mounted simultaneously (DisplayNone swap, zero remount!).
	if len(h.QueryMounted("id:chat-surface")) != 1 || len(h.QueryMounted("id:vt-surface")) != 1 {
		t.Fatalf("expected BOTH chat-surface and vt-surface kept mounted for 0ms switching, got chat=%d vt=%d",
			len(h.QueryMounted("id:chat-surface")), len(h.QueryMounted("id:vt-surface")))
	}

	// 2. Viewport windowing: latest turn (160) is mounted, while turn 1 is held in memory unmounted until requested!
	h.Find("text:/^reply-msg-160$/")
	if len(h.QueryMounted("text:/^turn-msg-1$/")) != 0 {
		t.Fatal("expected turn-msg-1 outside initial 36-item render window so switching stays instant")
	}

	// 3. Record TopHeader segmented pill bounds in Chat mode, switch to VT mode, and assert ZERO layout shift!
	chatModePillBounds := h.Find("id:session-mode-chat").Bounds()
	vtModePillBounds := h.Find("id:session-mode-vt").Bounds()

	h.Press("id:session-mode-vt")
	h.Find("id:vt-surface")
	h.Find("id:session-fit-toggle")

	// Chat surface must still be mounted (not destroyed!) while hidden!
	if len(h.QueryMounted("id:chat-surface")) != 1 {
		t.Fatal("switching to VT mode destroyed chat-surface instead of hiding via DisplayNone!")
	}

	afterChatPill := h.Find("id:session-mode-chat").Bounds()
	afterVTPill := h.Find("id:session-mode-vt").Bounds()
	if afterChatPill != chatModePillBounds || afterVTPill != vtModePillBounds {
		t.Fatalf("TopHeader mode switcher shifted position/size on mode toggle! before=(%+v, %+v) after=(%+v, %+v)",
			chatModePillBounds, vtModePillBounds, afterChatPill, afterVTPill)
	}

	// Switch back to Chat -> instant visibility flip, still zero header shift!
	h.Press("id:session-mode-chat")
	h.Find("id:chat-surface")
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
