package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/base/textview/highlight"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"
	"github.com/nsgo-dev/nsgo/ui/diff"
)

const (
	chatInitialVisibleItems = 36
	chatVisibleStepItems    = 50
)

type activeDiffModal struct {
	Title string
	Tool  ChatToolCall
}

// RenderChatSession renders the Moshi-inspired + official NSGo ui/chat & ui/diff conversation view.
func RenderChatSession(
	cx *core.Scope,
	a *App,
	chatCtrl *ChatSessionController,
	termFn func() AggregatedTerminal,
	onSwitchToVT func(),
) core.Node {
	return RenderChatSessionWithCallbacks(cx, a, chatCtrl, termFn, onSwitchToVT, nil, nil)
}

// RenderChatSessionWithCallbacks renders the conversation view with optional subagent navigation
// and quote-to-composer callbacks.
func RenderChatSessionWithCallbacks(
	cx *core.Scope,
	a *App,
	chatCtrl *ChatSessionController,
	termFn func() AggregatedTerminal,
	onSwitchToVT func(),
	onOpenSession func(TermKey),
	onQuoteReply func(string),
) core.Node {
	if chatCtrl == nil {
		return core.View{
			TestID: "chat-surface",
			Style:  core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
		}
	}

	convState := chat.NewConversationState(cx)
	highlighter := highlight.New(cx)
	visibleLimit := core.NewSignal(cx, chatInitialVisibleItems)
	diffSheet := core.NewSignalFunc(cx, (*activeDiffModal)(nil), func(x, y *activeDiffModal) bool { return x == y })

	chatCtrl.SetScrollHook(func(_ bool) {
		convState.JumpToLatest()
	})

	// Memoize filtered + windowed slice per (rev, query, limit) so multiple reactive readers
	// in the same render pass share one slice without redundant mutex locks or copies.
	var (
		cachedRev   uint64 = ^uint64(0)
		cachedQuery string
		cachedLimit int
		cachedTotal int
		cachedSlice []ChatTurnItem
	)
	computeWindow := func() (visible []ChatTurnItem, hiddenAbove int, totalFiltered int) {
		var curRev uint64
		if chatCtrl.rev != nil {
			curRev = chatCtrl.rev.Get()
		}
		q := ""
		if chatCtrl.searchQuery != nil {
			q = strings.ToLower(strings.TrimSpace(chatCtrl.searchQuery.Get()))
		}
		limit := visibleLimit.Get()
		if curRev == cachedRev && q == cachedQuery && limit == cachedLimit && cachedSlice != nil {
			return cachedSlice, cachedTotal - len(cachedSlice), cachedTotal
		}

		all := chatCtrl.Items()
		var filtered []ChatTurnItem
		if q == "" {
			filtered = all
		} else {
			filtered = make([]ChatTurnItem, 0, len(all))
			for _, it := range all {
				if chatItemMatchesQuery(it, q) {
					filtered = append(filtered, it)
				}
			}
		}
		cachedTotal = len(filtered)
		if q == "" && len(filtered) > limit {
			cachedSlice = filtered[len(filtered)-limit:]
		} else {
			cachedSlice = filtered
		}
		cachedRev = curRev
		cachedQuery = q
		cachedLimit = limit
		return cachedSlice, cachedTotal - len(cachedSlice), cachedTotal
	}

	filteredItems := func() []ChatTurnItem {
		vis, _, _ := computeWindow()
		return vis
	}
	hiddenLocalCount := func() int {
		_, hid, _ := computeWindow()
		return hid
	}
	totalFilteredCount := func() int {
		_, _, tot := computeWindow()
		return tot
	}

	lastAssistantID := func() string {
		vis := filteredItems()
		for i := len(vis) - 1; i >= 0; i-- {
			if vis[i].Role == RoleAssistant {
				return vis[i].ID
			}
		}
		return ""
	}

	// Child sub-sessions spawned from this session (via herdr-core Terminal.Parent).
	childSubsessions := func() []AggregatedTerminal {
		cur := termFn().Key
		if cur == (TermKey{}) {
			return nil
		}
		groups, _ := GroupSessions(a.aggTerms.Get())
		for _, g := range groups {
			if g.Primary.Key == cur {
				return g.Children
			}
		}
		return nil
	}

	parentKeyOfCurrent := func() (TermKey, string, bool) {
		cur := termFn()
		if cur.Terminal.Parent == nil || strings.TrimSpace(*cur.Terminal.Parent) == "" {
			return TermKey{}, "", false
		}
		pk := TermKey{InstanceID: cur.Key.InstanceID, Addr: strings.TrimSpace(*cur.Terminal.Parent)}
		if pt, ok := a.FindTerminal(pk); ok {
			return pk, pt.Terminal.Label(), true
		}
		return pk, pk.Addr, true
	}

	return core.View{
		TestID: "chat-surface",
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
			core.Scroll{
				Ref:         convState.Scroll(),
				EdgeEffects: core.EdgeEffects{Top: core.EdgeEffectSoft, Bottom: core.EdgeEffectSoft},
				Behavior:    core.ScrollBehavior{KeyboardDismissMode: core.KeyboardDismissInteractive},
				Style: core.Style{
					FlexGrow:   1,
					FlexShrink: 1,
					MinHeight:  core.Pt(0),
				},
				OnScroll: func(ev core.ScrollEvent) {
					if ev.Stale || chatCtrl.scrolledUp == nil {
						return
					}
					distFromBottom := float32(ev.ContentSize.Height) - (float32(ev.Offset.Y) + float32(ev.Size.Height))
					isUp := distFromBottom > 68
					if chatCtrl.scrolledUp.Get() != isUp {
						chatCtrl.scrolledUp.Set(isUp)
					}
				},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Padding: core.Edges{Top: Tokens.Space3, Right: Tokens.PagePadX, Bottom: 138, Left: Tokens.PagePadX},
							Gap:     core.Points(10),
						},
						Children: []core.Node{
							// If viewing a child subagent terminal, offer 1-tap return to root session!
							core.Show{
								When: func() bool {
									_, _, ok := parentKeyOfCurrent()
									return ok && onOpenSession != nil
								},
								Then: func(cx *core.Scope) core.Node {
									return ActionButtonFunc(cx, a, "chat-back-parent-btn", func() string {
										_, lbl, _ := parentKeyOfCurrent()
										return "← 返回父会话 · " + lbl
									}, ActionSecondary, func() {
										if pk, _, ok := parentKeyOfCurrent(); ok && onOpenSession != nil {
											onOpenSession(pk)
										}
									})
								},
							},
							// Compact source & manual refresh meta bar (shows ⚡ 0ms snapshot status when cached!)
							core.View{
								Style: core.Style{
									Direction:      core.Row,
									AlignItems:     core.AlignCenter,
									JustifyContent: core.JustifySpaceBetween,
									Gap:            Tokens.Space2,
								},
								Children: []core.Node{
									core.Text{
										MaxLines: 1,
										ContentFunc: func() string {
											t := termFn()
											parts := []string{t.Key.Addr}
											if d := shortPath(t.Terminal.CurrentDir()); d != "" {
												parts = append(parts, d)
											}
											return strings.Join(parts, " · ")
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
									FilterPill(cx, a, "chat-source-pill", func() string {
										if chatCtrl.IsCachedSnapshot() {
											return "秒开缓存 · 同步中"
										}
										return chatCtrl.Source().Label() + " · 刷新"
									}, func() bool {
										return chatCtrl.Source() == SourceJSONL && !chatCtrl.IsCachedSnapshot()
									}, nil, func() {
										chatCtrl.RefreshAsync()
										a.notifyToast("正在刷新对话记录…")
									}),
								},
							},
							// Pagination loader: reveals buffered in-memory items in 0ms first, then fetches older remote JSONL lines
							core.Show{
								When: func() bool { return hiddenLocalCount() > 0 || chatCtrl.HasOlder() },
								Then: func(cx *core.Scope) core.Node {
									return ActionButtonFunc(cx, a, "chat-load-older-btn", func() string {
										if hid := hiddenLocalCount(); hid > 0 {
											return fmt.Sprintf("↑ 展开更早消息（已缓存 %d 条）", hid)
										}
										if chatCtrl.IsLoadingOlder() {
											return "正在加载更早对话…"
										}
										return fmt.Sprintf("↑ 加载更早对话（剩余 %d 行）", chatCtrl.OlderRemaining())
									}, ActionSecondary, func() {
										animateSpringLayout(cx)
										if hiddenLocalCount() > 0 {
											visibleLimit.Set(visibleLimit.Get() + chatVisibleStepItems)
											return
										}
										visibleLimit.Set(visibleLimit.Get() + chatVisibleStepItems)
										chatCtrl.LoadOlder()
									})
								},
							},
							// Empty / Loading guidance card
							core.Show{
								When: func() bool { return totalFilteredCount() == 0 && chatCtrl.Source() == SourceLoading },
								Then: func(cx *core.Scope) core.Node {
									return EmptyStateCard(cx, a, "正在同步会话记录…", "正在定位并拉取远端会话日志，马上呈现。", "", "", nil)
								},
							},
							core.Show{
								When: func() bool { return totalFilteredCount() == 0 && chatCtrl.Source() != SourceLoading },
								Then: func(cx *core.Scope) core.Node {
									return EmptyStateCard(
										cx,
										a,
										"当前暂无结构化对话记录",
										"你可以直接在下方悬浮输入框发送指令开启对话，或切回「终端」查看原始屏幕。",
										"chat-empty-switch-vt",
										"切到终端视图",
										onSwitchToVT,
									)
								},
							},
							// Conversation items
							core.Each(
								filteredItems,
								func(it ChatTurnItem) string { return it.ID },
								func(cx *core.Scope, itemFn func() ChatTurnItem, _ func() int) core.Node {
									it0 := itemFn()
									switch it0.Role {
									case RoleUser:
										return renderChatUserBubble(cx, a, itemFn)
									case RoleThinking:
										return renderChatThinkingPill(cx, a, chatCtrl, itemFn)
									case RoleToolGroup:
										return renderChatToolGroup(cx, a, chatCtrl, itemFn, func(title string, tc ChatToolCall) {
											diffSheet.Set(&activeDiffModal{Title: title, Tool: tc})
										})
									default:
										return renderChatAssistantCard(cx, a, highlighter, itemFn, termFn, func() bool {
											return termFn().Terminal.StateWord() == "working" && itemFn().ID == lastAssistantID()
										}, onQuoteReply)
									}
								},
							),
							// Moshi-inspired Subagent Strip (SessionChatSubagentStrip)
							core.Show{
								When: func() bool { return len(childSubsessions()) > 0 },
								Then: func(cx *core.Scope) core.Node {
									return renderSubagentStrip(cx, a, childSubsessions, onOpenSession)
								},
							},
							// Live working status strip (OSC 7501 + animated 3-dot wave)
							core.Show{
								When: func() bool {
									return termFn().Terminal.StateWord() == "working"
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										TestID: "chat-working-banner",
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Direction:    core.Row,
												AlignItems:   core.AlignCenter,
												Gap:          Tokens.Space2,
												Background:   pal.AccentSoft,
												CornerRadius: Tokens.RadiusCard,
												BorderWidth:  1,
												BorderColor:  pal.Accent,
												Padding:      core.Symmetric(core.Points(9), Tokens.Space3),
											}
										},
										Children: []core.Node{
											renderWaveDots(cx, a),
											core.Text{
												MaxLines: 2,
												ContentFunc: func() string {
													t := termFn().Terminal
													if t.Status != nil && strings.TrimSpace(t.Status.Msg) != "" {
														return "正在执行 · " + firstLine(t.Status, t)
													}
													if t.Status != nil && strings.TrimSpace(t.Status.Title) != "" {
														return "正在执行 · " + strings.TrimSpace(t.Status.Title)
													}
													return "Agent 正在思考与执行…"
												},
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														FlexGrow:   1,
														FlexShrink: 1,
														MinWidth:   core.Pt(0),
														Color:      pal.Accent,
														FontSize:   Tokens.FontCaption,
														FontWeight: core.FontWeightSemibold,
													}
												},
											},
										},
									}
								},
							},
							// Inline permission / blocked card anchored directly at conversation bottom
							core.Show{
								When: func() bool { return termFn().Terminal.NeedsYou() },
								Then: func(cx *core.Scope) core.Node {
									return InlineApprovalCard(cx, a, termFn, func(TermKey) {
										if onSwitchToVT != nil {
											onSwitchToVT()
										}
									})
								},
							},
						},
					},
				},
			},
			// Progressive bottom edge blur behind the floating glass composer (official NSGo ui/chat)
			chat.FloatingEdgeBar(cx, convState.Scroll()),
			// Floating Glass Scroll-to-Bottom button (stays clear of the floating composer)
			core.Show{
				When: func() bool {
					return chatCtrl.scrolledUp != nil && chatCtrl.scrolledUp.Get()
				},
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						Style: core.Style{
							Position: core.Absolute,
							Right:    core.Pt(16),
							Bottom:   core.Pt(118),
						},
						Children: []core.Node{
							ActionButton(cx, a, "chat-scroll-bottom-fab", "↓ 最新", ActionPrimary, func() {
								chatCtrl.ScrollToBottom()
							}),
						},
					}
				},
			},
			// Swipe-dismissable Fullscreen Diff Sheet (official NSGo ui/diff + ui.Sheet)
			core.Show{
				When: func() bool { return diffSheet.Get() != nil },
				Then: func(cx *core.Scope) core.Node {
					modal := diffSheet.Get()
					if modal == nil {
						return nil
					}
					tc := modal.Tool
					return SheetModal(cx, a, "sheet-diff-viewer", modal.Title, "sheet-diff-close", func() {
						diffSheet.Set(nil)
					}, []core.Node{
						renderToolDiffBlock(cx, tc, 480),
					})
				},
			},
		},
	}
}

