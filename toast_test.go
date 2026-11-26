package main

import (
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestClassifyToastKind(t *testing.T) {
	cases := []struct {
		msg  string
		want toastKind
	}{
		{"已发送允许", toastSuccess},
		{"已插队优先处理", toastSuccess},
		{"已复制链接：https://example.com", toastSuccess},
		{"已切换模型：Claude Opus 4.7", toastSuccess},
		{"已发送拒绝", toastWarn},
		{"状态已变化，已取消直操，请进终端确认。", toastWarn},
		{"实例离线，附件和文字已保留在草稿中。", toastWarn},
		{"插队失败：connection reset", toastError},
		{"无法拍照：permission denied", toastError},
		{"该实例尚未连上。", toastError},
		{"正在上传 demo.png…", toastInfo},
		{"Ctrl 已双击锁定（再点一次解除）", toastInfo},
	}
	for _, tc := range cases {
		if got := classifyToastKind(tc.msg); got != tc.want {
			t.Errorf("classifyToastKind(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestFloatingToastAutoDismissAndSupersede(t *testing.T) {
	var app *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		app = NewApp(cx)
		app.toastDurationOverride = 60 * time.Millisecond
		return RootWithApp(cx, app)
	}, nsgotest.Size(390, 844))

	if len(h.Query("id:toast-banner")) != 0 {
		t.Fatal("toast-banner should not be visible initially")
	}

	app.notifyToast("已发送允许")
	h.Settle()
	h.Find("id:toast-banner")
	h.Find("text:已发送允许")

	// Wait past auto-dismiss duration: toast must clear on its own without user action.
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		h.Settle()
		if len(h.Query("id:toast-banner")) == 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if len(h.Query("id:toast-banner")) != 0 {
		t.Fatal("expected toast-banner to auto-dismiss after timeout")
	}
	if got := app.toastMsg.GetUntracked(); got != "" {
		t.Fatalf("expected toastMsg to be cleared, got %q", got)
	}

	// Superseding toast must cancel the previous timer and stay visible until its own timeout.
	app.toastDurationOverride = 160 * time.Millisecond
	app.notifyToast("已发送允许")
	h.Settle()
	h.Find("text:已发送允许")
	time.Sleep(90 * time.Millisecond)
	app.notifyToast("已切换为观察模式")
	h.Settle()
	h.Find("text:已切换为观察模式")
	time.Sleep(90 * time.Millisecond)
	h.Settle()
	if len(h.Query("text:已切换为观察模式")) == 0 {
		t.Fatal("second toast was prematurely dismissed by first toast timer")
	}

	// Tapping the floating pill dismisses it immediately (clearing state at once and unmounting after exit fade).
	h.Press("id:toast-banner")
	h.Settle()
	if got := app.toast.GetUntracked().Msg; got != "" {
		t.Fatalf("expected tapping toast-banner to clear toast immediately, got %q", got)
	}
	exitDeadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(exitDeadline) {
		h.Settle()
		if len(h.Query("id:toast-banner")) == 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if len(h.Query("id:toast-banner")) != 0 {
		t.Fatal("expected toast-banner to unmount after exit animation")
	}
}

func TestIdleAgentSendDoesNotShowWaitingOrSteer(t *testing.T) {
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		snaps := a.instances.Get()
		// Mark mac::t1 Claude session as idle/done (already finished its previous turn).
		snaps[1].Terminals[0].State = "done"
		snaps[1].Terminals[0].Status = &Status{State: "done", App: "claude", Msg: "Finished"}
		a.SeedSnapshots(snaps)
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		return root
	}, nsgotest.Size(390, 844))

	h.Press("id:session-row-mac::t1")
	h.Settle()
	h.Type("id:session-composer", "再帮我加个测试")
	h.Press("id:session-send-btn")
	h.Settle()

	h.Find("text:再帮我加个测试")
	h.Find("id:chat-user-actions-opt-1")
	if len(h.Query("text:等待中")) != 0 {
		t.Fatal("sending to an idle/done Claude session must NOT show '等待中'")
	}
	if len(h.Query("id:chat-steer-opt-1")) != 0 {
		t.Fatal("sending to an idle/done Claude session must NOT show '插队' button")
	}
}

func TestSettingsShowsNotificationPermissionPromptWhenNotGranted(t *testing.T) {
	var app *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		app = a
		return root
	}, nsgotest.Size(390, 844))

	h.Press("id:home-settings-btn")
	h.Settle()
	// Default notifPerm is NotificationsNotAsked while notifyAlerts is true -> prompt banner must appear.
	h.Find("id:settings-notify-perm-btn")

	// When granted, the prompt banner disappears cleanly.
	app.post(func() { app.notifPerm.Set(core.NotificationsGranted) })
	h.Settle()
	if len(h.Query("id:settings-notify-perm-btn")) != 0 {
		t.Fatal("expected notification permission prompt hidden once granted")
	}

	// When denied, the prompt banner reappears offering jump to system settings.
	app.post(func() { app.notifPerm.Set(core.NotificationsDenied) })
	h.Settle()
	h.Find("id:settings-notify-perm-btn")
	h.Find("text:/前往系统设置开启/")
	h.Find("id:settings-check-update-btn")
}

