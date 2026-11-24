package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nsgo-dev/nsgo/core"
)

const (
	chatInitialVisibleItems = 36
	chatVisibleStepItems    = 50
)

// RenderChatSession renders the Crisp Slate mobile-reflowed conversation view:
// top pagination loader, right-aligned Teal user/hc bubbles, 3px left-rail Agent cards
// with Markdown paragraphs/lists/code-copy blocks, collapsible thinking & tool capsules,
// live OSC 7501 working indicator, and inline permission approval card.
func RenderChatSession(
	cx *core.Scope,
	a *App,
	chat *ChatSessionController,
	termFn func() AggregatedTerminal,
	onSwitchToVT func(),
) core.Node {
	if chat == nil {
		return core.View{
			TestID: "chat-surface",
			Style:  core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
		}
	}

	visibleLimit := core.NewSignal(cx, chatInitialVisibleItems)

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
		if chat.rev != nil {
			curRev = chat.rev.Get()
		}
		q := ""
		if chat.searchQuery != nil {
			q = strings.ToLower(strings.TrimSpace(chat.searchQuery.Get()))
		}
		limit := visibleLimit.Get()
		if curRev == cachedRev && q == cachedQuery && limit == cachedLimit && cachedSlice != nil {
			return cachedSlice, cachedTotal - len(cachedSlice), cachedTotal
		}

		all := chat.Items()
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
				Ref: &chat.scrollRef,
				Style: core.Style{
					FlexGrow:   1,
					FlexShrink: 1,
					MinHeight:  core.Pt(0),
				},
				OnScroll: func(ev core.ScrollEvent) {
					if ev.Stale || chat.scrolledUp == nil {
						return
					}
					distFromBottom := float32(ev.ContentSize.Height) - (float32(ev.Offset.Y) + float32(ev.Size.Height))
					isUp := distFromBottom > 60
					if chat.scrolledUp.Get() != isUp {
						chat.scrolledUp.Set(isUp)
					}
				},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Padding: core.Symmetric(Tokens.Space3, Tokens.PagePadX),
							Gap:     Tokens.Space2,
						},
						Children: []core.Node{
							// Compact source & manual refresh meta bar (keeps TopHeader spacious on 390px phones)
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
										return chat.Source().Label() + " · 刷新"
									}, func() bool {
										return chat.Source() == SourceJSONL
									}, nil, func() {
										chat.RefreshAsync()
										a.notifyToast("正在刷新对话记录…")
									}),
								},
							},
							// Pagination loader: reveals buffered in-memory items in 0ms first, then fetches older remote JSONL lines
							core.Show{
								When: func() bool { return hiddenLocalCount() > 0 || chat.HasOlder() },
								Then: func(cx *core.Scope) core.Node {
									return ActionButtonFunc(cx, a, "chat-load-older-btn", func() string {
										if hid := hiddenLocalCount(); hid > 0 {
											return fmt.Sprintf("↑ 展开更早消息（已缓存 %d 条）", hid)
										}
										if chat.IsLoadingOlder() {
											return "正在加载更早对话…"
										}
										return fmt.Sprintf("↑ 加载更早对话（剩余 %d 行）", chat.OlderRemaining())
									}, ActionSecondary, func() {
										if hiddenLocalCount() > 0 {
											visibleLimit.Set(visibleLimit.Get() + chatVisibleStepItems)
											return
										}
										visibleLimit.Set(visibleLimit.Get() + chatVisibleStepItems)
										chat.LoadOlder()
									})
								},
							},
							// Empty / Loading guidance card (two distinct reactive conditions so leaving SourceLoading with 0 items ALWAYS updates!)
							core.Show{
								When: func() bool { return totalFilteredCount() == 0 && chat.Source() == SourceLoading },
								Then: func(cx *core.Scope) core.Node {
									return EmptyStateCard(cx, a, "正在同步会话记录…", "正在定位并拉取远端会话日志，马上呈现。", "", "", nil)
								},
							},
							core.Show{
								When: func() bool { return totalFilteredCount() == 0 && chat.Source() != SourceLoading },
								Then: func(cx *core.Scope) core.Node {
									return EmptyStateCard(
										cx,
										a,
										"当前暂无结构化对话记录",
										"你可以直接在下方输入框发送指令开启对话，或切回「终端」查看原始屏幕。",
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
										return renderChatThinkingPill(cx, a, chat, itemFn)
									case RoleToolGroup:
										return renderChatToolGroup(cx, a, chat, itemFn)
									default:
										return renderChatAssistantCard(cx, a, itemFn, termFn)
									}
								},
							),
							// Live working status strip (OSC 7501)
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
												CornerRadius: Tokens.RadiusCtrl,
												BorderWidth:  1,
												BorderColor:  pal.Accent,
												Padding:      core.Symmetric(core.Points(8), Tokens.Space3),
											}
										},
										Children: []core.Node{
											core.View{
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														FlexShrink:   0,
														Width:        core.Pt(8),
														Height:       core.Pt(8),
														CornerRadius: 4,
														Background:   pal.Accent,
													}
												},
											},
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
			// Floating Scroll-to-Bottom button
			core.Show{
				When: func() bool {
					return chat.scrolledUp != nil && chat.scrolledUp.Get()
				},
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						Style: core.Style{
							Position: core.Absolute,
							Right:    core.Pt(14),
							Bottom:   core.Pt(14),
						},
						Children: []core.Node{
							ActionButton(cx, a, "chat-scroll-bottom-fab", "↓ 底部", ActionPrimary, func() {
								chat.ScrollToBottom()
							}),
						},
					}
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
		if strings.Contains(strings.ToLower(tc.Name+" "+tc.Target+" "+tc.Output), q) {
			return true
		}
	}
	return false
}

