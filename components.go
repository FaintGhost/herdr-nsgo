package main

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// Native 120fps spring presets matching Quiet Studio motion feel.
var (
	MoshiPressSpring = core.SpringConfig{Damping: 14, Stiffness: 320, Mass: 0.6}
	MoshiSheetSpring = core.SpringConfig{Damping: 22, Stiffness: 240, Mass: 0.8}
)

func playSelectionHaptic(cx *core.Scope) {
	if cx != nil && !cx.Env().Simulated() {
		cx.PlayHaptic(core.HapticSelection)
	}
}

func animateSpringLayout(cx *core.Scope) {
	if cx != nil && !ui.ReduceMotion(cx) {
		cx.AnimateLayout(core.LayoutSpringPreset)
	}
}

func newPressScale(cx *core.Scope) *core.AnimatedValue {
	if cx == nil || ui.ReduceMotion(cx) {
		return nil
	}
	return core.NewAnimatedValue(cx, 1)
}

func pressSpringTo(cx *core.Scope, v *core.AnimatedValue, to float64) {
	if cx == nil || v == nil {
		return
	}
	ui.Spring(cx, v, to, MoshiPressSpring)
}

func pressAnimStyle(v *core.AnimatedValue) *core.AnimatedStyle {
	if v == nil {
		return nil
	}
	return &core.AnimatedStyle{Scale: v}
}

// AppName returns the detected agent/harness name from Status.App or Foreground.Name.
func (t Terminal) AppName() string {
	if t.Status != nil && t.Status.App != "" {
		return t.Status.App
	}
	if t.Foreground != nil && t.Foreground.Name != "" {
		return t.Foreground.Name
	}
	return ""
}

// HarnessIconBadge renders a quiet neutral Lucide terminal badge (kept for switcher compatibility).
func HarnessIconBadge(cx *core.Scope, a *App, _ func() string) core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexShrink:     0,
				Width:          core.Pt(24),
				Height:         core.Pt(24),
				CornerRadius:   Tokens.RadiusKey,
				Background:     pal.SurfaceRaised,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
			}
		},
		Children: []core.Node{
			core.Icon{
				SVG: iconTerminal,
				StyleFunc: func() core.Style {
					return core.Style{Width: core.Pt(14), Height: core.Pt(14), Color: a.paletteOf(cx).TextSecondary}
				},
			},
		},
	}
}

// shortHarnessLabel formats a concise right-aligned harness label (Claude, Codex, Gemini, Pi, Shell).
func shortHarnessLabel(app string) string {
	norm := strings.ToLower(strings.TrimSpace(app))
	switch {
	case strings.Contains(norm, "claude"):
		return "Claude"
	case strings.Contains(norm, "codex"), strings.Contains(norm, "openai"):
		return "Codex"
	case strings.Contains(norm, "antigravity"), strings.Contains(norm, "gemini"):
		return "Gemini"
	case norm == "pi" || norm == "omp" || strings.Contains(norm, "oh-my-pi"):
		return "Pi"
	case strings.Contains(norm, "opencode"):
		return "OpenCode"
	case norm == "" || norm == "sh" || norm == "zsh" || norm == "bash" || norm == "fish":
		return "Shell"
	default:
		return app
	}
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

// stateColor maps a terminal state word to Quiet Studio semantic tokens.
// Running uses decoupled emerald (pal.Running), never monochrome CTA Accent.
func stateColor(pal Palette, state string) (fg, soft core.Color, label string) {
	switch state {
	case "blocked":
		return pal.Warn, pal.WarnSoft, "等你"
	case "error":
		return pal.Danger, pal.DangerSoft, "出错"
	case "working":
		return pal.Running, pal.RunningSoft, "运行中"
	case "done":
		return pal.Running, pal.RunningSoft, "已完成"
	case "live":
		return pal.TextMuted, pal.SurfaceRaised, "在线"
	case "idle":
		return pal.TextMuted, pal.SurfaceRaised, "空闲"
	case "finished":
		return pal.TextMuted, pal.Fill, "已结束"
	default:
		return pal.TextMuted, pal.SurfaceRaised, state
	}
}

// stateDotColor returns the 8px indicator dot color for a terminal state.
func stateDotColor(pal Palette, state string) core.Color {
	switch state {
	case "working", "done":
		return pal.Running
	case "blocked":
		return pal.Warn
	case "error":
		return pal.Danger
	default:
		return pal.Fill
	}
}

// StatusBadge renders a quiet semantic status label for terminal state.
func StatusBadge(cx *core.Scope, a *App, stateFn func() string) core.Node {
	return core.Text{
		ContentFunc: func() string {
			pal := a.paletteOf(cx)
			_, _, label := stateColor(pal, stateFn())
			return label
		},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			fg, _, _ := stateColor(pal, stateFn())
			return core.Style{
				FlexShrink: 0,
				Color:      fg,
				FontSize:   Tokens.FontMonoMeta,
				FontWeight: core.FontWeightMedium,
			}
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

// ActionButton renders a Quiet Studio button with 40px touch target, 10px radius, and borderless surface steps.
func ActionButton(cx *core.Scope, a *App, testID, text string, variant ActionVariant, onPress func()) core.Node {
	return ActionButtonFunc(cx, a, testID, func() string { return text }, variant, onPress)
}

// ActionButtonFunc renders a reactive-label Quiet Studio ActionButton with 120fps press spring & haptics.
func ActionButtonFunc(cx *core.Scope, a *App, testID string, textFn func() string, variant ActionVariant, onPress func()) core.Node {
	scale := newPressScale(cx)
	return core.View{
		TestID:      testID,
		Role:        core.RoleButton,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.97) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			playSelectionHaptic(cx)
			if onPress != nil {
				onPress()
			}
		},
		Style: core.Style{Animated: pressAnimStyle(scale)},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bg := pal.Accent
			switch variant {
			case ActionSecondary:
				bg = pal.SurfaceRaised
			case ActionDanger:
				bg = pal.DangerSoft
			case ActionGhost:
				bg = core.Transparent
			}
			return core.Style{
				FlexShrink:     0,
				MinHeight:      core.Pt(Tokens.SizeTouch),
				Padding:        core.Symmetric(Tokens.Space2, Tokens.Space4),
				CornerRadius:   Tokens.RadiusCtrl,
				Background:     bg,
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
					case ActionSecondary:
						fg = pal.Text
					case ActionGhost:
						fg = pal.TextSecondary
					case ActionDanger:
						fg = pal.Danger
					}
					return core.Style{
						Color:      fg,
						FontSize:   core.Points(14),
						FontWeight: core.FontWeightMedium,
					}
				},
			},
		},
	}
}

