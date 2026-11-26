// Command herdr-nsgo is a cross-platform mobile and desktop client for herdr-core:
// concurrent multi-instance SSH + framed binary VT multiplexing, Agent semantic approval
// cards, TOFU security pinning, and Quiet Studio Light/Dark design system.
package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
)

func main() { nsgo.Run(Root) }

const (
	maxWarmChatControllers = 6
	maxPrefetchedSessions  = 8
	prefetchTTL            = 60 * time.Second
)

// uiController owns reactive navigation, modal state, and the active SessionTerminal / ChatSessionController lifecycle.
type uiController struct {
	app             *App
	activeTab       *core.Signal[string] // home, inbox, instances
	activeSession   *core.Signal[TermKey]
	activeTerm      *core.Signal[*SessionTerminal]
	activeChat      *core.Signal[*ChatSessionController]
	sessionMode     *core.Signal[string] // "chat" or "vt"
	sessionModes    map[TermKey]string   // per-session remembered mode during app session
	subPage         *core.Signal[string] // "", settings, tokens
	showNewSession  *core.Signal[bool]
	showAddInstance *core.Signal[bool]
	showQRModal     *core.Signal[bool]
	editingHost     *core.Signal[Host]
	attachGen       atomic.Uint64

	// Warm in-memory ChatSessionController LRU for 0ms parent<->child & recent session switching.
	warmChats      map[TermKey]*ChatSessionController
	warmOrder      []TermKey
	pendingAttach  func() // deferred VT attach triggered early if user switches to VT mode

	// Off-UI-thread prefetched transcripts for child sub-sessions / parent session.
	prefetchMu    sync.Mutex
	warmPrefetch  map[TermKey]prefetchedTranscript
	prefetchInFly map[TermKey]bool
}

// Root is the NSGo entry point.
func Root(cx *core.Scope) core.Node {
	return RootWithApp(cx, NewApp(cx))
}

// RootWithApp builds the complete responsive shell bound to app (also used by nsgotest).
func RootWithApp(cx *core.Scope, app *App) core.Node {
	node, _ := rootWithController(cx, app)
	return node
}

