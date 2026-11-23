package main

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
)

// AppName returns the detected agent/app name when reported in Status.
func (t Terminal) AppName() string {
	if t.Status != nil {
		return t.Status.App
	}
	return ""
}

// attentionWhy explains concisely why a terminal needs user attention.
func attentionWhy(t Terminal) string {
	if t.StateWord() == "error" {
		return "出错"
	}
	if t.Status != nil {
		switch t.Status.Kind {
		case "permission":
			return "请求权限"
		case "question":
			return "等待回答"
		case "auth":
			return "需要认证"
		}
	}
	return "等你处理"
}

// firstLine extracts a clean single-line status summary for cards and notifications.
func firstLine(st *Status, t Terminal) string {
	if st != nil && st.Msg != "" {
		if line, _, ok := strings.Cut(strings.TrimSpace(st.Msg), "\n"); ok {
			return strings.TrimSpace(line)
		}
		return strings.TrimSpace(st.Msg)
	}
	if t.CurrentDir() != "" {
		return t.CurrentDir()
	}
	return t.Address
}

// paletteOf resolves the active semantic Palette from App's themeMode signal.
func (a *App) paletteOf(cx *core.Scope) Palette {
	return ThemeOf(cx, a.themeMode.Get())
}

// vtPaletteOf resolves the active VT canvas palette from App's themeMode & vtThemeMode signals.
func (a *App) vtPaletteOf(cx *core.Scope) VTPalette {
	return VTPaletteOf(a.paletteOf(cx), a.vtThemeMode.Get())
}

// stateColor maps a terminal state word to semantic foreground and soft background tokens.
func stateColor(pal Palette, state string) (fg, soft core.Color, label string) {
	switch state {
	case "blocked":
		return pal.Warn, pal.WarnSoft, "等你"
	case "error":
		return pal.Danger, pal.DangerSoft, "出错"
	case "working":
		return pal.Accent, pal.AccentSoft, "运行中"
	case "idle":
		return pal.TextSecondary, pal.SurfaceRaised, "空闲"
	case "finished":
		return pal.TextSecondary, pal.Fill, "已结束"
	default:
		return pal.TextSecondary, pal.SurfaceRaised, state
	}
}

// StatusBadge renders a compact semantic pill for terminal state.
func StatusBadge(cx *core.Scope, a *App, stateFn func() string) core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			_, soft, _ := stateColor(pal, stateFn())
			return core.Style{
				FlexShrink:   0,
				Background:   soft,
				CornerRadius: Tokens.RadiusKey,
				Padding:      core.Symmetric(core.Points(3), Tokens.Space2),
				AlignItems:   core.AlignCenter,
			}
		},
		Children: []core.Node{
			core.Text{
				ContentFunc: func() string {
					pal := a.paletteOf(cx)
					_, _, label := stateColor(pal, stateFn())
					return label
				},
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg, _, _ := stateColor(pal, stateFn())
					return core.Style{
						Color:      fg,
						FontSize:   Tokens.FontMonoMeta,
						FontWeight: core.FontWeightSemibold,
					}
				},
			},
		},
	}
}

// ActionVariant controls visual emphasis of ActionButton.
type ActionVariant uint8

const (
	ActionPrimary ActionVariant = iota
	ActionSecondary
	ActionDanger
	ActionGhost
)

// ActionButton renders a Crisp Slate button with 40px touch target and FlexShrink: 0.
func ActionButton(cx *core.Scope, a *App, testID, text string, variant ActionVariant, onPress func()) core.Node {
	return ActionButtonFunc(cx, a, testID, func() string { return text }, variant, onPress)
}

