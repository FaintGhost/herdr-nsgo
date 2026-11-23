// Command herdr-nsgo is a cross-platform mobile and desktop client for herdr-core:
// concurrent multi-instance SSH + framed binary VT multiplexing, Agent semantic approval
// cards, TOFU security pinning, and Crisp Slate Light/Dark design system.
package main

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
)

func main() { nsgo.Run(Root) }

// uiController owns reactive navigation, modal state, and the active SessionTerminal lifecycle.
type uiController struct {
	app             *App
	activeTab       *core.Signal[string] // home, inbox, instances
	activeSession   *core.Signal[TermKey]
	activeTerm      *core.Signal[*SessionTerminal]
	subPage         *core.Signal[string] // "", settings, tokens
	showNewSession  *core.Signal[bool]
	showAddInstance *core.Signal[bool]
	showQRModal     *core.Signal[bool]
	editingHost     *core.Signal[Host]
	attachGen       atomic.Uint64
}

// Root is the NSGo entry point.
func Root(cx *core.Scope) core.Node {
	return RootWithApp(cx, NewApp(cx))
}

// RootWithApp builds the complete responsive 8-screen shell bound to app (also used by nsgotest).
func RootWithApp(cx *core.Scope, app *App) core.Node {
	u := &uiController{
		app:             app,
		activeTab:       core.NewSignal(cx, "home"),
		activeSession:   core.NewSignal(cx, TermKey{}),
		activeTerm:      core.NewSignal[*SessionTerminal](cx, nil),
		subPage:         core.NewSignal(cx, ""),
		showNewSession:  core.NewSignal(cx, false),
		showAddInstance: core.NewSignal(cx, false),
		showQRModal:     core.NewSignal(cx, false),
		editingHost:     core.NewSignal(cx, Host{UseLocalKey: true}),
	}

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
		// Release active VT attach stream on app exit.
		cx.Cleanup(func() {
			u.closeSession()
		})

		// Foreground wakeup & network change fast reconnect (<500ms wakeup target).
		var lastLifecycle core.Lifecycle = core.LifecycleActive
		cx.Effect(func() {
			lc := cx.Env().Lifecycle()
			if lc == core.LifecycleActive && lastLifecycle != core.LifecycleActive {
				app.Reconnect()
			}
			lastLifecycle = lc
		})
		cx.OnNetworkChange(func(core.Network) {
			app.Reconnect()
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
			if u.subPage.Get() == "tokens" {
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

	// Render the primary 3-Tab navigation column (used full-screen on phone, or as left sidebar on >=680px).
	tabShell := func(cx *core.Scope) core.Node {
		return core.View{
			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100), Height: core.Pct(100)},
			Children: []core.Node{
				core.View{
					Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Overflow: core.Hidden},
					Children: []core.Node{
						core.Show{
							When: func() bool { return u.activeTab.Get() == "inbox" },
							Then: func(cx *core.Scope) core.Node { return u.inboxScreen(cx) },
							Else: func(cx *core.Scope) core.Node {
								return core.Show{
									When: func() bool { return u.activeTab.Get() == "instances" },
									Then: func(cx *core.Scope) core.Node { return u.instancesScreen(cx) },
									Else: func(cx *core.Scope) core.Node { return u.homeScreen(cx) },
								}
							},
						},
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
			// Main page layer: Settings / Tokens -> Onboarding (if 0 instances) -> Wide Split Pane or Compact Phone Shell
			core.Show{
				When: func() bool { return u.subPage.Get() == "tokens" },
				Then: func(cx *core.Scope) core.Node { return u.tokensGalleryScreen(cx) },
				Else: func(cx *core.Scope) core.Node {
					return core.Show{
						When: func() bool { return u.subPage.Get() == "settings" },
						Then: func(cx *core.Scope) core.Node { return u.settingsScreen(cx) },
						Else: func(cx *core.Scope) core.Node {
							return core.Show{
								When: func() bool { return !hasAnyInstance() },
								Then: func(cx *core.Scope) core.Node { return u.onboardingScreen(cx) },
								Else: func(cx *core.Scope) core.Node {
									return core.Show{
										When: isWide,
										Then: func(cx *core.Scope) core.Node {
											// Wide >=680px Split Pane layout (Sidebar 280px + Right Session VT)
											return core.View{
												TestID: "split-pane-layout",
												Style:  core.Style{Direction: core.Row, FlexGrow: 1, Width: core.Pct(100), Height: core.Pct(100)},
												Children: []core.Node{
													core.View{
														StyleFunc: func() core.Style {
															pal := app.paletteOf(cx)
															return core.Style{
																Width:       core.Pt(Tokens.SidebarWidth),
																Height:      core.Pct(100),
																Borders:     core.Edges{Right: 1},
																BorderColor: pal.Separator,
															}
														},
														Children: []core.Node{tabShell(cx)},
													},
													core.View{
														Style: core.Style{FlexGrow: 1, Height: core.Pct(100)},
														Children: []core.Node{
															core.Show{
																When: func() bool { return u.activeSession.Get() != (TermKey{}) },
																Then: func(cx *core.Scope) core.Node { return u.sessionScreen(cx, false) },
																Else: func(cx *core.Scope) core.Node {
																	return core.View{
																		Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(Tokens.Space6)},
																		Children: []core.Node{
																			EmptyStateCard(cx, app, "选择一个会话开始查看", "点击左侧任意终端即可在此实时呈现完整 VT 终端与快捷键栏。", "", "", nil),
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
											// Compact phone layout: Session pushes full-screen over 3-Tab shell
											return core.Show{
												When: func() bool { return u.activeSession.Get() != (TermKey{}) },
												Then: func(cx *core.Scope) core.Node { return u.sessionScreen(cx, true) },
												Else: tabShell,
											}
										},
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
		},
	}
}

// openSession attaches to key (in observe mode by default), pre-seeding from LRU FrameCache.
// Always binds SessionTerminal signals to the root app scope (u.app.cx), never an ephemeral
// row/button child scope that gets disposed when switching from Home/Inbox to Session.
func (u *uiController) openSession(_ *core.Scope, key TermKey) {
	if prev := u.activeTerm.Get(); prev != nil {
		prev.Close()
		u.activeTerm.Set(nil)
	}
	u.activeSession.Set(key)

	rootCX := u.app.cx
	var cache *FrameCache
	if u.app.frameCacheOn.Get() {
		cache = u.app.cache
	}
	enableGhostty := rootCX != nil && !rootCX.Env().Simulated()
	observe := u.app.observeDefault.Get()
	st := NewSessionTerminal(rootCX, key, 100, 32, observe, cache, enableGhostty)

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
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			stream, res, err := client.Attach(ctx, AttachParams{
				Terminal:            key.Addr,
				Observe:             obs,
				ResizeNotifications: true,
				History:             200,
				Cols:                cols,
				Rows:                rows,
			})
			if err != nil {
				st.SetDiag("attach 失败，尝试读取快照：" + err.Error())
				if r, rerr := client.Read(ctx, key.Addr, 200); rerr == nil && r.Text != "" {
					st.Feed([]byte(strings.ReplaceAll(r.Text, "\n", "\r\n")))
					u.app.post(func() { st.ScrollToBottom() })
					return
				}
				st.SetDiag("无法接入终端流：" + explain(err))
				u.app.notifyToast("无法接入终端流：" + explain(err))
				return
			}
			// Atomic token check (safe off UI thread): drop stream if user switched away.
			if u.attachGen.Load() != gen {
				_ = stream.Close()
				return
			}
			st.Attach(stream, res.Terminal.Cols, res.Terminal.Rows)
			// If no initial VT bytes arrive within 400ms (e.g., quiet relayed terminal), seed from terminal.read.
			time.AfterFunc(400*time.Millisecond, func() {
				if u.attachGen.Load() != gen {
					return
				}
				if strings.TrimSpace(st.Screen().VisibleText()) == "" {
					rctx, rcancel := context.WithTimeout(context.Background(), 6*time.Second)
					defer rcancel()
					r, rerr := client.Read(rctx, key.Addr, 200)
					if rerr == nil && r.Text != "" {
						st.Feed([]byte(strings.ReplaceAll(r.Text, "\n", "\r\n")))
						u.app.post(func() { st.ScrollToBottom() })
					} else if rerr != nil {
						st.SetDiag("已接入流，但读取初始快照失败：" + rerr.Error())
					} else {
						st.SetDiag("已接入流（当前屏幕无输出）")
					}
				} else {
					u.app.post(func() { st.ScrollToBottom() })
				}
			})
		}()
	}

	st.SetReattach(attachWithMode)
	u.activeTerm.Set(st)
	attachWithMode(observe, 100, 32)
}

// closeSession releases the active VT attach stream and updates the LRU cache.
func (u *uiController) closeSession() {
	u.attachGen.Add(1)
	if prev := u.activeTerm.Get(); prev != nil {
		prev.Close()
		u.activeTerm.Set(nil)
	}
	u.activeSession.Set(TermKey{})
}
