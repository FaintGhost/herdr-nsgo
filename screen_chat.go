package main

import (
	"fmt"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"math"
	"strings"
	"time"

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
	return RenderChatSessionWithCallbacks(cx, a, chatCtrl, termFn, onSwitchToVT, nil, nil, nil)
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
	composerInset func() core.Points,
) core.Node {
	if composerInset == nil {
		composerInset = func() core.Points { return 138 }
	}
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

	var lastScrollY core.Points
	followAfterLayout := func(core.Bounds) {
		if chatCtrl.scrolledUp == nil || !chatCtrl.scrolledUp.GetUntracked() {
			convState.Scroll().ScrollToEnd(false)
		}
	}
	chatCtrl.SetScrollHook(func(animated bool) {
		convState.Scroll().ScrollToEnd(animated)
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

	// Bind live motion to the transcript tail even when a search hides later items.
	liveItemID := core.NewMemo(cx, func() string {
		if termFn().Terminal.StateWord() == "working" {
			items := chatCtrl.Items()
			if len(items) > 0 {
				return items[len(items)-1].ID
			}
		}
		return ""
	}).Get

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
				TestID:      "chat-transcript-scroll",
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
					// A growing transcript increases the remaining distance even at an unchanged offset.
					// Pause following only when the reader actually moves towards older messages.
					remaining := ev.ContentSize.Height - ev.Offset.Y - ev.Size.Height
					switch {
					case remaining <= 2:
						chatCtrl.scrolledUp.Set(false)
					case ev.Offset.Y < lastScrollY:
						chatCtrl.scrolledUp.Set(true)
					}
					lastScrollY = ev.Offset.Y
				},
				Children: []core.Node{
					core.View{
						OnLayout: followAfterLayout,
						StyleFunc: func() core.Style {
							return core.Style{
								Width:     core.Pct(100),
								MaxWidth:  core.Pt(Tokens.ChatMaxWidth),
								AlignSelf: core.AlignCenter,
								Padding:   core.Edges{Top: Tokens.Space3, Right: Tokens.PagePadX, Bottom: composerInset(), Left: Tokens.PagePadX},
								Gap:       core.Points(12),
							}
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
							// Source of the transcript and manual refresh.
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
									chat.Chip{TestID: "chat-source-pill", Icon: lucide.RefreshCw, Text: "刷新记录", TextFunc: func() string {
										if chatCtrl.IsCachedSnapshot() {
											return "同步中"
										}
										return chatCtrl.Source().Label()
									}, OnPress: func() {
										chatCtrl.RefreshAsync()
									}},
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
									return ui.Skeleton{TestID: "chat-history-skeleton", Label: "正在同步会话记录", Lines: 3, Style: core.Style{Padding: core.All(Tokens.Space3)}}
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
									isLive := func() bool { return it0.ID == liveItemID() }
									switch it0.Role {
									case RoleUser:
										return renderChatUserBubble(cx, a, itemFn)
									case RoleThinking:
										return chat.Reasoning{
											TestID: "chat-thinking-" + it0.ID, Conversation: convState,
											Text:  func() string { return itemFn().Text },
											Label: func() string { return "思考中" }, Summary: func() string { return "思考过程" },
											Status: func() chat.Status {
												if isLive() {
													return chat.Running
												}
												return chat.Completed
											},
										}
									case RoleToolGroup:
										return renderChatToolGroup(cx, a, chatCtrl, itemFn, isLive, func(title string, tc ChatToolCall) {
											diffSheet.Set(&activeDiffModal{Title: title, Tool: tc})
										})
									default:
										return renderChatAssistantCard(cx, a, highlighter, itemFn, termFn, isLive, onQuoteReply)
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
						StyleFunc: func() core.Style {
							return core.Style{
								Position: core.Absolute,
								Right:    core.Pt(16),
								Bottom:   core.Pt(composerInset()),
							}
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

func renderDeliveryIndicator(cx *core.Scope, a *App, phase func() string) core.Node {
	return core.Show{When: func() bool { return phase() != "" }, Then: func(cx *core.Scope) core.Node {
		return core.View{TestID: "chat-delivery-status", Role: core.RoleStatus, Accessible: true, LabelFunc: phase,
			Style: core.Style{Width: core.Pt(23), AlignItems: core.AlignCenter}, Children: []core.Node{
				core.Show{When: func() bool { return phase() == "正在发送" }, Then: func(*core.Scope) core.Node {
					return ui.Spinner{TestID: "chat-sending-indicator", Size: 15, Variant: ui.SpinnerSpokes, Tint: func() core.Color { return a.paletteOf(cx).Running }}
				}, Else: func(cx *core.Scope) core.Node { return renderWaveDots(cx, a) }},
			}}
	}}
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
				return core.Style{Background: a.paletteOf(cx).Running}
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
	return GroupCard(cx, a, "chat-subagent-strip", []core.Node{
		core.View{
			Style: core.Style{Padding: core.All(core.Points(12)), Gap: Tokens.Space2},
			Children: []core.Node{
				core.View{
					Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6)},
					Children: []core.Node{
						renderPulseDot(cx, func() core.Color { return a.paletteOf(cx).Running }),
						core.Icon{
							SVG: iconSubagent,
							StyleFunc: func() core.Style {
								return core.Style{Width: core.Pt(14), Height: core.Pt(14), Color: a.paletteOf(cx).Running}
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
									FontWeight: core.FontWeightMedium,
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
	})
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
								Background:   pal.SurfaceRaised,
								BorderWidth:  1,
								BorderColor:  pal.Border,
								CornerRadius: 16,
								Padding:      core.Symmetric(core.Points(10), core.Points(14)),
								Gap:          core.Points(4),
							}
						},
						Children: []core.Node{
							core.Show{
								When: func() bool { it := itemFn(); return it.FromAgent != "" || it.DeliveryMode != "" },
								Then: func(cx *core.Scope) core.Node {
									return core.Text{
										MaxLines: 1,
										ContentFunc: func() string {
											it := itemFn()
											if it.FromAgent != "" {
												return "子会话汇报 · " + it.FromAgent
											}
											return "忙时" + it.DeliveryMode
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Color:      pal.TextMuted,
												FontSize:   Tokens.FontMonoMeta,
												FontWeight: core.FontWeightMedium,
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
										LineHeight: core.Points(22),
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

func renderToolIndicator(cx *core.Scope, a *App, tool func() ChatToolCall, isLive func() bool) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		t := tool()
		icon, label := lucide.Wrench, "未收到工具结果"
		ink := func() core.Color { return a.paletteOf(cx).TextMuted }
		switch {
		case t.IsError:
			icon, label = lucide.X, "工具执行失败"
			ink = func() core.Color { return a.paletteOf(cx).Danger }
		case t.HasResult || t.Output != "":
			icon, label = lucide.Check, "工具已完成"
			ink = func() core.Color { return a.paletteOf(cx).Running }
		case isLive():
			return ui.Spinner{Size: 14, Variant: ui.SpinnerSpokes, Label: "工具执行中", Tint: func() core.Color { return a.paletteOf(cx).Running }}
		}
		return core.View{Accessible: true, Label: label, Children: []core.Node{ui.Icon(icon, 14, ink)}}
	})
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
		FontSize:   Tokens.FontMonoMeta,
		LineHeight: 18,
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
	isLive func() bool,
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
							return core.Style{
								Background:   pal.Surface,
								BorderWidth:  1,
								BorderColor:  pal.Border,
								CornerRadius: core.Points(12),
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
									Gap:        Tokens.Space2,
									MinHeight:  core.Pt(Tokens.SizeTouch),
									Padding:    core.Symmetric(Tokens.Space2, core.Points(12)),
									Animated:   pressAnimStyle(scale),
								},
								Children: []core.Node{
									renderToolIndicator(cx, a, getTC, isLive),
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
									// Quiet Studio DIFF capsule indicator when tool modified code
									core.Show{
										When: func() bool { return getTC().HasDiff() },
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														FlexShrink:   0,
														Background:   pal.RunningSoft,
														CornerRadius: core.Points(4),
														Padding:      core.Symmetric(core.Points(2), core.Points(6)),
													}
												},
												Children: []core.Node{
													core.Text{
														Content: "DIFF",
														StyleFunc: func() core.Style {
															return core.Style{
																Color:      a.paletteOf(cx).Running,
																FontSize:   core.Points(10),
																FontWeight: core.FontWeightSemibold,
																FontFamily: core.FontMonospace,
															}
														},
													},
												},
											}
										},
									},
									core.Show{
										When: func() bool { return chatCtrl.IsExpanded(expandKey) },
										Then: func(cx *core.Scope) core.Node {
											return core.Icon{
												SVG: iconChevronD,
												StyleFunc: func() core.Style {
													return core.Style{Width: core.Pt(14), Height: core.Pt(14), FlexShrink: 0, Color: a.paletteOf(cx).TextMuted}
												},
											}
										},
										Else: func(cx *core.Scope) core.Node {
											return core.Icon{
												SVG: iconChevronR,
												StyleFunc: func() core.Style {
													return core.Style{Width: core.Pt(14), Height: core.Pt(14), FlexShrink: 0, Color: a.paletteOf(cx).TextMuted}
												},
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
												Padding:     core.All(core.Points(12)),
												Borders:     core.Edges{Top: 1},
												BorderColor: pal.Separator,
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
																if getTC().HasResult {
																	return "无输出"
																}
																return "尚无输出"
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

	return core.View{TestID: "chat-assistant-" + cardID,
		Style: core.Style{MinWidth: core.Pt(0), Gap: 4, Padding: core.Edges{Top: 4, Bottom: 6}},
		Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node {
				isStreaming := streamingFn != nil && streamingFn()
				return ui.TextView{
					MarkdownFunc: func() string { return itemFn().Text },
					Highlighter:  highlighter,
					Selectable:   true,
					Options:      textview.Options{Streaming: isStreaming},
					StyleOf: func(part textview.Part) core.Style {
						if part == textview.PartRoot {
							return core.Style{FontSize: Tokens.FontBody, LineHeight: 23, Color: a.paletteOf(cx).Text}
						}
						return core.Style{}
					},
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
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
				core.Text{MaxLines: 1, ContentFunc: func() string {
					if model := itemFn().Model; model != "" {
						return model
					}
					return termFn().Terminal.AppName()
				}, Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0), FontSize: Tokens.FontMonoMeta}, StyleFunc: ui.Colored(func() core.Color { return a.paletteOf(cx).TextMuted })},
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
			}},
		},
	}
}