func renderWaveDots(cx *core.Scope, a *App) core.Node {
	reduced := cx == nil || ui.ReduceMotion(cx)
	var dots []core.Node
	for i := range 3 {
		st := core.Style{Width: core.Pt(5), Height: core.Pt(5), CornerRadius: 2.5, Opacity: core.Alpha(0.35)}
		if !reduced {
			v := core.NewAnimatedValue(cx, 0.35)
			stops := make([]float64, 25)
			for k := range stops {
				p := float64(k) / 24.0
				stops[k] = 0.35 + 0.65*max(0, math.Sin((p-float64(i)*0.16)*2*math.Pi))
			}
			cx.StartAnimation(core.AtFrameRate(core.ValueKeyframes(
				v,
				core.KeyframeTiming{Duration: 1150 * time.Millisecond, Iterations: core.Forever},
				core.Stops(stops...)...,
			), 30), nil)
			st.Animated = &core.AnimatedStyle{Opacity: v}
		}
		dots = append(dots, core.View{
			Style: st,
			StyleFunc: func() core.Style {
				return core.Style{Background: a.paletteOf(cx).Accent}
			},
		})
	}
	return core.View{
		Style:    core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(3), FlexShrink: 0},
		Children: dots,
	}
}

func renderPulseDot(cx *core.Scope, colorFn func() core.Color) core.Node {
	st := core.Style{Width: core.Pt(7), Height: core.Pt(7), CornerRadius: 3.5, FlexShrink: 0}
	if cx != nil && !ui.ReduceMotion(cx) {
		v := core.NewAnimatedValue(cx, 1)
		stops := make([]float64, 13)
		for k := range stops {
			p := float64(k) / 12.0
			stops[k] = 0.4 + 0.6*(1+math.Sin(p*2*math.Pi))/2
		}
		cx.StartAnimation(core.AtFrameRate(core.ValueKeyframes(
			v,
			core.KeyframeTiming{Duration: time.Second, Iterations: core.Forever},
			core.Stops(stops...)...,
		), 30), nil)
		st.Animated = &core.AnimatedStyle{Opacity: v}
	}
	return core.View{
		Style: st,
		StyleFunc: func() core.Style {
			return core.Style{Background: colorFn()}
		},
	}
}