func TestOnForegroundResumeIgnoresBriefFocusDrop(t *testing.T) {
	var app *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		app = a
		app.hosts = []Host{{ID: "mac", Label: "mac-mini", Target: "100.64.0.10:22", User: "dev"}}
		return root
	}, nsgotest.Size(390, 844))

	// Pulling down the notification shade (brief focus drop while all instances are online) must NOT show sync banner!
	if app.OnForegroundResume(false, 2*time.Second) {
		t.Fatal("expected brief notification-shade focus drop NOT to trigger BeginResumeSync when all instances are online")
	}
	h.Settle()
	if len(h.Query("id:sync-status-banner")) != 0 {
		t.Fatal("sync-status-banner must NOT appear after brief focus drop")
	}

	// Genuine long background (>= 8s) MUST trigger resume sync.
	if !app.OnForegroundResume(true, 10*time.Second) {
		t.Fatal("expected >=8s background resume to trigger BeginResumeSync")
	}
	h.Settle()
	h.Find("id:sync-status-banner")
}

func TestAllHarnessesExtractAndDisplayModelAndEffort(t *testing.T) {
	// 1. splitModelAndEffort handles BOTH Agy's "Gemini 4 Neon (High)" and "gemini-4-neon-high" formats.
	m, e := splitModelAndEffort("Gemini 4 Neon (High)")
	if m != "Gemini 4 Neon" || e != "high" {
		t.Fatalf("splitModelAndEffort = (%q, %q), want (Gemini 4 Neon, high)", m, e)
	}
	m2, e2 := splitModelAndEffort("gemini-4-neon-high")
	if m2 != "gemini-4-neon" || e2 != "high" || ShortModelLabel("gemini-4-neon-high") != "Gemini 4 Neon" {
		t.Fatalf("splitModelAndEffort(hyphen) = (%q, %q), label=%q", m2, e2, ShortModelLabel("gemini-4-neon-high"))
	}

	// 2. ParseAntigravityJSONL extracts both clean model and effort from real hyphenated PLANNER_RESPONSE model IDs.
	agyItems := ParseAntigravityJSONL([]string{
		`{"step_index":1,"type":"PLANNER_RESPONSE","model":"gemini-4-neon-high","content":"Done!"}`,
	})
	if len(agyItems) != 1 || agyItems[0].Model != "gemini-4-neon" || agyItems[0].Effort != "high" {
		t.Fatalf("unexpected Agy parsed item: %+v", agyItems)
	}

	// 2b. ParseClaudeJSONL extracts per-turn effort from api-request output_config.effort and /effort commands!
	clItems := ParseClaudeJSONL([]string{
		`{"type":"api-request","params":{"model":"claude-opus-5-5","output_config":{"effort":"high"}}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"msg1","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"Hi"}]}}`,
	})
	if len(clItems) != 1 || clItems[0].Model != "claude-opus-5-5" || clItems[0].Effort != "high" {
		t.Fatalf("unexpected Claude parsed item: %+v", clItems)
	}

	// 3. parsePiOmpJSONLAt extracts thinking_level_change effort.
	piItems := parsePiOmpJSONLAt([]string{
		`{"type":"thinking_level_change","thinkingLevel":"xhigh"}`,
		`{"type":"message","id":"m1","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Hi"}]}}`,
	}, 1, "pi")
	if len(piItems) != 1 || piItems[0].Effort != "xhigh" {
		t.Fatalf("unexpected Pi parsed item: %+v", piItems)
	}

	// 4. ParseCodexJSONL extracts turn_context effort.
	cxItems := ParseCodexJSONL([]string{
		`{"type":"turn_context","payload":{"model":"gpt-5.4","effort":"high"}}`,
		`{"type":"response_item","payload":{"type":"message","id":"c1","role":"assistant","content":[{"type":"output_text","text":"OK"}]}}`,
	})
	if len(cxItems) != 1 || cxItems[0].Model != "gpt-5.4" || cxItems[0].Effort != "high" {
		t.Fatalf("unexpected Codex parsed item: %+v", cxItems)
	}

	// 5. UI verifies Agy session displays both Model AND Thinking Effort chips immediately!
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Status = &Status{State: "idle", App: "antigravity", Msg: "Ready"}
		a.SeedSnapshots(snaps)
		a.snapshots.SaveCatalog("mac", "antigravity", AgentCatalog{
			DefaultModel:  "Gemini 4 Neon",
			DefaultEffort: "high",
		})
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		return root
	}, nsgotest.Size(390, 844))

	h.Press("id:session-row-mac::t1")
	h.Settle()
	h.Find("text:Gemini 4 Neon")
	h.Find("text:High")
}
