package main

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/base/textview"
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
	app.SeedSnapshots(app.instances.Get())
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

	// Open session mac::t1 (Claude agent -> defaults to Chat View; switch to Terminal View to exercise VT controls).
	app.Press("id:session-row-mac::t1")
	app.Find("id:screen-session")
	app.Find("id:chat-surface")
	app.Press("id:session-mode-vt")
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

	// Toggle in-session search bar via Tools sheet.
	app.Press("id:session-tools-btn")
	app.Find("id:sheet-session-tools")
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

func TestQuietStudioComponents(t *testing.T) {
	lp := LightPalette()
	fg, soft, label := stateColor(lp, "working")
	if fg != lp.Running || soft != lp.RunningSoft || label != "运行中" {
		t.Fatalf("working state must use decoupled Running emerald (%#x/%#x), got %#x/%#x (%q)",
			uint32(lp.Running), uint32(lp.RunningSoft), uint32(fg), uint32(soft), label)
	}
	if fg == lp.Accent {
		t.Fatal("working semantic color must be decoupled from monochrome Accent")
	}

	var tapped bool
	var railTab string
	var railSettings bool
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a := NewApp(cx)
		activeTab := core.NewSignal(cx, "home")
		return core.View{
			Style: core.Style{Direction: core.Row, Width: core.Pct(100), Height: core.Pct(100)},
			Children: []core.Node{
				WideNavRail(cx, a, activeTab, func() { railSettings = true }),
				core.View{
					Style: core.Style{FlexGrow: 1, Gap: Tokens.Space3, Padding: core.All(Tokens.Space3)},
					Children: []core.Node{
						GroupCard(cx, a, "test-group", []core.Node{
							SheetListRow(cx, a, "test-sheet-row", "会话文件与 Git 差异", ":3000", nil, false, true, func() {
								tapped = true
							}),
							InsetDivider(cx, a, 34),
						}),
						IconTap(cx, a, "test-icon-tap", "搜索", nil, func() {
							railTab = activeTab.Get()
						}),
						FloatingTabBar(cx, a, activeTab),
					},
				},
			},
		}
	}, nsgotest.Size(390, 844))

	app.Find("id:wide-nav-rail")
	app.Find("id:test-group")
	app.Find("id:bottom-tab-bar")
	app.Press("id:test-sheet-row")
	if !tapped {
		t.Fatal("SheetListRow press callback did not fire")
	}
	app.Press("id:rail-tab-inbox")
	app.Press("id:test-icon-tap")
	if railTab != "inbox" {
		t.Fatalf("WideNavRail tab switch expected inbox, got %q", railTab)
	}
	app.Press("id:rail-settings-btn")
	if !railSettings {
		t.Fatal("WideNavRail settings callback did not fire")
	}
}

func TestQuietStudioShellAndInboxGrouping(t *testing.T) {
	// Wide >=680px: 56px WideNavRail + 280px sidebar + right pane; NO mobile bottom-tab-bar, NO duplicate topbar settings button.
	wide := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(960, 720))

	wide.Find("id:split-pane-layout")
	wide.Find("id:wide-nav-rail")
	if len(wide.Query("id:bottom-tab-bar")) != 0 {
		t.Fatal("wide split-pane layout must replace mobile bottom-tab-bar with WideNavRail")
	}
	if len(wide.Query("id:home-settings-btn")) != 0 {
		t.Fatal("wide layout must hide duplicate home-settings-btn since rail-settings-btn is in WideNavRail")
	}

	// Switch to Inbox from Rail and verify 3-bucket urgency grouping (permission / question / error).
	phone := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, node := testSeededApp(cx)
		snaps := a.instances.Get()
		snaps[0].Terminals = append(snaps[0].Terminals, Terminal{
			Address: "t9",
			Name:    "rate-limited-job",
			Cwd:     "/srv/jobs",
			State:   "error",
			Status:  &Status{State: "error", App: "claude", Msg: "You've hit your 5-hour limit"},
		})
		a.instances.Set(snaps)
		a.SeedSnapshots(snaps)
		return node
	}, nsgotest.Size(390, 844))

	phone.Find("id:bottom-tab-bar")
	phone.Press("id:tab-inbox")
	phone.Find("id:screen-inbox")
	phone.Find("text:权限审批 · 1")
	phone.Find("text:等待回答与认证 · 1")
	phone.Find("text:异常与限流 · 1")
}

func TestQuietStudioSessionHeaderAndToolsSheet(t *testing.T) {
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		_, node := testSeededApp(cx)
		return node
	}, nsgotest.Size(390, 844))

	h.Press("id:session-row-mac::t1")
	h.Find("id:screen-session")
	h.Find("id:chat-glass-composer")

	// Session header must stay clean: Search lives inside sheet-session-tools, not in the top header.
	if len(h.Query("id:session-search-btn")) != 0 {
		t.Fatal("session-search-btn must move into sheet-session-tools to keep the 48px session header uncluttered")
	}

	// Working + empty composer shows fixed-size circular stop button (session-interrupt-btn).
	stopBounds := h.Find("id:session-interrupt-btn").Bounds()
	if stopBounds.Size.Width != stopBounds.Size.Height || stopBounds.Size.Width > 38 {
		t.Fatalf("session-interrupt-btn must be a compact fixed-size circle (34x34), got %+v", stopBounds.Size)
	}

	// Open tools sheet -> tap session-search-btn -> reveals session-search-input.
	h.Press("id:session-tools-btn")
	h.Find("id:sheet-session-tools")
	h.Press("id:session-search-btn")
	h.Find("id:session-search-input")
}