func rootWithController(cx *core.Scope, app *App) (core.Node, *uiController) {
	registerAppFonts(cx)
	u := &uiController{
		app:             app,
		activeTab:       core.NewSignal(cx, "home"),
		activeSession:   core.NewSignal(cx, TermKey{}),
		activeTerm:      core.NewSignal[*SessionTerminal](cx, nil),
		activeChat:      core.NewSignal[*ChatSessionController](cx, nil),
		sessionMode:     core.NewSignal(cx, "chat"),
		sessionModes:    make(map[TermKey]string),
		subPage:         core.NewSignal(cx, ""),
		showNewSession:  core.NewSignal(cx, false),
		showAddInstance: core.NewSignal(cx, false),
		showQRModal:     core.NewSignal(cx, false),
		editingHost:     core.NewSignal(cx, Host{UseLocalKey: true}),
		warmChats:       make(map[TermKey]*ChatSessionController),
		warmPrefetch:    make(map[TermKey]prefetchedTranscript),
		prefetchInFly:   make(map[TermKey]bool),
	}

	app.OnPoolSync(func() {
		if chat := u.activeChat.Get(); chat != nil {
			chat.RefreshAsync()
		}
	})

	// Open session directly when user taps a system notification (closing any open sheets).
	app.OnNotificationOpen(func(k TermKey) {
		app.post(func() {
			u.showNewSession.Set(false)
			u.showAddInstance.Set(false)
			u.showQRModal.Set(false)
			u.subPage.Set("")
			u.openSession(cx, k)
		})
	})

	if cx != nil {
		// Release active VT attach stream and warm chat controllers on app exit.
		cx.Cleanup(func() {
			u.closeSession()
			u.closeAllWarmChats()
		})

		// Foreground wakeup & network change fast reconnect (<500ms wakeup target).
		// Ignore brief focus drops (e.g. pulling down the notification shade) when all SSH instances are already online.
		lastLifecycle := core.LifecycleActive
		wasBackground := false
		var inactiveSince time.Time
		cx.Effect(func() {
			lc := cx.Env().Lifecycle()
			switch lc {
			case core.LifecycleBackground:
				wasBackground = true
				if inactiveSince.IsZero() {
					inactiveSince = time.Now()
				}
			case core.LifecycleInactive:
				if inactiveSince.IsZero() {
					inactiveSince = time.Now()
				}
			case core.LifecycleActive:
				if lastLifecycle != core.LifecycleActive {
					away := time.Duration(0)
					if !inactiveSince.IsZero() {
						away = time.Since(inactiveSince)
					}
					bg := wasBackground
					wasBackground = false
					inactiveSince = time.Time{}
					app.RefreshNotificationPermission()
					didSync := app.OnForegroundResume(bg, away)
					if chat := u.activeChat.Get(); chat != nil {
						if didSync {
							chat.ManualRefreshAsync()
						} else {
							chat.RefreshAsync()
						}
					}
				}
			}
			lastLifecycle = lc
		})
		var lastNet core.Network
		var netSeen bool
		cx.OnNetworkChange(func(n core.Network) {
			if !netSeen {
				netSeen = true
				lastNet = n
				if app.HasOfflineEnabledInstance() {
					app.BeginResumeSync()
				}
				return
			}
			if n == lastNet && !app.HasOfflineEnabledInstance() {
				return
			}
			lastNet = n
			app.BeginResumeSync()
		})

		// Native Android hardware/gesture back press handling.
		cx.OnBackPress(func() bool {
			if u.showNewSession.Get() {
				u.showNewSession.Set(false)
				return true
			}
			if u.showAddInstance.Get() {
				u.showAddInstance.Set(false)
				return true
			}
			if u.showQRModal.Get() {
				u.showQRModal.Set(false)
				return true
			}
			if sp := u.subPage.Get(); sp == "tokens" || sp == "settings-theme" || sp == "settings-fonts" {
				u.subPage.Set("settings")
				return true
			}
			if u.subPage.Get() != "" {
				u.subPage.Set("")
				return true
			}
			if u.activeSession.Get() != (TermKey{}) {
				u.closeSession()
				return true
			}
			return false
		})
	}

	// Render active primary tab content (shared by phone floating shell and wide 280px sidebar).
	tabContent := func(cx *core.Scope, wide bool) core.Node {
		return core.Show{
			When: func() bool { return u.activeTab.Get() == "inbox" },
			Then: func(cx *core.Scope) core.Node { return u.inboxScreen(cx, wide) },
			Else: func(cx *core.Scope) core.Node {
				return core.Show{
					When: func() bool { return u.activeTab.Get() == "instances" },
					Then: func(cx *core.Scope) core.Node { return u.instancesScreen(cx, wide) },
					Else: func(cx *core.Scope) core.Node { return u.homeScreen(cx, wide) },
				}
			},
		}
	}

	// Compact phone shell: primary tab feed with floating glass capsule TabBar overlaid at bottom.
	phoneTabShell := func(cx *core.Scope) core.Node {
		return core.View{
			Style: core.Style{
				Position:   core.Relative,
				FlexGrow:   1,
				FlexShrink: 1,
				MinHeight:  core.Pt(0),
				Width:      core.Pct(100),
				Height:     core.Pct(100),
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Overflow: core.Hidden},
					Children: []core.Node{
						tabContent(cx, false),
					},
				},
				TabBar(cx, app, u.activeTab),
			},
		}
	}

	isWide := func() bool {
		if cx == nil {
			return false
		}
		return cx.Env().Window().Width >= Tokens.WideBreakpoint
	}

	hasAnyInstance := func() bool {
		return len(app.instances.Get()) > 0 || len(app.Hosts()) > 0
	}

	return core.View{
		StyleFunc: func() core.Style {
			pal := app.paletteOf(cx)
			return core.Style{
				Width:      core.Pct(100),
				Height:     core.Pct(100),
				Background: pal.Bg,
				Position:   core.Relative,
			}
		},
		Children: []core.Node{
			// Main page layer: Settings / Theme / Fonts / Tokens -> Onboarding (if 0 instances) -> Wide Split Pane or Compact Phone Shell
			core.Show{
				When: func() bool { return u.subPage.Get() != "" },
				Then: func(cx *core.Scope) core.Node {
					return core.Dynamic(func(cx *core.Scope) core.Node {
						switch u.subPage.Get() {
						case "tokens":
							return u.tokensGalleryScreen(cx)
						case "settings-theme":
							return u.settingsThemeScreen(cx)
						case "settings-fonts":
							return u.settingsFontsScreen(cx)
						default:
							return u.settingsScreen(cx)
						}
					})
				},
				Else: func(cx *core.Scope) core.Node {
					return core.Show{
						When: func() bool { return !hasAnyInstance() },
						Then: func(cx *core.Scope) core.Node { return u.onboardingScreen(cx) },
						Else: func(cx *core.Scope) core.Node {
							return core.Show{
								When: isWide,
								Then: func(cx *core.Scope) core.Node {
									// Wide >=680px 3-pane layout (56px WideNavRail + 280px Sidebar + Right Live Session Pane)
									return core.View{
										TestID: "split-pane-layout",
										Style:  core.Style{Direction: core.Row, FlexGrow: 1, Width: core.Pct(100), Height: core.Pct(100)},
										Children: []core.Node{
											WideNavRail(cx, app, u.activeTab, func() {
												u.subPage.Set("settings")
											}),
											core.View{
												StyleFunc: func() core.Style {
													pal := app.paletteOf(cx)
													return core.Style{
														Width:       core.Pt(Tokens.SidebarWidth),
														Height:      core.Pct(100),
														Borders:     core.Edges{Right: 1},
														BorderColor: pal.Separator,
														Overflow:    core.Hidden,
													}
												},
												Children: []core.Node{tabContent(cx, true)},
											},
											core.View{
												Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pct(100)},
												Children: []core.Node{
													core.Show{
														When: func() bool { return u.activeSession.Get() != (TermKey{}) },
														Then: func(cx *core.Scope) core.Node { return u.sessionScreen(cx, false) },
														Else: func(cx *core.Scope) core.Node {
															return core.View{
																Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(Tokens.Space6)},
																Children: []core.Node{
																	EmptyStateCard(cx, app, "选择一个会话开始查看", "点击左侧任意会话即可在此实时查看对话流与完整 VT 终端。", "", "", nil),
																},
															}
														},
													},
												},
											},
										},
									}
								},
								Else: func(cx *core.Scope) core.Node {
									// Compact phone layout: Session pushes full-screen over floating 3-Tab shell
									return core.Show{
										When: func() bool { return u.activeSession.Get() != (TermKey{}) },
										Then: func(cx *core.Scope) core.Node { return u.sessionScreen(cx, true) },
										Else: phoneTabShell,
									}
								},
							}
						},
					}
				},
			},
			// Modal sheets layer
			core.Show{
				When: u.showNewSession.Get,
				Then: func(cx *core.Scope) core.Node { return u.newSessionSheet(cx) },
			},
			core.Show{
				When: u.showAddInstance.Get,
				Then: func(cx *core.Scope) core.Node { return u.addInstanceModal(cx) },
			},
			core.Show{
				When: u.showQRModal.Get,
				Then: func(cx *core.Scope) core.Node { return u.qrPairingModal(cx) },
			},
			// Root-level floating toast overlay (zero layout shift across all screens & sheets)
			FloatingToastOverlay(cx, app),
		},
	}, u
}

