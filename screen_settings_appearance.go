package main

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
)

// settingsThemeScreen renders the dedicated Theme Workshop page with live Chat+Code+VT preview,
// Light/Dark/System mode pills, 4 complete workspace theme presets, and 4 independent VT color schemes.
func (u *uiController) settingsThemeScreen(cx *core.Scope) core.Node {
	a := u.app
	highlighter := newAppHighlighter(cx)

	themeSwatchCard := func(preset ThemePreset) core.Node {
		isSelected := func() bool { return a.themePreset.Get() == preset }
		scale := newPressScale(cx)
		return core.View{
			TestID:      "theme-preset-" + string(preset),
			Role:        core.RoleButton,
			Label:       preset.Label(),
			OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.97) },
			OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
			OnPress: func(core.PressEvent) {
				pressSpringTo(cx, scale, 1.0)
				playSelectionHaptic(cx)
				animateSpringLayout(cx)
				a.SetThemePreset(preset)
			},
			Style: core.Style{
				FlexGrow:   1,
				FlexShrink: 1,
				MinWidth:   core.Pt(0),
				Animated:   pressAnimStyle(scale),
			},
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				border := pal.Border
				bw := core.Points(1)
				if isSelected() {
					border = pal.Running
					bw = 2
				}
				return core.Style{
					Background:   pal.Surface,
					BorderColor:  border,
					BorderWidth:  bw,
					CornerRadius: Tokens.RadiusCard,
					Padding:      core.All(core.Points(10)),
					Gap:          Tokens.Space2,
				}
			},
			Children: []core.Node{
				// Miniature workspace preview using this preset's real Light/Dark tokens
				core.View{
					StyleFunc: func() core.Style {
						p := PaletteFor(preset, a.paletteOf(cx).Dark)
						return core.Style{
							Height:       core.Pt(62),
							Background:   p.Bg,
							BorderColor:  p.Border,
							BorderWidth:  1,
							CornerRadius: Tokens.RadiusCtrl,
							Padding:      core.All(core.Points(8)),
							JustifyContent: core.JustifySpaceBetween,
						}
					},
					Children: []core.Node{
						core.View{
							Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6)},
							Children: []core.Node{
								core.View{
									StyleFunc: func() core.Style {
										p := PaletteFor(preset, a.paletteOf(cx).Dark)
										return core.Style{Width: core.Pt(7), Height: core.Pt(7), CornerRadius: 4, Background: p.Running}
									},
								},
								core.View{
									StyleFunc: func() core.Style {
										p := PaletteFor(preset, a.paletteOf(cx).Dark)
										return core.Style{Width: core.Pct(56), Height: core.Pt(6), CornerRadius: 3, Background: p.Accent}
									},
								},
							},
						},
						core.View{
							StyleFunc: func() core.Style {
								p := PaletteFor(preset, a.paletteOf(cx).Dark)
								return core.Style{
									Background:   p.Surface,
									BorderColor:  p.Border,
									BorderWidth:  1,
									CornerRadius: Tokens.RadiusKey,
									Padding:      core.Symmetric(core.Points(5), core.Points(7)),
									Direction:    core.Row,
									AlignItems:   core.AlignCenter,
									JustifyContent: core.JustifySpaceBetween,
								}
							},
							Children: []core.Node{
								core.View{
									StyleFunc: func() core.Style {
										p := PaletteFor(preset, a.paletteOf(cx).Dark)
										return core.Style{Width: core.Pct(62), Height: core.Pt(5), CornerRadius: 3, Background: p.Fill}
									},
								},
								core.View{
									StyleFunc: func() core.Style {
										p := PaletteFor(preset, a.paletteOf(cx).Dark)
										return core.Style{Width: core.Pt(14), Height: core.Pt(7), CornerRadius: 4, Background: p.Accent}
									},
								},
							},
						},
					},
				},
				core.View{
					Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifySpaceBetween, Gap: Tokens.Space1},
					Children: []core.Node{
						core.View{
							Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0)},
							Children: []core.Node{
								core.Text{
									MaxLines: 1,
									Content:  preset.Label(),
									StyleFunc: func() core.Style {
										return core.Style{Color: a.paletteOf(cx).Text, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold}
									},
								},
								core.Text{
									MaxLines: 1,
									Content:  preset.Desc(),
									StyleFunc: func() core.Style {
										return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: core.Points(11)}
									},
								},
							},
						},
						core.Show{
							When: isSelected,
							Then: func(cx *core.Scope) core.Node {
								return core.View{
									StyleFunc: func() core.Style {
										return core.Style{
											Width:          core.Pt(18),
											Height:         core.Pt(18),
											CornerRadius:   9,
											Background:     a.paletteOf(cx).Running,
											AlignItems:     core.AlignCenter,
											JustifyContent: core.JustifyCenter,
											FlexShrink:     0,
										}
									},
									Children: []core.Node{
										ui.Icon(lucide.Check, 11, func() core.Color { return a.paletteOf(cx).AccentFg }),
									},
								}
							},
						},
					},
				},
			},
		}
	}

	vtSchemeTile := func(scheme VTColorScheme) core.Node {
		isSelected := func() bool { return a.vtColorScheme.Get() == scheme }
		return base.Button{
			TestID: "vt-scheme-" + string(scheme),
			Label:  scheme.Label(),
			OnPress: func(core.PressEvent) {
				playSelectionHaptic(cx)
				a.SetVTColorScheme(scheme)
			},
			Style: core.Style{
				FlexGrow:   1,
				FlexShrink: 1,
				MinWidth:   core.Pt(0),
			},
			StyleFunc: func() core.Style {
				vp := VTPaletteFor(a.paletteOf(cx), a.vtThemeMode.Get(), scheme)
				border := a.paletteOf(cx).Border
				bw := core.Points(1)
				if isSelected() {
					border = a.paletteOf(cx).Running
					bw = 2
				}
				return core.Style{
					Background:     vp.Bg,
					BorderColor:    border,
					BorderWidth:    bw,
					CornerRadius:   Tokens.RadiusCtrl,
					Padding:        core.Symmetric(core.Points(9), core.Points(8)),
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifyCenter,
					Gap:            core.Points(4),
				}
			},
			Children: []core.Node{
				core.Text{
					MaxLines: 1,
					Content:  scheme.Label(),
					StyleFunc: func() core.Style {
						vp := VTPaletteFor(a.paletteOf(cx), a.vtThemeMode.Get(), scheme)
						return core.Style{Color: vp.Fg, FontSize: core.Points(11), FontWeight: core.FontWeightSemibold, FontFamily: a.MonoFontFamily()}
					},
				},
				core.View{
					Style: core.Style{Direction: core.Row, Gap: core.Points(4)},
					Children: []core.Node{
						core.View{StyleFunc: func() core.Style {
							vp := VTPaletteFor(a.paletteOf(cx), a.vtThemeMode.Get(), scheme)
							return core.Style{Width: core.Pt(7), Height: core.Pt(7), CornerRadius: 4, Background: vp.Accent}
						}},
						core.View{StyleFunc: func() core.Style {
							vp := VTPaletteFor(a.paletteOf(cx), a.vtThemeMode.Get(), scheme)
							return core.Style{Width: core.Pt(7), Height: core.Pt(7), CornerRadius: 4, Background: vp.Blue}
						}},
						core.View{StyleFunc: func() core.Style {
							vp := VTPaletteFor(a.paletteOf(cx), a.vtThemeMode.Get(), scheme)
							return core.Style{Width: core.Pt(7), Height: core.Pt(7), CornerRadius: 4, Background: vp.Amber}
						}},
						core.View{StyleFunc: func() core.Style {
							vp := VTPaletteFor(a.paletteOf(cx), a.vtThemeMode.Get(), scheme)
							return core.Style{Width: core.Pt(7), Height: core.Pt(7), CornerRadius: 4, Background: vp.Red}
						}},
					},
				},
			},
		}
	}

	return core.View{
		TestID: "screen-settings-theme",
		StyleFunc: func() core.Style {
			return core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Background: a.paletteOf(cx).Bg}
		},
		Children: []core.Node{
			TopHeader(cx, a, func() string { return "主题外观" },
				IconTap(cx, a, "settings-theme-back-btn", "返回", iconBack, func() {
					u.subPage.Set("settings")
				}),
				nil,
			),
			core.Scroll{
				EdgeEffects: core.EdgeEffects{Top: core.EdgeEffectSoft, Bottom: core.EdgeEffectSoft},
				Style:       core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
				Children: []core.Node{
					core.View{
						Style: core.Style{Width: core.Pct(100), AlignItems: core.AlignCenter, PaddingEnv: core.EnvEdges{Bottom: core.EnvSafeArea}},
						Children: []core.Node{
							core.View{
								Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(640), Padding: core.All(Tokens.PagePadX), Gap: Tokens.SectionGap},
								Children: []core.Node{
									// 1. Live Preview Card
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "实时预览 · " + a.themePreset.Get().Label() }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													StyleFunc: func() core.Style {
														return core.Style{Background: a.paletteOf(cx).Bg, Padding: core.All(core.Points(12)), Gap: Tokens.Space2}
													},
													Children: []core.Node{
														core.View{
															Style: core.Style{AlignItems: core.AlignEnd},
															Children: []core.Node{
																core.View{
																	StyleFunc: func() core.Style {
																		pal := a.paletteOf(cx)
																		return core.Style{
																			Background:   pal.SurfaceRaised,
																			BorderColor:  pal.Border,
																			BorderWidth:  1,
																			CornerRadius: 14,
																			Padding:      core.Symmetric(core.Points(7), core.Points(11)),
																		}
																	},
																	Children: []core.Node{
																		core.Text{
																			Content: "切换一下工作台主题和终端配色",
																			StyleFunc: func() core.Style {
																				return core.Style{Color: a.paletteOf(cx).Text, FontSize: Tokens.FontCaption, FontFamily: a.BodyFontFamily()}
																			},
																		},
																	},
																},
															},
														},
														ui.TextView{
															MarkdownFunc: func() string {
																return fmt.Sprintf("已应用 **%s**，正文、代码块与终端实时联动：\n```go\nfunc Apply(p Preset) { cx.SetTheme(%q) }\n```", a.themePreset.Get().Label(), string(a.themePreset.Get()))
															},
															Highlighter: highlighter,
															Options:     textview.Options{NoMath: true, NoHTML: true},
															StyleOf: func(part textview.Part) core.Style {
																pal := a.paletteOf(cx)
																switch part {
																case textview.PartRoot:
																	return core.Style{Color: pal.Text, FontSize: Tokens.FontCaption, LineHeight: 19, Gap: 6, FontFamily: a.BodyFontFamily()}
																case textview.PartCodeBlock:
																	return core.Style{Background: pal.Surface, BorderColor: pal.Border, BorderWidth: 1, CornerRadius: Tokens.RadiusKey}
																case textview.PartCodeBlockScroll:
																	return core.Style{Padding: core.Symmetric(8, 10)}
																case textview.PartCodeBlockCode:
																	return core.Style{FontSize: Tokens.FontMonoMeta, LineHeight: 17, FontFamily: a.MonoFontFamily()}
																}
																return core.Style{}
															},
														},
														// Mini VT Terminal Preview Strip
														core.View{
															StyleFunc: func() core.Style {
																vp := a.vtPaletteOf(cx)
																return core.Style{
																	Background:   vp.Bg,
																	BorderColor:  a.paletteOf(cx).Border,
																	BorderWidth:  1,
																	CornerRadius: Tokens.RadiusKey,
																	Padding:      core.Symmetric(core.Points(8), core.Points(10)),
																	Gap:          core.Points(3),
																}
															},
															Children: []core.Node{
																core.Text{
																	MaxLines: 1,
																	Content:  "dev@mac ~/herdr $ go test -race ./...",
																	StyleFunc: func() core.Style {
																		vp := a.vtPaletteOf(cx)
																		return core.Style{Color: vp.Fg, FontSize: core.Points(11), FontFamily: a.MonoFontFamily()}
																	},
																},
																core.Text{
																	MaxLines: 1,
																	Content:  "ok  github.com/herdr/herdr-nsgo  2.4s",
																	StyleFunc: func() core.Style {
																		vp := a.vtPaletteOf(cx)
																		return core.Style{Color: vp.Accent, FontSize: core.Points(11), FontFamily: a.MonoFontFamily()}
																	},
																},
															},
														},
													},
												},
												InsetDivider(cx, a, 0),
												core.View{
													Style: core.Style{Direction: core.Row, Gap: Tokens.Space2, Padding: core.Symmetric(core.Points(12), core.Points(14))},
													Children: []core.Node{
														FilterPill(cx, a, "theme-workshop-light", func() string { return "亮色" }, func() bool { return a.themeMode.Get() == ThemeLight }, nil, func() { a.SetThemeMode(ThemeLight) }),
														FilterPill(cx, a, "theme-workshop-dark", func() string { return "暗色" }, func() bool { return a.themeMode.Get() == ThemeDark }, nil, func() { a.SetThemeMode(ThemeDark) }),
														FilterPill(cx, a, "theme-workshop-system", func() string { return "跟随系统" }, func() bool { return a.themeMode.Get() == ThemeSystem }, nil, func() { a.SetThemeMode(ThemeSystem) }),
													},
												},
											}),
										},
									},

									// 2. 4 Workspace Theme Presets (2x2 Grid)
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "工作台主题（4 套亮/暗配对）" }, nil),
											core.View{
												Style: core.Style{Gap: Tokens.Space2},
												Children: []core.Node{
													core.View{
														Style:    core.Style{Direction: core.Row, Gap: Tokens.Space2},
														Children: []core.Node{themeSwatchCard(PresetStudio), themeSwatchCard(PresetPaper)},
													},
													core.View{
														Style:    core.Style{Direction: core.Row, Gap: Tokens.Space2},
														Children: []core.Node{themeSwatchCard(PresetNordic), themeSwatchCard(PresetEmerald)},
													},
												},
											},
										},
									},

									// 3. 4 Independent VT Color Schemes
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "终端画布配色" }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{Padding: core.All(core.Points(12)), Gap: Tokens.Space2},
													Children: []core.Node{
														core.View{
															Style:    core.Style{Direction: core.Row, Gap: Tokens.Space2},
															Children: []core.Node{vtSchemeTile(VTSchemeGraphite), vtSchemeTile(VTSchemeTokyo)},
														},
														core.View{
															Style:    core.Style{Direction: core.Row, Gap: Tokens.Space2},
															Children: []core.Node{vtSchemeTile(VTSchemeCatppuccin), vtSchemeTile(VTSchemeGruvbox)},
														},
													},
												},
												InsetDivider(cx, a, 14),
												core.View{
													Style: core.Style{
														Direction:      core.Row,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifySpaceBetween,
														Gap:            Tokens.Space3,
														MinHeight:      core.Pt(48),
														Padding:        core.Symmetric(core.Points(11), core.Points(14)),
													},
													Children: []core.Node{
														core.View{
															Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
															Children: []core.Node{
																core.Text{
																	Content: "Graphite 始终使用暗色画布",
																	StyleFunc: func() core.Style {
																		return core.Style{Color: a.paletteOf(cx).Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
																	},
																},
																core.Text{
																	Content: "亮色主题下仍保持深色终端背景",
																	StyleFunc: func() core.Style {
																		return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: Tokens.FontCaption}
																	},
																},
															},
														},
														ui.Switch{
															TestID:  "theme-vt-dark-switch",
															Label:   "终端画布始终暗色",
															Checked: func() bool { return a.vtThemeMode.Get() == VTAlwaysDark },
															OnChange: func(v bool) {
																if v {
																	a.SetVTThemeMode(VTAlwaysDark)
																} else {
																	a.SetVTThemeMode(VTFollowTheme)
																}
															},
														},
													},
												},
											}),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// settingsFontsScreen renders the dedicated Multi-Font & Typography Workshop page with live
