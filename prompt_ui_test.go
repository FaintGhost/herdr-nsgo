package main

import (
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestChatMultiOptionAgyPermissionAndInlineOtherAndAskQuestionUI(t *testing.T) {
	agyKey := TermKey{InstanceID: "mac", Addr: "t1"}
	agyScreen := strings.Join([]string{
		"Tool execution requires confirmation",
		"run_command: git push origin main",
		"❯ 1. Allow once",
		"  2. Allow for this session",
		"  3. Always allow (save to config)",
		"  4. Deny",
		"↑/↓ to navigate · Enter to select · Esc to cancel",
	}, "\n")
	agyPrompt, ok := ParseInteractivePrompt(agyScreen, "antigravity", "permission")
	if !ok {
		t.Fatal("failed to parse Agy prompt")
	}

	var uiRef *uiController
	var appRef *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		appRef = a
		snaps := a.instances.Get()
		snaps[1].Terminals = []Terminal{{
			Address:    "t1",
			Name:       "agy-release",
			Cwd:        "/repo",
			State:      "blocked",
			Status:     &Status{State: "blocked", Kind: "permission", App: "antigravity", Msg: "git push origin main"},
			Foreground: &Foreground{PID: 500, Name: "agy", Cwd: "/repo"},
		}}
		a.SeedSnapshots(snaps)
		a.SetTestPrompt(agyKey, agyPrompt)
		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	// 1. On Home/Inbox compact card: shows 36pt fast actions + "展开全部 4 项" toggle.
	h.Find("id:approve-allow-mac::t1")
	h.Find("id:approve-expand-mac::t1")
	h.Press("id:approve-expand-mac::t1")
	h.Settle()
	h.Find("text:Allow for this session")
	h.Find("text:Always allow (save to config)")

	// 2. Open session in Chat View -> Full vertical 4-option permission card renders all 4 options directly!
	uiRef.openSession(appRef.cx, agyKey)
	h.Settle()
	h.Find("id:chat-prompt-card-mac::t1")
	h.Find("id:prompt-opt-mac::t1-0")
	h.Find("id:prompt-opt-mac::t1-1")
	h.Find("id:prompt-opt-mac::t1-2")
	h.Find("id:prompt-opt-mac::t1-3")
	h.Find("text:实时 4 项")

	// 3. Switch prompt to Pi multi-select AskUserQuestion with inline Other & tabs!
	qScreen := strings.Join([]string{
		"Feature Type  [Testing]  Submit",
		"Which checks should we run?",
		"❯ 1. [x] Unit tests",
		"  2. [ ] Integration tests",
		"  3. Type something.",
		"  Next",
		"Space to toggle · Tab to switch · Esc to cancel",
	}, "\n")
	qPrompt, ok := ParseInteractivePrompt(qScreen, "pi", "question")
	if !ok {
		t.Fatal("failed to parse Pi question prompt")
	}
	snaps := appRef.instances.Get()
	snaps[1].Terminals[0].Status = &Status{State: "blocked", Kind: "question", App: "pi", Msg: "Which checks should we run?"}
	appRef.SeedSnapshots(snaps)
	appRef.SetTestPrompt(agyKey, qPrompt)
	h.Settle()

	h.Find("text:Which checks should we run?")
	h.Find("id:prompt-multi-submit-mac::t1")
	h.Find("id:prompt-tab-prev-mac::t1")
	h.Find("id:prompt-other-input-mac::t1")
}
