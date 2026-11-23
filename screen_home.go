package main

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// homeScreen renders the primary Home tab: crisp top bar with icon buttons,
// live search box, deduplicated multi-instance filter pills, "需要你" inline approvals, and sessions.
func (u *uiController) homeScreen(cx *core.Scope) core.Node {
	a := u.app
	showSearch := core.NewSignal(cx, false)

	countForInstance := func(instID string) int {
		n := 0
		for _, it := range a.aggTerms.Get() {
			if it.Key.InstanceID == instID {
				n++
			}
		}
		return n
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
								return fmt.Sprintf("全部 (%d)", len(a.aggTerms.Get()))
							}, func() bool {
								return a.activeInstanceFilter.Get() == ""
							}, nil, func() {
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
			// Collapsible live search box (keeps Home dense & matches flow-home artboard)
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
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
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
							// Section 1: "需要你" (waiting / approval cards)
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
							// Section 2: Active & all sessions
							core.View{
								Style: core.Style{Gap: Tokens.RowGap},
								Children: []core.Node{
									SectionHeader(cx, a, func() string {
										return fmt.Sprintf("活跃会话 · %d", len(a.FilteredTerminals()))
									}, nil),
									core.Show{
										When: func() bool { return len(a.FilteredTerminals()) == 0 },
										Then: func(cx *core.Scope) core.Node {
											return EmptyStateCard(cx, a,
												"当前筛选下没有会话",
												"点击右上角「+」在已连上的实例里启动一个 Claude / Codex 或 Shell 终端。",
												"home-empty-new-btn", "新建会话", func() {
													u.showNewSession.Set(true)
												},
											)
										},
									},
									core.Each(
										func() []AggregatedTerminal { return a.FilteredTerminals() },
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
							},
						},
					},
				},
			},
		},
	}
}