// ActionButtonFunc renders a reactive-label ActionButton.
func ActionButtonFunc(cx *core.Scope, a *App, testID string, textFn func() string, variant ActionVariant, onPress func()) core.Node {
	return core.View{
		TestID:  testID,
		Role:    core.RoleButton,
		OnPress: func(core.PressEvent) {
			if onPress != nil {
				onPress()
			}
		},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bg := pal.Accent
			border := pal.Accent
			bw := core.Points(0)
			switch variant {
			case ActionSecondary:
				bg = pal.SurfaceRaised
				border = pal.Border
				bw = 1
			case ActionDanger:
				bg = pal.DangerSoft
				border = pal.Danger
				bw = 1
			case ActionGhost:
				bg = pal.Surface
				border = pal.Border
				bw = 1
			}
			return core.Style{
				FlexShrink:     0,
				MinHeight:      core.Pt(38),
				Padding:        core.Symmetric(Tokens.Space2, core.Points(14)),
				CornerRadius:   Tokens.RadiusCtrl,
				Background:     bg,
				BorderWidth:    bw,
				BorderColor:    border,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
			}
		},
		Children: []core.Node{
			core.Text{
				ContentFunc: textFn,
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg := pal.AccentFg
					switch variant {
					case ActionSecondary, ActionGhost:
						fg = pal.Text
					case ActionDanger:
						fg = pal.Danger
					}
					return core.Style{
						Color:      fg,
						FontSize:   Tokens.FontBody,
						FontWeight: core.FontWeightSemibold,
					}
				},
			},
		},
	}
}

// IconButton renders a crisp 36x36 vector icon button (for TopHeader and Composer docks).
func IconButton(cx *core.Scope, a *App, testID, label string, svg *core.SVG, primary bool, onPress func()) core.Node {
	return core.View{
		TestID: testID,
		Role:   core.RoleButton,
		Label:  label,
		OnPress: func(core.PressEvent) {
			if onPress != nil {
				onPress()
			}
		},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bg := pal.SurfaceRaised
			bc := pal.Border
			bw := core.Points(1)
			if primary {
				bg = pal.Accent
				bc = pal.Accent
				bw = 0
			}
			return core.Style{
				FlexShrink:     0,
				Width:          core.Pt(36),
				Height:         core.Pt(36),
				CornerRadius:   Tokens.RadiusCtrl,
				Background:     bg,
				BorderWidth:    bw,
				BorderColor:    bc,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
			}
		},
		Children: []core.Node{
			core.Icon{
				SVG: svg,
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg := pal.Text
					if primary {
						fg = pal.AccentFg
					}
					return core.Style{
						Width:  core.Pt(Tokens.SizeIcon),
						Height: core.Pt(Tokens.SizeIcon),
						Color:  fg,
					}
				},
			},
		},
	}
}

// FilterPill renders a selectable horizontal filter chip with optional reactive online dot.
func FilterPill(cx *core.Scope, a *App, testID string, labelFn func() string, activeFn func() bool, onlineFn func() bool, onPress func()) core.Node {
	return core.View{
		TestID:  testID,
		Role:    core.RoleButton,
		OnPress: func(core.PressEvent) {
			if onPress != nil {
				onPress()
			}
		},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			active := activeFn()
			bg := pal.SurfaceRaised
			bc := pal.Border
			if active {
				bg = pal.AccentSoft
				bc = pal.Accent
			}
			return core.Style{
				FlexShrink:     0,
				Direction:      core.Row,
				AlignItems:     core.AlignCenter,
				Gap:            core.Points(6),
				MinHeight:      core.Pt(30),
				Padding:        core.Symmetric(core.Points(5), Tokens.Space3),
				CornerRadius:   Tokens.RadiusCtrl,
				Background:     bg,
				BorderWidth:    1,
				BorderColor:    bc,
			}
		},
		Children: func() []core.Node {
			var kids []core.Node
			if onlineFn != nil {
				kids = append(kids, core.View{
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						c := pal.TextMuted
						if onlineFn() {
							c = pal.Accent
						}
						return core.Style{
							Width:        core.Pt(7),
							Height:       core.Pt(7),
							CornerRadius: 4,
							Background:   c,
						}
					},
				})
			}
			kids = append(kids, core.Text{
				ContentFunc: labelFn,
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg := pal.TextSecondary
					if activeFn() {
						fg = pal.Accent
					}
					return core.Style{
						Color:      fg,
						FontSize:   Tokens.FontCaption,
						FontWeight: core.FontWeightSemibold,
					}
				},
			})
			return kids
		}(),
	}
}