func (u *uiController) setSessionMode(mode string) {
	if mode != "chat" && mode != "vt" {
		return
	}
	if u.sessionMode.Get() == mode {
		return
	}
	playSelectionHaptic(u.app.cx)
	cur := u.activeSession.Get()
	if cur != (TermKey{}) {
		u.sessionModes[cur] = mode
	}
	u.sessionMode.Set(mode)
	if mode == "vt" {
		if fn := u.pendingAttach; fn != nil {
			u.pendingAttach = nil
			fn()
		}
		return
	}
	if mode == "chat" {
		if chat := u.activeChat.Get(); chat != nil && chat.Source() == SourceLoading {
			chat.RefreshAsync()
		}
	}
}

func (u *uiController) parkWarmChat(key TermKey, chat *ChatSessionController) {
	if key == (TermKey{}) || chat == nil || chat.isClosed() {
		return
	}
	chat.PauseLoop()
	if u.warmChats == nil {
		u.warmChats = make(map[TermKey]*ChatSessionController)
	}
	if existing, ok := u.warmChats[key]; ok && existing != chat {
		existing.Close()
	}
	u.warmChats[key] = chat
	kept := u.warmOrder[:0]
	for _, k := range u.warmOrder {
		if k != key {
			kept = append(kept, k)
		}
	}
	kept = append(kept, key)
	if len(kept) > maxWarmChatControllers {
		evict := kept[0]
		kept = append([]TermKey(nil), kept[1:]...)
		if old, ok := u.warmChats[evict]; ok {
			delete(u.warmChats, evict)
			old.Close()
		}
	}
	u.warmOrder = kept
}