// IconTap renders a borderless 40x40 Quiet Studio Lucide icon tap target (topbars, toolbars).
func IconTap(cx *core.Scope, a *App, testID, label string, svg *core.SVG, onPress func()) core.Node {
	scale := newPressScale(cx)
	return core.View{
		TestID:      testID,
		Role:        core.RoleButton,
		Label:       label,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.94) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			playSelectionHaptic(cx)
			if onPress != nil {
				onPress()
			}
		},
		Style: core.Style{
			FlexShrink:     0,
			Width:          core.Pt(Tokens.SizeTouch),
			Height:         core.Pt(Tokens.SizeTouch),
			CornerRadius:   Tokens.RadiusCtrl,
			AlignItems:     core.AlignCenter,
			JustifyContent: core.JustifyCenter,
			Animated:       pressAnimStyle(scale),
		},
		Children: func() []core.Node {
			if svg == nil {
				return nil
			}
			return []core.Node{
				core.Icon{
					SVG: svg,
					StyleFunc: func() core.Style {
						return core.Style{
							Width:  core.Pt(Tokens.SizeIcon),
							Height: core.Pt(Tokens.SizeIcon),
							Color:  a.paletteOf(cx).Text,
						}
					},
				},
			}
		}(),
	}
}

// IconButton renders a borderless Quiet Studio icon tap (or soft-filled circular/rounded icon button when primary).
// Kept for compatibility with existing callers while eliminating legacy boxy bordered squares.
func IconButton(cx *core.Scope, a *App, testID, label string, svg *core.SVG, primary bool, onPress func()) core.Node {
	if !primary {
		return IconTap(cx, a, testID, label, svg, onPress)
	}
	scale := newPressScale(cx)
	return core.View{
		TestID:      testID,
		Role:        core.RoleButton,
		Label:       label,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.94) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			playSelectionHaptic(cx)
			if onPress != nil {
				onPress()
			}
		},
		Style: core.Style{Animated: pressAnimStyle(scale)},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexShrink:     0,
				Width:          core.Pt(36),
				Height:         core.Pt(36),
				CornerRadius:   Tokens.RadiusPill,
				Background:     pal.Accent,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
			}
		},
		Children: func() []core.Node {
			if svg == nil {
				return nil
			}
			return []core.Node{
				core.Icon{
					SVG: svg,
					StyleFunc: func() core.Style {
						return core.Style{
							Width:  core.Pt(18),
							Height: core.Pt(18),
							Color:  a.paletteOf(cx).AccentFg,
						}
					},
				},
			}
		}(),
	}
}