// SectionHeader renders a 12px muted section label with optional right action.
func SectionHeader(cx *core.Scope, a *App, titleFn func() string, right core.Node) core.Node {
	kids := []core.Node{
		core.Text{
			ContentFunc: titleFn,
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Color:      pal.TextMuted,
					FontSize:   Tokens.FontSection,
					FontWeight: core.FontWeightSemibold,
				}
			},
		},
	}
	if right != nil {
		kids = append(kids, right)
	}
	return core.View{
		Style: core.Style{
			Direction:      core.Row,
			AlignItems:     core.AlignCenter,
			JustifyContent: core.JustifySpaceBetween,
			Padding:        core.Symmetric(core.Points(2), core.Points(2)),
		},
		Children: kids,
	}
}

// SessionRow renders a compact crisp-slate session list card with a 3px left status rail,
// instance badge, cwd path, and last semantic status line.
func SessionRow(cx *core.Scope, a *App, itemFn func() AggregatedTerminal, selectedFn func() bool, onOpen func(TermKey)) core.Node {
	initial := itemFn()
	return core.View{
		TestID: "session-row-" + initial.Key.String(),
		Role:   core.RoleButton,
		OnPress: func(core.PressEvent) {
			if onOpen != nil {
				onOpen(itemFn().Key)
			}
		},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bg := pal.Surface
			bc := pal.Border
			if selectedFn != nil && selectedFn() {
				bg = pal.AccentSoft
				bc = pal.Accent
			}
			return core.Style{
				Direction:    core.Row,
				Background:   bg,
				CornerRadius: Tokens.RadiusCard,
				BorderWidth:  1,
				BorderColor:  bc,
				Overflow:     core.Hidden,
			}
		},
		Children: []core.Node{
			// 3px semantic left rail
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg, _, _ := stateColor(pal, itemFn().Terminal.StateWord())
					return core.Style{
						Width:      core.Pt(3),
						Background: fg,
					}
				},
			},
			core.View{
				Style: core.Style{
					FlexGrow:   1,
					FlexShrink: 1,
					MinWidth:   core.Pt(0),
					Padding:    core.Symmetric(core.Points(10), Tokens.Space3),
					Gap:        core.Points(4),
				},
				Children: []core.Node{
					// Top row: Session name (1-line ellipsized) + StatusBadge (never collides)
					core.View{
						Style: core.Style{
							Direction:      core.Row,
							AlignItems:     core.AlignCenter,
							JustifyContent: core.JustifySpaceBetween,
							Gap:            Tokens.Space2,
						},
						Children: []core.Node{
							core.Text{
								MaxLines:    1,
								ContentFunc: func() string { return itemFn().Terminal.Label() },
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										FlexGrow:   1,
										FlexShrink: 1,
										MinWidth:   core.Pt(0),
										Color:      pal.Text,
										FontSize:   Tokens.FontRowTitle,
										FontWeight: core.FontWeightSemibold,
									}
								},
							},
							StatusBadge(cx, a, func() string { return itemFn().Terminal.StateWord() }),
						},
					},
					// Second line: Semantic message / last output (max 2 lines)
					core.Text{
						MaxLines: 2,
						ContentFunc: func() string {
							it := itemFn()
							return firstLine(it.Terminal.Status, it.Terminal)
						},
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Color:    pal.TextSecondary,
								FontSize: Tokens.FontCaption,
							}
						},
					},
					// Third line: Instance chip + mono metadata (addr · app · cwd)
					core.View{
						Style: core.Style{
							Direction:  core.Row,
							AlignItems: core.AlignCenter,
							Gap:        core.Points(6),
							Margin:     core.Edges{Top: 2},
						},
						Children: []core.Node{
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										FlexShrink:   0,
										Background:   pal.SurfaceRaised,
										CornerRadius: core.Points(4),
										Padding:      core.Symmetric(core.Points(2), core.Points(6)),
									}
								},
								Children: []core.Node{
									core.Text{
										MaxLines: 1,
										ContentFunc: func() string {
											it := itemFn()
											if it.Relayed {
												return it.InstanceName + " · 中继"
											}
											return it.InstanceName
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Color:      pal.TextSecondary,
												FontSize:   core.Points(10.5),
												FontFamily: core.FontMonospace,
											}
										},
									},
								},
							},
							core.Text{
								MaxLines: 1,
								ContentFunc: func() string {
									it := itemFn()
									parts := []string{it.Key.Addr}
									if app := it.Terminal.AppName(); app != "" {
										parts = append(parts, app)
									}
									if d := shortPath(it.Terminal.CurrentDir()); d != "" {
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
						},
					},
				},
			},
		},
	}
}

