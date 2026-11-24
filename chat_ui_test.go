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