func renderSubagentStrip(
	cx *core.Scope,
	a *App,
	childrenFn func() []AggregatedTerminal,
	onOpenSession func(TermKey),
) core.Node {
	return chat.GlassPanel{
		TestID: "chat-subagent-strip",
		Children: []core.Node{
			core.View{
				Style: core.Style{Gap: core.Points(6)},
				Children: []core.Node{
					core.View{
						Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6)},
						Children: []core.Node{
							renderPulseDot(cx, func() core.Color { return a.paletteOf(cx).Accent }),
							core.Icon{
								SVG: iconSubagent,
								StyleFunc: func() core.Style {
									return core.Style{Width: core.Pt(14), Height: core.Pt(14), Color: a.paletteOf(cx).Accent}
								},
							},
							core.Text{
								MaxLines: 1,
								ContentFunc: func() string {
									kids := childrenFn()
									running := 0
									for _, ch := range kids {
										if ch.Terminal.StateWord() == "working" || ch.Terminal.NeedsYou() {
											running++
										}
									}
									if running > 0 {
										return fmt.Sprintf("子代理协作中 · %d/%d 活跃（点按跳转）", running, len(kids))
									}
									return fmt.Sprintf("派生子会话 · %d 个（点按查看）", len(kids))
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										FlexGrow:   1,
										FlexShrink: 1,
										MinWidth:   core.Pt(0),
										Color:      pal.Text,
										FontSize:   Tokens.FontMonoMeta,
										FontWeight: core.FontWeightSemibold,
									}
								},
							},
						},
					},
					core.Scroll{
						Horizontal:    true,
						NoShrink:      true,
						HideIndicator: true,
						Children: []core.Node{
							core.View{
								Style: core.Style{Direction: core.Row, Gap: core.Points(6)},
								Children: []core.Node{
									core.Each(
										childrenFn,
										func(ch AggregatedTerminal) string { return ch.Key.String() },
										func(cx *core.Scope, chFn func() AggregatedTerminal, _ func() int) core.Node {
											ch0 := chFn()
											return FilterPill(cx, a, "subagent-chip-"+ch0.Key.String(), func() string {
												cur := chFn()
												_, _, stLbl := stateColor(a.paletteOf(cx), cur.Terminal.StateWord())
												return fmt.Sprintf("%s · %s", cur.Terminal.Label(), stLbl)
											}, func() bool {
												return chFn().Terminal.StateWord() == "working" || chFn().Terminal.NeedsYou()
											}, nil, func() {
												if onOpenSession != nil {
													onOpenSession(chFn().Key)
												}
											})
										},
									),
								},
							},
						},
					},
				},
			},
		},
	}
}