// reading + code preview, independent Body Font & Monospace Font pickers, and dual size steppers.
func (u *uiController) settingsFontsScreen(cx *core.Scope) core.Node {
	a := u.app
	highlighter := newAppHighlighter(cx)

	fontOptionRow := func(testID, title, subtitle, sampleFont string, selected func() bool, onSelect func()) core.Node {
		return base.Button{
			TestID: testID,
			Label:  title,
			OnPress: func(core.PressEvent) {
				playSelectionHaptic(cx)
				onSelect()
			},
			Style: core.Style{
				Direction:      core.Row,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifySpaceBetween,
				Gap:            Tokens.Space3,
				MinHeight:      core.Pt(54),
				Padding:        core.Symmetric(core.Points(11), core.Points(14)),
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
					Children: []core.Node{
						core.Text{
							MaxLines: 1,
							Content:  title,
							StyleFunc: func() core.Style {
								return core.Style{
									Color:      a.paletteOf(cx).Text,
									FontSize:   Tokens.FontRowTitle,
									FontWeight: core.FontWeightMedium,
									FontFamily: sampleFont,
								}
							},
						},
						core.Text{
							MaxLines: 1,
							Content:  subtitle,
							StyleFunc: func() core.Style {
								return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: Tokens.FontCaption}
							},
						},
					},
				},
				core.Show{
					When: selected,
					Then: func(cx *core.Scope) core.Node {
						return core.View{
							StyleFunc: func() core.Style {
								return core.Style{
									Background:   a.paletteOf(cx).RunningSoft,
									CornerRadius: Tokens.RadiusPill,
									Padding:      core.Symmetric(core.Points(3), core.Points(8)),
								}
							},
							Children: []core.Node{
								core.Text{
									Content: "已选",
									StyleFunc: func() core.Style {
										return core.Style{Color: a.paletteOf(cx).Running, FontSize: core.Points(11), FontWeight: core.FontWeightSemibold}
									},
								},
							},
						}
					},
				},
			},
		}
	}

	var bodyRows []core.Node
	for i, bf := range AllBodyFontPresets() {
		if i > 0 {
			bodyRows = append(bodyRows, InsetDivider(cx, a, 14))
		}
		preset := bf
		bodyRows = append(bodyRows, fontOptionRow(
			"font-body-"+string(preset),
			preset.Label(),
			preset.Desc(),
			ResolveBodyFontFamily(preset, a.monoFont.GetUntracked()),
			func() bool { return a.bodyFont.Get() == preset },
			func() { a.SetBodyFont(preset) },
		))
	}

	var monoRows []core.Node
	for i, mf := range AllMonoFontPresets() {
		if i > 0 {
			monoRows = append(monoRows, InsetDivider(cx, a, 14))
		}
		preset := mf
		monoRows = append(monoRows, fontOptionRow(
			"font-mono-"+string(preset),
			preset.Label()+"  ·  0O 1lI != =>",
			preset.Desc(),
			ResolveMonoFontFamily(preset),
			func() bool { return a.monoFont.Get() == preset },
			func() { a.SetMonoFont(preset) },
		))
	}

	return core.View{
		TestID: "screen-settings-fonts",
		StyleFunc: func() core.Style {
			return core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Background: a.paletteOf(cx).Bg}
		},
		Children: []core.Node{
			TopHeader(cx, a, func() string { return "字体与字号" },
				IconTap(cx, a, "settings-fonts-back-btn", "返回", iconBack, func() {
					u.subPage.Set("settings")
				}),
				nil,
			),
			core.Scroll{
				EdgeEffects: core.EdgeEffects{Top: core.EdgeEffectSoft, Bottom: core.EdgeEffectSoft},
				Style:       core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
				Children: []core.Node{
					core.View{
						Style: core.Style{Width: core.Pct(100), AlignItems: core.AlignCenter, PaddingEnv: core.EnvEdges{Bottom: core.EnvSafeArea}},
						Children: []core.Node{
							core.View{
								Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(640), Padding: core.All(Tokens.PagePadX), Gap: Tokens.SectionGap},
								Children: []core.Node{
									// 1. Live Typography Preview
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string {
												return fmt.Sprintf("实时预览 · %s + %s", a.bodyFont.Get().ShortLabel(), a.monoFont.Get().ShortLabel())
											}, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space3},
													Children: []core.Node{
														core.Text{
															Content: "重构完成：收件箱已新增「汇报完成」提醒，聊天正文、代码块与终端均按所选字体实时渲染。",
															StyleFunc: func() core.Style {
																fs := a.BodyFontSize()
																return core.Style{
																	Color:      a.paletteOf(cx).Text,
																	FontSize:   fs,
																	LineHeight: fs + 8,
																	FontFamily: a.BodyFontFamily(),
																}
															},
														},
														ui.TextView{
															Markdown: "```go\n// 0123456789 O0 Il1 != => ->\nfunc Stream(ch chan Event) error {\n\treturn nil\n}\n```",
															Highlighter: highlighter,
															Options:     textview.Options{NoMath: true, NoHTML: true},
															StyleOf: func(part textview.Part) core.Style {
																pal := a.paletteOf(cx)
																monoFS := core.Points(a.vtFontSize.Get())
																switch part {
																case textview.PartRoot:
																	return core.Style{Gap: 0}
																case textview.PartCodeBlock:
																	return core.Style{Background: pal.Bg, BorderColor: pal.Border, BorderWidth: 1, CornerRadius: Tokens.RadiusCtrl}
																case textview.PartCodeBlockScroll:
																	return core.Style{Padding: core.Symmetric(10, 12)}
																case textview.PartCodeBlockCode:
																	return core.Style{FontSize: monoFS, LineHeight: monoFS + 6, FontFamily: a.MonoFontFamily()}
																}
																return core.Style{}
															},
														},
													},
												},
											}),
										},
									},

									// 2. Reading / Chat Body Font
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "阅读与聊天字体（UI 正文）" }, nil),
											GroupCard(cx, a, "", bodyRows),
										},
									},

									// 3. Code & Terminal Monospace Font
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "代码与终端等宽字体" }, nil),
											GroupCard(cx, a, "", monoRows),
										},
									},

									// 4. Dual Font Size Steppers
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "字号调节" }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{
														Direction:      core.Row,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifySpaceBetween,
														MinHeight:      core.Pt(50),
														Padding:        core.Symmetric(core.Points(10), core.Points(14)),
													},
													Children: []core.Node{
														core.Text{
															ContentFunc: func() string { return fmt.Sprintf("聊天正文字号 · %.0f px", a.bodyFontSize.Get()) },
															StyleFunc: func() core.Style {
																return core.Style{Color: a.paletteOf(cx).Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
															},
														},
														core.View{
															Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
															Children: []core.Node{
																ActionButton(cx, a, "body-font-dec-btn", "A-", ActionSecondary, func() {
																	a.SetBodyFontSize(a.bodyFontSize.Get() - 1)
																}),
																ActionButton(cx, a, "body-font-inc-btn", "A+", ActionSecondary, func() {
																	a.SetBodyFontSize(a.bodyFontSize.Get() + 1)
																}),
															},
														},
													},
												},
												InsetDivider(cx, a, 14),
												core.View{
													Style: core.Style{
														Direction:      core.Row,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifySpaceBetween,
														MinHeight:      core.Pt(50),
														Padding:        core.Symmetric(core.Points(10), core.Points(14)),
													},
													Children: []core.Node{
														core.Text{
															ContentFunc: func() string { return fmt.Sprintf("代码与终端字号 · %.0f px", a.vtFontSize.Get()) },
															StyleFunc: func() core.Style {
																return core.Style{Color: a.paletteOf(cx).Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
															},
														},
														core.View{
															Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
															Children: []core.Node{
																ActionButton(cx, a, "fonts-vt-dec-btn", "A-", ActionSecondary, func() {
																	a.SetVTFontSize(a.vtFontSize.Get() - 1)
																}),
																ActionButton(cx, a, "fonts-vt-inc-btn", "A+", ActionSecondary, func() {
																	a.SetVTFontSize(a.vtFontSize.Get() + 1)
																}),
															},
														},
													},
												},
											}),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
