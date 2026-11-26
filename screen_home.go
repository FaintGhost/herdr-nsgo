package main

import (
	"fmt"
	"time"

	"github.com/nsgo-dev/nsgo/base/dragdrop"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// homeScreen renders the Quiet Studio attention-priority Home tab:
// borderless top header, pill host filters directly on Bg, and grouped inset cards
// ("需要你" inline approvals -> "运行中" -> collapsible "就绪与空闲" -> subtle "已结束"),
// with active-time (newest/oldest) sorting and long-press drag-to-reorder.
func (u *uiController) homeScreen(cx *core.Scope, wide bool) core.Node {
	a := u.app
	showSearch := core.NewSignal(cx, false)
	showSortSheet := core.NewSignal(cx, false)
	showIdle := core.NewSignal(cx, true)
	showFinished := core.NewSignal(cx, false)
	expandedGroups := core.NewSignalFunc(cx, map[string]bool{}, nil)
	draggingKey := core.NewSignal(cx, "")
	dropTargetKey := core.NewSignal(cx, "")
	dropTargetAfter := core.NewSignal(cx, false)

	toggleGroup := func(key string) {
		animateSpringLayout(cx)
		playSelectionHaptic(cx)
		cur := expandedGroups.Get()
		next := make(map[string]bool, len(cur)+1)
		for k, v := range cur {
			next[k] = v
		}
		next[key] = !next[key]
		expandedGroups.Set(next)
	}

	activeAllCount := func() int {
		active, _ := GroupSessions(a.aggTerms.Get())
		return len(active)
	}

	countForInstance := func(instID string) int {
		active, _ := GroupSessions(a.aggTerms.Get())
		n := 0
		for _, g := range active {
			if g.Primary.Key.InstanceID == instID {
				n++
			}
		}
		return n
	}

	filteredBuckets := func() (working, rest []SessionGroup, finished []AggregatedTerminal) {
		active, fin := GroupSessions(a.FilteredTerminals())
		mode := a.SessionSort()
		order := a.ManualSessionOrder()
		live := a.LiveActiveMap()
		if mode == SortManual {
			return SortSessionGroups(active, mode, order, live), nil, fin
		}
		for _, g := range active {
			isWorking := g.Primary.Terminal.StateWord() == "working" || g.ActiveChildren > 0
			if isWorking {
				working = append(working, g)
			} else {
				rest = append(rest, g)
			}
		}
		working = SortSessionGroups(working, mode, order, live)
		rest = SortSessionGroups(rest, mode, order, live)
		return working, rest, fin
	}

	allVisibleGroupKeys := func() []string {
		w, r, _ := filteredBuckets()
		keys := make([]string, 0, len(w)+len(r))
		for _, g := range w {
			keys = append(keys, g.Primary.Key.String())
		}
		for _, g := range r {
			keys = append(keys, g.Primary.Key.String())
		}
		return keys
	}

	renderSessionGroupItem := func(cx *core.Scope, item func() SessionGroup, idx func() int) core.Node {
		g0 := item()
		rootKey := g0.Primary.Key.String()
		isExpanded := func() bool {
			g := item()
			if v, set := expandedGroups.Get()[rootKey]; set {
				return v
			}
			return g.AttentionChildren > 0
		}

		dropIndicator := func(show func() bool) core.Node {
			return core.Show{
				When: show,
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						StyleFunc: func() core.Style {
							return core.Style{
								Height:       core.Pt(2),
								Margin:       core.Symmetric(0, Tokens.Space3),
								CornerRadius: Tokens.RadiusPill,
								Background:   a.paletteOf(cx).Running,
							}
						},
					}
				},
			}
		}

		rowContent := core.View{
			StyleFunc: func() core.Style {
				var op core.Opacity
				if draggingKey.Get() == rootKey {
					op = core.Alpha(0.42)
				}
				return core.Style{
					Direction:  core.Row,
					AlignItems: core.AlignCenter,
					Opacity:    op,
				}
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
					Children: []core.Node{
						SessionRow(cx, a, func() AggregatedTerminal { return item().Primary }, func() bool {
							return u.activeSession.Get() == item().Primary.Key
						}, func(k TermKey) {
							u.openSession(cx, k)
						}),
					},
				},
				core.Show{
					When: func() bool { return a.SessionSort() == SortManual },
					Then: func(cx *core.Scope) core.Node {
						return core.View{
							Style: core.Style{
								Padding:    core.Edges{Right: 10, Left: 2},
								FlexShrink: 0,
							},
							Children: []core.Node{
								core.Icon{
									SVG: iconGrip,
									StyleFunc: func() core.Style {
										return core.Style{Width: core.Pt(15), Height: core.Pt(15), Color: a.paletteOf(cx).TextMuted}
									},
								},
							},
						}
					},
				},
			},
		}

		draggableRow := dragdrop.Target{
			Accept: func(data any) bool {
				k, ok := data.(string)
				return ok && k != "" && k != rootKey
			},
			OnEnter: func(ev dragdrop.Event) {
				dropTargetKey.Set(rootKey)
				dropTargetAfter.Set(ev.Position.Y > 30)
			},
			OnMove: func(ev dragdrop.Event) {
				if dropTargetKey.GetUntracked() != rootKey {
					dropTargetKey.Set(rootKey)
				}
				dropTargetAfter.Set(ev.Position.Y > 30)
			},
			OnLeave: func(dragdrop.Event) {
				if dropTargetKey.GetUntracked() == rootKey {
					dropTargetKey.Set("")
				}
			},
			OnDrop: func(ev dragdrop.Event) {
				fromKey, _ := ev.Data.(string)
				after := ev.Position.Y > 30
				dropTargetKey.Set("")
				draggingKey.Set("")
				if fromKey != "" && fromKey != rootKey {
					animateSpringLayout(cx)
					playSelectionHaptic(cx)
					a.ReorderSession(fromKey, rootKey, allVisibleGroupKeys(), after)
					a.notifyToast("已更新手动排序")
				}
			},
			View: core.View{
				Children: []core.Node{
					dropIndicator(func() bool { return dropTargetKey.Get() == rootKey && !dropTargetAfter.Get() }),
					dragdrop.Source{
						Activation: dragdrop.AfterLongPress,
						Threshold:  4,
						Data:       func() any { return rootKey },
						OnStart: func(any) {
							draggingKey.Set(rootKey)
							if cx != nil && !cx.Env().Simulated() {
								cx.PlayHaptic(core.HapticMedium)
							}
						},
						OnEnd: func(dragdrop.Result) {
							draggingKey.Set("")
							dropTargetKey.Set("")
						},
						Preview: func(any) core.Node {
							return core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Direction:    core.Row,
										AlignItems:   core.AlignCenter,
										Gap:          Tokens.Space2,
										Background:   pal.GlassSurface,
										BorderWidth:  1,
										BorderColor:  pal.GlassBorder,
										CornerRadius: Tokens.RadiusCard,
										Padding:      core.Symmetric(Tokens.Space2, Tokens.Space3),
										ShadowColor:  pal.Overlay,
										ShadowOffset: core.Point{Y: 6},
										ShadowRadius: 16,
									}
								},
								Children: []core.Node{
									HarnessBrandIcon(cx, a, func() Terminal { return item().Primary.Terminal }, "", 15),
									core.Text{
										MaxLines:    1,
										ContentFunc: func() string { return item().Primary.Terminal.Label() },
										StyleFunc: func() core.Style {
											return core.Style{
												Color:      a.paletteOf(cx).Text,
												FontSize:   Tokens.FontRowTitle,
												FontWeight: core.FontWeightMedium,
											}
										},
									},
								},
							}
						},
						View: core.View{
							DelayLongPress: 280 * time.Millisecond,
							Children:       []core.Node{rowContent},
						},
					},
					dropIndicator(func() bool { return dropTargetKey.Get() == rootKey && dropTargetAfter.Get() }),
				},
			},
		}

		return core.View{
			Children: []core.Node{
				core.Show{
					When: func() bool { return idx() > 0 },
					Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 34) },
				},
				draggableRow,
				// Sub-sessions disclosure row when parent spawned child terminals
				core.Show{
					When: func() bool { return len(item().Children) > 0 },
					Then: func(cx *core.Scope) core.Node {
						return core.View{
							Style: core.Style{
								Padding: core.Edges{Left: 34, Bottom: Tokens.Space2},
								Gap:     Tokens.Space1,
							},
							Children: []core.Node{
								core.View{
									TestID: "subsessions-toggle-" + rootKey,
									Role:   core.RoleButton,
									OnPress: func(core.PressEvent) {
										toggleGroup(rootKey)
									},
									StyleFunc: func() core.Style {
										return core.Style{
											Direction:  core.Row,
											AlignItems: core.AlignCenter,
											AlignSelf:  core.AlignStart,
											Gap:        core.Points(4),
											Padding:    core.Symmetric(core.Points(4), core.Points(2)),
										}
									},
									Children: []core.Node{
										core.Icon{
											SVG: iconSubagent,
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												fg := pal.TextMuted
												if item().AttentionChildren > 0 {
													fg = pal.Warn
												}
												return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: fg}
											},
										},
										core.Text{
											ContentFunc: func() string {
												g := item()
												if g.ActiveChildren > 0 {
													return fmt.Sprintf("%d 个子会话 · %d 活跃", len(g.Children), g.ActiveChildren)
												}
												return fmt.Sprintf("%d 个子会话", len(g.Children))
											},
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												fg := pal.TextMuted
												if item().AttentionChildren > 0 {
													fg = pal.Warn
												}
												return core.Style{
													Color:      fg,
													FontSize:   Tokens.FontMonoMeta,
													FontWeight: core.FontWeightMedium,
												}
											},
										},
										core.Show{
											When: isExpanded,
											Then: func(cx *core.Scope) core.Node {
												return core.Icon{
													SVG: iconChevronU,
													StyleFunc: func() core.Style {
														return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
													},
												}
											},
											Else: func(cx *core.Scope) core.Node {
												return core.Icon{
													SVG: iconChevronD,
													StyleFunc: func() core.Style {
														return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
													},
												}
											},
										},
									},
								},
								core.Show{
									When: isExpanded,
									Then: func(cx *core.Scope) core.Node {
										return core.View{
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{
													Borders:     core.Edges{Left: 2},
													BorderColor: pal.Separator,
													Padding:     core.Edges{Left: Tokens.Space2},
												}
											},
											Children: []core.Node{
												core.Each(
													func() []AggregatedTerminal { return item().Children },
													func(ch AggregatedTerminal) string { return ch.Key.String() },
													func(cx *core.Scope, chItem func() AggregatedTerminal, chIdx func() int) core.Node {
														return core.View{
															Children: []core.Node{
																core.Show{
																	When: func() bool { return chIdx() > 0 },
																	Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 14) },
																},
																SessionRow(cx, a, chItem, func() bool {
																	return u.activeSession.Get() == chItem().Key
																}, func(k TermKey) {
																	u.openSession(cx, k)
																}),
															},
														}
													},
												),
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
	}

	headerRight := []core.Node{
		IconTap(cx, a, "home-sort-btn", "排序方式", iconSort, func() {
			playSelectionHaptic(cx)
			showSortSheet.Set(true)
		}),
		IconTap(cx, a, "home-search-btn", "搜索会话", iconSearch, func() {
			animateSpringLayout(cx)
			showSearch.Set(!showSearch.Get())
		}),
		IconTap(cx, a, "fab-new-session", "新建会话", iconPlus, func() {
			u.showNewSession.Set(true)
		}),
	}
	if !wide {
		headerRight = append(headerRight, IconTap(cx, a, "home-settings-btn", "设置", iconSettings, func() {
			u.subPage.Set("settings")
		}))
	}

	bottomPad := core.Points(96)
	if wide {
		bottomPad = Tokens.Space6
	}

	return core.View{
		TestID: "screen-home",
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
			TopHeader(cx, a, func() string { return "herdr" }, nil, headerRight),
			SyncStatusBanner(cx, a),
			// Borderless horizontal instance filter pills directly on canvas Bg
			core.Scroll{
				Horizontal:    true,
				NoShrink:      true,
				HideIndicator: true,
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Direction: core.Row,
							Gap:       Tokens.Space2,
							Padding:   core.Edges{Top: Tokens.Space1, Bottom: Tokens.Space2, Left: Tokens.PagePadX, Right: Tokens.PagePadX},
						},
						Children: []core.Node{
							FilterPill(cx, a, "pill-all", func() string {
								return fmt.Sprintf("全部 (%d)", activeAllCount())
							}, func() bool {
								return a.activeInstanceFilter.Get() == ""
							}, nil, func() {
								animateSpringLayout(cx)
								a.activeInstanceFilter.Set("")
							}),
							core.Each(
								func() []InstanceSnapshot { return a.instances.Get() },
								func(s InstanceSnapshot) string { return s.InstanceID },
								func(cx *core.Scope, item func() InstanceSnapshot, _ func() int) core.Node {
									s0 := item()
									return FilterPill(cx, a, "pill-inst-"+s0.InstanceID, func() string {
										cur := item()
										return fmt.Sprintf("%s (%d)", cur.InstanceName, countForInstance(cur.InstanceID))
									}, func() bool {
										return a.activeInstanceFilter.Get() == item().InstanceID
									}, func() bool {
										return item().Online
									}, func() {
										animateSpringLayout(cx)
										id := item().InstanceID
										if a.activeInstanceFilter.Get() == id {
											a.activeInstanceFilter.Set("")
										} else {
											a.activeInstanceFilter.Set(id)
										}
									})
								},
							),
							core.Show{
								When: func() bool { return a.SessionSort() != SortActiveNewest },
								Then: func(cx *core.Scope) core.Node {
									return FilterPill(cx, a, "home-sort-pill", func() string {
										return "排序 · " + a.SessionSort().Label()
									}, func() bool { return true }, nil, func() {
										playSelectionHaptic(cx)
										showSortSheet.Set(true)
									})
								},
							},
						},
					},
				},
			},
			// Collapsible live search box on canvas Bg
			core.Show{
				When: showSearch.Get,
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						Style: core.Style{
							Padding: core.Edges{Top: Tokens.Space1, Bottom: Tokens.Space2, Left: Tokens.PagePadX, Right: Tokens.PagePadX},
						},
						Children: []core.Node{
							ui.Input{
								TestID:      "home-search",
								Placeholder: "搜索会话名、目录或地址…",
								Value:       a.searchQuery.Get,
								OnChange:    func(s string) { a.searchQuery.Set(s) },
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Width:        core.Pct(100),
										MinHeight:    core.Pt(Tokens.SizeTouch),
										Background:   pal.Surface,
										Color:        pal.Text,
										CornerRadius: Tokens.RadiusCtrl,
										BorderColor:  pal.Border,
										BorderWidth:  1,
									}
								},
							},
						},
					}
				},
			},
			// Main scrollable feed wrapped in dragdrop.Area for long-press drag-to-reorder
			dragdrop.Area{
				Children: []core.Node{
					core.Scroll{
						EdgeEffects: core.EdgeEffects{Top: core.EdgeEffectSoft, Bottom: core.EdgeEffectSoft},
						Style:       core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
						Children: []core.Node{
					core.View{
						Style: core.Style{
							Padding: core.Edges{
								Top:    Tokens.Space2,
								Left:   Tokens.PagePadX,
								Right:  Tokens.PagePadX,
								Bottom: bottomPad,
							},
							Gap: Tokens.SectionGap,
						},
						Children: []core.Node{
							// Offline/error instance alert banner
							core.Show{
								When: func() bool {
									for _, inst := range a.instances.Get() {
										if inst.State == stateFailed && inst.Problem != "" {
											return true
										}
									}
									return false
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Background:   pal.Surface,
												BorderColor:  pal.DangerSoft,
												BorderWidth:  1,
												CornerRadius: Tokens.RadiusCard,
												Padding:      core.All(core.Points(14)),
												Gap:          Tokens.Space2,
											}
										},
										Children: []core.Node{
											core.Text{
												ContentFunc: func() string {
													for _, inst := range a.instances.Get() {
														if inst.State == stateFailed && inst.Problem != "" {
															return fmt.Sprintf("%s 连接异常：%s", inst.InstanceName, inst.Problem)
														}
													}
													return ""
												},
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														Color:    pal.Danger,
														FontSize: Tokens.FontCaption,
													}
												},
											},
											ActionButton(cx, a, "home-reconnect-btn", "全部重连", ActionSecondary, func() {
												a.Reconnect()
											}),
										},
									}
								},
							},
							// Section 1: "需要你" (top-priority waiting / approval cards)
							core.Show{
								When: func() bool {
									for _, it := range a.FilteredTerminals() {
										if it.Terminal.NeedsYou() {
											return true
										}
									}
									return false
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeaderTinted(cx, a, func() string {
												n := 0
												for _, it := range a.FilteredTerminals() {
													if it.Terminal.NeedsYou() {
														n++
													}
												}
												return fmt.Sprintf("需要你 · %d", n)
											}, func(p Palette) core.Color { return p.Warn }, nil),
											core.Each(
												func() []AggregatedTerminal {
													var out []AggregatedTerminal
													for _, it := range a.FilteredTerminals() {
														if it.Terminal.NeedsYou() {
															out = append(out, it)
														}
													}
													return out
												},
												func(it AggregatedTerminal) string { return it.Key.String() },
												func(cx *core.Scope, item func() AggregatedTerminal, _ func() int) core.Node {
													return InlineApprovalCard(cx, a, item, func(k TermKey) {
														u.openSession(cx, k)
													})
												},
											),
										},
									}
								},
							},
							// Cold-start connecting skeleton (shown while dialing with no cached sessions yet)
							core.Show{
								When: func() bool {
									w, r, _ := filteredBuckets()
									syncing, _ := a.SyncStatus()
									return len(w) == 0 && len(r) == 0 && syncing && a.searchQuery.Get() == ""
								},
								Then: func(cx *core.Scope) core.Node {
									return HomeSkeletonFeed(cx, a, "home-connecting-skeleton")
								},
							},
							// Empty state when neither working nor idle sessions match current filter (and not initial dialing)
							core.Show{
								When: func() bool {
									w, r, _ := filteredBuckets()
									syncing, _ := a.SyncStatus()
									if syncing && a.searchQuery.Get() == "" {
										return false
									}
									return len(w) == 0 && len(r) == 0
								},
								Then: func(cx *core.Scope) core.Node {
									return EmptyStateCard(cx, a,
										"当前筛选下没有活跃会话",
										"点击右上角「+」在已连上的实例里启动一个 Claude / Codex 或 Shell 终端。",
										"home-empty-new-btn", "新建会话", func() {
											u.showNewSession.Set(true)
										},
									)
								},
							},
							// Section 2: "运行中" (or unified "活跃会话" when in manual drag-reorder mode)
							core.Show{
								When: func() bool {
									w, _, _ := filteredBuckets()
									return len(w) > 0
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string {
												w, _, _ := filteredBuckets()
												if a.SessionSort() == SortManual {
													return fmt.Sprintf("活跃会话 · %d", len(w))
												}
												return fmt.Sprintf("运行中 · %d", len(w))
											}, nil),
											GroupCard(cx, a, "home-group-working", []core.Node{
												core.Each(
													func() []SessionGroup {
														w, _, _ := filteredBuckets()
														return w
													},
													func(g SessionGroup) string { return g.Primary.Key.String() },
													renderSessionGroupItem,
												),
											}),
										},
									}
								},
							},
							// Section 3: "就绪与空闲" (Grouped inset card with subtle right collapse text+chevron)
							core.Show{
								When: func() bool {
									_, r, _ := filteredBuckets()
									return len(r) > 0
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string {
												_, r, _ := filteredBuckets()
												return fmt.Sprintf("就绪与空闲 · %d", len(r))
											}, core.View{
												TestID: "home-idle-toggle",
												Role:   core.RoleButton,
												OnPress: func(core.PressEvent) {
													animateSpringLayout(cx)
													playSelectionHaptic(cx)
													showIdle.Set(!showIdle.Get())
												},
												Style: core.Style{
													Direction:  core.Row,
													AlignItems: core.AlignCenter,
													Gap:        core.Points(3),
													Padding:    core.Symmetric(core.Points(4), core.Points(4)),
												},
												Children: []core.Node{
													core.Text{
														ContentFunc: func() string {
															if showIdle.Get() {
																return "收起"
															}
															return "展开"
														},
														StyleFunc: func() core.Style {
															return core.Style{
																Color:      a.paletteOf(cx).TextMuted,
																FontSize:   Tokens.FontSection,
																FontWeight: core.FontWeightMedium,
															}
														},
													},
													core.Show{
														When: showIdle.Get,
														Then: func(cx *core.Scope) core.Node {
															return core.Icon{
																SVG: iconChevronU,
																StyleFunc: func() core.Style {
																	return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
																},
															}
														},
														Else: func(cx *core.Scope) core.Node {
															return core.Icon{
																SVG: iconChevronD,
																StyleFunc: func() core.Style {
																	return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
																},
															}
														},
													},
												},
											}),
											core.Show{
												When: showIdle.Get,
												Then: func(cx *core.Scope) core.Node {
													return GroupCard(cx, a, "home-group-idle", []core.Node{
														core.Each(
															func() []SessionGroup {
																_, r, _ := filteredBuckets()
																return r
															},
															func(g SessionGroup) string { return g.Primary.Key.String() },
															renderSessionGroupItem,
														),
													})
												},
											},
										},
									}
								},
							},
							// Section 4: Subtle collapsible finished sessions disclosure
							core.Show{
								When: func() bool {
									_, _, fin := filteredBuckets()
									return len(fin) > 0
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											core.View{
												TestID: "home-finished-toggle",
												Role:   core.RoleButton,
												OnPress: func(core.PressEvent) {
													animateSpringLayout(cx)
													playSelectionHaptic(cx)
													showFinished.Set(!showFinished.Get())
												},
												Style: core.Style{
													Direction:  core.Row,
													AlignItems: core.AlignCenter,
													AlignSelf:  core.AlignStart,
													Gap:        core.Points(4),
													Padding:    core.Symmetric(core.Points(4), Tokens.Space1),
												},
												Children: []core.Node{
													core.Text{
														ContentFunc: func() string {
															_, _, fin := filteredBuckets()
															if showFinished.Get() {
																return fmt.Sprintf("收起已结束会话 · %d", len(fin))
															}
															return fmt.Sprintf("已结束会话 · %d", len(fin))
														},
														StyleFunc: func() core.Style {
															return core.Style{
																Color:      a.paletteOf(cx).TextMuted,
																FontSize:   Tokens.FontSection,
																FontWeight: core.FontWeightMedium,
															}
														},
													},
													core.Show{
														When: showFinished.Get,
														Then: func(cx *core.Scope) core.Node {
															return core.Icon{
																SVG: iconChevronU,
																StyleFunc: func() core.Style {
																	return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
																},
															}
														},
														Else: func(cx *core.Scope) core.Node {
															return core.Icon{
																SVG: iconChevronD,
																StyleFunc: func() core.Style {
																	return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
																},
															}
														},
													},
												},
											},
											core.Show{
												When: showFinished.Get,
												Then: func(cx *core.Scope) core.Node {
													return GroupCard(cx, a, "home-group-finished", []core.Node{
														core.Each(
															func() []AggregatedTerminal {
																_, _, fin := filteredBuckets()
																return fin
															},
															func(it AggregatedTerminal) string { return it.Key.String() },
															func(cx *core.Scope, item func() AggregatedTerminal, idx func() int) core.Node {
																return core.View{
																	Children: []core.Node{
																		core.Show{
																			When: func() bool { return idx() > 0 },
																			Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 34) },
																		},
																		SessionRow(cx, a, item, func() bool {
																			return u.activeSession.Get() == item().Key
																		}, func(k TermKey) {
																			u.openSession(cx, k)
																		}),
																	},
																}
															},
														),
													})
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
			// Sort Mode Bottom Sheet
			core.Show{
				When: showSortSheet.Get,
				Then: func(cx *core.Scope) core.Node {
					pickSort := func(mode SessionSortMode, toast string) {
						animateSpringLayout(cx)
						playSelectionHaptic(cx)
						a.SetSessionSort(mode)
						showSortSheet.Set(false)
						if toast != "" {
							a.notifyToast(toast)
						}
					}
					modeBadge := func(m SessionSortMode) string {
						if a.SessionSort() == m {
							return "当前"
						}
						return ""
					}
					return SheetModal(cx, a, "home-sort-sheet", "会话排序", "支持按活跃时间排序或长按拖拽自由排序", func() { showSortSheet.Set(false) }, []core.Node{
						GroupCard(cx, a, "", []core.Node{
							SheetListRow(cx, a, "sort-mode-newest", "最近活跃（新 → 旧）", modeBadge(SortActiveNewest), iconSort, false, false, func() {
								pickSort(SortActiveNewest, "已按最近活跃排序")
							}),
							InsetDivider(cx, a, 14),
							SheetListRow(cx, a, "sort-mode-oldest", "最早活跃（旧 → 新）", modeBadge(SortActiveOldest), iconSort, false, false, func() {
								pickSort(SortActiveOldest, "已按最早活跃排序")
							}),
							InsetDivider(cx, a, 14),
							SheetListRow(cx, a, "sort-mode-manual", "手动拖拽排序", modeBadge(SortManual), iconGrip, false, false, func() {
								pickSort(SortManual, "长按任意会话行即可拖拽调整顺序")
							}),
						}),
					})
				},
			},
		},
	}
}