// InlineApprovalCard renders a "需要你" attention card with one-tap Allow/Deny buttons ONLY
// for whitelisted (Status.App + blocked + permission) terminals; other waiting items get "进入终端处理".
func InlineApprovalCard(cx *core.Scope, a *App, itemFn func() AggregatedTerminal, onOpen func(TermKey)) core.Node {
	initial := itemFn()
	keyStr := initial.Key.String()

	return core.View{
		TestID: "attention-card-" + keyStr,
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
					fg, _, _ := stateColor(pal, itemFn().Terminal.StateWord())
					return core.Style{Width: core.Pt(3), Background: fg}
				},
			},
			core.View{
				Style: core.Style{
					FlexGrow:   1,
					FlexShrink: 1,
					MinWidth:   core.Pt(0),
					Padding:    core.All(Tokens.Space3),
					Gap:        Tokens.Space2,
				},
				Children: []core.Node{
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
									it := itemFn()
									return fmt.Sprintf("%s · %s", it.Terminal.Label(), it.InstanceName)
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										FlexGrow:   1,
										FlexShrink: 1,
										MinWidth:   core.Pt(0),
										Color:      pal.Text,
										FontSize:   Tokens.FontRowTitle,
										FontWeight: core.FontWeightSemibold,
									}
								},
							},
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									_, soft, _ := stateColor(pal, itemFn().Terminal.StateWord())
									return core.Style{
										FlexShrink:   0,
										Background:   soft,
										CornerRadius: Tokens.RadiusKey,
										Padding:      core.Symmetric(core.Points(3), Tokens.Space2),
									}
								},
								Children: []core.Node{
									core.Text{
										ContentFunc: func() string { return attentionWhy(itemFn().Terminal) },
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											fg, _, _ := stateColor(pal, itemFn().Terminal.StateWord())
											return core.Style{
												Color:      fg,
												FontSize:   Tokens.FontMonoMeta,
												FontWeight: core.FontWeightSemibold,
											}
										},
									},
								},
							},
						},
					},
					core.Text{
						MaxLines: 2,
						ContentFunc: func() string {
							it := itemFn()
							return firstLine(it.Terminal.Status, it.Terminal)
						},
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Color:    pal.TextSecondary,
								FontSize: Tokens.FontCaption,
							}
						},
					},
					core.Show{
						When: func() bool { return CanInlineApprove(itemFn().Terminal) },
						Then: func(cx *core.Scope) core.Node {
							return core.View{
								Style: core.Style{
									Direction: core.Row,
									Gap:       Tokens.Space2,
								},
								Children: []core.Node{
									core.View{
										Style: core.Style{FlexGrow: 1},
										Children: []core.Node{
											ActionButton(cx, a, "approve-allow-"+keyStr, "允许", ActionPrimary, func() {
												a.InlineApprove(itemFn().Key, true)
											}),
										},
									},
									core.View{
										Style: core.Style{FlexGrow: 1},
										Children: []core.Node{
											ActionButton(cx, a, "approve-deny-"+keyStr, "拒绝", ActionDanger, func() {
												a.InlineApprove(itemFn().Key, false)
											}),
										},
									},
									ActionButton(cx, a, "approve-open-"+keyStr, "终端", ActionSecondary, func() {
										if onOpen != nil {
											onOpen(itemFn().Key)
										}
									}),
								},
							}
						},
						Else: func(cx *core.Scope) core.Node {
							return ActionButton(cx, a, "attention-open-"+keyStr, "进入终端处理", ActionSecondary, func() {
								if onOpen != nil {
									onOpen(itemFn().Key)
								}
							})
						},
					},
				},
			},
		},
	}
}

