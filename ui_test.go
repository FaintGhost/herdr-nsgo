package main

import (
	"testing"

	"github.com/nsgo-dev/nsgo/nsgotest"
)

// TestHomeEmptyStateIsAnInstruction walks the app as a person does on a fresh
// install: the empty state has to name the next action, and taking it has to
// lead somewhere.
func TestHomeEmptyStateIsAnInstruction(t *testing.T) {
	app := nsgotest.Start(t, Root)

	app.Find("role:header herdr")
	app.Find("text:还没有活跃会话")
	app.Find("text:还没有机器")
	// The banner says what is missing and offers the repair.
	app.Find("text:还没连上核心")
	app.Find("role:button 重连")

	app.Press("role:button 连接一台机器")
	app.Find("text:添加机器")
	// Each path states its cost before asking for anything.
	app.Find("text:推荐 · 约1分钟")
	app.Find("text:只用于桌面端 · 需要本机装 herdr-core")
	app.Find("role:button 用本机")
}

// TestInboxEmptyStateExplainsItself checks the inbox only claims work when a
// terminal waits on a person.
func TestInboxEmptyStateExplainsItself(t *testing.T) {
	app := nsgotest.Start(t, Root)
	app.Press("role:button 收件箱")
	app.Find("text:没有等你的事")
}

// TestStateWords names the states the way the screens show them.
func TestStateWords(t *testing.T) {
	cases := []struct {
		term Terminal
		want string
	}{
		{Terminal{State: "working"}, "跑着"},
		{Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "permission"}}, "等你"},
		{Terminal{Status: &Status{State: "error"}}, "出错"},
		{Terminal{State: "idle"}, "空闲"},
	}
	for _, c := range cases {
		if got := stateWord(c.term); got != c.want {
			t.Errorf("stateWord(%+v) = %q, want %q", c.term, got, c.want)
		}
	}
	if !(Terminal{State: "blocked"}).NeedsYou() || (Terminal{State: "working"}).NeedsYou() {
		t.Error("NeedsYou does not select exactly the waiting terminals")
	}
}