func TestQuietStudioUnifiedSheetsAndGallery(t *testing.T) {
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, node := testSeededApp(cx)
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		return node
	}, nsgotest.Size(390, 844))

	// 1. Settings -> Tokens gallery shows live Quiet Studio component preview slice and unified 40x40 icon back button.
	h.Press("id:home-settings-btn")
	h.Find("id:screen-settings")
	h.Press("id:settings-tokens-btn")
	h.Find("id:screen-tokens")
	h.Find("id:tokens-gallery-preview")
	backBounds := h.Find("id:tokens-back-btn").Bounds()
	if backBounds.Size.Width != backBounds.Size.Height || backBounds.Size.Width > 44 {
		t.Fatalf("tokens-back-btn must use unified 40x40 IconTap, got %+v", backBounds.Size)
	}
	h.Press("id:tokens-back-btn")
	h.Press("id:settings-back-btn")

	// 2. Open session -> verify mobileAgentPicker, projectSheet, and browserPreviewSheet all use unified SheetModal (32x32 circular close).
	h.Press("id:session-row-mac::t1")
	h.Press("id:chat-model-selector")
	h.Find("id:chat-model-sheet")
	modelClose := h.Find("id:chat-model-close").Bounds()
	if modelClose.Size.Width != modelClose.Size.Height || modelClose.Size.Width > 36 {
		t.Fatalf("chat-model-close must use unified SheetModal 32x32 circular X button, got %+v", modelClose.Size)
	}
	h.Press("id:chat-model-close")

	h.Press("id:session-tools-btn")
	h.Press("id:tool-project")
	h.Find("id:project-sheet")
	projClose := h.Find("id:project-close").Bounds()
	if projClose.Size.Width != projClose.Size.Height || projClose.Size.Width > 36 {
		t.Fatalf("project-close must use unified SheetModal 32x32 circular X button, got %+v", projClose.Size)
	}
	h.Press("id:project-close")

	h.Press("id:session-tools-btn")
	h.Press("id:tool-preview")
	h.Find("id:browser-preview")
	prevClose := h.Find("id:preview-close").Bounds()
	if prevClose.Size.Width != prevClose.Size.Height || prevClose.Size.Width > 36 {
		t.Fatalf("preview-close must use unified SheetModal 32x32 circular X button, got %+v", prevClose.Size)
	}
	h.Press("id:preview-close")
}

func TestQuietStudioReviewFixes(t *testing.T) {
	// 1. Wide 680x720 boundary: Inbox InlineApprovalCard 36px buttons, Instances actions, and Session composer stay inside bounds.
	wide := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, node := testSeededApp(cx)
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		return node
	}, nsgotest.Size(680, 720))

	wide.Press("id:rail-tab-inbox")
	wide.Find("id:screen-inbox")
	allowB := wide.Find("id:approve-allow-kr::t7").Bounds()
	if allowB.Size.Height > 36.5 {
		t.Fatalf("InlineApprovalCard button must be compact 36px height per spec, got %+v", allowB.Size)
	}
	if allowB.Origin.X+allowB.Size.Width > Tokens.RailWidth+Tokens.SidebarWidth {
		t.Fatalf("InlineApprovalCard allow button overflows 280px sidebar: %+v", allowB)
	}
	openB := wide.Find("id:approve-open-kr::t7").Bounds()
	if openB.Size.Height > 36.5 || openB.Origin.X+openB.Size.Width > Tokens.RailWidth+Tokens.SidebarWidth {
		t.Fatalf("InlineApprovalCard open button must be 36x36 inside sidebar, got %+v", openB)
	}

	wide.Press("id:rail-tab-instances")
	wide.Find("id:screen-instances")
	delB := wide.Find("id:inst-delete-kr").Bounds()
	if delB.Origin.X+delB.Size.Width > Tokens.RailWidth+Tokens.SidebarWidth {
		t.Fatalf("instanceCard delete action overflows 280px sidebar: %+v", delB)
	}

	wide.Press("id:rail-tab-home")
	wide.Press("id:session-row-mac::t1")
	wide.Find("id:chat-glass-composer")
	wideStop := wide.Find("id:session-interrupt-btn").Bounds()
	if wideStop.Origin.X+wideStop.Size.Width > 680 {
		t.Fatalf("680px wide split-pane composer stop button overflows viewport: %+v", wideStop)
	}

	// 2. Narrow 360x720 with active chips + delivery indicator + open keyboard with bottom SafeArea: no overflow & no keyboard gap.
	narrow := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, node := testSeededApp(cx)
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		return node
	}, nsgotest.Size(360, 720), nsgotest.Environment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, SafeArea: core.Edges{Bottom: 34}}))

	narrow.Press("id:session-row-mac::t1")
	narrow.Type("id:session-composer", "检查状态")
	narrow.Press("id:session-send-btn")
	narrow.Find("id:chat-delivery-status")
	stopB := narrow.Find("id:session-interrupt-btn").Bounds()
	if stopB.Origin.X+stopB.Size.Width > 360 {
		t.Fatalf("360px composer stop button overflows screen with active delivery indicator: %+v", stopB)
	}
	narrow.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, SafeArea: core.Edges{Bottom: 34}, KeyboardHeight: 260})
	compB := narrow.Find("id:chat-glass-composer").Bounds()
	// Bottom of composer should sit ~8px above the keyboard (720 - 260 = 460), NOT 38px+ above it due to SafeArea.Bottom!
	if 460-(compB.Origin.Y+compB.Size.Height) > 16 {
		t.Fatalf("composer left stale SafeArea.Bottom gap above open keyboard: composer=%+v keyboardTop=460", compB)
	}

	// 3. Cross-session quotedText isolation: quoting in Session A must not leak into Session B!
	var uiRef *uiController
	quoteH := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Foreground = &Foreground{PID: 34488, Name: "claude", Cwd: "/repo", Argv: []string{"claude"}}
		a.SeedSnapshots(snaps)
		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				{InstanceID: "mac", Addr: "t1"}: {Key: TermKey{InstanceID: "mac", Addr: "t1"}, Terminal: snaps[1].Terminals[0]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {`{"pid":34488,"sessionId":"q1","cwd":"/repo"}`},
				"::/Users/dev/.claude/projects/-repo/q1.jsonl": {
					`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"session-A-secret-quote"}]}}`,
				},
			},
		}
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)
		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	quoteH.Press("id:session-row-mac::t1")
	_ = uiRef.activeChat.Get().RefreshSync(t.Context())
	quoteH.Press("id:chat-quote-m1-txt-0-0")
	quoteH.Find("id:composer-quote-banner")
	quoteH.Press("id:session-switcher-btn")
	quoteH.Press("id:switcher-item-kr::t8")
	if len(quoteH.Query("id:composer-quote-banner")) != 0 {
		t.Fatal("quotedText leaked from Session A into Session B across session switch")
	}
}

