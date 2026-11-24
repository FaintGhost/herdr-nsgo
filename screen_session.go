package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// sessionScreen renders the unified Session view with seamless 0ms switching between
// Chat View (official NSGo ui/chat + ui/diff + Moshi Floating Glass Composer) and real 2D VT Terminal View:
// - 44px compact safe-area header with 1-tap Quick Session Switcher, fixed 聊天|终端 segmented pill, search, tools
// - Collapsible search bar (filters Chat cards in chat mode; jumps VT matches in vt mode)
// - Active viewport (Chat View or VT Canvas, both kept mounted for <1ms visibility swap)
// - Mode-adaptive bottom composer: Floating Liquid-Glass multiline composer in Chat mode (with >_ quick VT switch,
//   expandable tall editor, quote preview, and swipe-down keyboard dismiss) vs KeycapBar + terminal dock in VT mode.
func (u *uiController) sessionScreen(cx *core.Scope, showBack bool) core.Node {
	a := u.app
	showSearch := core.NewSignal(cx, false)
	showSnippets := core.NewSignal(cx, false)
	showTools := core.NewSignal(cx, false)
	showSwitcher := core.NewSignal(cx, false)

	ctrlArmed := core.NewSignal(cx, false)
	ctrlLocked := core.NewSignal(cx, false)
	var lastCtrlTap time.Time

	composerExpanded := core.NewSignal(cx, true)
	composerTall := core.NewSignal(cx, false)
	composerHeight := core.NewSignal(cx, core.Points(22))
	quotedText := core.NewSignal(cx, "")

	curKey := func() TermKey { return u.activeSession.Get() }
	curTerm := func() AggregatedTerminal {
		t, _ := a.FindTerminal(curKey())
		return t
	}
	curST := func() *SessionTerminal { return u.activeTerm.Get() }
	curChat := func() *ChatSessionController { return u.activeChat.Get() }
	isChatMode := func() bool { return u.sessionMode.Get() == "chat" }

	isCtrlActive := func() bool { return ctrlArmed.Get() || ctrlLocked.Get() }

	sendKeyOrBytes := func(logicalKey string, rawBytes []byte) {
		st := curST()
		if st != nil && st.Attached() {
			_ = st.SendInput(rawBytes, a.vtFontSize.Get())
			return
		}
		a.PressTo(curKey(), logicalKey)
	}

	buildFinalSendText := func(raw string) string {
		q := strings.TrimSpace(quotedText.Get())
		if q == "" {
			return raw
		}
		var qb strings.Builder
		for _, ln := range strings.Split(singleLineClip(q, 220), "\n") {
			qb.WriteString("> ")
			qb.WriteString(ln)
			qb.WriteByte('\n')
		}
		qb.WriteByte('\n')
		qb.WriteString(raw)
		return qb.String()
	}

	sendComposerText := func() {
		k := curKey()
		raw := strings.TrimSpace(a.Compose(k.String()))
		if raw == "" {
			return
		}
		text := buildFinalSendText(raw)
		quotedText.Set("")
		composerTall.Set(false)

		// In Chat View, ALWAYS route through RPC terminal.send so the background
		// SessionTerminal stays in observe=true and never shrinks a 140-col desktop PTY!
		if isChatMode() {
			chatCtrl := curChat()
			var optID string
			if chatCtrl != nil {
				optID = chatCtrl.AddOptimisticUser(text)
			}
			a.SetCompose(k.String(), "")
			playSelectionHaptic(cx)
			// If a test injected a fake TranscriptBackend without a live RPC client, keep the optimistic turn cleanly.
			if a.clientForKey(k) == nil && a.transcriptBackend != nil {
				return
			}
			a.SendToWithCallback(k, text, func(err error) {
				if err != nil {
					if chatCtrl != nil {
						chatCtrl.RemoveOptimistic(optID)
					}
					a.post(func() {
						if strings.TrimSpace(a.Compose(k.String())) == "" {
							a.setCompose(k.String(), raw)
						}
					})
					return
				}
				if chatCtrl != nil {
					chatCtrl.RefreshAsync()
				}
			})
			return
		}
		st := curST()
		if st != nil && st.Attached() {
			if err := st.SendInput([]byte(text+"\r"), a.vtFontSize.Get()); err == nil {
				a.SetCompose(k.String(), "")
				return
			}
		}
		a.SendTo(k, text)
	}

	keycaps := []KeycapItem{
		{TestID: "keycap-esc", Label: "Esc", OnPress: func() { sendKeyOrBytes("esc", []byte{0x1b}) }},
		{TestID: "keycap-tab", Label: "Tab", OnPress: func() { sendKeyOrBytes("tab", []byte{'\t'}) }},
		{
			TestID: "keycap-ctrl",
			LabelFunc: func() string {
				if ctrlLocked.Get() {
					return "Ctrl🔒"
				}
				return "Ctrl"
			},
			Active: isCtrlActive,
			OnPress: func() {
				now := time.Now()
				if ctrlLocked.Get() {
					ctrlLocked.Set(false)
					ctrlArmed.Set(false)
					return
				}
				if ctrlArmed.Get() && now.Sub(lastCtrlTap) < 480*time.Millisecond {
					ctrlLocked.Set(true)
					ctrlArmed.Set(false)
					a.notifyToast("Ctrl 已双击锁定（再点一次解除）")
					return
				}
				ctrlArmed.Set(!ctrlArmed.Get())
				lastCtrlTap = now
			},
		},
		{TestID: "keycap-up", Label: "↑", OnPress: func() { sendKeyOrBytes("up", []byte("\x1b[A")) }},
		{TestID: "keycap-down", Label: "↓", OnPress: func() { sendKeyOrBytes("down", []byte("\x1b[B")) }},
		{TestID: "keycap-left", Label: "←", OnPress: func() { sendKeyOrBytes("left", []byte("\x1b[D")) }},
		{TestID: "keycap-right", Label: "→", OnPress: func() { sendKeyOrBytes("right", []byte("\x1b[C")) }},
		{TestID: "keycap-enter", Label: "↵", OnPress: func() { sendKeyOrBytes("enter", []byte{'\r'}) }},
		{
			TestID: "keycap-rightclick",
			Label:  "右键",
			Active: func() bool {
				st := curST()
				return st != nil && st.RightClickArmed()
			},
			OnPress: func() {
				st := curST()
				if st == nil {
					return
				}
				if st.ToggleRightClickArmed() {
					a.notifyToast("已武装右键：点按终端任意位置呼出 herdr 菜单")
				} else {
					a.notifyToast("已取消右键武装")
				}
			},
		},
		{TestID: "keycap-ctrlc", Label: "^C", Danger: true, OnPress: func() { sendKeyOrBytes("ctrl+c", []byte{0x03}) }},
		{TestID: "keycap-ctrld", Label: "^D", OnPress: func() { sendKeyOrBytes("ctrl+d", []byte{0x04}) }},
		{TestID: "keycap-ctrll", Label: "^L", OnPress: func() { sendKeyOrBytes("ctrl+l", []byte{0x0c}) }},
		{
			TestID: "keycap-composer",
			Label:  "⌨",
			Active: composerExpanded.Get,
			OnPress: func() {
				animateSpringLayout(cx)
				composerExpanded.Set(!composerExpanded.Get())
			},
		},
	}

	var leftControls []core.Node
	if showBack {
		leftControls = append(leftControls, IconButton(cx, a, "session-back-btn", "返回", iconBack, false, func() {
			u.closeSession()
		}))
	}
	// Moshi-inspired 1-tap Quick Session Switcher button in TopHeader (fixed across Chat & VT modes!)
	leftControls = append(leftControls, IconButton(cx, a, "session-switcher-btn", "快速切换会话", iconSwitcher, false, func() {
		showSwitcher.Set(true)
	}))
	leftNode := core.View{
		Style:    core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6), FlexShrink: 0},
		Children: leftControls,
	}

	modeSegmentPill := core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexShrink:   0,
				Direction:    core.Row,
				AlignItems:   core.AlignCenter,
				Background:   pal.SurfaceRaised,
				CornerRadius: Tokens.RadiusCtrl,
				BorderWidth:  1,
				BorderColor:  pal.Border,
				Padding:      core.All(core.Points(2)),
				Gap:          core.Points(2),
			}
		},
		Children: []core.Node{
			sessionModeSegmentBtn(cx, a, "session-mode-chat", "聊天", isChatMode, func() {
				u.setSessionMode("chat")
			}),
			sessionModeSegmentBtn(cx, a, "session-mode-vt", "终端", func() bool { return !isChatMode() }, func() {
				u.setSessionMode("vt")
			}),
		},
	}

	// Shared single-instance input editor (`id:session-composer`) used in both Floating Chat Glass Card and VT Dock
	// so there is never a duplicate TestID while switching modes in <1ms!
	composerEditor := func(cx *core.Scope) core.Node {
		return base.Input{
			TestID:         "session-composer",
			Label:          "发消息或指令",
			Placeholder:    "发消息或指令…",
			Multiline:      true,
			SubmitBehavior: core.SubmitBehaviorSubmit,
			Value: func() string {
				return a.Compose(curKey().String())
			},
			OnChange: func(txt string) {
				if !isChatMode() && isCtrlActive() && len(txt) == 1 {
					ch := strings.ToLower(txt)[0]
					if ch >= 'a' && ch <= 'z' {
						if !ctrlLocked.Get() {
							ctrlArmed.Set(false)
						}
						sendKeyOrBytes("ctrl+"+string(ch), []byte{ch - 'a' + 1})
						return
					}
				}
				a.SetCompose(curKey().String(), txt)
			},
			OnSubmit: func(string) {
				sendComposerText()
			},
			OnContentSizeChange: func(sz core.Size) {
				composerHeight.Set(sz.Height)
			},
			Style: core.Style{
				Width:             core.Pct(100),
				FontSize:          Tokens.FontBody,
				LineHeight:        core.Points(20),
				TextAlignVertical: core.TextAlignVerticalTop,
			},
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				if !isChatMode() {
					return core.Style{
						MinHeight:        core.Pt(Tokens.SizeTouch),
						MaxHeight:        core.Pt(64),
						Padding:          core.Symmetric(core.Points(9), Tokens.Space3),
						Background:       pal.SurfaceRaised,
						Color:            pal.Text,
						PlaceholderColor: pal.TextMuted,
						CaretColor:       pal.Accent,
						CornerRadius:     Tokens.RadiusCtrl,
						BorderColor:      pal.Border,
						BorderWidth:      1,
					}
				}
				maxLinesH := core.Points(112)
				minH := core.Points(36)
				if composerTall.Get() {
					maxLinesH = 220
					minH = 148
				}
				curH := composerHeight.Get() + 10
				if curH < minH {
					curH = minH
				}
				if curH > maxLinesH {
					curH = maxLinesH
				}
				return core.Style{
					Height:           core.Pt(curH),
					MinHeight:        core.Pt(minH),
					Padding:          core.Symmetric(core.Points(4), core.Points(6)),
					Background:       core.Transparent,
					Color:            pal.Text,
					PlaceholderColor: pal.TextMuted,
					CaretColor:       pal.Accent,
				}
			},
		}
	}

	uploadAction := func() {
		if cx == nil {
			return
		}
		cx.PickImages(core.PickOptions{Limit: 1}, func(files []core.PickedFile) {
			if len(files) == 0 {
				return
			}
			f := files[0]
			a.UploadFileTo(curKey(), f.Name, f.Data)
		})
	}

	sendOrInterruptNode := func(cx *core.Scope) core.Node {
		return core.Show{
			When: func() bool {
				return isChatMode() &&
					curTerm().Terminal.StateWord() == "working" &&
					strings.TrimSpace(a.Compose(curKey().String())) == ""
			},
			Then: func(cx *core.Scope) core.Node {
				return ActionButton(cx, a, "session-interrupt-btn", "中断", ActionDanger, func() {
					a.InterruptTo(curKey())
				})
			},
			Else: func(cx *core.Scope) core.Node {
				return IconButton(cx, a, "session-send-btn", "发送", iconSend, true, sendComposerText)
			},
		}
	}

	return core.View{
		TestID: "screen-session",
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexGrow:   1,
				FlexShrink: 1,
				MinHeight:  core.Pt(0),
				Position:   core.Relative,
				Background: pal.Bg,
			}
		},
		Children: []core.Node{
			// 44px compact session top bar with FIXED right controls across modes so toggling 聊天 <-> 终端 NEVER shifts or deforms the header!
			TopHeader(cx, a, func() string {
				return curTerm().Terminal.Label()
			}, leftNode, []core.Node{
				modeSegmentPill,
				IconButton(cx, a, "session-search-btn", "搜索", iconSearch, false, func() {
					animateSpringLayout(cx)
					showSearch.Set(!showSearch.Get())
				}),
				IconButton(cx, a, "session-tools-btn", "更多工具", iconMore, false, func() {
					showTools.Set(true)
				}),
			}),
			ToastBanner(cx, a),
			// Collapsible in-session search bar (mode-aware)
			core.Show{
				When: showSearch.Get,
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Direction:   core.Row,
								AlignItems:  core.AlignCenter,
								Gap:         Tokens.Space2,
								Padding:     core.Symmetric(Tokens.Space2, Tokens.Space3),
								Background:  pal.Surface,
								Borders:     core.Edges{Bottom: 1},
								BorderColor: pal.Separator,
							}
						},
						Children: []core.Node{
							core.View{
								Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
								Children: []core.Node{
									ui.Input{
										TestID:      "session-search-input",
										Placeholder: "搜索当前会话内容…",
										Value: func() string {
											if isChatMode() {
												if ch := curChat(); ch != nil && ch.searchQuery != nil {
													return ch.searchQuery.Get()
												}
												return ""
											}
											st := curST()
											if st == nil || st.searchQuery == nil {
												return ""
											}
											return st.searchQuery.Get()
										},
										OnChange: func(q string) {
											if isChatMode() {
												if ch := curChat(); ch != nil && ch.searchQuery != nil {
													ch.searchQuery.Set(q)
												}
												return
											}
											if st := curST(); st != nil {
												st.SetSearch(q)
											}
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Width:        core.Pct(100),
												MinHeight:    core.Pt(Tokens.SizeKey),
												Background:   pal.SurfaceRaised,
												Color:        pal.Text,
												CornerRadius: Tokens.RadiusKey,
												BorderColor:  pal.Border,
												BorderWidth:  1,
											}
										},
									},
								},
							},
							core.Show{
								When: func() bool { return !isChatMode() },
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2},
										Children: []core.Node{
											core.Text{
												ContentFunc: func() string {
													st := curST()
													if st == nil {
														return "0"
													}
													m := st.SearchMatches()
													if len(m) == 0 {
														return "0"
													}
													idx := 0
													if st.searchIdx != nil {
														idx = st.searchIdx.Get()
													}
													return fmt.Sprintf("%d/%d", idx+1, len(m))
												},
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
												},
											},
											ActionButton(cx, a, "search-prev-btn", "↑", ActionSecondary, func() {
												if st := curST(); st != nil {
													st.PrevSearchMatch()
												}
											}),
											ActionButton(cx, a, "search-next-btn", "↓", ActionSecondary, func() {
												if st := curST(); st != nil {
													st.NextSearchMatch()
												}
											}),
										},
									}
								},
							},
						},
					}
				},
			},
			// Main session body: BOTH Chat View and Terminal View stay mounted AND laid out in memory
			// (Absolute full-bleed + VisibilityHidden), so toggling 聊天 <-> 终端 runs ZERO Yoga re-layout
			// and ZERO view remount — flipping native View visibility in <1ms!
			core.View{
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Position: core.Relative, Overflow: core.Hidden},
				Children: []core.Node{
					// Layer 1: Chat View (always mounted & laid out while session is open)
					core.View{
						StyleFunc: func() core.Style {
							vis := core.VisibilityVisible
							pe := core.PointerEventsAuto
							z := 2
							if !isChatMode() {
								vis = core.VisibilityHidden
								pe = core.PointerEventsNone
								z = 0
							}
							return core.Style{
								Position:      core.Absolute,
								Top:           core.Pt(0),
								Right:         core.Pt(0),
								Bottom:        core.Pt(0),
								Left:          core.Pt(0),
								Visibility:    vis,
								PointerEvents: pe,
								ZIndex:        z,
							}
						},
						Children: []core.Node{
							core.Each(
								func() []*ChatSessionController {
									ch := curChat()
									if ch == nil {
										return nil
									}
									return []*ChatSessionController{ch}
								},
								func(ch *ChatSessionController) *ChatSessionController { return ch },
								func(cx *core.Scope, item func() *ChatSessionController, _ func() int) core.Node {
									return RenderChatSessionWithCallbacks(
										cx,
										a,
										item(),
										curTerm,
										func() { u.setSessionMode("vt") },
										func(targetKey TermKey) { u.openSession(cx, targetKey) },
										func(replyText string) {
											quotedText.Set(replyText)
											a.notifyToast("已引用回复到输入框")
										},
									)
								},
							),
						},
					},
					// Layer 2: Terminal View + dedicated VT sub-bar (always mounted & laid out while session is open)
					core.View{
						StyleFunc: func() core.Style {
							vis := core.VisibilityVisible
							pe := core.PointerEventsAuto
							z := 2
							if isChatMode() {
								vis = core.VisibilityHidden
								pe = core.PointerEventsNone
								z = 0
							}
							return core.Style{
								Position:      core.Absolute,
								Top:           core.Pt(0),
								Right:         core.Pt(0),
								Bottom:        core.Pt(0),
								Left:          core.Pt(0),
								Visibility:    vis,
								PointerEvents: pe,
								ZIndex:        z,
							}
						},
						Children: []core.Node{
							// Dedicated VT sub-status bar: shows PTY cols×rows + Observe/Fit toggle without touching TopHeader!
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Direction:      core.Row,
										AlignItems:     core.AlignCenter,
										JustifyContent: core.JustifySpaceBetween,
										Gap:            Tokens.Space2,
										Padding:        core.Symmetric(core.Points(5), Tokens.PagePadX),
										Background:     pal.Surface,
										Borders:        core.Edges{Bottom: 1},
										BorderColor:    pal.Separator,
									}
								},
								Children: []core.Node{
									core.Text{
										MaxLines: 1,
										ContentFunc: func() string {
											st := curST()
											cols, rows := 0, 0
											if st != nil {
												cols, rows = st.Screen().Size()
											}
											if cols <= 0 {
												cols = curTerm().Terminal.Cols
												rows = curTerm().Terminal.Rows
											}
											obs := st == nil || (st.observeSig != nil && st.observeSig.Get()) || (st.observeSig == nil && st.Observe())
											modeNote := "已适配手机列宽"
											if obs {
												modeNote = "观察模式 · 不改桌面列宽"
											}
											if cols > 0 && rows > 0 {
												return fmt.Sprintf("%d×%d · %s", cols, rows, modeNote)
											}
											return modeNote
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexGrow:   1,
												FlexShrink: 1,
												MinWidth:   core.Pt(0),
												Color:      pal.TextMuted,
												FontSize:   Tokens.FontMonoMeta,
												FontFamily: core.FontMonospace,
											}
										},
									},
									FilterPill(cx, a, "session-fit-toggle", func() string {
										st := curST()
										if st == nil || (st.observeSig != nil && st.observeSig.Get()) || (st.observeSig == nil && st.Observe()) {
											return "观察中"
										}
										return "适配列宽"
									}, func() bool {
										st := curST()
										return st != nil && !st.Observe()
									}, nil, func() {
										st := curST()
										if st == nil {
											return
										}
										nextObserve := !st.Observe()
										st.SetObserve(nextObserve, a.vtFontSize.Get())
										if nextObserve {
											a.notifyToast("已切换为观察模式（不改动远端桌面尺寸）")
										} else {
											a.notifyToast("已适配手机列宽（发送 RESIZE）")
										}
									}),
								},
							},
							core.View{
								Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Position: core.Relative, Overflow: core.Hidden},
								Children: []core.Node{
									core.Each(
										func() []*SessionTerminal {
											st := curST()
											if st == nil {
												return nil
											}
											return []*SessionTerminal{st}
										},
										func(st *SessionTerminal) *SessionTerminal { return st },
										func(cx *core.Scope, item func() *SessionTerminal, _ func() int) core.Node {
											return RenderSessionTerminalVisible(
												cx,
												item(),
												func() Palette { return a.paletteOf(cx) },
												func() VTPalette { return a.vtPaletteOf(cx) },
												a.vtFontSize.Get,
												func() bool { return !isChatMode() },
											)
										},
									),
									core.Show{
										When: func() bool { return curST() == nil },
										Then: func(cx *core.Scope) core.Node {
											return RenderSessionTerminal(
												cx,
												nil,
												func() Palette { return a.paletteOf(cx) },
												func() VTPalette { return a.vtPaletteOf(cx) },
												a.vtFontSize.Get,
											)
										},
									},
									core.Show{
										When: func() bool {
											st := curST()
											return st != nil && st.scrolledUp != nil && st.scrolledUp.Get()
										},
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												Style: core.Style{
													Position: core.Absolute,
													Right:    core.Pt(14),
													Bottom:   core.Pt(14),
												},
												Children: []core.Node{
													ActionButton(cx, a, "vt-scroll-bottom-fab", "↓ 底部", ActionPrimary, func() {
														if st := curST(); st != nil {
															st.ScrollToBottom()
														}
													}),
												},
											}
										},
									},
								},
							},
						},
					},
					// Moshi Floating Liquid-Glass Composer overlay in Chat mode (hovers over blurred bottom edge!)
					core.View{
						PointerEvents: core.PointerEventsBoxNone,
						StyleFunc: func() core.Style {
							if !isChatMode() {
								return core.Style{Display: core.DisplayNone}
							}
							return core.Style{
								Position:   core.Absolute,
								Left:       core.Pt(12),
								Right:      core.Pt(12),
								Bottom:     core.Pt(8),
								PaddingEnv: core.EnvEdges{Bottom: core.EnvKeyboard},
								ZIndex:     5,
							}
						},
						Children: []core.Node{
							core.View{
								TestID:        "chat-glass-composer",
								PanActivation: core.PanActivation{ActiveOffsetY: 8, FailOffsetX: 16},
								OnPan: func(ev core.PanEvent) {
									if ev.Phase != core.GestureEnded {
										return
									}
									// Moshi COMPOSER_SWIPE_DISMISS: swipe down on composer dismisses keyboard;
									// swipe up expands into tall prompt editor!
									if ev.Translation.Y > 26 {
										if composerTall.Get() {
											animateSpringLayout(cx)
											composerTall.Set(false)
										} else if cx != nil {
											cx.DismissKeyboard()
										}
									} else if ev.Translation.Y < -32 && !composerTall.Get() {
										animateSpringLayout(cx)
										playSelectionHaptic(cx)
										composerTall.Set(true)
									}
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Background:      pal.GlassSurface,
										BackdropFilters: []core.Filter{core.Blur(24)},
										GlassEffect:     core.GlassRegular,
										CornerRadius:    core.Points(14),
										BorderWidth:     1,
										BorderColor:     pal.GlassBorder,
										Padding:         core.Symmetric(core.Points(8), core.Points(10)),
										Margin:          core.Edges{Bottom: max(0, cx.Env().SafeArea().Bottom-4)},
										Gap:             core.Points(6),
									}
								},
								Children: []core.Node{
									// Subtle Moshi resize pill handle
									core.View{
										Style: core.Style{AlignItems: core.AlignCenter},
										Children: []core.Node{
											core.View{
												StyleFunc: func() core.Style {
													return core.Style{
														Width:        core.Pt(28),
														Height:       core.Pt(3),
														CornerRadius: 2,
														Background:   a.paletteOf(cx).Fill,
													}
												},
											},
										},
									},
									// Optional Quoted Reply chip (commentOnSelection)
									core.Show{
										When: func() bool { return strings.TrimSpace(quotedText.Get()) != "" },
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												TestID: "composer-quote-banner",
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														Direction:    core.Row,
														AlignItems:   core.AlignCenter,
														Gap:          Tokens.Space2,
														Background:   pal.AccentSoft,
														CornerRadius: Tokens.RadiusKey,
														Borders:      core.Edges{Left: 3},
														BorderColor:  pal.Accent,
														Padding:      core.Symmetric(core.Points(4), core.Points(8)),
													}
												},
												Children: []core.Node{
													core.Text{
														MaxLines:    1,
														ContentFunc: func() string { return "引用: " + singleLineClip(quotedText.Get(), 64) },
														StyleFunc: func() core.Style {
															return core.Style{
																FlexGrow:   1,
																FlexShrink: 1,
																MinWidth:   core.Pt(0),
																Color:      a.paletteOf(cx).TextSecondary,
																FontSize:   Tokens.FontMonoMeta,
															}
														},
													},
													core.View{
														TestID: "composer-quote-clear",
														Role:   core.RoleButton,
														OnPress: func(core.PressEvent) {
															quotedText.Set("")
														},
														Style: core.Style{Padding: core.Symmetric(core.Points(2), core.Points(6))},
														Children: []core.Node{
															core.Text{
																Content: "×",
																StyleFunc: func() core.Style {
																	return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: Tokens.FontBody, FontWeight: core.FontWeightBold}
																},
															},
														},
													},
												},
											}
										},
									},
									// Shared single-instance editor mounted inside Chat Glass Card ONLY when isChatMode()!
									core.Show{
										When: isChatMode,
										Then: composerEditor,
									},
									// Bottom control row inside glass card: [图片] [短语] [展开长文] ... [>_ 终端] [中断/发送]
									core.Show{
										When: isChatMode,
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												Style: core.Style{
													Direction:      core.Row,
													AlignItems:     core.AlignCenter,
													JustifyContent: core.JustifySpaceBetween,
													Gap:            Tokens.Space2,
												},
												Children: []core.Node{
													core.View{
														Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6)},
														Children: []core.Node{
															IconButton(cx, a, "session-upload-btn", "上传图片", iconImage, false, uploadAction),
															IconButton(cx, a, "session-snippets-btn", "常用短语", iconSnippets, false, func() {
																showSnippets.Set(true)
															}),
															IconButton(cx, a, "session-expand-composer-btn", "展开长文编辑", iconExpand, composerTall.Get(), func() {
																animateSpringLayout(cx)
																composerTall.Set(!composerTall.Get())
															}),
														},
													},
													core.View{
														Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6)},
														Children: []core.Node{
															// Moshi signature thumb-level quick return to terminal (>_ 终端)!
															FilterPill(cx, a, "chat-composer-vt-btn", func() string { return ">_ 终端" }, func() bool { return false }, nil, func() {
																u.setSessionMode("vt")
															}),
															sendOrInterruptNode(cx),
														},
													},
												},
											}
										},
									},
								},
							},
						},
					},
				},
			},
			// Terminal-mode bottom dock (KeycapBar + compact terminal composer; hidden in Chat mode!)
			core.View{
				StyleFunc: func() core.Style {
					if isChatMode() {
						return core.Style{Display: core.DisplayNone}
					}
					pal := a.paletteOf(cx)
					return core.Style{
						Display:    core.DisplayFlex,
						Background: pal.Surface,
						PaddingEnv: core.EnvEdges{Bottom: core.EnvKeyboard},
					}
				},
				Children: []core.Node{
					KeycapBar(cx, a, keycaps),
					core.Show{
						When: func() bool { return !isChatMode() && composerExpanded.Get() },
						Then: func(cx *core.Scope) core.Node {
							return core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Direction:   core.Row,
										AlignItems:  core.AlignCenter,
										Gap:         Tokens.Space2,
										Padding:     core.Symmetric(Tokens.Space2, Tokens.Space3),
										PaddingEnv:  core.EnvEdges{Bottom: core.EnvSafeArea},
										Background:  pal.Surface,
										Borders:     core.Edges{Top: 1},
										BorderColor: pal.Separator,
									}
								},
								Children: []core.Node{
									IconButton(cx, a, "session-upload-btn", "上传图片", iconImage, false, uploadAction),
									IconButton(cx, a, "session-snippets-btn", "常用短语", iconSnippets, false, func() {
										showSnippets.Set(true)
									}),
									core.View{
										Style:    core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
										Children: []core.Node{composerEditor(cx)},
									},
									sendOrInterruptNode(cx),
								},
							}
						},
					},
				},
			},
			// Moshi-inspired Quick Session Switcher Sheet (swipe-dismissable, grouped by NeedsYou -> Working -> Idle)
			core.Show{
				When: showSwitcher.Get,
				Then: func(cx *core.Scope) core.Node {
					all := a.aggTerms.Get()
					var waiting, working, idle []AggregatedTerminal
					for _, it := range all {
						if it.Terminal.StateWord() == "finished" {
							continue
						}
						switch {
						case it.Terminal.NeedsYou():
							waiting = append(waiting, it)
						case it.Terminal.StateWord() == "working":
							working = append(working, it)
						default:
							idle = append(idle, it)
						}
					}
					var body []core.Node
					appendGroup := func(title string, list []AggregatedTerminal) {
						if len(list) == 0 {
							return
						}
						tCopy := fmt.Sprintf("%s · %d", title, len(list))
						body = append(body, SectionHeader(cx, a, func() string { return tCopy }, nil))
						for _, entry := range list {
							it := entry
							isCur := it.Key == curKey()
							body = append(body, core.View{
								TestID: "switcher-item-" + it.Key.String(),
								Role:   core.RoleButton,
								OnPress: func(core.PressEvent) {
									playSelectionHaptic(cx)
									showSwitcher.Set(false)
									if it.Key != curKey() {
										u.openSession(cx, it.Key)
									}
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									bg := pal.SurfaceRaised
									bc := pal.Border
									if isCur {
										bg = pal.AccentSoft
										bc = pal.Accent
									}
									return core.Style{
										Direction:      core.Row,
										AlignItems:     core.AlignCenter,
										JustifyContent: core.JustifySpaceBetween,
										Gap:            Tokens.Space2,
										Background:     bg,
										CornerRadius:   Tokens.RadiusCard,
										BorderWidth:    1,
										BorderColor:    bc,
										Padding:        core.Symmetric(core.Points(10), Tokens.Space3),
									}
								},
								Children: []core.Node{
									HarnessIconBadge(cx, a, func() string { return it.Terminal.AppName() }),
									core.View{
										Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
										Children: []core.Node{
											core.Text{
												MaxLines: 1,
												Content:  it.Terminal.Label(),
												StyleFunc: func() core.Style {
													return core.Style{
														Color:      a.paletteOf(cx).Text,
														FontSize:   Tokens.FontRowTitle,
														FontWeight: core.FontWeightSemibold,
													}
												},
											},
											core.Text{
												MaxLines: 1,
												Content:  fmt.Sprintf("%s · %s · %s", it.InstanceName, it.Key.Addr, shortPath(it.Terminal.CurrentDir())),
												StyleFunc: func() core.Style {
													return core.Style{
														Color:      a.paletteOf(cx).TextMuted,
														FontSize:   Tokens.FontMonoMeta,
														FontFamily: core.FontMonospace,
													}
												},
											},
										},
									},
									StatusBadge(cx, a, func() string { return it.Terminal.StateWord() }),
								},
							})
						}
					}
					appendGroup("需要你", waiting)
					appendGroup("运行中", working)
					appendGroup("空闲与在线", idle)
					if len(body) == 0 {
						body = append(body, EmptyStateCard(cx, a, "暂无其他活跃会话", "你可以在首页新建 Claude / Codex 或终端会话。", "", "", nil))
					}
					return SheetModal(cx, a, "sheet-session-switcher", "快速切换会话", "sheet-session-switcher-close", func() {
						showSwitcher.Set(false)
					}, body)
				},
			},
			// Snippets bottom sheet
			core.Show{
				When: showSnippets.Get,
				Then: func(cx *core.Scope) core.Node {
					return SheetModal(cx, a, "sheet-snippets", "常用短语 (Snippets)", "sheet-snippets-close", func() {
						showSnippets.Set(false)
					}, []core.Node{
						core.Each(
							func() []Snippet { return a.snippets.Get() },
							func(s Snippet) string { return s.ID },
							func(cx *core.Scope, item func() Snippet, _ func() int) core.Node {
								sn0 := item()
								return core.View{
									TestID: "snippet-item-" + sn0.ID,
									Role:   core.RoleButton,
									OnPress: func(core.PressEvent) {
										playSelectionHaptic(cx)
										sn := item()
										a.SetCompose(curKey().String(), sn.Text)
										showSnippets.Set(false)
									},
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{
											Background:   pal.SurfaceRaised,
											CornerRadius: Tokens.RadiusCtrl,
											BorderWidth:  1,
											BorderColor:  pal.Border,
											Padding:      core.All(Tokens.Space3),
											Gap:          core.Points(2),
										}
									},
									Children: []core.Node{
										core.Text{
											ContentFunc: func() string { return item().Label },
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightSemibold}
											},
										},
										core.Text{
											ContentFunc: func() string { return item().Text },
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
											},
										},
									},
								}
							},
						),
					})
				},
			},
			// Tools / Links / Lifecycle bottom sheet (with Moshi's StripHerdrScrollbar clean copy!)
			core.Show{
				When: showTools.Get,
				Then: func(cx *core.Scope) core.Node {
					var links []string
					if st := curST(); st != nil {
						links = st.Screen().ExtractLinks()
					}
					body := []core.Node{
						ActionButton(cx, a, "tool-copy-text", "复制干净屏幕文本（自动去滚动条）", ActionSecondary, func() {
							if st := curST(); st != nil && cx != nil {
								clean := StripHerdrScrollbar(st.Screen().PlainText())
								cx.SetClipboard(clean)
								a.notifyToast("已复制干净终端文本到剪贴板")
							}
							showTools.Set(false)
						}),
						ActionButton(cx, a, "tool-pick-file", "上传本地文件到工作目录", ActionSecondary, func() {
							showTools.Set(false)
							if cx == nil {
								return
							}
							cx.PickFiles(core.PickFilesOptions{}, func(files []core.File, err error) {
								if err != nil || len(files) == 0 {
									return
								}
								f := files[0]
								if f.Path == "" {
									a.notifyToast("无法读取所选文件路径")
									return
								}
								data, rerr := os.ReadFile(f.Path)
								if rerr != nil {
									a.notifyToast("读取文件失败：" + rerr.Error())
									return
								}
								a.UploadFileTo(curKey(), f.Name, data)
							})
						}),
						ActionButton(cx, a, "tool-interrupt", "发送中断 (Interrupt)", ActionSecondary, func() {
							a.InterruptTo(curKey())
							showTools.Set(false)
						}),
						ActionButton(cx, a, "tool-kill", "终止终端 (Kill)", ActionDanger, func() {
							a.KillTerminal(curKey())
							showTools.Set(false)
						}),
						ActionButton(cx, a, "tool-rm", "清理已结束终端 (Rm)", ActionDanger, func() {
							a.RemoveTerminal(curKey())
							showTools.Set(false)
							u.closeSession()
						}),
					}
					if len(links) > 0 {
						body = append(body, SectionHeader(cx, a, func() string { return "识别到的链接与路径" }, nil))
						for i, l := range links {
							link := l
							body = append(body, ActionButton(cx, a, fmt.Sprintf("tool-link-%d", i), link, ActionGhost, func() {
								if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
									if cx != nil {
										cx.OpenURL(link, nil)
									}
								} else if cx != nil {
									cx.SetClipboard(link)
									a.notifyToast("已复制路径：" + link)
								}
								showTools.Set(false)
							}))
						}
					}
					return SheetModal(cx, a, "sheet-session-tools", "会话工具与链接", "sheet-session-tools-close", func() {
						showTools.Set(false)
					}, body)
				},
			},
		},
	}
}

func sessionModeSegmentBtn(cx *core.Scope, a *App, testID, label string, activeFn func() bool, onPress func()) core.Node {
	scale := newPressScale(cx)
	return core.View{
		TestID:      testID,
		Role:        core.RoleButton,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.95) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			if onPress != nil {
				onPress()
			}
		},
		Style: core.Style{Animated: pressAnimStyle(scale)},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bg := pal.SurfaceRaised
			if activeFn() {
				bg = pal.Accent
			}
			return core.Style{
				FlexShrink:     0,
				MinHeight:      core.Pt(26),
				Padding:        core.Symmetric(core.Points(3), core.Points(9)),
				CornerRadius:   Tokens.RadiusKey,
				Background:     bg,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
			}
		},
		Children: []core.Node{
			core.Text{
				Content: label,
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg := pal.TextSecondary
					if activeFn() {
						fg = pal.AccentFg
					}
					return core.Style{
						Color:      fg,
						FontSize:   Tokens.FontCaption,
						FontWeight: core.FontWeightSemibold,
					}
				},
			},
		},
	}
}