// KeycapItem defines one terminal keycap on the single-row keycap bar.
type KeycapItem struct {
	TestID  string
	Label   string
	Accent  bool
	Danger  bool
	Active  func() bool
	OnPress func()
}

// KeycapBar renders the single-row horizontal scrollable 34px keycap dock.
func KeycapBar(cx *core.Scope, a *App, items []KeycapItem) core.Node {
	nodes := make([]core.Node, 0, len(items))
	for _, it := range items {
		item := it
		nodes = append(nodes, core.View{
			TestID: item.TestID,
			Role:   core.RoleButton,
			OnPress: func(core.PressEvent) {
				if item.OnPress != nil {
					item.OnPress()
				}
			},
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				bg := pal.SurfaceRaised
				bc := pal.Border
				if item.Active != nil && item.Active() {
					bg = pal.AccentSoft
					bc = pal.Accent
				} else if item.Danger {
					bg = pal.DangerSoft
					bc = pal.Danger
				}
				return core.Style{
					FlexShrink:     0,
					MinHeight:      core.Pt(Tokens.SizeKey),
					MinWidth:       core.Pt(40),
					Padding:        core.Symmetric(core.Points(6), core.Points(10)),
					CornerRadius:   Tokens.RadiusKey,
					Background:     bg,
					BorderWidth:    1,
					BorderColor:    bc,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifyCenter,
				}
			},
			Children: []core.Node{
				core.Text{
					Content: item.Label,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.Text
						if item.Active != nil && item.Active() {
							fg = pal.Accent
						} else if item.Danger {
							fg = pal.Danger
						}
						return core.Style{
							Color:      fg,
							FontSize:   Tokens.FontCaption,
							FontWeight: core.FontWeightSemibold,
							FontFamily: core.FontMonospace,
						}
					},
				},
			},
		})
	}

	return core.Scroll{
		Horizontal:    true,
		NoShrink:      true,
		HideIndicator: true,
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Background:  pal.Surface,
				BorderColor: pal.Separator,
				Borders:     core.Edges{Top: 1},
			}
		},
		Children: []core.Node{
			core.View{
				Style: core.Style{
					Direction: core.Row,
					Gap:       core.Points(6),
					Padding:   core.Symmetric(core.Points(6), Tokens.Space3),
				},
				Children: nodes,
			},
		},
	}
}

// TopHeader renders a crisp 44px top bar with safe-area top padding so mobile status bars never overlap it.
func TopHeader(cx *core.Scope, a *App, titleFn func() string, left core.Node, right []core.Node) core.Node {
	var leftChildren []core.Node
	if left != nil {
		leftChildren = append(leftChildren, left)
	}
	leftChildren = append(leftChildren, core.Text{
		MaxLines:    1,
		ContentFunc: titleFn,
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexShrink: 1,
				MinWidth:   core.Pt(0),
				Color:      pal.Text,
				FontSize:   Tokens.FontNav,
				FontWeight: core.FontWeightBold,
			}
		},
	})

	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Background:  pal.Surface,
				Borders:     core.Edges{Bottom: 1},
				BorderColor: pal.Separator,
				PaddingEnv:  core.EnvEdges{Top: core.EnvSafeArea},
			}
		},
		Children: []core.Node{
			core.View{
				Style: core.Style{
					Direction:      core.Row,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifySpaceBetween,
					MinHeight:      core.Pt(Tokens.SizeTopbar),
					Padding:        core.Symmetric(core.Points(6), Tokens.PagePadX),
					Gap:            Tokens.Space2,
				},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Direction:  core.Row,
							AlignItems: core.AlignCenter,
							Gap:        Tokens.Space2,
							FlexGrow:   1,
							FlexShrink: 1,
							MinWidth:   core.Pt(0),
						},
						Children: leftChildren,
					},
					core.View{
						Style: core.Style{
							Direction:  core.Row,
							AlignItems: core.AlignCenter,
							Gap:        Tokens.Space2,
							FlexShrink: 0,
						},
						Children: right,
					},
				},
			},
		},
	}
}

