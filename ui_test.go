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