func chatItemMatchesQuery(it ChatTurnItem, q string) bool {
	if strings.Contains(strings.ToLower(it.Text), q) || strings.Contains(strings.ToLower(it.FromAgent), q) {
		return true
	}
	for _, tc := range it.Tools {
		if strings.Contains(strings.ToLower(tc.Name+" "+tc.Target+" "+tc.Output+" "+tc.DiffFile), q) {
			return true
		}
	}
	return false
}

func renderChatUserBubble(cx *core.Scope, a *App, itemFn func() ChatTurnItem) core.Node {
	it0 := itemFn()
	return chat.Message{
		Outgoing: true,
		Children: []core.Node{
			core.View{
				TestID: "chat-user-" + it0.ID,
				Style:  core.Style{Padding: core.Edges{Left: 32}, AlignItems: core.AlignEnd},
				Children: []core.Node{
					core.View{
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								FlexShrink:   1,
								MinWidth:     core.Pt(0),
								MaxWidth:     core.Pt(540),
								Background:   pal.AccentSoft,
								CornerRadius: core.Points(13),
								BorderWidth:  1,
								BorderColor:  pal.Accent,
								Padding:      core.Symmetric(core.Points(9), Tokens.Space3),
								Gap:          core.Points(4),
							}
						},
						Children: []core.Node{
							core.Show{
								When: func() bool { return itemFn().FromAgent != "" || itemFn().Optimistic },
								Then: func(cx *core.Scope) core.Node {
									return core.Text{
										MaxLines: 1,
										ContentFunc: func() string {
											it := itemFn()
											if it.FromAgent != "" {
												return "子会话汇报 · " + it.FromAgent
											}
											return "✦ 已即时发送 · 同步中"
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Color:      pal.Accent,
												FontSize:   Tokens.FontMonoMeta,
												FontWeight: core.FontWeightSemibold,
												FontFamily: core.FontMonospace,
											}
										},
									}
								},
							},
							core.Text{
								Selectable:  core.SelectableOn,
								ContentFunc: func() string { return itemFn().Text },
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Color:      pal.Text,
										FontSize:   Tokens.FontBody,
										LineHeight: core.Points(20),
									}
								},
							},
						},
					},
				},
			},
		},
	}
}

