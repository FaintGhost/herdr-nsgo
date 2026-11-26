package main

import (
	"fmt"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestUISearchSkillInLargePiMenu(t *testing.T) {
	var draft *core.Signal[string]
	key := TermKey{InstanceID: "local", Addr: "t1"}
	terminal := AggregatedTerminal{Key: key, Terminal: Terminal{Argv: []string{"pi"}}}
	var a *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = NewApp(cx)
		draft = core.NewSignal(cx, "/")
		c := NewChatSessionController(cx, key, nil, nil, nil)
		for i := range 125 {
			c.piSkills = append(c.piSkills, slashEntry{Name: fmt.Sprintf("skill:task-%03d", i), Description: "项目技能"})
		}
		return slashCommandBar(cx, a, draft.Get, func() AggregatedTerminal { return terminal }, func() *ChatSessionController { return c }, func() bool { return true })
	}, nsgotest.Size(360, 520))
	h.Find("id:slash-command-list")
	draft.Set("/task-124")
	h.Settle()
	h.Press("id:slash-command-skill:task-124")
	if got := a.Compose(key.String()); got != "/skill:task-124 " {
		t.Fatalf("selected skill %q", got)
	}
}

func TestUIChatSlashCommands(t *testing.T) {
	var app *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		app = a
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		return root
	}, nsgotest.Size(390, 844))
	const editor = "id:session-composer"
	h.Press("id:session-row-mac::t1")
	h.Find("id:chat-model-selector")
	h.Find("id:chat-effort-selector")
	h.Press("id:chat-model-selector")
	h.Find("id:chat-model-sheet")
	h.Find("id:chat-model-close")
	h.Press("id:chat-model-close")
	h.Type(editor, "检查进度")
	h.Press("id:chat-effort-selector")
	h.Find("id:chat-model-sheet")
	if len(h.Query("id:model-apply")) != 0 {
		t.Fatal("opening Effort must not arm a default choice")
	}
	h.Press("id:chat-model-close")
	if app.Compose("mac::t1") != "检查进度" {
		t.Fatal("cancel lost draft")
	}
	if len(h.Query("id:chat-mode-toggle")) != 0 {
		t.Fatal("chat-mode-toggle must be moved to Settings instead of cluttering the composer")
	}
	h.Press("id:session-send-btn")
	h.Find("id:chat-delivery-status")
	h.Find("label:正在发送")
	if len(h.Query("id:chat-sending-indicator")) != 1 {
		t.Fatal("expected a single sending indicator in the composer toolbar")
	}
	// Default send mode is 等待 (chatQueue), so the waiting bubble offers a 1-tap "插队" action!
	h.Find("text:等待中")
	h.Press("id:chat-steer-opt-1")
	h.Settle()
	h.Find("text:已插队")
	if len(h.Query("id:chat-steer-opt-1")) != 0 {
		t.Fatal("expected steer button hidden once message is already steered")
	}
	h.Type(editor, "/")
	if len(h.Query("id:slash-command-help")) != 1 {
		t.Fatal("typing / in a Claude chat should offer /help")
	}
	h.Find("id:slash-command-compact")
	h.Find("text:压缩上下文")
	h.Screenshot("chat-commands")
	first, second := h.Find("id:slash-command-help").Bounds(), h.Find("id:slash-command-compact").Bounds()
	if second.Origin.Y < first.Origin.Y+first.Size.Height {
		t.Fatal("command suggestions must stack vertically")
	}
	before := h.Find("id:session-composer").Bounds().Size.Height
	h.Drag("id:slash-command-compact", core.Point{Y: -20}, core.Point{Y: -50}, core.Point{Y: -100})
	if h.Find("id:slash-command-list").Offset.Y <= 0 || h.Find("id:session-composer").Bounds().Size.Height != before {
		t.Fatal("dragging the candidates must scroll the list without expanding the editor")
	}
	h.Type(editor, "co")
	h.Find("id:slash-command-compact")
	if len(h.Query("id:slash-command-help")) != 0 {
		t.Fatal("/co should filter out /help")
	}
	h.Press("id:slash-command-compact")
	if got := app.Compose("mac::t1"); got != "/compact " {
		t.Fatalf("selected command draft = %q", got)
	}
	if len(h.Query("id:slash-command-compact")) != 0 {
		t.Fatal("completion should close while editing arguments")
	}
	h.Type(editor, "保留测试结果")
	h.Press("id:session-send-btn")
	h.Find("text:/^\\/compact 保留测试结果$/")

	h.Type(editor, "/my-custom-command")
	h.Submit(editor)
	h.Find("text:/^\\/my-custom-command$/")
	h.Type(editor, "/")
	h.Press("id:session-mode-vt")
	if len(h.Query("id:slash-command-help")) != 0 {
		t.Fatal("terminal mode should leave slash input alone")
	}
	h.Press("id:session-back-btn")
	h.Press("id:session-row-kr::t8")
	h.Type(editor, "/di")
	h.Find("id:slash-command-diff")
	if got := app.Compose("mac::t1"); got != "/" {
		t.Fatalf("switching to Codex changed the Claude draft to %q", got)
	}
}