// FilterPill renders a Quiet Studio rounded-pill filter chip (32px height, surface step, running green dot).
func FilterPill(cx *core.Scope, a *App, testID string, labelFn func() string, activeFn func() bool, onlineFn func() bool, onPress func()) core.Node {
	scale := newPressScale(cx)
	return core.View{
		TestID:      testID,
		Role:        core.RoleButton,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.96) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			playSelectionHaptic(cx)
			if onPress != nil {
				onPress()
			}
		},
		Style: core.Style{Animated: pressAnimStyle(scale)},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			active := activeFn()
			bg := pal.Surface
			bw := core.Points(0)
			bc := core.Transparent
			if pal.Dark && !active {
				bw = 1
				bc = pal.Border
			}
			if active {
				bg = pal.Accent
			}
			return core.Style{
				FlexShrink:   0,
				Direction:    core.Row,
				AlignItems:   core.AlignCenter,
				Gap:          core.Points(6),
				MinHeight:    core.Pt(32),
				Padding:      core.Symmetric(core.Points(6), Tokens.Space3),
				CornerRadius: Tokens.RadiusPill,
				Background:   bg,
				BorderWidth:  bw,
				BorderColor:  bc,
			}
		},
		Children: func() []core.Node {
			var kids []core.Node
			if onlineFn != nil {
				kids = append(kids, core.View{
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						c := pal.Fill
						if onlineFn() {
							c = pal.Running
						}
						return core.Style{
							Width:        core.Pt(6),
							Height:       core.Pt(6),
							CornerRadius: 3,
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
						fg = pal.AccentFg
					}
					return core.Style{
						Color:      fg,
						FontSize:   Tokens.FontCaption,
						FontWeight: core.FontWeightMedium,
					}
				},
			})
			return kids
		}(),
	}
}

// SectionHeader renders a quiet 12px muted section label with optional right action.
func SectionHeader(cx *core.Scope, a *App, titleFn func() string, right core.Node) core.Node {
	return SectionHeaderTinted(cx, a, titleFn, nil, right)
}

// SectionHeaderTinted renders a section label with an optional semantic tint color (e.g., pal.Warn / pal.Danger).
func SectionHeaderTinted(cx *core.Scope, a *App, titleFn func() string, colorFn func(Palette) core.Color, right core.Node) core.Node {
	kids := []core.Node{
		core.Text{
			ContentFunc: titleFn,
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				fg := pal.TextMuted
				if colorFn != nil {
					fg = colorFn(pal)
				}
				return core.Style{
					Color:      fg,
					FontSize:   Tokens.FontSection,
					FontWeight: core.FontWeightMedium,
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
			Padding:        core.Symmetric(core.Points(2), Tokens.Space1),
		},
		Children: kids,
	}
}

// GroupCard renders a Quiet Studio 14px inset grouped surface container (hairline-free in light, 1px subtle border in dark).
func GroupCard(cx *core.Scope, a *App, testID string, children []core.Node) core.Node {
	return core.View{
		TestID: testID,
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bw := core.Points(0)
			if pal.Dark {
				bw = 1
			}
			return core.Style{
				Background:   pal.Surface,
				CornerRadius: Tokens.RadiusCard,
				BorderWidth:  bw,
				BorderColor:  pal.Border,
				Overflow:     core.Hidden,
			}
		},
		Children: children,
	}
}

// InsetDivider renders a quiet 1px inset hairline inside GroupCard rows.
func InsetDivider(cx *core.Scope, a *App, leftInset core.Points) core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Height:     core.Pt(1),
				Background: pal.Separator,
				Margin:     core.Edges{Left: leftInset, Right: core.Points(14)},
			}
		},
	}
}

// SessionRow renders a de-cluttered Quiet Studio session row designed to live inside a GroupCard:
// 8px semantic status dot, 15px Medium title, quiet right-aligned harness/state label, optional status summary,
// and clean monospace host · cwd metadata (zero outer border, zero 3px stripe, zero gray host chip).
func SessionRow(cx *core.Scope, a *App, itemFn func() AggregatedTerminal, selectedFn func() bool, onOpen func(TermKey)) core.Node {
	return SessionRowWithDivider(cx, a, itemFn, selectedFn, false, onOpen)
}

