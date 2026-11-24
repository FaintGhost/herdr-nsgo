package main

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// homeScreen renders the Moshi Home V2 attention-priority Home tab:
// crisp top bar, live search box, deduplicated multi-instance filter pills,
// and status-priority sections ("需要你" inline approvals -> "运行中" -> collapsible "就绪与空闲" -> "已结束").
func (u *uiController) homeScreen(cx *core.Scope) core.Node {
	a := u.app
	showSearch := core.NewSignal(cx, false)
	showIdle := core.NewSignal(cx, true)
	showFinished := core.NewSignal(cx, false)
	expandedGroups := core.NewSignalFunc(cx, map[string]bool{}, nil)

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
		for _, g := range active {
			isWorking := g.Primary.Terminal.StateWord() == "working" || g.ActiveChildren > 0
			if isWorking {
				working = append(working, g)
			} else {
				rest = append(rest, g)
			}
		}
		return working, rest, fin
	}

	renderSessionGroupItem := func(cx *core.Scope, item func() SessionGroup, _ func() int) core.Node {
		g0 := item()
		rootKey := g0.Primary.Key.String()
		isExpanded := func() bool {
			g := item()
			if v, set := expandedGroups.Get()[rootKey]; set {
				return v
			}
			return g.AttentionChildren > 0
		}
		return core.View{
			Style: core.Style{Gap: core.Points(4)},
			Children: []core.Node{
				SessionRow(cx, a, func() AggregatedTerminal { return item().Primary }, func() bool {
					return u.activeSession.Get() == item().Primary.Key
				}, func(k TermKey) {
					u.openSession(cx, k)
				}),
				// Sub-sessions toggle chip when parent spawned child terminals
				core.Show{
					When: func() bool { return len(item().Children) > 0 },
					Then: func(cx *core.Scope) core.Node {
						return core.View{
							Style: core.Style{
								Padding: core.Edges{Left: Tokens.Space3},
								Gap:     core.Points(5),
							},
							Children: []core.Node{
								core.View{
									TestID: "subsessions-toggle-" + rootKey,
									Role:   core.RoleButton,
									OnPress: func(core.PressEvent) {
										toggleGroup(rootKey)
									},
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{
											Direction:    core.Row,
											AlignItems:   core.AlignCenter,
											AlignSelf:    core.AlignStart,
											Gap:          core.Points(6),
											Background:   pal.SurfaceRaised,
											BorderWidth:  1,
											BorderColor:  pal.Border,
											CornerRadius: Tokens.RadiusKey,
											Padding:      core.Symmetric(core.Points(4), core.Points(10)),
										}
									},
									Children: []core.Node{
										core.Text{
											ContentFunc: func() string {
												g := item()
												arrow := "▸"
												if isExpanded() {
													arrow = "▾"
												}
												if g.ActiveChildren > 0 {
													return fmt.Sprintf("%s %d 个子会话（%d 活跃）", arrow, len(g.Children), g.ActiveChildren)
												}
												return fmt.Sprintf("%s %d 个子会话", arrow, len(g.Children))
											},
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												fg := pal.TextSecondary
												if item().AttentionChildren > 0 {
													fg = pal.Warn
												}
												return core.Style{
													Color:      fg,
													FontSize:   Tokens.FontMonoMeta,
													FontWeight: core.FontWeightSemibold,
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
													Gap:         core.Points(6),
												}
											},
											Children: []core.Node{
												core.Each(
													func() []AggregatedTerminal { return item().Children },
													func(ch AggregatedTerminal) string { return ch.Key.String() },
													func(cx *core.Scope, chItem func() AggregatedTerminal, _ func() int) core.Node {
														return SessionRow(cx, a, chItem, func() bool {
															return u.activeSession.Get() == chItem().Key
														}, func(k TermKey) {
															u.openSession(cx, k)
														})
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
			TopHeader(cx, a, func() string { return "herdr" }, nil, []core.Node{
				IconButton(cx, a, "home-search-btn", "搜索会话", iconSearch, false, func() {
					animateSpringLayout(cx)
					showSearch.Set(!showSearch.Get())
				}),
				IconButton(cx, a, "fab-new-session", "新建会话", iconPlus, true, func() {
					u.showNewSession.Set(true)
				}),
				IconButton(cx, a, "home-settings-btn", "设置", iconSettings, false, func() {
					u.subPage.Set("settings")
				}),
			}),
			ToastBanner(cx, a),
			// Top horizontal instance filter pills (NoShrink prevents vertical clipping!)
			core.Scroll{
				Horizontal:    true,
				NoShrink:      true,
				HideIndicator: true,
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Background:  pal.Surface,
						Borders:     core.Edges{Bottom: 1},
						BorderColor: pal.Separator,
					}
				},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Direction: core.Row,
							Gap:       Tokens.Space2,
							Padding:   core.Symmetric(Tokens.Space2, Tokens.PagePadX),
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
						},
					},
				},
			},
			// Collapsible live search box
			core.Show{
				When: showSearch.Get,
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Background:  pal.Surface,
								Padding:     core.Symmetric(Tokens.Space2, Tokens.PagePadX),
								Borders:     core.Edges{Bottom: 1},
								BorderColor: pal.Separator,
							}
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
										MinHeight:    core.Pt(36),
										Background:   pal.SurfaceRaised,
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
			// Main scrollable list
			core.Scroll{
				EdgeEffects: core.EdgeEffects{Top: core.EdgeEffectSoft, Bottom: core.EdgeEffectSoft},
				Style:       core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Padding: core.All(Tokens.PagePadX),
							Gap:     Tokens.Space4,
						},
						Children: []core.Node{
							// Any offline/error instance quick banner
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
												Background:   pal.DangerSoft,
												BorderColor:  pal.Danger,
												BorderWidth:  1,
												CornerRadius: Tokens.RadiusCard,
												Padding:      core.All(Tokens.Space3),
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
							// Section 1: "需要你" (Moshi Home V2 top-priority waiting / approval cards)
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
											SectionHeader(cx, a, func() string {
												n := 0
												for _, it := range a.FilteredTerminals() {
													if it.Terminal.NeedsYou() {
														n++
													}
												}
												return fmt.Sprintf("需要你 · %d", n)
											}, nil),
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
							// Empty state when neither working nor idle sessions match current filter
							core.Show{
								When: func() bool {
									w, r, _ := filteredBuckets()
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
							// Section 2: "运行中" (Active working agents)
							core.Show{
								When: func() bool {
									w, _, _ := filteredBuckets()
									return len(w) > 0
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Gap: Tokens.RowGap},
										Children: []core.Node{
											SectionHeader(cx, a, func() string {
												w, _, _ := filteredBuckets()
												return fmt.Sprintf("运行中 · %d", len(w))
											}, nil),
											core.Each(
												func() []SessionGroup {
													w, _, _ := filteredBuckets()
													return w
												},
												func(g SessionGroup) string { return g.Primary.Key.String() },
												renderSessionGroupItem,
											),
										},
									}
								},
							},
							// Section 3: "就绪与空闲" (Open by default, 1-tap collapsible like Moshi Home V2 Idle section!)
							core.Show{
								When: func() bool {
									_, r, _ := filteredBuckets()
									return len(r) > 0
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Gap: Tokens.RowGap},
										Children: []core.Node{
											SectionHeader(cx, a, func() string {
												_, r, _ := filteredBuckets()
												return fmt.Sprintf("就绪与空闲 · %d", len(r))
											}, FilterPill(cx, a, "home-idle-toggle", func() string {
												if showIdle.Get() {
													return "收起 ▴"
												}
												return "展开 ▾"
											}, showIdle.Get, nil, func() {
												animateSpringLayout(cx)
												showIdle.Set(!showIdle.Get())
											})),
											core.Show{
												When: showIdle.Get,
												Then: func(cx *core.Scope) core.Node {
													return core.View{
														Style: core.Style{Gap: Tokens.RowGap},
														Children: []core.Node{
															core.Each(
																func() []SessionGroup {
																	_, r, _ := filteredBuckets()
																	return r
																},
																func(g SessionGroup) string { return g.Primary.Key.String() },
																renderSessionGroupItem,
															),
														},
													}
												},
											},
										},
									}
								},
							},
							// Section 4: Collapsible finished sessions (hidden by default so Home stays focused)
							core.Show{
								When: func() bool {
									_, _, fin := filteredBuckets()
									return len(fin) > 0
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											ActionButtonFunc(cx, a, "home-finished-toggle", func() string {
												_, _, fin := filteredBuckets()
												if showFinished.Get() {
													return fmt.Sprintf("收起已结束会话 · %d ▴", len(fin))
												}
												return fmt.Sprintf("查看已结束会话 · %d ▾", len(fin))
											}, ActionGhost, func() {
												animateSpringLayout(cx)
												showFinished.Set(!showFinished.Get())
											}),
											core.Show{
												When: showFinished.Get,
												Then: func(cx *core.Scope) core.Node {
													return core.View{
														Style: core.Style{Gap: Tokens.RowGap},
														Children: []core.Node{
															core.Each(
																func() []AggregatedTerminal {
																	_, _, fin := filteredBuckets()
																	return fin
																},
																func(it AggregatedTerminal) string { return it.Key.String() },
																func(cx *core.Scope, item func() AggregatedTerminal, _ func() int) core.Node {
																	return SessionRow(cx, a, item, func() bool {
																		return u.activeSession.Get() == item().Key
																	}, func(k TermKey) {
																		u.openSession(cx, k)
																	})
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
					},
				},
			},
		},
	}
}