func TestQuietStudioVerifyScreenshots(t *testing.T) {
	// 1. Phone Light Home, Inbox, Instances, Tools Sheet, Settings, Tokens Gallery, Dark Home
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, node := testSeededApp(cx)
		snaps := a.instances.Get()
		snaps[0].Terminals = append(snaps[0].Terminals, Terminal{
			Address: "t9",
			Name:    "rate-limited-job",
			Cwd:     "/srv/jobs",
			State:   "error",
			Status:  &Status{State: "error", App: "claude", Msg: "You've hit your 5-hour limit"},
		})
		a.instances.Set(snaps)
		a.SeedSnapshots(snaps)
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		return node
	}, nsgotest.Size(390, 844))

	h.Find("id:screen-home")
	h.Screenshot("actual-home-light")

	h.Press("id:tab-inbox")
	h.Find("id:screen-inbox")
	h.Screenshot("actual-inbox")

	h.Press("id:tab-instances")
	h.Find("id:screen-instances")
	h.Screenshot("actual-instances")

	h.Press("id:tab-home")
	h.Press("id:session-row-mac::t1")
	h.Find("id:screen-session")
	h.Press("id:session-tools-btn")
	h.Find("id:sheet-session-tools")
	h.Screenshot("actual-session-tools")
	h.Press("id:sheet-session-tools-close")
	h.Press("id:session-back-btn")

	h.Press("id:home-settings-btn")
	h.Find("id:screen-settings")
	h.Screenshot("actual-settings")
	h.Press("id:settings-tokens-btn")
	h.Find("id:tokens-gallery-preview")
	h.Screenshot("actual-tokens-gallery")
	h.Press("id:tokens-back-btn")
	h.Press("id:theme-dark")
	h.Press("id:settings-back-btn")
	h.Find("id:screen-home")
	h.Screenshot("actual-home-dark")

	// 2. Wide 1080x680 3-pane (56px WideNavRail + 280px sidebar + 720px centered Chat)
	var wideUI *uiController
	wide := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Foreground = &Foreground{PID: 34488, Name: "claude", Cwd: "/Volumes/ExternalSSD/workspace/herdr-nsgo", Argv: []string{"claude"}}
		a.SeedSnapshots(snaps)
		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				{InstanceID: "mac", Addr: "t1"}: {Key: TermKey{InstanceID: "mac", Addr: "t1"}, Terminal: snaps[1].Terminals[0]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {`{"pid":34488,"sessionId":"w1","cwd":"/Volumes/ExternalSSD/workspace/herdr-nsgo"}`},
				"::/Users/dev/.claude/projects/-Volumes-ExternalSSD-workspace-herdr-nsgo/w1.jsonl": {
					`{"type":"user","uuid":"u1","message":{"role":"user","content":"帮我把桌面宽屏三栏和会话阅读宽度调到最舒服的比例"}}`,
					`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Edit","input":{"file_path":"main.go","old_string":"old","new_string":"new"}}]}}`,
					`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Updated main.go"}]}}`,
					`{"type":"assistant","uuid":"a2","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"已完成三栏布局：左侧 56px 图标 Rail、280px 会话侧栏、右侧居中 720px 阅读流与悬浮输入器。"}]}}`,
				},
			},
		}
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)
		node, ctrl := rootWithController(cx, a)
		wideUI = ctrl
		return node
	}, nsgotest.Size(1080, 680))

	wide.Press("id:session-row-mac::t1")
	_ = wideUI.activeChat.Get().RefreshSync(t.Context())
	wide.Find("id:wide-nav-rail")
	wide.Find("id:chat-surface")
	wide.Screenshot("actual-wide-rail")
}

