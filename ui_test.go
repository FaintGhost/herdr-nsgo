package main

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func testSeededApp(cx *core.Scope) (*App, core.Node) {
	app := NewApp(cx)
	// Seed 2 instances & 3 terminals (1 claude permission, 1 codex question, 1 working) without dialing real SSH.
	app.instances.Set([]InstanceSnapshot{
		{
			InstanceID:   "kr",
			InstanceName: "kr-prod",
			Target:       "100.97.151.80:22",
			User:         "root",
			CoreID:       "core-kr",
			Online:       true,
			State:        stateOnline,
			LatencyMS:    42,
			Templates:    []Template{{Name: "claude", Description: "Claude Code"}, {Name: "codex", Description: "Codex CLI"}},
			Terminals: []Terminal{
				{
					Address: "t7",
					Name:    "wms-token",
					Cwd:     "/srv/wms",
					State:   "blocked",
					Status:  &Status{State: "blocked", Kind: "permission", App: "claude", Msg: "Allow running git push origin main?"},
				},
				{
					Address: "t8",
					Name:    "api-refactor",
					Cwd:     "/srv/api",
					State:   "blocked",
					Status:  &Status{State: "blocked", Kind: "question", App: "codex", Msg: "Pick migration strategy A or B?"},
				},
			},
		},
		{
			InstanceID:   "mac",
			InstanceName: "mac-mini",
			Target:       "100.64.0.10:22",
			User:         "dev",
			CoreID:       "core-mac",
			Online:       true,
			State:        stateOnline,
			LatencyMS:    12,
			Templates:    []Template{{Name: "claude", Description: "Claude Code"}},
			Terminals: []Terminal{
				{
					Address: "t1",
					Name:    "herdr-nsgo-dev",
					Cwd:     "/Volumes/ExternalSSD/workspace/herdr-nsgo",
					State:   "working",
					Status:  &Status{State: "working", App: "claude", Msg: "Running go test -race ./..."},
				},
			},
		},
	})
	app.aggTerms.Set(AggregateTerminals(app.instances.Get()))
	app.hosts = []Host{
		{ID: "kr", Label: "kr-prod", Target: "100.97.151.80:22", User: "root", UseLocalKey: true},
		{ID: "mac", Label: "mac-mini", Target: "100.64.0.10:22", User: "dev", UseLocalKey: true},
	}
	app.tofuRecords.Set([]TOFURecord{
		{HostID: "kr", Fingerprint: "SHA256:krFakeFingerprint1234567890"},
	})
	return app, RootWithApp(cx, app)
}

func TestUIOnboardingColdStartAndManualAdd(t *testing.T) {
	app := nsgotest.Start(t, Root, nsgotest.Size(390, 844))
	// 0 hosts -> cold-start onboarding screen.
	app.Find("id:screen-onboarding")
	app.Find("text:连接你的第一台 herdr 机器")

	// Tap manual add -> opens add instance form.
	app.Press("id:onboarding-manual-btn")
	app.Find("id:instance-form")
	app.Press("id:instance-form-close")

	// Tap settings from onboarding -> opens settings screen.
	app.Press("id:onboarding-settings-btn")
	app.Find("id:screen-settings")
}

func TestUIHomeMultiInstancePillsAndInlineApproval(t *testing.T) {
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(390, 844))

	app.Find("id:screen-home")
	app.Find("id:pill-all")
	app.Find("id:pill-inst-kr")
	app.Find("id:pill-inst-mac")

	// Inline approval buttons appear for claude permission (kr::t7).
	app.Find("id:approve-allow-kr::t7")
	app.Find("id:approve-deny-kr::t7")

	// Filter to mac-mini only: herdr-nsgo-dev stays visible, wms-token filtered out.
	app.Press("id:pill-inst-mac")
	app.Find("text:herdr-nsgo-dev")
	if len(app.Query("text:wms-token")) != 0 {
		t.Error("expected wms-token hidden when filtered to mac-mini")
	}
}

func TestUIInboxReactiveSearchFilter(t *testing.T) {
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(390, 844))

	app.Press("id:tab-inbox")
	app.Find("id:screen-inbox")
	app.Find("text:wms-token")
	app.Find("text:api-refactor")

	// Live search filtering must reactively narrow rows!
	app.Type("id:inbox-search", "wms")
	app.Find("text:wms-token")
	if len(app.Query("text:api-refactor")) != 0 {
		t.Error("expected api-refactor filtered out by inbox search 'wms'")
	}
}

func TestUIInstancesPoolTOFUAndQR(t *testing.T) {
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(390, 844))

	app.Press("id:tab-instances")
	app.Find("id:screen-instances")
	app.Find("text:kr-prod")
	app.Find("text:mac-mini")
	app.Find("text:SHA256:krFake")

	// Open QR pairing modal and verify paste input exists.
	app.Press("id:instances-qr-btn")
	app.Find("id:qr-modal")
	app.Type("id:qr-paste-input", "herdr://pair?v=1&host=100.1.2.3&port=22&user=alice&label=studio&fp=SHA256:abc123")
	app.Press("id:qr-apply-btn")
	app.Find("id:instance-card-studio")
}

func TestUINewSessionSheetTemplatesAndShell(t *testing.T) {
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(390, 844))

	app.Press("id:fab-new-session")
	app.Find("id:sheet-new-session")
	app.Find("text:新建会话")
	app.Find("id:tpl-chip-claude")
	app.Find("id:tpl-chip-shell")
}

func TestUISessionVTViewObserveToggleKeycapAndSnippets(t *testing.T) {
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, node := testSeededApp(cx)
		a.cache.Put(TermKey{InstanceID: "mac", Addr: "t1"}, []byte("hello-vt-canvas\r\nhttps://herdr.dev/test\r\n"))
		return node
	}, nsgotest.Size(390, 844))

	// Open session mac::t1 from Home.
	app.Press("id:session-row-mac::t1")
	app.Find("id:screen-session")
	app.Find("id:session-fit-toggle")
	app.Find("id:vt-surface")
	app.Find("id:keycap-esc")
	app.Find("id:keycap-ctrlc")

	// Toggle observe -> fit width.
	app.Press("id:session-fit-toggle")

	// Open snippets sheet.
	app.Press("id:session-snippets-btn")
	app.Find("id:sheet-snippets")
	app.Press("id:sheet-snippets-close")

	// Toggle in-session search bar.
	app.Press("id:session-search-btn")
	app.Find("id:session-search-input")
}

func TestUISettingsThemeToggleAndTokenGallery(t *testing.T) {
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(390, 844))

	app.Press("id:home-settings-btn")
	app.Find("id:screen-settings")
	app.Press("id:theme-dark")
	app.Press("id:theme-light")

	// Open runtime Design Tokens gallery.
	app.Press("id:settings-tokens-btn")
	app.Find("id:screen-tokens")
	app.Find("text:/^--color-accent$/")
}

func TestUIResponsiveWideSplitPane(t *testing.T) {
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(960, 720))

	app.Find("id:split-pane-layout")
}