// SessionRowWithDivider renders a SessionRow with an optional top inset hairline (for index > 0 inside a GroupCard).
func SessionRowWithDivider(cx *core.Scope, a *App, itemFn func() AggregatedTerminal, selectedFn func() bool, showTopDivider bool, onOpen func(TermKey)) core.Node {
	initial := itemFn()
	scale := newPressScale(cx)

	row := core.View{
		TestID:      "session-row-" + initial.Key.String(),
		Role:        core.RoleButton,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.985) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			playSelectionHaptic(cx)
			if onOpen != nil {
				onOpen(itemFn().Key)
			}
		},
		Style: core.Style{Animated: pressAnimStyle(scale)},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bg := core.Transparent
			if selectedFn != nil && selectedFn() {
				bg = pal.AccentSoft
			}
			return core.Style{
				Direction:  core.Row,
				AlignItems: core.AlignStart,
				Gap:        Tokens.Space3,
				Padding:    core.Symmetric(Tokens.Space3, core.Points(14)),
				Background: bg,
			}
		},
		Children: []core.Node{
			// 8px semantic status dot (signature Quiet Studio detail)
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Width:        core.Pt(8),
						Height:       core.Pt(8),
						CornerRadius: 4,
						Margin:       core.Edges{Top: 6},
						FlexShrink:   0,
						Background:   stateDotColor(pal, itemFn().Terminal.StateWord()),
					}
				},
			},
			core.View{
				Style: core.Style{
					FlexGrow:   1,
					FlexShrink: 1,
					MinWidth:   core.Pt(0),
					Gap:        core.Points(3),
				},
				Children: []core.Node{
					// Line 1: Session title + quiet right-aligned harness/state label
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
									return core.Style{
										FlexGrow:   1,
										FlexShrink: 1,
										MinWidth:   core.Pt(0),
										Color:      a.paletteOf(cx).Text,
										FontSize:   Tokens.FontRowTitle,
										FontWeight: core.FontWeightMedium,
									}
								},
							},
							core.Text{
								MaxLines: 1,
								ContentFunc: func() string {
									it := itemFn()
									st := it.Terminal.StateWord()
									if st == "blocked" || st == "error" || st == "finished" {
										_, _, lbl := stateColor(a.paletteOf(cx), st)
										return lbl
									}
									return shortHarnessLabel(it.Terminal.AppName())
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									fg, _, _ := stateColor(pal, itemFn().Terminal.StateWord())
									return core.Style{
										FlexShrink: 0,
										Color:      fg,
										FontSize:   Tokens.FontMonoMeta,
										FontWeight: core.FontWeightMedium,
									}
								},
							},
						},
					},
					// Line 2 (only when semantic status message exists): single-line summary
					core.Show{
						When: func() bool {
							it := itemFn()
							return it.Terminal.Status != nil && strings.TrimSpace(it.Terminal.Status.Msg) != ""
						},
						Then: func(cx *core.Scope) core.Node {
							return core.Text{
								MaxLines: 1,
								ContentFunc: func() string {
									it := itemFn()
									return firstLine(it.Terminal.Status, it.Terminal)
								},
								StyleFunc: func() core.Style {
									return core.Style{
										Color:    a.paletteOf(cx).TextSecondary,
										FontSize: Tokens.FontCaption,
									}
								},
							}
						},
					},
					// Line 3: clean monospace metadata (host · cwd)
					core.Text{
						MaxLines: 1,
						ContentFunc: func() string {
							it := itemFn()
							host := it.InstanceName
							if it.Relayed {
								host += " · 中继"
							}
							parts := []string{host}
							if d := shortPath(it.Terminal.CurrentDir()); d != "" {
								parts = append(parts, d)
							} else if it.Key.Addr != "" {
								parts = append(parts, it.Key.Addr)
							}
							return strings.Join(parts, " · ")
						},
						StyleFunc: func() core.Style {
							return core.Style{
								Color:      a.paletteOf(cx).TextMuted,
								FontSize:   Tokens.FontMonoMeta,
								FontFamily: core.FontMonospace,
							}
						},
					},
				},
			},
		},
	}

	if !showTopDivider {
		return row
	}
	return core.View{
		Children: []core.Node{
			InsetDivider(cx, a, 34),
			row,
		},
	}
}

// InlineApprovalCard renders a Quiet Studio "需要你" attention card with soft semantic border,
// pal.Bg monospace command block, and one-tap Allow/Deny buttons ONLY when CanInlineApprove(term) passes.
func InlineApprovalCard(cx *core.Scope, a *App, itemFn func() AggregatedTerminal, onOpen func(TermKey)) core.Node {
	initial := itemFn()
	keyStr := initial.Key.String()

	return core.View{
		TestID: "attention-card-" + keyStr,
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bc := pal.WarnSoft
			if itemFn().Terminal.StateWord() == "error" {
				bc = pal.DangerSoft
			}
			return core.Style{
				Background:   pal.Surface,
				CornerRadius: Tokens.RadiusCard,
				BorderWidth:  1,
				BorderColor:  bc,
				Padding:      core.All(core.Points(14)),
				Gap:          core.Points(10),
			}
		},
		Children: []core.Node{
			// Top line: 8px status dot + Session name + instance + right semantic reason
			core.View{
				Style: core.Style{
					Direction:      core.Row,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifySpaceBetween,
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
						Children: []core.Node{
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Width:        core.Pt(8),
										Height:       core.Pt(8),
										CornerRadius: 4,
										FlexShrink:   0,
										Background:   stateDotColor(pal, itemFn().Terminal.StateWord()),
									}
								},
							},
							core.Text{
								MaxLines: 1,
								ContentFunc: func() string {
									it := itemFn()
									return it.Terminal.Label()
								},
								StyleFunc: func() core.Style {
									return core.Style{
										FlexShrink: 1,
										MinWidth:   core.Pt(0),
										Color:      a.paletteOf(cx).Text,
										FontSize:   Tokens.FontRowTitle,
										FontWeight: core.FontWeightMedium,
									}
								},
							},
							core.Text{
								MaxLines: 1,
								ContentFunc: func() string {
									return itemFn().InstanceName
								},
								StyleFunc: func() core.Style {
									return core.Style{
										FlexShrink: 1,
										MinWidth:   core.Pt(0),
										Color:      a.paletteOf(cx).TextMuted,
										FontSize:   Tokens.FontMonoMeta,
										FontFamily: core.FontMonospace,
									}
								},
							},
						},
					},
					core.Text{
						ContentFunc: func() string { return attentionWhy(itemFn().Terminal) },
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							fg, _, _ := stateColor(pal, itemFn().Terminal.StateWord())
							return core.Style{
								FlexShrink: 0,
								Color:      fg,
								FontSize:   Tokens.FontMonoMeta,
								FontWeight: core.FontWeightMedium,
							}
						},
					},
				},
			},
			// Command / message box
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Background:   pal.Bg,
						CornerRadius: core.Points(8),
						Padding:      core.Symmetric(Tokens.Space2, core.Points(10)),
					}
				},
				Children: []core.Node{
					core.Text{
						MaxLines: 2,
						ContentFunc: func() string {
							it := itemFn()
							return firstLine(it.Terminal.Status, it.Terminal)
						},
						StyleFunc: func() core.Style {
							return core.Style{
								Color:      a.paletteOf(cx).TextSecondary,
								FontSize:   Tokens.FontMonoMeta,
								FontFamily: core.FontMonospace,
							}
						},
					},
				},
			},
			// Action row
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
							core.View{
								TestID: "approve-open-" + keyStr,
								Role:   core.RoleButton,
								Label:  "进入终端",
								OnPress: func(core.PressEvent) {
									playSelectionHaptic(cx)
									if onOpen != nil {
										onOpen(itemFn().Key)
									}
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										FlexShrink:     0,
										Width:          core.Pt(Tokens.SizeTouch),
										Height:         core.Pt(Tokens.SizeTouch),
										CornerRadius:   Tokens.RadiusCtrl,
										Background:     pal.SurfaceRaised,
										AlignItems:     core.AlignCenter,
										JustifyContent: core.JustifyCenter,
									}
								},
								Children: []core.Node{
									core.Icon{
										SVG: iconTerminal,
										StyleFunc: func() core.Style {
											return core.Style{Width: core.Pt(16), Height: core.Pt(16), Color: a.paletteOf(cx).Text}
										},
									},
								},
							},
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
	}
}