func TestInstantColdStartResumeAndModelPicker(t *testing.T) {
	// 1. Direct slash command & compact model label unit checks.
	if cmd, ok := DirectApplyCommand("claude", true, "high"); !ok || cmd != "/effort high" {
		t.Fatalf("claude effort direct cmd = %q %v", cmd, ok)
	}
	if cmd, ok := DirectApplyCommand("claude", false, "sonnet"); !ok || cmd != "/model sonnet" {
		t.Fatalf("claude model direct cmd = %q %v", cmd, ok)
	}
	if cmd, ok := DirectApplyCommand("codex", true, "high"); !ok || cmd != "/model --reasoning=high" {
		t.Fatalf("codex effort direct cmd = %q %v", cmd, ok)
	}
	if cmd, ok := DirectApplyCommand("pi", true, "xhigh"); !ok || cmd != "/thinking xhigh" {
		t.Fatalf("pi effort direct cmd = %q %v", cmd, ok)
	}
	if got := ShortModelLabel("claude-opus-4-7"); got != "Opus 4.7" {
		t.Fatalf("ShortModelLabel(claude-opus-4-7) = %q", got)
	}
	if got := ShortModelLabel("remote-magpie/auto/claude-sonnet-5-5"); got != "Sonnet 5.5" {
		t.Fatalf("ShortModelLabel(pi model) = %q", got)
	}

	// 2. Persisted Home snapshot round-trip restores sessions marked dialing on Frame 0.
	store := NewSnapshotStore(nil)
	store.SaveHome([]InstanceSnapshot{{
		InstanceID:   "kr",
		InstanceName: "kr-prod",
		Online:       true,
		State:        stateOnline,
		Terminals:    []Terminal{{Address: "t1", Name: "cached-agent", State: "working"}},
	}})
	restored, ok := store.LoadHome()
	if !ok || len(restored) != 1 || restored[0].Online || restored[0].State != stateDialing || len(restored[0].Terminals) != 1 {
		t.Fatalf("unexpected restored home snapshot: ok=%v snaps=%+v", ok, restored)
	}

	// 3. Cold-start dialing shows connecting skeleton + sync-status-banner, never flashing empty state.
	var appRef *App
	cold := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a := NewApp(cx)
		appRef = a
		a.hosts = []Host{{ID: "kr", Label: "kr-prod", Target: "100.97.151.80:22", User: "root", UseLocalKey: true}}
		a.SeedSnapshots([]InstanceSnapshot{{
			InstanceID:   "kr",
			InstanceName: "kr-prod",
			Target:       "100.97.151.80:22",
			State:        stateDialing,
			Online:       false,
		}})
		return RootWithApp(cx, a)
	}, nsgotest.Size(390, 844))

	cold.Find("id:sync-status-banner")
	cold.Find("text:正在连接 kr-prod…")
	cold.Find("id:home-connecting-skeleton")
	if len(cold.Query("text:当前筛选下没有活跃会话")) != 0 {
		t.Fatal("cold start dialing must show skeleton instead of flashing empty state")
	}
	cold.Screenshot("actual-home-connecting")

	// Transition to online live sessions -> skeleton & banner clear cleanly.
	appRef.post(func() {
		appRef.SeedSnapshots([]InstanceSnapshot{{
			InstanceID:   "kr",
			InstanceName: "kr-prod",
			Target:       "100.97.151.80:22",
			State:        stateOnline,
			Online:       true,
			Terminals:    []Terminal{{Address: "t1", Name: "live-session", State: "working"}},
		}})
	})
	cold.Settle()
	cold.Find("text:live-session")
	if len(cold.Query("id:home-connecting-skeleton")) != 0 || len(cold.Query("id:sync-status-banner")) != 0 {
		t.Fatal("expected skeleton and sync banner cleared once online")
	}

	// Foreground resume immediately shows live sync banner.
	appRef.BeginResumeSync()
	cold.Settle()
	cold.Find("id:sync-status-banner")
	cold.Find("text:正在同步最新会话…")

	// 4. 0ms Instant Model & Effort Picker + Dynamic Composer Chip from transcript model!
	var uiRef *uiController
	sess := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Foreground = &Foreground{PID: 34488, Name: "claude", Cwd: "/repo", Argv: []string{"claude"}}
		a.SeedSnapshots(snaps)
		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				{InstanceID: "mac", Addr: "t1"}: {Key: TermKey{InstanceID: "mac", Addr: "t1"}, Terminal: snaps[1].Terminals[0]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {`{"pid":34488,"sessionId":"m1","cwd":"/repo"}`},
				"::/Users/dev/.claude/projects/-repo/m1.jsonl": {
					`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"ready"}]}}`,
				},
			},
		}
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)
		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	sess.Press("id:session-row-mac::t1")
	_ = uiRef.activeChat.Get().RefreshSync(t.Context())
	// Composer chip dynamically displays short model name from transcript!
	sess.Find("text:Opus 4.7")

	// Model picker opens in 0ms with instant Claude options (zero remote TUI wait)!
	sess.Press("id:chat-model-selector")
	sess.Find("id:chat-model-sheet")
	sess.Find("id:model-option-opus")
	sess.Find("id:model-option-sonnet")
	sess.Find("id:model-option-haiku")
	sess.Screenshot("actual-instant-model-picker")
	sess.Press("id:chat-model-close")

	// Effort picker opens in 0ms with instant effort options (without pre-arming model-apply)!
	sess.Press("id:chat-effort-selector")
	sess.Find("id:chat-model-sheet")
	sess.Find("id:model-option-low")
	sess.Find("id:model-option-medium")
	sess.Find("id:model-option-high")
	sess.Find("id:model-option-max")
	if len(sess.Query("id:model-apply")) != 0 {
		t.Fatal("opening effort picker must not pre-arm model-apply")
	}
	sess.Press("id:model-option-high")
	sess.Find("id:model-apply")
	sess.Screenshot("actual-instant-effort-picker")
	sess.Press("id:chat-model-close")
}

