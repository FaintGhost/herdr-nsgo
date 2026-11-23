package main

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// inboxScreen renders the Inbox ("待办") tab with reactive live search filtering
// across all waiting (blocked / permission / question / error) terminals across all instances.
func (u *uiController) inboxScreen(cx *core.Scope) core.Node {
	a := u.app

	return core.View{
		TestID: "screen-inbox",
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
			TopHeader(cx, a, func() string {
				return fmt.Sprintf("待办 · %d", len(a.AggregatedAttention()))
			}, nil, []core.Node{
				IconButton(cx, a, "inbox-refresh-btn", "刷新", iconRefresh, false, func() {
					a.Reconnect()
				}),
			}),
			ToastBanner(cx, a),
			// Reactive live search box (fixes v0.1.0 non-reactive bug)
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Background:  pal.Surface,
						Padding:     core.Symmetric(Tokens.PagePadX, Tokens.Space2),
						Borders:     core.Edges{Bottom: 1},
						BorderColor: pal.Separator,
					}
				},
				Children: []core.Node{
					ui.Input{
						TestID:      "inbox-search",
						Placeholder: "按会话名、目录或实例筛选…",
						Value:       a.inboxFilter.Get,
						OnChange:    func(s string) { a.inboxFilter.Set(s) },
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
			core.Scroll{
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Padding: core.All(Tokens.PagePadX),
							Gap:     Tokens.Space3,
						},
						Children: []core.Node{
							core.Show{
								When: func() bool { return len(a.AggregatedAttention()) == 0 },
								Then: func(cx *core.Scope) core.Node {
									return EmptyStateCard(cx, a,
										"一切顺利，没有等待你确认的终端",
										"当任何实例上的 Agent 请求权限、提问或报错时，会立刻置顶到这里并触发通知。",
										"", "", nil,
									)
								},
							},
							core.Each(
								func() []AggregatedTerminal { return a.AggregatedAttention() },
								func(it AggregatedTerminal) string { return it.Key.String() },
								func(cx *core.Scope, item func() AggregatedTerminal, _ func() int) core.Node {
									return InlineApprovalCard(cx, a, item, func(k TermKey) {
										u.openSession(cx, k)
									})
								},
							),
						},
					},
				},
			},
		},
	}
}
