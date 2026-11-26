package main

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// inboxScreen renders the Quiet Studio Inbox ("待办") tab with reactive live search
// and 3-bucket urgency grouping (权限审批 / 等待回答与认证 / 异常与限流), hiding empty buckets.
func (u *uiController) inboxScreen(cx *core.Scope, wide bool) core.Node {
	a := u.app

	buckets := func() (perm, wait, errs []AggregatedTerminal) {
		for _, it := range a.AggregatedAttention() {
			switch {
			case CanInlineApprove(it.Terminal):
				perm = append(perm, it)
			case it.Terminal.StateWord() == "error":
				errs = append(errs, it)
			default:
				wait = append(wait, it)
			}
		}
		return perm, wait, errs
	}

	renderBucket := func(label string, itemsFn func() []AggregatedTerminal, tintFn func(Palette) core.Color) core.Node {
		return core.Show{
			When: func() bool { return len(itemsFn()) > 0 },
			Then: func(cx *core.Scope) core.Node {
				return core.View{
					Style: core.Style{Gap: Tokens.Space2},
					Children: []core.Node{
						SectionHeaderTinted(cx, a, func() string {
							return fmt.Sprintf("%s · %d", label, len(itemsFn()))
						}, tintFn, nil),
						core.Each(
							itemsFn,
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
		}
	}

	bottomPad := core.Points(96)
	if wide {
		bottomPad = Tokens.Space6
	}

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
				n := len(a.AggregatedAttention())
				if n == 0 {
					return "待办"
				}
				return fmt.Sprintf("待办 · %d", n)
			}, nil, []core.Node{
				IconTap(cx, a, "inbox-refresh-btn", "刷新", iconRefresh, func() {
					a.Reconnect()
				}),
			}),
			ToastBanner(cx, a),
			// Rounded surface search field directly on canvas Bg
			core.View{
				Style: core.Style{
					Padding: core.Edges{Top: Tokens.Space1, Bottom: Tokens.Space2, Left: Tokens.PagePadX, Right: Tokens.PagePadX},
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
								Background:   pal.Surface,
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
							renderBucket("权限审批", func() []AggregatedTerminal {
								p, _, _ := buckets()
								return p
							}, func(p Palette) core.Color { return p.Warn }),
							renderBucket("等待回答与认证", func() []AggregatedTerminal {
								_, w, _ := buckets()
								return w
							}, func(p Palette) core.Color { return p.Warn }),
							renderBucket("异常与限流", func() []AggregatedTerminal {
								_, _, e := buckets()
								return e
							}, func(p Palette) core.Color { return p.Danger }),
						},
					},
				},
			},
		},
	}
}