func TestSessionAccelerationSyncSpinnerVoiceAndCleanVT(t *testing.T) {
	// 1. ParseTranscriptUsage extracts token/context usage across Claude, Codex, and Pi.
	claudeUsage := ParseTranscriptUsage(FormatClaude, []string{
		`{"type":"assistant","uuid":"u1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":4000,"cache_creation_input_tokens":10000,"cache_read_input_tokens":50000,"output_tokens":600},"content":[{"type":"text","text":"ok"}]}}`,
	})
	if !claudeUsage.Valid() || claudeUsage.ContextTokens != 64000 || claudeUsage.Label() != "64k · 32%" {
		t.Fatalf("unexpected Claude usage: %+v label=%q", claudeUsage, claudeUsage.Label())
	}
	codexUsage := ParseTranscriptUsage(FormatCodex, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":200000,"last_token_usage":{"input_tokens":160000,"output_tokens":500,"total_tokens":160500}}}}`,
	})
	if !codexUsage.Valid() || codexUsage.Label() != "160k · 80%" {
		t.Fatalf("unexpected Codex usage: %+v label=%q", codexUsage, codexUsage.Label())
	}

	// 2. Persisted $HOME + KnownLines eliminates duplicate probe FileRead RPC!
	parentKey := TermKey{InstanceID: "mac", Addr: "t1"}
	childKey := TermKey{InstanceID: "mac", Addr: "t9"}
	parentAddr := "t1"
	var uiRef *uiController
	var appRef *App
	var fbRef *fakeTranscriptBackend

	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		appRef = a
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Foreground = &Foreground{PID: 34488, Name: "claude", Cwd: "/repo", Argv: []string{"claude"}}
		snaps[1].Terminals = append(snaps[1].Terminals, Terminal{
			Address:    "t9",
			Name:       "subagent-fast",
			Cwd:        "/repo",
			State:      "idle",
			Parent:     &parentAddr,
			Status:     &Status{State: "idle", App: "claude", Msg: "Ready"},
			Foreground: &Foreground{PID: 34499, Name: "claude", Cwd: "/repo", Argv: []string{"claude"}},
		})
		a.SeedSnapshots(snaps)

		fb := &fakeTranscriptBackend{
			terms: map[TermKey]AggregatedTerminal{
				parentKey: {Key: parentKey, Terminal: snaps[1].Terminals[0]},
				childKey:  {Key: childKey, Terminal: snaps[1].Terminals[1]},
			},
			files: map[string][]string{
				"::/Users/dev/.claude/sessions/34488.json": {`{"pid":34488,"sessionId":"parent-s","cwd":"/repo"}`},
				"::/Users/dev/.claude/sessions/34499.json": {`{"pid":34499,"sessionId":"child-s","cwd":"/repo"}`},
				"::/Users/dev/.claude/projects/-repo/parent-s.jsonl": {
					`{"type":"user","uuid":"pu1","message":{"role":"user","content":"父会话问题"}}`,
					`{"type":"assistant","uuid":"pa1","message":{"id":"pm1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":4000,"cache_read_input_tokens":60000,"output_tokens":200},"content":[{"type":"text","text":"父会话回答"}]}}`,
				},
				"::/Users/dev/.claude/projects/-repo/child-s.jsonl": {
					`{"type":"user","uuid":"cu1","message":{"role":"user","content":"子会话任务"}}`,
					`{"type":"assistant","uuid":"ca1","message":{"id":"cm1","model":"claude-sonnet-4-6","role":"assistant","content":[{"type":"text","text":"子会话已完成"}]}}`,
				},
			},
		}
		fbRef = fb
		a.transcriptResolver.SetHome("mac", "", "/Users/dev")
		a.SetTranscriptBackend(fb)
		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	// Verify home dir was persisted into SnapshotStore!
	if dirs := appRef.snapshots.AllHomeDirs(); dirs["mac::"] != "/Users/dev" {
		t.Fatalf("expected persisted home dir for mac::, got %+v", dirs)
	}

	// Open parent session -> sync -> verify context usage badge & synced source pill.
	h.Press("id:session-row-mac::t1")
	parentCtrl := uiRef.activeChat.Get()
	_ = parentCtrl.RefreshSync(t.Context())
	h.Settle()
	h.Find("text:父会话回答")
	h.Find("id:chat-context-pill")
	h.Find("text:64k · 32%")

	// Switch to child sub-session, then switch back to parent -> parent controller MUST be reused warm in 0ms!
	h.Press("id:subagent-chip-mac::t9")
	_ = uiRef.activeChat.Get().RefreshSync(t.Context())
	h.Settle()
	h.Find("text:子会话已完成")

	h.Press("id:chat-back-parent-btn")
	h.Settle()
	if uiRef.activeChat.Get() != parentCtrl {
		t.Fatal("expected parent ChatSessionController reused warm from LRU without recreating")
	}
	h.Find("text:父会话回答")

	// 1-tap voice button directly opens voice sheet!
	h.Press("id:composer-voice-btn")
	h.Find("id:dictation-sheet")
	h.Press("id:dictation-close")

	// Switch to VT view -> verify NO commentary prose like "观察模式 · 不改桌面列宽" appears!
	h.Press("id:session-mode-vt")
	h.Find("id:vt-surface")
	h.Find("id:session-fit-toggle")
	if len(h.Query("text:/不改桌面列宽|已适配手机列宽/")) != 0 {
		t.Fatal("VT sub-bar must not render verbose developer commentary copy")
	}

	// Open Settings and verify default send mode toggle (等待 vs 插队) persists in App state.
	h.Press("id:session-back-btn")
	h.Press("id:home-settings-btn")
	h.Find("id:settings-send-mode-queue")
	h.Find("id:settings-send-mode-steer")
	if appRef.DefaultSendMode() != chatQueue {
		t.Fatalf("expected default send mode = chatQueue, got %q", appRef.DefaultSendMode())
	}
	h.Press("id:settings-send-mode-steer")
	h.Settle()
	if appRef.DefaultSendMode() != chatSteer {
		t.Fatalf("expected send mode updated to chatSteer, got %q", appRef.DefaultSendMode())
	}
	_ = fbRef
}