func renderChatThinkingPill(cx *core.Scope, a *App, chatCtrl *ChatSessionController, itemFn func() ChatTurnItem) core.Node {
	it0 := itemFn()
	itemID := it0.ID
	scale := newPressScale(cx)
	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Background:   pal.SurfaceRaised,
				CornerRadius: Tokens.RadiusCtrl,
				BorderWidth:  1,
				BorderColor:  pal.Border,
				Overflow:     core.Hidden,
			}
		},
		Children: []core.Node{
			core.View{
				TestID:      "chat-thinking-" + itemID,
				Role:        core.RoleButton,
				OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.985) },
				OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
				OnPress: func(core.PressEvent) {
					pressSpringTo(cx, scale, 1.0)
					playSelectionHaptic(cx)
					animateSpringLayout(cx)
					chatCtrl.ToggleExpanded(itemID)
				},
				Style: core.Style{
					Direction:  core.Row,
					AlignItems: core.AlignCenter,
					Gap:        Tokens.Space2,
					Padding:    core.Symmetric(core.Points(7), Tokens.Space3),
					Animated:   pressAnimStyle(scale),
				},
				Children: []core.Node{
					core.Text{
						MaxLines: 1,
						ContentFunc: func() string {
							arrow := "▸"
							if chatCtrl.IsExpanded(itemID) {
								arrow = "▾"
							}
							n := utf8.RuneCountInString(itemFn().Text)
							return fmt.Sprintf("✻ %s 思考过程 · %d 字", arrow, n)
						},
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								FlexGrow:   1,
								FlexShrink: 1,
								MinWidth:   core.Pt(0),
								Color:      pal.TextMuted,
								FontSize:   Tokens.FontMonoMeta,
								FontWeight: core.FontWeightSemibold,
								FontFamily: core.FontMonospace,
							}
						},
					},
				},
			},
			core.Show{
				When: func() bool { return chatCtrl.IsExpanded(itemID) },
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Padding:     core.Symmetric(Tokens.Space2, Tokens.Space3),
								Borders:     core.Edges{Top: 1},
								BorderColor: pal.Separator,
								Background:  pal.Surface,
							}
						},
						Children: []core.Node{
							core.Text{
								Selectable:  core.SelectableOn,
								ContentFunc: func() string { return itemFn().Text },
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Color:      pal.TextSecondary,
										FontSize:   Tokens.FontCaption,
										LineHeight: core.Points(18),
									}
								},
							},
						},
					}
				},
			},
		},
	}
}