// KeycapItem defines one terminal keycap on the single-row keycap bar.
type KeycapItem struct {
	TestID    string
	Label     string
	LabelFunc func() string
	Accent    bool
	Danger    bool
	Active    func() bool
	OnPress   func()
}

// KeycapBar renders the single-row horizontal scrollable 34px keycap dock with tactile press spring.
func KeycapBar(cx *core.Scope, a *App, items []KeycapItem) core.Node {
	nodes := make([]core.Node, 0, len(items))
	for _, it := range items {
		item := it
		scale := newPressScale(cx)
		lblFn := item.LabelFunc
		if lblFn == nil {
			lbl := item.Label
			lblFn = func() string { return lbl }
		}
		nodes = append(nodes, core.View{
			TestID:      item.TestID,
			Role:        core.RoleButton,
			OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.94) },
			OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
			OnPress: func(core.PressEvent) {
				pressSpringTo(cx, scale, 1.0)
				playSelectionHaptic(cx)
				if item.OnPress != nil {
					item.OnPress()
				}
			},
			Style: core.Style{Animated: pressAnimStyle(scale)},
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				bg := pal.SurfaceRaised
				bc := pal.Border
				bw := core.Points(1)
				if item.Active != nil && item.Active() {
					bg = pal.Accent
					bc = pal.Accent
					bw = 0
				} else if item.Danger {
					bg = pal.DangerSoft
					bc = core.Transparent
					bw = 0
				}
				return core.Style{
					FlexShrink:     0,
					MinHeight:      core.Pt(Tokens.SizeKey),
					MinWidth:       core.Pt(38),
					Padding:        core.Symmetric(core.Points(6), core.Points(10)),
					CornerRadius:   Tokens.RadiusKey,
					Background:     bg,
					BorderWidth:    bw,
					BorderColor:    bc,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifyCenter,
				}
			},
			Children: []core.Node{
				core.Text{
					ContentFunc: lblFn,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.Text
						if item.Active != nil && item.Active() {
							fg = pal.AccentFg
						} else if item.Danger {
							fg = pal.Danger
						}
						return core.Style{
							Color:      fg,
							FontSize:   Tokens.FontMonoMeta,
							FontWeight: core.FontWeightMedium,
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

// TopHeader renders a Quiet Studio 48px borderless header sitting directly on pal.Bg.
func TopHeader(cx *core.Scope, a *App, titleFn func() string, left core.Node, right []core.Node) core.Node {
	return topHeaderStyled(cx, a, titleFn, left, right, false)
}

// TopHeaderBordered renders a 48px header with a crisp 1px bottom hairline (for pushed detail/session views).
func TopHeaderBordered(cx *core.Scope, a *App, titleFn func() string, left core.Node, right []core.Node) core.Node {
	return topHeaderStyled(cx, a, titleFn, left, right, true)
}

func topHeaderStyled(cx *core.Scope, a *App, titleFn func() string, left core.Node, right []core.Node, bordered bool) core.Node {
	var leftChildren []core.Node
	if left != nil {
		leftChildren = append(leftChildren, left)
	}
	titleSize := Tokens.FontNav
	if left != nil {
		titleSize = Tokens.FontSession
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
				FontSize:   titleSize,
				FontWeight: core.FontWeightSemibold,
			}
		},
	})

	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			st := core.Style{
				Background: pal.Bg,
				PaddingEnv: core.EnvEdges{Top: core.EnvSafeArea},
			}
			if bordered {
				st.Borders = core.Edges{Bottom: 1}
				st.BorderColor = pal.Separator
			}
			return st
		},
		Children: []core.Node{
			core.View{
				Style: core.Style{
					Direction:      core.Row,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifySpaceBetween,
					MinHeight:      core.Pt(Tokens.SizeTopbar),
					Padding:        core.Symmetric(Tokens.Space1, Tokens.PagePadX),
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
							Gap:        core.Points(2),
							FlexShrink: 0,
						},
						Children: right,
					},
				},
			},
		},
	}
}