// EmptyStateCard renders an inline guidance card with title, body, and optional CTA button.
func EmptyStateCard(cx *core.Scope, a *App, title, body, ctaTestID, ctaLabel string, onCTA func()) core.Node {
	kids := []core.Node{
		core.Text{
			Content: title,
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Color:      pal.Text,
					FontSize:   Tokens.FontRowTitle,
					FontWeight: core.FontWeightSemibold,
				}
			},
		},
		core.Text{
			Content: body,
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Color:    pal.TextSecondary,
					FontSize: Tokens.FontCaption,
				}
			},
		},
	}
	if ctaLabel != "" && onCTA != nil {
		kids = append(kids, ActionButton(cx, a, ctaTestID, ctaLabel, ActionPrimary, onCTA))
	}
	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Background:   pal.Surface,
				CornerRadius: Tokens.RadiusCard,
				BorderWidth:  1,
				BorderColor:  pal.Border,
				Padding:      core.All(Tokens.Space4),
				Gap:          Tokens.Space2,
			}
		},
		Children: kids,
	}
}

// SheetModal renders a keyboard- and safe-area-aware bottom sheet overlay.
func SheetModal(cx *core.Scope, a *App, testID, title, closeTestID string, onClose func(), body []core.Node) core.Node {
	return core.View{
		TestID: testID,
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Position:       core.Absolute,
				Top:            core.Pt(0),
				Left:           core.Pt(0),
				Right:          core.Pt(0),
				Bottom:         core.Pt(0),
				Background:     pal.Overlay,
				JustifyContent: core.JustifyEnd,
				AlignItems:     core.AlignCenter,
				Padding:        core.All(Tokens.Space3),
				PaddingEnv:     core.EnvEdges{Top: core.EnvSafeArea, Bottom: core.EnvKeyboard},
			}
		},
		Children: []core.Node{
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Width:        core.Pct(100),
						MaxWidth:     core.Pt(560),
						MaxHeight:    core.Pct(86),
						Background:   pal.Surface,
						CornerRadius: Tokens.RadiusSheet,
						BorderWidth:  1,
						BorderColor:  pal.Border,
						Padding:      core.All(Tokens.Space4),
						PaddingEnv:   core.EnvEdges{Bottom: core.EnvSafeArea},
						Gap:          Tokens.Space3,
					}
				},
				Children: []core.Node{
					core.View{
						Style: core.Style{AlignItems: core.AlignCenter},
						Children: []core.Node{
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Width:        core.Pt(36),
										Height:       core.Pt(4),
										CornerRadius: 2,
										Background:   pal.Fill,
									}
								},
							},
						},
					},
					core.View{
						Style: core.Style{
							Direction:      core.Row,
							AlignItems:     core.AlignCenter,
							JustifyContent: core.JustifySpaceBetween,
						},
						Children: []core.Node{
							core.Text{
								Content: title,
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Color:      pal.Text,
										FontSize:   Tokens.FontNav,
										FontWeight: core.FontWeightBold,
									}
								},
							},
							ActionButton(cx, a, closeTestID, "关闭", ActionGhost, onClose),
						},
					},
					core.Scroll{
						Style: core.Style{FlexShrink: 1},
						Children: []core.Node{
							core.View{
								Style:    core.Style{Gap: Tokens.Space3},
								Children: body,
							},
						},
					},
				},
			},
		},
	}
}

// ToastBanner shows transient user feedback cleanly.
func ToastBanner(cx *core.Scope, a *App) core.Node {
	return core.Show{
		When: func() bool { return strings.TrimSpace(a.toastMsg.Get()) != "" },
		Then: func(cx *core.Scope) core.Node {
			return core.View{
				TestID: "toast-banner",
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Direction:      core.Row,
						AlignItems:     core.AlignCenter,
						JustifyContent: core.JustifySpaceBetween,
						Background:     pal.SurfaceRaised,
						BorderColor:    pal.Accent,
						BorderWidth:    1,
						CornerRadius:   Tokens.RadiusCtrl,
						Padding:        core.Symmetric(Tokens.Space2, Tokens.Space3),
						Margin:         core.Symmetric(Tokens.Space1, Tokens.PagePadX),
					}
				},
				Children: []core.Node{
					core.Text{
						ContentFunc: func() string { return a.toastMsg.Get() },
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Color:      pal.Text,
								FontSize:   Tokens.FontCaption,
								FlexShrink: 1,
							}
						},
					},
					core.View{
						Role: core.RoleButton,
						OnPress: func(core.PressEvent) {
							a.toastMsg.Set("")
						},
						Style: core.Style{FlexShrink: 0, Padding: core.Symmetric(core.Points(2), Tokens.Space2)},
						Children: []core.Node{
							core.Text{
								Content: "知道了",
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Color:      pal.Accent,
										FontSize:   Tokens.FontCaption,
										FontWeight: core.FontWeightSemibold,
									}
								},
							},
						},
					},
				},
			}
		},
	}
}