func TestSyntaxHighlightingAndInboxCompletionReports(t *testing.T) {
	// 1. Verify language normalization, file path detection, and unlabeled code inference.
	if got := normalizeCodeLanguage("go:main.go", "package main"); got != "go" {
		t.Fatalf("expected go for go:main.go, got %q", got)
	}
	if got := normalizeCodeLanguage("ts title=\"app.ts\"", "const x = 1"); got != "typescript" {
		t.Fatalf("expected typescript, got %q", got)
	}
	if got := normalizeCodeLanguage("", "package main\nfunc main() {\n\tx := 1\n}"); got != "go" {
		t.Fatalf("expected inferred go for unlabeled fence, got %q", got)
	}
	if got := normalizeCodeLanguage("", "{\"ok\": true, \"count\": 3}"); got != "json" {
		t.Fatalf("expected inferred json, got %q", got)
	}
	if got := langFromFilePath("/repo/src/index.tsx:42"); got != "tsx" {
		t.Fatalf("expected tsx from path with line number, got %q", got)
	}
	if got := toolOutputLanguage(ChatToolCall{Name: "Bash", Output: "go test ./..."}); got != "bash" {
		t.Fatalf("expected bash for Bash tool, got %q", got)
	}

	// Verify real Chroma highlight spans are produced by newAppHighlighter (for both explicit and unlabeled code).
	var hlRef textview.Highlighter
	hlHost := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		hlRef = newAppHighlighter(cx)
		return core.View{}
	}, nsgotest.Size(320, 200))
	codeSample := textview.CodeBlock{Language: "", Code: "package main\nfunc hello() string { return \"hi\" }\n"}
	var gotHighlights []textview.Highlight
	for range 20 {
		gotHighlights = hlRef(codeSample)
		if len(gotHighlights) > 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
		hlHost.Settle()
	}
	if len(gotHighlights) == 0 {
		t.Fatal("expected newAppHighlighter to produce colored Chroma syntax spans for unlabeled Go code")
	}

	// 2. Verify Inbox completion reports ("汇报完成") for explicit "done" AND live working -> idle transitions!
	var appRef *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		appRef = a
		return root
	}, nsgotest.Size(390, 844))

	// Initially mac::t1 is "working" and kr::t7, kr::t8 are "blocked" (2 urgent, 0 done).
	u0, d0 := appRef.InboxBadgeCount()
	if u0 != 2 || d0 != 0 {
		t.Fatalf("expected initial inbox badge (2 urgent, 0 done), got (%d, %d)", u0, d0)
	}

	// Simulate mac::t1 finishing its task with explicit "done" report!
	doneMS := uint64(1700000100000)
	snaps := appRef.instances.Get()
	snaps[1].Terminals[0].State = "done"
	snaps[1].Terminals[0].Status = &Status{State: "done", App: "claude", Msg: "重构完成，全部测试通过"}
	snaps[1].Terminals[0].StatusMS = &doneMS
	appRef.SeedSnapshots(snaps)
	h.Settle()

	u1, d1 := appRef.InboxBadgeCount()
	if u1 != 2 || d1 != 1 {
		t.Fatalf("expected inbox badge (2 urgent, 1 done) after completion report, got (%d, %d)", u1, d1)
	}

	// Switch to Inbox tab -> verify "汇报完成" bucket and completion summary are shown!
	h.Press("id:tab-inbox")
	h.Find("id:screen-inbox")
	h.Find("id:inbox-completed-bucket")
	h.Find("text:重构完成，全部测试通过")
	h.Find("id:inbox-mark-all-read")

	// Dismiss via "已读" -> clears from Inbox and badge drops back to 2!
	h.Press("id:complete-dismiss-mac::t1")
	h.Settle()
	u2, d2 := appRef.InboxBadgeCount()
	if u2 != 2 || d2 != 0 {
		t.Fatalf("expected done count 0 after dismiss, got (%d, %d)", u2, d2)
	}
	if len(h.Query("id:inbox-completed-bucket")) != 0 {
		t.Fatal("expected completed bucket hidden after dismissing last completion report")
	}

	// Verify live working -> idle transition also surfaces in Inbox AND fires alertTracker notification!
	tr := newAlertTracker()
	workSnaps := appRef.instances.Get()
	workSnaps[1].Terminals[0].State = "working"
	workSnaps[1].Terminals[0].Status = &Status{State: "working", App: "claude", Msg: "Running build"}
	workMS := uint64(1700000200000)
	workSnaps[1].Terminals[0].StatusMS = &workMS
	_ = tr.diff(workSnaps, AggregateTerminals(workSnaps))
	appRef.SeedSnapshots(workSnaps)
	h.Settle()

	idleSnaps := appRef.instances.Get()
	idleSnaps[1].Terminals[0].State = "idle"
	idleSnaps[1].Terminals[0].Status = &Status{State: "idle", App: "claude", Msg: "构建完毕"}
	idleMS := uint64(1700000300000)
	idleSnaps[1].Terminals[0].StatusMS = &idleMS
	alerts := tr.diff(idleSnaps, AggregateTerminals(idleSnaps))
	if len(alerts) != 1 || alerts[0].Key.Addr != "t1" {
		t.Fatalf("expected 1 completion alert on working->idle transition, got %+v", alerts)
	}
	appRef.SeedSnapshots(idleSnaps)
	h.Settle()
	h.Find("id:inbox-completed-bucket")
	h.Find("text:构建完毕")

	// Tapping "查看结果" opens the session and automatically marks the completion report read!
	h.Press("id:complete-open-mac::t1")
	h.Settle()
	h.Find("id:screen-session")
	_, d3 := appRef.InboxBadgeCount()
	if d3 != 0 {
		t.Fatalf("expected completion report auto-acknowledged on open, got %d", d3)
	}
}