func renderChatUserBubble(cx *core.Scope, a *App, itemFn func() ChatTurnItem) core.Node {
	it0 := itemFn()
	return core.View{
		TestID: "chat-user-" + it0.ID,
		Style: core.Style{
			Direction:      core.Row,
			JustifyContent: core.JustifyEnd,
			Padding:        core.Edges{Left: 36},
		},
		Children: []core.Node{
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						FlexShrink:   1,
						MinWidth:     core.Pt(0),
						Background:   pal.AccentSoft,
						CornerRadius: Tokens.RadiusCard,
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
									return "已发送 · 等待落盘"
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
						ContentFunc: func() string { return itemFn().Text },
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Color:    pal.Text,
								FontSize: Tokens.FontBody,
							}
						},
					},
				},
			},
		},
	}
}

func renderChatThinkingPill(cx *core.Scope, a *App, chat *ChatSessionController, itemFn func() ChatTurnItem) core.Node {
	it0 := itemFn()
	itemID := it0.ID
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
				TestID: "chat-thinking-" + itemID,
				Role:   core.RoleButton,
				OnPress: func(core.PressEvent) {
					chat.ToggleExpanded(itemID)
				},
				Style: core.Style{
					Direction:  core.Row,
					AlignItems: core.AlignCenter,
					Gap:        Tokens.Space2,
					Padding:    core.Symmetric(core.Points(6), Tokens.Space3),
				},
				Children: []core.Node{
					core.Text{
						MaxLines: 1,
						ContentFunc: func() string {
							arrow := "▸"
							if chat.IsExpanded(itemID) {
								arrow = "▾"
							}
							n := utf8.RuneCountInString(itemFn().Text)
							return fmt.Sprintf("%s 思考过程 · %d 字", arrow, n)
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
				When: func() bool { return chat.IsExpanded(itemID) },
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
								ContentFunc: func() string { return itemFn().Text },
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Color:    pal.TextSecondary,
										FontSize: Tokens.FontCaption,
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

func renderChatToolGroup(cx *core.Scope, a *App, chat *ChatSessionController, itemFn func() ChatTurnItem) core.Node {
	groupID := itemFn().ID
	return core.View{
		Style: core.Style{Gap: core.Points(4)},
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
					getTC := func() ChatToolCall { return entryFn().Tool }

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
								TestID: testID,
								Role:   core.RoleButton,
								OnPress: func(core.PressEvent) {
									chat.ToggleExpanded(expandKey)
								},
								Style: core.Style{
									Direction:  core.Row,
									AlignItems: core.AlignCenter,
									Gap:        core.Points(6),
									Padding:    core.Symmetric(core.Points(6), Tokens.Space3),
								},
								Children: []core.Node{
									core.View{
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											tc := getTC()
											c := pal.TextMuted
											if tc.IsError {
												c = pal.Danger
											} else if tc.Output != "" {
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
									core.Text{
										ContentFunc: func() string {
											if chat.IsExpanded(expandKey) {
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
								When: func() bool { return chat.IsExpanded(expandKey) },
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Padding:     core.Symmetric(Tokens.Space2, Tokens.Space3),
												Borders:     core.Edges{Top: 1},
												BorderColor: pal.Separator,
												Background:  pal.Surface,
												Gap:         core.Points(4),
											}
										},
										Children: []core.Node{
											core.Show{
												When: func() bool { return getTC().InputSummary != "" },
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
											core.Text{
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

type indexedMarkdownBlock struct {
	Key   string
	Index int
	Block MarkdownBlock
}

func renderChatAssistantCard(
	cx *core.Scope,
	a *App,
	itemFn func() ChatTurnItem,
	termFn func() AggregatedTerminal,
) core.Node {
	cardID := itemFn().ID

	blocksNode := core.Each(
		func() []indexedMarkdownBlock {
			it := itemFn()
			blks := it.Blocks
			if len(blks) == 0 && it.Text != "" {
				blks = SplitMarkdownBlocks(it.Text)
			}
			out := make([]indexedMarkdownBlock, len(blks))
			for i, b := range blks {
				out[i] = indexedMarkdownBlock{
					Key:   fmt.Sprintf("%s-blk-%d-%d", cardID, i, b.Kind),
					Index: i,
					Block: b,
				}
			}
			return out
		},
		func(b indexedMarkdownBlock) string { return b.Key },
		func(cx *core.Scope, blkFn func() indexedMarkdownBlock, _ func() int) core.Node {
			initBlk := blkFn()
			bi := initBlk.Index
			switch initBlk.Block.Kind {
			case BlockCode:
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
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{
									Direction:      core.Row,
									AlignItems:     core.AlignCenter,
									JustifyContent: core.JustifySpaceBetween,
									Gap:            Tokens.Space2,
									Padding:        core.Symmetric(core.Points(4), Tokens.Space2),
									Borders:        core.Edges{Bottom: 1},
									BorderColor:    pal.Separator,
								}
							},
							Children: []core.Node{
								core.Text{
									MaxLines: 1,
									ContentFunc: func() string {
										lang := blkFn().Block.Lang
										if lang == "" {
											return "code"
										}
										return lang
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
								core.View{
									TestID: fmt.Sprintf("chat-copy-code-%s-%d", cardID, bi),
									Role:   core.RoleButton,
									OnPress: func(core.PressEvent) {
										if cx != nil {
											cx.SetClipboard(blkFn().Block.Text)
											a.notifyToast("已复制代码块")
										}
									},
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{
											FlexShrink:   0,
											Background:   pal.Surface,
											CornerRadius: Tokens.RadiusKey,
											BorderWidth:  1,
											BorderColor:  pal.Border,
											Padding:      core.Symmetric(core.Points(2), core.Points(8)),
										}
									},
									Children: []core.Node{
										core.Text{
											Content: "复制",
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{
													Color:      pal.Accent,
													FontSize:   Tokens.FontMonoMeta,
													FontWeight: core.FontWeightSemibold,
												}
											},
										},
									},
								},
							},
						},
						core.Scroll{
							Horizontal:    true,
							NoShrink:      true,
							HideIndicator: false,
							Children: []core.Node{
								core.View{
									Style: core.Style{Padding: core.Symmetric(Tokens.Space2, Tokens.Space3)},
									Children: []core.Node{
										core.Text{
											ContentFunc: func() string { return blkFn().Block.Text },
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{
													Color:      pal.Text,
													FontSize:   Tokens.FontMonoMeta,
													FontFamily: vtFontFamily,
												}
											},
										},
									},
								},
							},
						},
					},
				}

			case BlockList:
				return core.View{
					Style: core.Style{Gap: core.Points(4)},
					Children: []core.Node{
						core.Each(
							func() []string { return blkFn().Block.Items },
							func(s string) string { return s },
							func(cx *core.Scope, liFn func() string, _ func() int) core.Node {
								return core.View{
									Style: core.Style{
										Direction: core.Row,
										Gap:       core.Points(6),
									},
									Children: []core.Node{
										core.Text{
											Content: "•",
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{
													FlexShrink: 0,
													Color:      pal.Accent,
													FontSize:   Tokens.FontBody,
													FontWeight: core.FontWeightBold,
												}
											},
										},
										core.Text{
											ContentFunc: liFn,
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{
													FlexGrow:   1,
													FlexShrink: 1,
													MinWidth:   core.Pt(0),
													Color:      pal.Text,
													FontSize:   Tokens.FontBody,
												}
											},
										},
									},
								}
							},
						),
					},
				}

			default:
				return core.Text{
					ContentFunc: func() string { return blkFn().Block.Text },
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{
							Color:    pal.Text,
							FontSize: Tokens.FontBody,
						}
					},
				}
			}
		},
	)

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
					core.View{
						Style: core.Style{
							Direction:  core.Row,
							AlignItems: core.AlignCenter,
							Gap:        core.Points(6),
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
					blocksNode,
				},
			},
		},
	}
}