func (u *uiController) takeWarmChat(key TermKey) *ChatSessionController {
	chat, ok := u.warmChats[key]
	if !ok || chat == nil || chat.isClosed() {
		delete(u.warmChats, key)
		return nil
	}
	delete(u.warmChats, key)
	kept := u.warmOrder[:0]
	for _, k := range u.warmOrder {
		if k != key {
			kept = append(kept, k)
		}
	}
	u.warmOrder = kept
	return chat
}

func (u *uiController) closeAllWarmChats() {
	for k, c := range u.warmChats {
		if c != nil {
			c.Close()
		}
		delete(u.warmChats, k)
	}
	u.warmOrder = nil
}

func (u *uiController) takePrefetched(key TermKey) (prefetchedTranscript, bool) {
	u.prefetchMu.Lock()
	defer u.prefetchMu.Unlock()
	p, ok := u.warmPrefetch[key]
	if !ok {
		return prefetchedTranscript{}, false
	}
	delete(u.warmPrefetch, key)
	if time.Since(p.FetchedAt) > prefetchTTL {
		return prefetchedTranscript{}, false
	}
	return p, true
}

// prefetchRelatedSessions warms child sub-sessions (and parent) off the UI thread after a brief delay
// so the active session gets first crack at the SSH mux while sibling/child switches become 0ms.
func (u *uiController) prefetchRelatedSessions(activeKey TermKey) {
	if u.app == nil || u.app.TranscriptBackend() == nil {
		return
	}
	cur, ok := u.app.FindTerminal(activeKey)
	if !ok {
		return
	}
	var targets []AggregatedTerminal
	seen := map[TermKey]bool{activeKey: true}
	all := u.app.aggTerms.GetUntracked()

	// 1. Direct child sub-sessions of activeKey (highest priority).
	for _, it := range all {
		if it.Key.InstanceID != activeKey.InstanceID || seen[it.Key] {
			continue
		}
		if it.Terminal.Parent != nil && strings.TrimSpace(*it.Terminal.Parent) == activeKey.Addr && IsAgentTerminal(it.Terminal) {
			seen[it.Key] = true
			targets = append(targets, it)
		}
	}
	// 2. Parent session if activeKey is itself a child sub-session.
	if cur.Terminal.Parent != nil {
		if pAddr := strings.TrimSpace(*cur.Terminal.Parent); pAddr != "" {
			pk := TermKey{InstanceID: activeKey.InstanceID, Addr: pAddr}
			if !seen[pk] {
				if pt, ok := u.app.FindTerminal(pk); ok && IsAgentTerminal(pt.Terminal) {
					seen[pk] = true
					targets = append(targets, pt)
				}
			}
		}
	}
	if len(targets) == 0 {
		return
	}
	if len(targets) > 3 {
		targets = targets[:3]
	}

	// Skip targets that are already warm in memory or freshly prefetched.
	var toFetch []AggregatedTerminal
	u.prefetchMu.Lock()
	for _, t := range targets {
		if _, isWarm := u.warmChats[t.Key]; isWarm {
			continue
		}
		if p, ok := u.warmPrefetch[t.Key]; ok && time.Since(p.FetchedAt) < prefetchTTL {
			continue
		}
		if u.prefetchInFly[t.Key] {
			continue
		}
		u.prefetchInFly[t.Key] = true
		toFetch = append(toFetch, t)
	}
	u.prefetchMu.Unlock()
	if len(toFetch) == 0 {
		return
	}

	backend := u.app.TranscriptBackend()
	resolver := u.app.transcriptResolver
	snapshots := u.app.snapshots
	gen := u.attachGen.Load()

	time.AfterFunc(220*time.Millisecond, func() {
		defer func() {
			u.prefetchMu.Lock()
			for _, t := range toFetch {
				delete(u.prefetchInFly, t.Key)
			}
			u.prefetchMu.Unlock()
		}()
		if u.attachGen.Load() != gen {
			return
		}
		var wg sync.WaitGroup
		for _, target := range toFetch {
			wg.Add(1)
			go func(at AggregatedTerminal) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				if pref, ok := PrefetchTranscript(ctx, at.Key, at.Terminal, backend, resolver, snapshots); ok {
					u.prefetchMu.Lock()
					if len(u.warmPrefetch) >= maxPrefetchedSessions {
						var oldestKey TermKey
						var oldestAt time.Time
						for k, v := range u.warmPrefetch {
							if oldestKey == (TermKey{}) || v.FetchedAt.Before(oldestAt) {
								oldestKey = k
								oldestAt = v.FetchedAt
							}
						}
						delete(u.warmPrefetch, oldestKey)
					}
					u.warmPrefetch[at.Key] = pref
					u.prefetchMu.Unlock()
				}
			}(target)
		}
		wg.Wait()
	})
}