func TestThemeAndMultiFontWorkshopAndOSC7501(t *testing.T) {
	// 1. VTScreen direct OSC 7501 parsing
	scr := NewVTScreen(80, 24)
	scr.Write([]byte("\x1b]7501;{\"state\":\"working\",\"title\":\"编译中\",\"msg\":\"android_arm64\",\"progress\":65}\x07"))
	oscSt := scr.OSCStatus()
	if oscSt == nil || oscSt.State != "working" || oscSt.Title != "编译中" || oscSt.Progress == nil || *oscSt.Progress != 65 {
		t.Fatalf("unexpected VTScreen OSC 7501 status: %+v", oscSt)
	}

	// 2. UI verification: OSC 7501 Title + Progress bar on Home + Theme Workshop + Fonts Workshop
	var appRef *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		appRef = a
		prog := 65
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Status = &Status{State: "working", App: "antigravity", Title: "构建任务", Msg: "android_arm64", Progress: &prog}
		a.SeedSnapshots(snaps)
		return root
	}, nsgotest.Size(390, 844))

	// Home row shows OSC 7501 Title + Msg and Progress bar (65%)!
	h.Find("text:构建任务 · android_arm64")
	h.Find("id:session-progress-mac::t1")
	h.Find("text:65%")

	// Open Settings -> Theme Workshop -> select Warm Paper + Tokyo Night!
	h.Press("id:home-settings-btn")
	h.Find("id:screen-settings")
	h.Press("id:settings-open-theme")
	h.Find("id:screen-settings-theme")
	h.Press("id:theme-preset-paper")
	h.Press("id:vt-scheme-tokyo")
	h.Settle()
	if appRef.themePreset.Get() != PresetPaper || appRef.vtColorScheme.Get() != VTSchemeTokyo {
		t.Fatalf("expected PresetPaper + VTSchemeTokyo, got %q / %q", appRef.themePreset.Get(), appRef.vtColorScheme.Get())
	}
	h.Press("id:settings-theme-back-btn")
	h.Find("id:screen-settings")

	// Open Fonts Workshop -> select Serif + Fira Code + bump body font size!
	h.Press("id:settings-open-fonts")
	h.Find("id:screen-settings-fonts")
	h.Press("id:font-body-serif")
	h.Press("id:font-mono-firacode")
	h.Press("id:body-font-inc-btn")
	h.Settle()
	if appRef.bodyFont.Get() != BodyFontSerif || appRef.monoFont.Get() != MonoFontFiraCode || appRef.bodyFontSize.Get() != 16 {
		t.Fatalf("unexpected font state: body=%q mono=%q size=%v", appRef.bodyFont.Get(), appRef.monoFont.Get(), appRef.bodyFontSize.Get())
	}
	h.Press("id:settings-fonts-back-btn")
	h.Find("id:screen-settings")

	// 3. Verify "π - " stripped from Gemini session title, Pi renders colorful P brand icon,
	// and Gemini (antigravity) session exposes Model + Effort chips in Chat composer!
	snaps := appRef.instances.Get()
	snaps[1].Terminals[0].Name = ""
	snaps[1].Terminals[0].Title = "π - herdr-nsgo"
	snaps[1].Terminals[0].Status = &Status{State: "working", App: "antigravity", Msg: "Ready"}
	snaps[1].Terminals = append(snaps[1].Terminals,
		Terminal{
			Address: "t2",
			Title:   "π - arcteryx-console",
			State:   "idle",
			Status:  &Status{State: "idle", App: "pi"},
		},
		Terminal{
			Address: "t3",
			Title:   "claude-worker",
			State:   "working",
			Status:  &Status{State: "working", App: "claude"},
		},
		Terminal{
			Address: "t4",
			Title:   "codex-review",
			State:   "idle",
			Status:  &Status{State: "idle", App: "codex"},
		},
	)
	appRef.SeedSnapshots(snaps)
	h.Press("id:settings-back-btn")
	h.Settle()

	// All 4 AI harnesses render their brand SVG marks + clean titles!
	h.Find("text:/^herdr-nsgo$/")
	if len(h.Query("text:/π -/")) != 0 {
		t.Fatal("expected raw 'π -' prefix stripped from session titles")
	}
	h.Find("id:harness-brand-icon-antigravity-mac::t1")
	h.Find("id:pi-brand-icon-mac::t2")
	h.Find("id:harness-brand-icon-claude-mac::t3")
	h.Find("id:harness-brand-icon-codex-mac::t4")
	h.Find("text:/^arcteryx-console$/")

	// Opening the Gemini (antigravity) session MUST show Model & Effort chips with instant Gemini presets!
	h.Press("id:session-row-mac::t1")
	h.Find("id:chat-model-selector")
	h.Find("id:chat-effort-selector")
	h.Press("id:chat-model-selector")
	h.Find("id:chat-model-sheet")
	h.Find("id:model-option-gemini-3.1-pro")
	h.Press("id:chat-model-close")
}

