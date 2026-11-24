package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// sessionScreen renders the unified Session view with seamless 0ms switching between
// Chat View (native JSONL + VT fallback) and real 2D VT Terminal View:
// - 44px compact safe-area header (back, title, 聊天|终端 segmented pill, mode chip, search, tools)
// - Collapsible search bar (filters Chat cards in chat mode; jumps VT matches in vt mode)
// - Active viewport (Chat View or VT Canvas)
// - Keyboard-aware bottom dock (KeycapBar shown only in VT mode + Composer with working Interrupt button).
func (u *uiController) sessionScreen(cx *core.Scope, showBack bool) core.Node {
	a := u.app
	showSearch := core.NewSignal(cx, false)
	showSnippets := core.NewSignal(cx, false)
	showTools := core.NewSignal(cx, false)
	ctrlLatched := core.NewSignal(cx, false)
	composerExpanded := core.NewSignal(cx, true)

	curKey := func() TermKey { return u.activeSession.Get() }
	curTerm := func() AggregatedTerminal {
		t, _ := a.FindTerminal(curKey())
		return t
	}
	curST := func() *SessionTerminal { return u.activeTerm.Get() }
	curChat := func() *ChatSessionController { return u.activeChat.Get() }
	isChatMode := func() bool { return u.sessionMode.Get() == "chat" }

	sendKeyOrBytes := func(logicalKey string, rawBytes []byte) {
		st := curST()
		if st != nil && st.Attached() {
			_ = st.SendInput(rawBytes, a.vtFontSize.Get())
			return
		}
		a.PressTo(curKey(), logicalKey)
	}

	sendComposerText := func() {
		k := curKey()
		text := strings.TrimSpace(a.Compose(k.String()))
		if text == "" {
			return
		}
		// In Chat View, ALWAYS route through RPC terminal.send so the background
		// SessionTerminal stays in observe=true and never shrinks a 140-col desktop PTY!
		if isChatMode() {
			chat := curChat()
			var optID string
			if chat != nil {
				optID = chat.AddOptimisticUser(text)
			}
			a.SetCompose(k.String(), "")
			// If a test injected a fake TranscriptBackend without a live RPC client, keep the optimistic turn cleanly.
			if a.clientForKey(k) == nil && a.transcriptBackend != nil {
				return
			}
			a.SendToWithCallback(k, text, func(err error) {
				if err != nil {
					if chat != nil {
						chat.RemoveOptimistic(optID)
					}
					a.post(func() {
						if strings.TrimSpace(a.Compose(k.String())) == "" {
							a.setCompose(k.String(), text)
						}
					})
					return
				}
				if chat != nil {
					chat.RefreshAsync()
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
			Label:  "Ctrl",
			Active: ctrlLatched.Get,
			OnPress: func() {
				ctrlLatched.Set(!ctrlLatched.Get())
			},
		},
		{TestID: "keycap-up", Label: "↑", OnPress: func() { sendKeyOrBytes("up", []byte("\x1b[A")) }},
		{TestID: "keycap-down", Label: "↓", OnPress: func() { sendKeyOrBytes("down", []byte("\x1b[B")) }},
		{TestID: "keycap-left", Label: "←", OnPress: func() { sendKeyOrBytes("left", []byte("\x1b[D")) }},
		{TestID: "keycap-right", Label: "→", OnPress: func() { sendKeyOrBytes("right", []byte("\x1b[C")) }},
		{TestID: "keycap-enter", Label: "↵", OnPress: func() { sendKeyOrBytes("enter", []byte{'\r'}) }},
		{TestID: "keycap-ctrlc", Label: "^C", Danger: true, OnPress: func() { sendKeyOrBytes("ctrl+c", []byte{0x03}) }},
		{TestID: "keycap-ctrld", Label: "^D", OnPress: func() { sendKeyOrBytes("ctrl+d", []byte{0x04}) }},
		{TestID: "keycap-ctrll", Label: "^L", OnPress: func() { sendKeyOrBytes("ctrl+l", []byte{0x0c}) }},
		{
			TestID: "keycap-composer",
			Label:  "⌨",
			Active: composerExpanded.Get,
			OnPress: func() {
				composerExpanded.Set(!composerExpanded.Get())
			},
		},
	}

	var leftBtn core.Node
	if showBack {
		leftBtn = IconButton(cx, a, "session-back-btn", "返回", iconBack, false, func() {
			u.closeSession()
		})
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

	return core.View{
		TestID: "screen-session",
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexGrow:   1,
				FlexShrink: 1,
				MinHeight:  core.Pt(0),
				Background: pal.Bg,
			}
		},
		Children: []core.Node{
			// 44px compact session top bar with FIXED right controls across modes so toggling 聊天 <-> 终端 NEVER shifts or deforms the header!
			TopHeader(cx, a, func() string {
				return curTerm().Terminal.Label()
			}, leftBtn, []core.Node{
				modeSegmentPill,
				IconButton(cx, a, "session-search-btn", "搜索", iconSearch, false, func() {
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
									return RenderChatSession(cx, a, item(), curTerm, func() {
										u.setSessionMode("vt")
									})
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
				},
			},
			// Keyboard- & SafeArea-aware bottom dock (KeycapBar kept mounted & toggled via DisplayNone + Composer)
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Background: pal.Surface,
						PaddingEnv: core.EnvEdges{Bottom: core.EnvKeyboard},
					}
				},
				Children: []core.Node{
					core.View{
						StyleFunc: func() core.Style {
							if isChatMode() {
								return core.Style{Display: core.DisplayNone}
							}
							return core.Style{Display: core.DisplayFlex}
						},
						Children: []core.Node{
							KeycapBar(cx, a, keycaps),
						},
					},
					core.Show{
						When: func() bool { return isChatMode() || composerExpanded.Get() },
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
									IconButton(cx, a, "session-upload-btn", "上传图片", iconImage, false, func() {
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
									}),
									IconButton(cx, a, "session-snippets-btn", "常用短语", iconSnippets, false, func() {
										showSnippets.Set(true)
									}),
									core.View{
										Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
										Children: []core.Node{
											ui.Input{
												TestID:      "session-composer",
												Placeholder: "发消息或指令…",
												Value: func() string {
													return a.Compose(curKey().String())
												},
												OnChange: func(txt string) {
													if !isChatMode() && ctrlLatched.Get() && len(txt) == 1 {
														ch := strings.ToLower(txt)[0]
														if ch >= 'a' && ch <= 'z' {
															ctrlLatched.Set(false)
															sendKeyOrBytes("ctrl+"+string(ch), []byte{ch - 'a' + 1})
															return
														}
													}
													a.SetCompose(curKey().String(), txt)
												},
												OnSubmit: func(string) {
													sendComposerText()
												},
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														Width:        core.Pct(100),
														MinHeight:    core.Pt(Tokens.SizeTouch),
														Background:   pal.SurfaceRaised,
														Color:        pal.Text,
														CornerRadius: Tokens.RadiusCtrl,
														BorderColor:  pal.Border,
														BorderWidth:  1,
													}
												},
											},
										},
									},
									core.Show{
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
									},
								},
							}
						},
					},
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
			// Tools / Links / Lifecycle bottom sheet
			core.Show{
				When: showTools.Get,
				Then: func(cx *core.Scope) core.Node {
					var links []string
					if st := curST(); st != nil {
						links = st.Screen().ExtractLinks()
					}
					body := []core.Node{
						ActionButton(cx, a, "tool-copy-text", "复制屏幕文本", ActionSecondary, func() {
							if st := curST(); st != nil && cx != nil {
								cx.SetClipboard(st.Screen().PlainText())
								a.notifyToast("已复制终端文本到剪贴板")
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
	return core.View{
		TestID: testID,
		Role:   core.RoleButton,
		OnPress: func(core.PressEvent) {
			if onPress != nil {
				onPress()
			}
		},
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