// openSession attaches to key (in observe mode by default), reusing warm controllers / prefetched sub-sessions
// for 0ms parent<->child switching and deferring VT attach in Chat mode so Chat first paint gets full bandwidth.
// Always binds SessionTerminal and ChatSessionController signals to the root app scope (u.app.cx),
// never an ephemeral row/button child scope that gets disposed on navigation.
func (u *uiController) openSession(_ *core.Scope, key TermKey) {
	prevKey := u.activeSession.Get()
	u.pendingAttach = nil
	if prevChat := u.activeChat.Get(); prevChat != nil {
		if prevKey != (TermKey{}) && prevKey != key {
			u.parkWarmChat(prevKey, prevChat)
		} else if prevKey == key {
			// Re-opening same key: keep warm temporarily so we reuse below.
			u.parkWarmChat(prevKey, prevChat)
		} else {
			prevChat.Close()
		}
		u.activeChat.Set(nil)
	}
	if prev := u.activeTerm.Get(); prev != nil {
		prev.Close()
		u.activeTerm.Set(nil)
		if prevKey != (TermKey{}) && u.app.frameCacheOn.Get() && u.app.cache != nil && u.app.snapshots != nil {
			if snapBytes, ok := u.app.cache.Get(prevKey); ok && len(snapBytes) > 0 {
				u.app.snapshots.SaveVT(prevKey, snapBytes)
			}
		}
	}
	u.activeSession.Set(key)
	u.app.SetActiveSession(key)
	u.app.TouchSessionActivity(key)

	// Determine initial mode: remembered per-session choice -> Agent="chat" / Shell="vt".
	mode, remembered := u.sessionModes[key]
	if !remembered {
		if at, ok := u.app.FindTerminal(key); ok && IsAgentTerminal(at.Terminal) {
			mode = "chat"
		} else {
			mode = "vt"
		}
		u.sessionModes[key] = mode
	}
	u.sessionMode.Set(mode)

	rootCX := u.app.cx
	var cache *FrameCache
	if u.app.frameCacheOn.Get() {
		cache = u.app.cache
		// Cold-start 0ms VT snapshot seed: if LRU memory cache doesn't have key yet, load persisted VT bytes!
		if _, hasMem := cache.Get(key); !hasMem && u.app.snapshots != nil {
			if rawVT, ok := u.app.snapshots.LoadVT(key); ok && len(rawVT) > 0 {
				cache.Put(key, rawVT)
			}
		}
	}
	enableGhostty := rootCX != nil && !rootCX.Env().Simulated()
	observe := u.app.observeDefault.Get()
	st := NewSessionTerminal(rootCX, key, 100, 32, observe, cache, enableGhostty)

	chatCtrl := u.takeWarmChat(key)
	reusedWarm := chatCtrl != nil
	if reusedWarm {
		chatCtrl.SetVTFallback(func() string { return st.Screen().PlainText() })
	} else {
		chatCtrl = NewChatSessionController(
			rootCX,
			key,
			u.app.TranscriptBackend(),
			u.app.transcriptResolver,
			func() string { return st.Screen().PlainText() },
		)
		hydrated := false
		if pref, ok := u.takePrefetched(key); ok {
			hydrated = chatCtrl.HydratePrefetch(pref)
		}
		if !hydrated && u.app.frameCacheOn.Get() && u.app.snapshots != nil {
			var live *Terminal
			if u.app.pool.ClientFor(key.InstanceID) != nil {
				if at, ok := u.app.FindTerminal(key); ok {
					live = &at.Terminal
				}
			}
			chatCtrl.SeedFromSnapshot(u.app.snapshots, live)
		}
	}

	attachWithMode := func(obs bool, cols, rows int) {
		gen := u.attachGen.Add(1)
		client := u.app.clientForKey(key)
		if client == nil {
			st.SetDiag("实例未在线（client=nil），等待重连…")
			return
		}
		if cols <= 0 {
			cols = 100
		}
		if rows <= 0 {
			rows = 32
		}
		st.SetDiag("正在连接 " + key.Addr + "…")
		initHist := 2000
		if u.sessionMode.Get() == "chat" {
			initHist = 250
		}
		go func(hist int) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			stream, res, err := client.Attach(ctx, AttachParams{
				Terminal:            key.Addr,
				Observe:             obs,
				ResizeNotifications: true,
				History:             hist,
				Cols:                cols,
				Rows:                rows,
			})
			if err != nil {
				if u.attachGen.Load() != gen {
					return
				}
				if rpc, ok := errors.AsType[*RPCError](err); ok && rpc.Kind() == "not_found" {
					u.app.pool.RefreshInstance(key.InstanceID)
					u.app.post(func() {
						if u.attachGen.Load() == gen && st.diag != nil {
							st.diag.Set("远端找不到终端 " + key.Addr + "。")
						}
					})
					return
				}
				st.SetDiag("attach 失败，尝试读取快照：" + err.Error())
				if r, rerr := client.Read(ctx, key.Addr, 200); rerr == nil && r.Text != "" {
					st.Feed([]byte(strings.ReplaceAll(r.Text, "\n", "\r\n")))
					u.app.post(func() { st.ScrollToBottom() })
					return
				}
				if u.attachGen.Load() == gen {
					st.SetDiag("无法接入终端流：" + explain(err))
				}
				return
			}
			// Atomic token check (safe off UI thread): drop stream if user switched away.
			if u.attachGen.Load() != gen {
				_ = stream.Close()
				return
			}
			st.Attach(stream, res.Terminal.Cols, res.Terminal.Rows)
			// Only fall back to plain-text terminal.read if the ANSI attach stream produced zero bytes after 2.5s.
			time.AfterFunc(2500*time.Millisecond, func() {
				if u.attachGen.Load() != gen {
					return
				}
				if !st.HasReceivedBytes() {
					rctx, rcancel := context.WithTimeout(context.Background(), 6*time.Second)
					defer rcancel()
					r, rerr := client.Read(rctx, key.Addr, 500)
					if rerr == nil && r.Text != "" && !st.HasReceivedBytes() {
						st.Feed([]byte(strings.ReplaceAll(r.Text, "\n", "\r\n")))
						u.app.post(func() { st.ScrollToBottom() })
					} else if rerr != nil {
						st.SetDiag("已接入流，但读取初始快照失败：" + rerr.Error())
					} else if !st.HasReceivedBytes() {
						st.SetDiag("已接入流（当前屏幕无输出）")
					}
				} else {
					u.app.post(func() { st.ScrollToBottom() })
				}
			})
		}(initHist)
	}

	var historyDepth atomic.Int32
	historyDepth.Store(2000)
	st.SetLoadMore(func() {
		client := u.app.clientForKey(key)
		if client == nil {
			return
		}
		nextLines := int(historyDepth.Load()) * 2
		if nextLines > 16000 {
			nextLines = 16000
		}
		historyDepth.Store(int32(nextLines))
		st.SetHistoryHint("正在加载更早历史…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			r, err := client.Read(ctx, key.Addr, nextLines)
			if err != nil {
				st.SetHistoryHint("加载失败，点此重试")
				return
			}
			if r.Text != "" {
				st.Screen().ReplaceHistory([]byte(strings.ReplaceAll(r.Text, "\n", "\r\n")))
				st.bumpRevImmediate()
			}
			if r.Complete || nextLines >= 16000 {
				st.SetHistoryHint("已加载全部可用历史")
			} else {
				st.SetHistoryHint("↑ 继续加载更早历史")
			}
		}()
	})

	st.SetReattach(attachWithMode)
	u.activeTerm.Set(st)
	u.activeChat.Set(chatCtrl)
	if reusedWarm {
		chatCtrl.ResumeLoop()
	} else {
		chatCtrl.StartLoop()
	}
	if at, ok := u.app.FindTerminal(key); ok {
		u.app.PrefetchAgentCatalog(key, at.Terminal, nil)
	}
	if mode == "chat" {
		// Defer VT attach briefly in Chat mode so the JSONL first paint gets 100% of SSH channel priority.
		openGen := u.attachGen.Add(1)
		var fired atomic.Bool
		doAttach := func() {
			if !fired.CompareAndSwap(false, true) {
				return
			}
			if u.attachGen.Load() != openGen {
				return
			}
			attachWithMode(observe, 100, 32)
		}
		u.pendingAttach = doAttach
		time.AfterFunc(320*time.Millisecond, func() {
			u.app.post(doAttach)
		})
	} else {
		attachWithMode(observe, 100, 32)
	}
	u.prefetchRelatedSessions(key)
}

// closeSession releases the active VT attach stream, parks Chat in the warm LRU, and updates persistent snapshot caches.
func (u *uiController) closeSession() {
	u.attachGen.Add(1)
	u.pendingAttach = nil
	curKey := u.activeSession.Get()
	if prevChat := u.activeChat.Get(); prevChat != nil {
		if curKey != (TermKey{}) {
			u.parkWarmChat(curKey, prevChat)
		} else {
			prevChat.Close()
		}
		u.activeChat.Set(nil)
	}
	if prev := u.activeTerm.Get(); prev != nil {
		prev.Close()
		u.activeTerm.Set(nil)
		if curKey != (TermKey{}) && u.app.frameCacheOn.Get() && u.app.cache != nil && u.app.snapshots != nil {
			if snapBytes, ok := u.app.cache.Get(curKey); ok && len(snapBytes) > 0 {
				u.app.snapshots.SaveVT(curKey, snapBytes)
			}
		}
	}
	u.activeSession.Set(TermKey{})
	u.app.SetActiveSession(TermKey{})
}