func TestStaleClaudeClearRotationSelfHealsAndClearsSpinner(t *testing.T) {
	key := TermKey{InstanceID: "mac", Addr: "t1"}
	oldPath := "/Users/dev/.claude/projects/-repo/old-657.jsonl"
	newPath := "/Users/dev/.claude/projects/-repo/new-664.jsonl"
	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			key: {
				Key: key,
				Terminal: Terminal{
					Address:    "t1",
					Title:      "#664 /triage",
					Cwd:        "/repo",
					State:      "working",
					Status:     &Status{State: "working", App: "claude", Msg: "Analyzing #664"},
					Foreground: &Foreground{PID: 4200, Name: "claude", Cwd: "/repo", Argv: []string{"claude"}},
				},
			},
		},
		files: map[string][]string{
			"::/Users/dev/.claude/sessions/4200.json": {`{"pid":4200,"sessionId":"old-657","cwd":"/repo"}`},
			"::" + oldPath: {
				`{"type":"user","uuid":"u1","message":{"role":"user","content":"旧会话 #657"}}`,
				`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"旧内容 #657 #658 #663"}]}}`,
			},
			"::" + newPath: {
				`{"type":"user","uuid":"u2","message":{"role":"user","content":"/triage #664"}}`,
				`{"type":"assistant","uuid":"a2","message":{"id":"m2","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"正在处理最新 #664"}]}}`,
			},
		},
	}
	fb.execHandler = func(_ string, _ ExecParams) (ExecResult, error) {
		code := int32(0)
		return ExecResult{Code: &code, Stdout: base64.StdEncoding.EncodeToString([]byte(newPath + "\n"))}, nil
	}

	resolver := NewTranscriptResolver()
	resolver.SetHome("mac", "", "/Users/dev")
	ctrl := NewChatSessionController(nil, key, fb, resolver, nil)
	defer ctrl.Close()

	// 1st sync resolves via stale PID file -> loads old #657 file.
	if err := ctrl.RefreshSync(t.Context()); err != nil {
		t.Fatalf("first RefreshSync: %v", err)
	}
	if got := ctrl.Items(); len(got) != 2 || got[1].Text != "旧内容 #657 #658 #663" {
		t.Fatalf("expected initial old file loaded, got %+v", got)
	}

	// Simulate cachedSnapshot being true when sameTail occurs -> spinner MUST clear!
	ctrl.mu.Lock()
	ctrl.cachedSnapshot = true
	ctrl.mu.Unlock()
	_ = ctrl.RefreshSync(t.Context())
	if ctrl.IsCachedSnapshot() {
		t.Fatal("expected sameTail to clear cachedSnapshot so header spinner never gets stuck")
	}

	// 2nd stale poll while working -> self-heals via ResolveLatestForStale and switches to new #664 JSONL!
	if err := ctrl.RefreshSync(t.Context()); err != nil {
		t.Fatalf("stale recheck RefreshSync: %v", err)
	}
	items := ctrl.Items()
	if len(items) != 2 || items[1].Text != "正在处理最新 #664" {
		t.Fatalf("expected stale working session to self-heal to new #664 JSONL, got %+v", items)
	}
}