// TabBar renders the 54px safe-area-aware bottom 3-Tab navigation bar with crisp vector icons.
func TabBar(cx *core.Scope, a *App, activeTab *core.Signal[string]) core.Node {
	tabItem := func(id, testID, label string, svg *core.SVG, badgeFn func() int) core.Node {
		return core.View{
			TestID: testID,
			Role:   core.RoleButton,
			OnPress: func(core.PressEvent) {
				activeTab.Set(id)
			},
			Style: core.Style{
				FlexGrow:       1,
				MinHeight:      core.Pt(Tokens.SizeTabBar),
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
				Gap:            core.Points(3),
				Padding:        core.Symmetric(core.Points(6), core.Points(4)),
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{
						Position:       core.Relative,
						Width:          core.Pt(28),
						Height:         core.Pt(22),
						AlignItems:     core.AlignCenter,
						JustifyContent: core.JustifyCenter,
					},
					Children: []core.Node{
						core.Icon{
							SVG: svg,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								fg := pal.TextMuted
								if activeTab.Get() == id {
									fg = pal.Accent
								}
								return core.Style{
									Width:  core.Pt(20),
									Height: core.Pt(20),
									Color:  fg,
								}
							},
						},
						core.Show{
							When: func() bool { return badgeFn != nil && badgeFn() > 0 },
							Then: func(cx *core.Scope) core.Node {
								return core.View{
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{
											Position:       core.Absolute,
											Top:            core.Pt(-4),
											Right:          core.Pt(-8),
											Background:     pal.Danger,
											BorderWidth:    core.Points(1.5),
											BorderColor:    pal.Surface,
											CornerRadius:   core.Points(8),
											MinWidth:       core.Pt(16),
											Height:         core.Pt(16),
											Padding:        core.Symmetric(core.Points(0), core.Points(4)),
											AlignItems:     core.AlignCenter,
											JustifyContent: core.JustifyCenter,
										}
									},
									Children: []core.Node{
										core.Text{
											ContentFunc: func() string {
												n := badgeFn()
												if n > 99 {
													return "99+"
												}
												return fmt.Sprintf("%d", n)
											},
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{
													Color:      pal.AccentFg,
													FontSize:   core.Points(9.5),
													FontWeight: core.FontWeightBold,
												}
											},
										},
									},
								}
							},
						},
					},
				},
				core.Text{
					Content: label,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.TextMuted
						w := core.FontWeightNormal
						if activeTab.Get() == id {
							fg = pal.Accent
							w = core.FontWeightSemibold
						}
						return core.Style{
							Color:      fg,
							FontSize:   core.Points(11),
							FontWeight: w,
						}
					},
				},
			},
		}
	}

	return core.View{
		TestID: "bottom-tab-bar",
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Direction:   core.Row,
				Background:  pal.Surface,
				Borders:     core.Edges{Top: 1},
				BorderColor: pal.Separator,
				PaddingEnv:  core.EnvEdges{Bottom: core.EnvSafeArea},
			}
		},
		Children: []core.Node{
			tabItem("home", "tab-home", "会话", iconTerminal, nil),
			tabItem("inbox", "tab-inbox", "待办", iconInbox, func() int {
				n := 0
				for _, t := range a.aggTerms.Get() {
					if t.Terminal.NeedsYou() {
						n++
					}
				}
				return n
			}),
			tabItem("instances", "tab-instances", "实例", iconServer, nil),
		},
	}
}

func shortPath(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return "…/" + parts[len(parts)-2] + "/" + parts[len(parts)-1]
}
