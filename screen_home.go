package main

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
)

// homeScreen renders the primary Home tab: top bar (Settings + New Session),
// multi-instance filter pills, "需要你" inline approval cards, and active sessions list.
func (u *uiController) homeScreen(cx *core.Scope) core.Node {
	a := u.app

	return core.View{
		TestID: "screen-home",
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexGrow:   1,
				Background: pal.Bg,
			}
		},
		Children: []core.Node{
			TopHeader(cx, a, func() string { return "会话" }, nil, []core.Node{
				ActionButton(cx, a, "fab-new-session", "+ 新建", ActionPrimary, func() {
					u.showNewSession.Set(true)
				}),
				ActionButton(cx, a, "home-settings-btn", "设置", ActionGhost, func() {
					u.subPage.Set("settings")
				}),
			}),
			ToastBanner(cx, a),
			// Top horizontal instance filter pills
			core.Scroll{
				Horizontal:    true,
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
							Padding:   core.Symmetric(Tokens.PagePadX, Tokens.Space2),
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
									online := s0.Online
									return FilterPill(cx, a, "pill-inst-"+s0.InstanceID, func() string {
										cur := item()
										return fmt.Sprintf("%s (%d)", cur.InstanceName, len(cur.Terminals))
									}, func() bool {
										return a.activeInstanceFilter.Get() == item().InstanceID
									}, &online, func() {
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
			// Main scrollable list
			core.Scroll{
				Style: core.Style{FlexGrow: 1},
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
												"点击右上角「+ 新建」在已连上的实例里启动一个 Claude / Codex 或 Shell 终端。",
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