// EmptyStateCard renders a quiet guidance card inside a 14px grouped surface.
func EmptyStateCard(cx *core.Scope, a *App, title, body, ctaTestID, ctaLabel string, onCTA func()) core.Node {
	kids := []core.Node{
		core.Text{
			Content: title,
			StyleFunc: func() core.Style {
				return core.Style{
					Color:      a.paletteOf(cx).Text,
					FontSize:   Tokens.FontRowTitle,
					FontWeight: core.FontWeightMedium,
				}
			},
		},
		core.Text{
			Content: body,
			StyleFunc: func() core.Style {
				return core.Style{
					Color:    a.paletteOf(cx).TextSecondary,
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
			bw := core.Points(0)
			if pal.Dark {
				bw = 1
			}
			return core.Style{
				Background:   pal.Surface,
				CornerRadius: Tokens.RadiusCard,
				BorderWidth:  bw,
				BorderColor:  pal.Border,
				Padding:      core.All(Tokens.Space4),
				Gap:          Tokens.Space2,
			}
		},
		Children: kids,
	}
}

// SheetListRow renders a Quiet Studio 46px left-aligned modal list row with leading Lucide icon,
// optional trailing value, and optional disclosure chevron.
func SheetListRow(cx *core.Scope, a *App, testID, title, value string, svg *core.SVG, danger, chevron bool, onPress func()) core.Node {
	return SheetListRowFunc(cx, a, testID, func() string { return title }, func() string { return value }, svg, danger, chevron, onPress)
}

// SheetListRowFunc renders a reactive-label Quiet Studio 46px left-aligned modal list row.
func SheetListRowFunc(cx *core.Scope, a *App, testID string, titleFn, valueFn func() string, svg *core.SVG, danger, chevron bool, onPress func()) core.Node {
	scale := newPressScale(cx)
	return core.View{
		TestID:      testID,
		Role:        core.RoleButton,
		LabelFunc:   titleFn,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.985) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			playSelectionHaptic(cx)
			if onPress != nil {
				onPress()
			}
		},
		Style: core.Style{
			Direction:  core.Row,
			AlignItems: core.AlignCenter,
			MinHeight:  core.Pt(46),
			Padding:    core.Symmetric(Tokens.Space2, core.Points(14)),
			Gap:        Tokens.Space3,
			Animated:   pressAnimStyle(scale),
		},
		Children: func() []core.Node {
			var kids []core.Node
			if svg != nil {
				kids = append(kids, core.Icon{
					SVG: svg,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.TextSecondary
						if danger {
							fg = pal.Danger
						}
						return core.Style{Width: core.Pt(18), Height: core.Pt(18), FlexShrink: 0, Color: fg}
					},
				})
			}
			kids = append(kids, core.Text{
				MaxLines:    1,
				ContentFunc: titleFn,
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg := pal.Text
					if danger {
						fg = pal.Danger
					}
					return core.Style{
						FlexGrow:   1,
						FlexShrink: 1,
						MinWidth:   core.Pt(0),
						Color:      fg,
						FontSize:   Tokens.FontRowTitle,
						FontWeight: core.FontWeightNormal,
					}
				},
			})
			if valueFn != nil {
				kids = append(kids, core.Show{
					When: func() bool { return strings.TrimSpace(valueFn()) != "" },
					Then: func(cx *core.Scope) core.Node {
						return core.Text{
							MaxLines:    1,
							ContentFunc: valueFn,
							StyleFunc: func() core.Style {
								return core.Style{
									FlexShrink: 0,
									Color:      a.paletteOf(cx).TextMuted,
									FontSize:   Tokens.FontMonoMeta,
									FontFamily: core.FontMonospace,
								}
							},
						}
					},
				})
			}
			if chevron {
				kids = append(kids, core.Icon{
					SVG: iconChevronR,
					StyleFunc: func() core.Style {
						return core.Style{Width: core.Pt(15), Height: core.Pt(15), FlexShrink: 0, Color: a.paletteOf(cx).TextMuted}
					},
				})
			}
			return kids
		}(),
	}
}