type indexedChatTool struct {
	Key   string
	Index int
	Tool  ChatToolCall
}

func renderToolDiffBlock(_ *core.Scope, tc ChatToolCall, maxH core.Points) core.Node {
	oldVal := tc.DiffOld
	newVal := tc.DiffNew
	patchVal := tc.DiffPatch
	lang := tc.DiffFile
	if lang == "" {
		lang = tc.Target
	}
	dv := diff.View{
		Language:   lang,
		Unified:    true,
		SoftWrap:   true,
		Selectable: true,
		FontSize:   11.5,
		LineHeight: 17.5,
	}
	if patchVal != "" {
		dv.Patch = func() string { return patchVal }
	} else {
		dv.OldValue = func() string { return oldVal }
		dv.NewValue = func() string { return newVal }
	}
	return core.Scroll{
		Style:    core.Style{MaxHeight: core.Pt(maxH), FlexShrink: 1, CornerRadius: Tokens.RadiusKey, Overflow: core.Hidden},
		Children: []core.Node{dv},
	}
}

func renderChatToolGroup(
	cx *core.Scope,
	a *App,
	chatCtrl *ChatSessionController,
	itemFn func() ChatTurnItem,
	onOpenDiff func(title string, tc ChatToolCall),
) core.Node {
	groupID := itemFn().ID
	return core.View{
		Style: core.Style{Gap: core.Points(5)},
		Children: []core.Node{
			core.Each(
				func() []indexedChatTool {
					tools := itemFn().Tools
					out := make([]indexedChatTool, len(tools))
					for i, tc := range tools {
						out[i] = indexedChatTool{
							Key:   fmt.Sprintf("%s#%d", groupID, i),
							Index: i,
							Tool:  tc,
						}
					}
					return out
				},
				func(it indexedChatTool) string { return it.Key },
				func(cx *core.Scope, entryFn func() indexedChatTool, _ func() int) core.Node {
					initEntry := entryFn()
					expandKey := initEntry.Key
					testID := fmt.Sprintf("chat-tool-%s-%d", groupID, initEntry.Index)
					openDiffID := fmt.Sprintf("chat-open-diff-%s-%d", groupID, initEntry.Index)
					getTC := func() ChatToolCall { return entryFn().Tool }
					scale := newPressScale(cx)

					return core.View{
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							bc := pal.Border
							if getTC().IsError {
								bc = pal.Danger
							}
							return core.Style{
								Background:   pal.SurfaceRaised,
								CornerRadius: Tokens.RadiusCtrl,
								BorderWidth:  1,
								BorderColor:  bc,
								Overflow:     core.Hidden,
							}
						},
						Children: []core.Node{
							core.View{
								TestID:      testID,
								Role:        core.RoleButton,
								OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.985) },
								OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
								OnPress: func(core.PressEvent) {
									pressSpringTo(cx, scale, 1.0)
									playSelectionHaptic(cx)
									animateSpringLayout(cx)
									chatCtrl.ToggleExpanded(expandKey)
								},
								Style: core.Style{
									Direction:  core.Row,
									AlignItems: core.AlignCenter,
									Gap:        core.Points(6),
									Padding:    core.Symmetric(core.Points(7), Tokens.Space3),
									Animated:   pressAnimStyle(scale),
								},
								Children: []core.Node{
									core.View{
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											tc := getTC()
											c := pal.TextMuted
											if tc.IsError {
												c = pal.Danger
											} else if tc.Output != "" || tc.HasDiff() {
												c = pal.Accent
											}
											return core.Style{
												FlexShrink:   0,
												Width:        core.Pt(7),
												Height:       core.Pt(7),
												CornerRadius: 4,
												Background:   c,
											}
										},
									},
									core.Text{
										MaxLines:    1,
										ContentFunc: func() string { return singleLineClip(getTC().Name, 24) },
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											fg := pal.Text
											if getTC().IsError {
												fg = pal.Danger
											}
											return core.Style{
												FlexShrink: 0,
												Color:      fg,
												FontSize:   Tokens.FontMonoMeta,
												FontWeight: core.FontWeightSemibold,
												FontFamily: core.FontMonospace,
											}
										},
									},
									core.Text{
										MaxLines:    1,
										ContentFunc: func() string { return getTC().Target },
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexGrow:   1,
												FlexShrink: 1,
												MinWidth:   core.Pt(0),
												Color:      pal.TextSecondary,
												FontSize:   Tokens.FontMonoMeta,
												FontFamily: core.FontMonospace,
											}
										},
									},
									// Moshi-style DIFF capsule indicator when tool modified code
									core.Show{
										When: func() bool { return getTC().HasDiff() },
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														FlexShrink:   0,
														Background:   pal.AccentSoft,
														CornerRadius: core.Points(4),
														Padding:      core.Symmetric(core.Points(1), core.Points(5)),
													}
												},
												Children: []core.Node{
													core.Text{
														Content: "DIFF",
														StyleFunc: func() core.Style {
															return core.Style{
																Color:      a.paletteOf(cx).Accent,
																FontSize:   core.Points(10),
																FontWeight: core.FontWeightBold,
																FontFamily: core.FontMonospace,
															}
														},
													},
												},
											}
										},
									},
									core.Text{
										ContentFunc: func() string {
											if chatCtrl.IsExpanded(expandKey) {
												return "▾"
											}
											return "▸"
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexShrink: 0,
												Color:      pal.TextMuted,
												FontSize:   Tokens.FontMonoMeta,
											}
										},
									},
								},
							},
							core.Show{
								When: func() bool { return chatCtrl.IsExpanded(expandKey) },
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Padding:     core.Symmetric(Tokens.Space2, Tokens.Space3),
												Borders:     core.Edges{Top: 1},
												BorderColor: pal.Separator,
												Background:  pal.Surface,
												Gap:         core.Points(6),
											}
										},
										Children: []core.Node{
											// Inline syntax-highlighted Unified Diff + Fullscreen Diff Sheet launcher!
											core.Show{
												When: func() bool { return getTC().HasDiff() },
												Then: func(cx *core.Scope) core.Node {
													tc := getTC()
													return core.View{
														Style: core.Style{Gap: core.Points(6)},
														Children: []core.Node{
															core.View{
																Style: core.Style{
																	Direction:      core.Row,
																	AlignItems:     core.AlignCenter,
																	JustifyContent: core.JustifySpaceBetween,
																},
																Children: []core.Node{
																	core.Text{
																		MaxLines: 1,
																		ContentFunc: func() string {
																			t := getTC()
																			if t.DiffFile != "" {
																				return shortPath(t.DiffFile)
																			}
																			return t.Target
																		},
																		StyleFunc: func() core.Style {
																			return core.Style{
																				FlexGrow:   1,
																				FlexShrink: 1,
																				MinWidth:   core.Pt(0),
																				Color:      a.paletteOf(cx).TextSecondary,
																				FontSize:   Tokens.FontMonoMeta,
																				FontFamily: core.FontMonospace,
																			}
																		},
																	},
																	FilterPill(cx, a, openDiffID, func() string { return "全屏 Diff" }, func() bool { return true }, nil, func() {
																		if onOpenDiff != nil {
																			t := getTC()
																			title := t.Target
																			if t.DiffFile != "" {
																				title = shortPath(t.DiffFile)
																			}
																			if title == "" {
																				title = t.Name + " Diff"
																			}
																			onOpenDiff(title, t)
																		}
																	}),
																},
															},
															renderToolDiffBlock(cx, tc, 240),
														},
													}
												},
											},
											core.Show{
												When: func() bool { return !getTC().HasDiff() && getTC().InputSummary != "" },
												Then: func(cx *core.Scope) core.Node {
													return core.Text{
														ContentFunc: func() string { return "参数: " + getTC().InputSummary },
														StyleFunc: func() core.Style {
															pal := a.paletteOf(cx)
															return core.Style{
																Color:      pal.TextMuted,
																FontSize:   Tokens.FontMonoMeta,
																FontFamily: core.FontMonospace,
															}
														},
													}
												},
											},
											core.Show{
												When: func() bool { return !getTC().HasDiff() || getTC().Output != "" },
												Then: func(cx *core.Scope) core.Node {
													return core.Text{
														Selectable: core.SelectableOn,
														ContentFunc: func() string {
															out := getTC().Output
															if out == "" {
																return "（执行中或无输出）"
															}
															return out
														},
														StyleFunc: func() core.Style {
															pal := a.paletteOf(cx)
															fg := pal.TextSecondary
															if getTC().IsError {
																fg = pal.Danger
															}
															return core.Style{
																Color:      fg,
																FontSize:   Tokens.FontMonoMeta,
																FontFamily: core.FontMonospace,
															}
														},
													}
												},
											},
										},
									}
								},
							},
						},
					}
				},
			),
		},
	}
}