// SheetModal renders a unified Quiet Studio bottom sheet (20px top radius, drag handle, circular X close button).
func SheetModal(cx *core.Scope, a *App, testID, title, closeTestID string, onClose func(), body []core.Node) ui.Sheet {
	closeFn := func() {
		if onClose != nil {
			onClose()
		}
	}
	return ui.Sheet{
		Open:         func() bool { return true },
		OnClose:      closeFn,
		CloseOnSwipe: true,
		Label:        title,
		TestID:       testID,
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Width:        core.Pct(100),
				MaxWidth:     core.Pt(560),
				MaxHeight:    core.Pct(86),
				AlignSelf:    core.AlignCenter,
				Background:   pal.Bg,
				CornerRadius: Tokens.RadiusSheet,
				Borders:      core.Edges{Top: 1, Left: 1, Right: 1},
				BorderColor:  pal.Border,
				Gap:          Tokens.Space3,
			}
		},
		Children: []core.Node{
			// Quiet Studio drag handle
			core.View{
				StyleFunc: func() core.Style {
					return core.Style{
						Width:        core.Pt(36),
						Height:       core.Pt(4),
						CornerRadius: 2,
						Background:   a.paletteOf(cx).Fill,
						AlignSelf:    core.AlignCenter,
					}
				},
			},
			// Sheet header: 18px title + circular 32x32 Lucide X close button
			core.View{
				Style: core.Style{
					Direction:      core.Row,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifySpaceBetween,
					Padding:        core.Symmetric(0, Tokens.Space1),
				},
				Children: []core.Node{
					core.Text{
						MaxLines: 1,
						Content:  title,
						StyleFunc: func() core.Style {
							return core.Style{
								FlexShrink: 1,
								MinWidth:   core.Pt(0),
								Color:      a.paletteOf(cx).Text,
								FontSize:   core.Points(18),
								FontWeight: core.FontWeightSemibold,
							}
						},
					},
					core.View{
						TestID: closeTestID,
						Role:   core.RoleButton,
						Label:  "关闭",
						OnPress: func(core.PressEvent) {
							playSelectionHaptic(cx)
							closeFn()
						},
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								FlexShrink:     0,
								Width:          core.Pt(32),
								Height:         core.Pt(32),
								CornerRadius:   Tokens.RadiusPill,
								Background:     pal.SurfaceRaised,
								AlignItems:     core.AlignCenter,
								JustifyContent: core.JustifyCenter,
							}
						},
						Children: []core.Node{
							core.Icon{
								SVG: iconClose,
								StyleFunc: func() core.Style {
									return core.Style{Width: core.Pt(16), Height: core.Pt(16), Color: a.paletteOf(cx).Text}
								},
							},
						},
					},
				},
			},
			core.Scroll{
				Style: core.Style{FlexShrink: 1, MinHeight: core.Pt(0)},
				Children: []core.Node{
					core.View{
						Style:    core.Style{Gap: Tokens.Space3, Padding: core.Edges{Bottom: Tokens.Space4}},
						Children: body,
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
						Background:     pal.Surface,
						BorderColor:    pal.Border,
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
							return core.Style{
								Color:      a.paletteOf(cx).Text,
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
									return core.Style{
										Color:      a.paletteOf(cx).Text,
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

func attentionCount(a *App) int {
	n := 0
	for _, t := range a.aggTerms.Get() {
		if t.Terminal.NeedsYou() {
			n++
		}
	}
	return n
}

// FloatingTabBar renders the Quiet Studio 56px floating glass capsule 3-Tab bar (lay-home-float).
func FloatingTabBar(cx *core.Scope, a *App, activeTab *core.Signal[string]) core.Node {
	tabItem := func(id, testID, label string, svg *core.SVG, badgeFn func() int) core.Node {
		return core.View{
			TestID: testID,
			Role:   core.RoleButton,
			OnPress: func(core.PressEvent) {
				playSelectionHaptic(cx)
				activeTab.Set(id)
			},
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				bg := core.Transparent
				if activeTab.Get() == id {
					bg = pal.AccentSoft
				}
				return core.Style{
					FlexGrow:       1,
					Height:         core.Pt(48),
					CornerRadius:   24,
					Background:     bg,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifyCenter,
					Gap:            core.Points(2),
				}
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{
						Position:       core.Relative,
						Width:          core.Pt(26),
						Height:         core.Pt(20),
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
									fg = pal.Text
								}
								return core.Style{
									Width:  core.Pt(19),
									Height: core.Pt(19),
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
											Right:          core.Pt(-10),
											Background:     pal.Warn,
											CornerRadius:   Tokens.RadiusPill,
											MinWidth:       core.Pt(16),
											Height:         core.Pt(16),
											Padding:        core.Symmetric(0, core.Points(4)),
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
												return core.Style{
													Color:      a.paletteOf(cx).AccentFg,
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
					},
				},
				core.Text{
					Content: label,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.TextMuted
						if activeTab.Get() == id {
							fg = pal.Text
						}
						return core.Style{
							Color:      fg,
							FontSize:   core.Points(11),
							FontWeight: core.FontWeightMedium,
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
				Direction:    core.Row,
				AlignItems:   core.AlignCenter,
				Height:       core.Pt(Tokens.SizeTabBar),
				Padding:      core.All(Tokens.Space1),
				CornerRadius: 28,
				Background:   pal.GlassSurface,
				BorderWidth:  1,
				BorderColor:  pal.GlassBorder,
			}
		},
		Children: []core.Node{
			tabItem("home", "tab-home", "会话", iconTerminal, nil),
			tabItem("inbox", "tab-inbox", "待办", iconInbox, func() int { return attentionCount(a) }),
			tabItem("instances", "tab-instances", "实例", iconServer, nil),
		},
	}
}

// TabBar renders the floating capsule TabBar with safe-area bottom framing for mobile screens.
func TabBar(cx *core.Scope, a *App, activeTab *core.Signal[string]) core.Node {
	return core.View{
		PointerEvents: core.PointerEventsBoxNone,
		Style: core.Style{
			Position:   core.Absolute,
			Left:       core.Pt(Tokens.PagePadX),
			Right:      core.Pt(Tokens.PagePadX),
			Bottom:     core.Pt(Tokens.Space2),
			PaddingEnv: core.EnvEdges{Bottom: core.EnvSafeArea},
		},
		Children: []core.Node{FloatingTabBar(cx, a, activeTab)},
	}
}

// WideNavRail renders the 56px vertical desktop/tablet icon rail (lay-wide-rail):
// brand mark, Sessions, Inbox (with semantic warn dot), Instances, spacer, bottom Settings.
func WideNavRail(cx *core.Scope, a *App, activeTab *core.Signal[string], onOpenSettings func()) core.Node {
	railBtn := func(id, testID, label string, svg *core.SVG, hasDot func() bool) core.Node {
		return core.View{
			TestID: testID,
			Role:   core.RoleButton,
			Label:  label,
			OnPress: func(core.PressEvent) {
				playSelectionHaptic(cx)
				activeTab.Set(id)
			},
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				bg := core.Transparent
				if activeTab.Get() == id {
					bg = pal.AccentSoft
				}
				return core.Style{
					Position:       core.Relative,
					Width:          core.Pt(Tokens.SizeTouch),
					Height:         core.Pt(Tokens.SizeTouch),
					CornerRadius:   Tokens.RadiusCtrl,
					Background:     bg,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifyCenter,
				}
			},
			Children: []core.Node{
				core.Icon{
					SVG: svg,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.TextMuted
						if activeTab.Get() == id {
							fg = pal.Text
						}
						return core.Style{Width: core.Pt(Tokens.SizeIcon), Height: core.Pt(Tokens.SizeIcon), Color: fg}
					},
				},
				core.Show{
					When: func() bool { return hasDot != nil && hasDot() },
					Then: func(cx *core.Scope) core.Node {
						return core.View{
							StyleFunc: func() core.Style {
								return core.Style{
									Position:     core.Absolute,
									Top:          core.Pt(7),
									Right:        core.Pt(7),
									Width:        core.Pt(7),
									Height:       core.Pt(7),
									CornerRadius: 4,
									Background:   a.paletteOf(cx).Warn,
								}
							},
						}
					},
				},
			},
		}
	}

	return core.View{
		TestID: "wide-nav-rail",
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Width:       core.Pt(Tokens.RailWidth),
				Height:      core.Pct(100),
				Background:  pal.Surface,
				Borders:     core.Edges{Right: 1},
				BorderColor: pal.Separator,
				AlignItems:  core.AlignCenter,
				Padding:     core.Symmetric(Tokens.Space4, 0),
				PaddingEnv:  core.EnvEdges{Top: core.EnvSafeArea, Bottom: core.EnvSafeArea},
				Gap:         Tokens.Space2,
			}
		},
		Children: []core.Node{
			// Brand monogram
			core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						Width:          core.Pt(34),
						Height:         core.Pt(34),
						CornerRadius:   Tokens.RadiusCtrl,
						Background:     pal.Accent,
						AlignItems:     core.AlignCenter,
						JustifyContent: core.JustifyCenter,
						Margin:         core.Edges{Bottom: Tokens.Space2},
					}
				},
				Children: []core.Node{
					core.Text{
						Content: "h",
						StyleFunc: func() core.Style {
							return core.Style{
								Color:      a.paletteOf(cx).AccentFg,
								FontSize:   core.Points(15),
								FontWeight: core.FontWeightBold,
								FontFamily: core.FontMonospace,
							}
						},
					},
				},
			},
			railBtn("home", "rail-tab-home", "会话", iconTerminal, nil),
			railBtn("inbox", "rail-tab-inbox", "待办", iconInbox, func() bool { return attentionCount(a) > 0 }),
			railBtn("instances", "rail-tab-instances", "实例", iconServer, nil),
			core.View{Style: core.Style{FlexGrow: 1}},
			IconTap(cx, a, "rail-settings-btn", "设置", iconSettings, onOpenSettings),
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