func renderChatAssistantCard(
	cx *core.Scope,
	a *App,
	highlighter textview.Highlighter,
	itemFn func() ChatTurnItem,
	termFn func() AggregatedTerminal,
	streamingFn func() bool,
	onQuoteReply func(string),
) core.Node {
	cardID := itemFn().ID

	return core.View{
		TestID: "chat-assistant-" + cardID,
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Direction:    core.Row,
				Background:   pal.Surface,
				CornerRadius: Tokens.RadiusCard,
				BorderWidth:  1,
				BorderColor:  pal.Border,
				Overflow:     core.Hidden,
			}
		},
		Children: []core.Node{
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Width:      core.Pt(3),
						Background: pal.Accent,
					}
				},
			},
			core.View{
				Style: core.Style{
					FlexGrow:   1,
					FlexShrink: 1,
					MinWidth:   core.Pt(0),
					Padding:    core.Symmetric(core.Points(10), Tokens.Space3),
					Gap:        Tokens.Space2,
				},
				Children: []core.Node{
					// Header: Harness dot + Model name + Copy/Quote actions
					core.View{
						Style: core.Style{
							Direction:      core.Row,
							AlignItems:     core.AlignCenter,
							JustifyContent: core.JustifySpaceBetween,
							Gap:            core.Points(6),
						},
						Children: []core.Node{
							core.View{
								Style: core.Style{
									Direction:  core.Row,
									AlignItems: core.AlignCenter,
									Gap:        core.Points(6),
									FlexGrow:   1,
									FlexShrink: 1,
									MinWidth:   core.Pt(0),
								},
								Children: []core.Node{
									core.View{
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexShrink:   0,
												Width:        core.Pt(7),
												Height:       core.Pt(7),
												CornerRadius: 4,
												Background:   pal.Accent,
											}
										},
									},
									core.Text{
										MaxLines: 1,
										ContentFunc: func() string {
											it := itemFn()
											if it.Model != "" {
												return it.Model
											}
											if app := termFn().Terminal.AppName(); app != "" {
												return app
											}
											return "Agent"
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexGrow:   1,
												FlexShrink: 1,
												MinWidth:   core.Pt(0),
												Color:      pal.TextSecondary,
												FontSize:   Tokens.FontMonoMeta,
												FontWeight: core.FontWeightSemibold,
												FontFamily: core.FontMonospace,
											}
										},
									},
								},
							},
							// Official NSGo chat.Actions (copy with receipt) + Moshi quote-reply action
							chat.Actions{
								TestID: "chat-actions-" + cardID,
								Text:   func() string { return itemFn().Text },
								Children: func() []core.Node {
									if onQuoteReply == nil {
										return nil
									}
									return []core.Node{
										chat.IconAction{
											TestID: "chat-quote-" + cardID,
											Label:  "引用回复",
											Icon:   func() *core.SVG { return iconQuote },
											OnPress: func() {
												playSelectionHaptic(cx)
												onQuoteReply(itemFn().Text)
											},
										},
									}
								}(),
							},
						},
					},
					// Official NSGo GFM Markdown + Chroma syntax highlighting + streaming TextView!
					core.Dynamic(func(cx *core.Scope) core.Node {
						isStreaming := streamingFn != nil && streamingFn()
						return ui.TextView{
							MarkdownFunc: func() string { return itemFn().Text },
							Highlighter:  highlighter,
							Selectable:   true,
							Options:      textview.Options{Streaming: isStreaming},
							CodeBlockActions: []textview.CodeAction{
								{ID: textview.CopyAction, Label: "复制"},
							},
							RenderCodeBlockAction: func(_ *core.Scope, act textview.CodeBlockAction) core.Node {
								return core.Text{
									ContentFunc: func() string {
										if act.Done() {
											return "✓ 已复制"
										}
										return "复制"
									},
								}
							},
							OnLinkPress: func(ev textview.LinkPressEvent) {
								u := strings.TrimSpace(ev.URL)
								if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
									if cx != nil {
										cx.OpenURL(u, nil)
									}
								} else if u != "" && cx != nil {
									cx.SetClipboard(u)
									a.notifyToast("已复制链接：" + u)
								}
							},
						}
					}),
				},
			},
		},
	}
}
