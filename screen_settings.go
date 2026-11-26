package main

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/updater"
)

// settingsScreen renders the Quiet Studio Settings page (Appearance, Terminal, Security & TOFU,
// Local ed25519 Key, Notifications, Snippets, and Runtime Design Tokens Gallery).
func (u *uiController) settingsScreen(cx *core.Scope) core.Node {
	a := u.app
	newSnipLabel := core.NewSignal(cx, "")
	newSnipText := core.NewSignal(cx, "")

	toggleRow := func(testID, title, subtitle string, checked func() bool, onChange func(bool)) core.Node {
		return core.View{
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
							Content: title,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
							},
						},
						core.Text{
							Content: subtitle,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontCaption}
							},
						},
					},
				},
				ui.Switch{
					TestID:   testID,
					Checked:  checked,
					OnChange: onChange,
					Label:    title,
				},
			},
		}
	}

	return core.View{
		TestID: "screen-settings",
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
			TopHeader(cx, a, func() string { return "设置" },
				IconTap(cx, a, "settings-back-btn", "返回", iconBack, func() {
					u.subPage.Set("")
				}),
				nil,
			),
			core.Scroll{
				EdgeEffects: core.EdgeEffects{Top: core.EdgeEffectSoft, Bottom: core.EdgeEffectSoft},
				Style:       core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Width:      core.Pct(100),
							AlignItems: core.AlignCenter,
							PaddingEnv: core.EnvEdges{Bottom: core.EnvSafeArea},
						},
						Children: []core.Node{
							core.View{
								Style: core.Style{
									Width:    core.Pct(100),
									MaxWidth: core.Pt(640),
									Padding:  core.All(Tokens.PagePadX),
									Gap:      Tokens.SectionGap,
								},
								Children: []core.Node{
									// 1. Appearance & Personalization
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "外观与个性化" }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{
														Direction: core.Row,
														Gap:       Tokens.Space2,
														Padding:   core.Symmetric(core.Points(12), core.Points(14)),
													},
													Children: []core.Node{
														FilterPill(cx, a, "theme-light", func() string { return "亮色" }, func() bool {
															return a.themeMode.Get() == ThemeLight
														}, nil, func() { a.SetThemeMode(ThemeLight) }),
														FilterPill(cx, a, "theme-dark", func() string { return "暗色" }, func() bool {
															return a.themeMode.Get() == ThemeDark
														}, nil, func() { a.SetThemeMode(ThemeDark) }),
														FilterPill(cx, a, "theme-system", func() string { return "跟随系统" }, func() bool {
															return a.themeMode.Get() == ThemeSystem
														}, nil, func() { a.SetThemeMode(ThemeSystem) }),
													},
												},
												InsetDivider(cx, a, 14),
												SheetListRowFunc(cx, a, "settings-open-theme",
													func() string { return "主题外观" },
													func() string {
														return fmt.Sprintf("%s · %s", a.themePreset.Get().Label(), a.vtColorScheme.Get().Label())
													},
													iconPalette, false, true, func() {
														u.subPage.Set("settings-theme")
													},
												),
												InsetDivider(cx, a, 46),
												SheetListRowFunc(cx, a, "settings-open-fonts",
													func() string { return "字体与字号" },
													func() string {
														return fmt.Sprintf("%s · %s · %.0fpx", a.bodyFont.Get().ShortLabel(), a.monoFont.Get().ShortLabel(), a.vtFontSize.Get())
													},
													iconType, false, true, func() {
														u.subPage.Set("settings-fonts")
													},
												),
												InsetDivider(cx, a, 14),
												toggleRow("settings-vt-dark", "终端画布始终暗色", "亮色主题下仍保持深色终端画布",
													func() bool { return a.vtThemeMode.Get() == VTAlwaysDark },
													func(v bool) {
														if v {
															a.SetVTThemeMode(VTAlwaysDark)
														} else {
															a.SetVTThemeMode(VTFollowTheme)
														}
													},
												),
											}),
										},
									},

									// 2. Terminal behavior
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "终端与交互" }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{
														Direction:      core.Row,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifySpaceBetween,
														MinHeight:      core.Pt(48),
														Padding:        core.Symmetric(core.Points(10), core.Points(14)),
													},
													Children: []core.Node{
														core.Text{
															ContentFunc: func() string { return fmt.Sprintf("终端字号 · %.0f px", a.vtFontSize.Get()) },
															StyleFunc: func() core.Style {
																pal := a.paletteOf(cx)
																return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
															},
														},
														core.View{
															Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
															Children: []core.Node{
																ActionButton(cx, a, "font-dec-btn", "A-", ActionSecondary, func() {
																	a.SetVTFontSize(a.vtFontSize.Get() - 1)
																}),
																ActionButton(cx, a, "font-inc-btn", "A+", ActionSecondary, func() {
																	a.SetVTFontSize(a.vtFontSize.Get() + 1)
																}),
															},
														},
													},
												},
												InsetDivider(cx, a, 14),
												core.View{
													Style: core.Style{
														Padding: core.Symmetric(core.Points(12), core.Points(14)),
														Gap:     Tokens.Space2,
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
																	Content: "发送消息默认行为",
																	StyleFunc: func() core.Style {
																		pal := a.paletteOf(cx)
																		return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
																	},
																},
																core.View{
																	Style: core.Style{Direction: core.Row, Gap: Tokens.Space1},
																	Children: []core.Node{
																		FilterPill(cx, a, "settings-send-mode-queue", func() string { return "等待" }, func() bool {
																			return a.DefaultSendMode() == chatQueue
																		}, nil, func() { a.SetDefaultSendMode(chatQueue) }),
																		FilterPill(cx, a, "settings-send-mode-steer", func() string { return "插队" }, func() bool {
																			return a.DefaultSendMode() == chatSteer
																		}, nil, func() { a.SetDefaultSendMode(chatSteer) }),
																	},
																},
															},
														},
														core.Text{
															Content: "Agent 忙碌时发出消息的默认方式；等待中的消息可随时点按插队",
															StyleFunc: func() core.Style {
																pal := a.paletteOf(cx)
																return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontCaption}
															},
														},
													},
												},
												InsetDivider(cx, a, 14),
												toggleRow("settings-observe-default", "默认观察模式打开终端", "默认保持远端终端原有尺寸",
													a.observeDefault.Get, a.SetObserveDefault,
												),
												InsetDivider(cx, a, 14),
												toggleRow("settings-frame-cache", "内存 LRU 帧缓存（最多 24 会话）", "切回最近查看过的会话时 0ms 瞬开首帧",
													a.frameCacheOn.Get, a.SetFrameCacheOn,
												),
												InsetDivider(cx, a, 14),
												toggleRow("settings-notify", "Agent 通知提醒", "需要批准权限、出错或任务完成时弹出系统通知",
													a.notifyAlerts.Get, func(v bool) {
														a.SetNotifyAlerts(v)
														if v {
															a.RequestNotificationPermissionIfNeeded(true)
														}
													},
												),
												core.Show{
													When: func() bool {
														if !a.notifyAlerts.Get() {
															return false
														}
														p := a.notifPerm.Get()
														return p == core.NotificationsNotAsked || p == core.NotificationsDenied
													},
													Then: func(cx *core.Scope) core.Node {
														return core.View{
															Style: core.Style{
																Padding: core.Edges{Left: 14, Right: 14, Bottom: 12},
															},
															Children: []core.Node{
																core.View{
																	TestID: "settings-notify-perm-btn",
																	Role:   core.RoleButton,
																	OnPress: func(core.PressEvent) {
																		playSelectionHaptic(cx)
																		a.RequestNotificationPermissionIfNeeded(true)
																	},
																	StyleFunc: func() core.Style {
																		pal := a.paletteOf(cx)
																		bg := pal.WarnSoft
																		if a.notifPerm.Get() == core.NotificationsNotAsked {
																			bg = pal.AccentSoft
																		}
																		return core.Style{
																			Direction:      core.Row,
																			AlignItems:     core.AlignCenter,
																			JustifyContent: core.JustifySpaceBetween,
																			Gap:            Tokens.Space2,
																			Background:     bg,
																			CornerRadius:   Tokens.RadiusCtrl,
																			Padding:        core.Symmetric(Tokens.Space2, Tokens.Space3),
																		}
																	},
																	Children: []core.Node{
																		core.Text{
																			ContentFunc: func() string {
																				if a.notifPerm.Get() == core.NotificationsDenied {
																					return "系统通知权限已关闭 · 点按前往系统设置开启"
																				}
																				return "尚未授予系统通知权限 · 点按立即授权"
																			},
																			StyleFunc: func() core.Style {
																				pal := a.paletteOf(cx)
																				fg := pal.Warn
																				if a.notifPerm.Get() == core.NotificationsNotAsked {
																					fg = pal.Text
																				}
																				return core.Style{
																					FlexShrink: 1,
																					Color:      fg,
																					FontSize:   Tokens.FontCaption,
																					FontWeight: core.FontWeightMedium,
																				}
																			},
																		},
																		core.Icon{
																			SVG: iconChevronR,
																			StyleFunc: func() core.Style {
																				pal := a.paletteOf(cx)
																				fg := pal.Warn
																				if a.notifPerm.Get() == core.NotificationsNotAsked {
																					fg = pal.TextMuted
																				}
																				return core.Style{Width: core.Pt(14), Height: core.Pt(14), Color: fg, FlexShrink: 0}
																			},
																		},
																	},
																},
															},
														}
													},
												},
											}),
										},
									},

									// 3. SSH Key & Security (TOFU)
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "本机 SSH 密钥与 TOFU 指纹" }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space3},
													Children: []core.Node{
														core.View{
															StyleFunc: func() core.Style {
																pal := a.paletteOf(cx)
																return core.Style{
																	Background:   pal.Bg,
																	CornerRadius: Tokens.RadiusCtrl,
																	Padding:      core.Symmetric(Tokens.Space2, Tokens.Space3),
																}
															},
															Children: []core.Node{
																core.Text{
																	ContentFunc: func() string {
																		pub := a.localPubKey.Get()
																		if len(pub) > 56 {
																			return pub[:56] + "…"
																		}
																		return pub
																	},
																	StyleFunc: func() core.Style {
																		pal := a.paletteOf(cx)
																		return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
																	},
																},
															},
														},
														core.View{
															Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
															Children: []core.Node{
																core.View{
																	Style: core.Style{FlexGrow: 1},
																	Children: []core.Node{
																		ActionButton(cx, a, "settings-copy-pub-btn", "复制 authorized_keys", ActionSecondary, func() {
																			if cx != nil {
																				cx.SetClipboard(AuthorizedKeysCommand(a.localPubKey.Get()))
																			}
																			a.notifyToast("已复制公钥授权命令")
																		}),
																	},
																},
																ActionButton(cx, a, "settings-rotate-key-btn", "重生成密钥", ActionDanger, func() {
																	if err := a.RegenerateLocalKey(); err != nil {
																		a.notifyToast("重生成失败：" + err.Error())
																	} else {
																		a.notifyToast("已生成新 ed25519 密钥对，请重新授权到远端")
																	}
																}),
															},
														},
													},
												},
												core.Show{
													When: func() bool { return len(a.tofuRecords.Get()) > 0 },
													Then: func(cx *core.Scope) core.Node {
														return core.View{
															Children: []core.Node{
																InsetDivider(cx, a, 14),
																core.View{
																	Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space2},
																	Children: []core.Node{
																		core.Text{
																			Content: "已信任主机指纹 (TOFU)",
																			StyleFunc: func() core.Style {
																				pal := a.paletteOf(cx)
																				return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontSection, FontWeight: core.FontWeightMedium}
																			},
																		},
																		core.Each(
																			func() []TOFURecord { return a.tofuRecords.Get() },
																			func(r TOFURecord) string { return r.HostID },
																			func(cx *core.Scope, item func() TOFURecord, _ func() int) core.Node {
																				r0 := item()
																				return core.View{
																					StyleFunc: func() core.Style {
																						pal := a.paletteOf(cx)
																						return core.Style{
																							Direction:      core.Row,
																							AlignItems:     core.AlignCenter,
																							JustifyContent: core.JustifySpaceBetween,
																							Gap:            Tokens.Space2,
																							Padding:        core.Symmetric(Tokens.Space2, Tokens.Space3),
																							Background:     pal.Bg,
																							CornerRadius:   Tokens.RadiusCtrl,
																						}
																					},
																					Children: []core.Node{
																						core.View{
																							Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
																							Children: []core.Node{
																								core.Text{
																									ContentFunc: func() string { return item().HostID },
																									StyleFunc: func() core.Style {
																										pal := a.paletteOf(cx)
																										return core.Style{Color: pal.Text, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightMedium}
																									},
																								},
																								core.Text{
																									MaxLines:    1,
																									ContentFunc: func() string { return item().Fingerprint },
																									StyleFunc: func() core.Style {
																										pal := a.paletteOf(cx)
																										return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
																									},
																								},
																							},
																						},
																						ActionButton(cx, a, "tofu-revoke-"+r0.HostID, "撤销", ActionDanger, func() {
																							a.tofu.Revoke(item().HostID)
																							a.notifyToast("已撤销主机指纹：" + item().HostID)
																						}),
																					},
																				}
																			},
																		),
																	},
																},
															},
														}
													},
												},
											}),
										},
									},

									// 4. Snippets quick add
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "常用短语 (Snippets)" }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space2},
													Children: []core.Node{
														ui.Input{
															TestID:      "snippet-new-label",
															Placeholder: "短语名称（例如 跑测试）",
															Value:       newSnipLabel.Get,
															OnChange:    newSnipLabel.Set,
															StyleFunc: func() core.Style {
																pal := a.paletteOf(cx)
																return core.Style{Width: core.Pct(100), MinHeight: core.Pt(Tokens.SizeTouch), Background: pal.SurfaceRaised, Color: pal.Text, CornerRadius: Tokens.RadiusCtrl, BorderColor: pal.Border, BorderWidth: 1}
															},
														},
														ui.Input{
															TestID:      "snippet-new-text",
															Placeholder: "命令、提示词或按键组合（如 C-b, S-t）",
															Value:       newSnipText.Get,
															OnChange:    newSnipText.Set,
															StyleFunc: func() core.Style {
																pal := a.paletteOf(cx)
																return core.Style{Width: core.Pct(100), MinHeight: core.Pt(Tokens.SizeTouch), Background: pal.SurfaceRaised, Color: pal.Text, CornerRadius: Tokens.RadiusCtrl, BorderColor: pal.Border, BorderWidth: 1}
															},
														},
														core.View{
															Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
															Children: []core.Node{
																core.View{
																	Style: core.Style{FlexGrow: 1},
																	Children: []core.Node{
																		ActionButton(cx, a, "binding-add-btn", "存为快捷键", ActionSecondary, func() {
																			binding := newSnipText.Get()
																			if _, err := terminalBinding(binding); err != nil {
																				a.notifyToast(err.Error())
																				return
																			}
																			a.SaveSnippet(Snippet{Label: strings.TrimSpace(newSnipLabel.Get()), Text: binding, Binding: true})
																			newSnipLabel.Set("")
																			newSnipText.Set("")
																			a.notifyToast("已保存终端快捷键")
																		}),
																	},
																},
																core.View{
																	Style: core.Style{FlexGrow: 1},
																	Children: []core.Node{
																		ActionButton(cx, a, "snippet-add-btn", "添加短语", ActionPrimary, func() {
																			if strings.TrimSpace(newSnipText.Get()) == "" {
																				return
																			}
																			a.SaveSnippet(Snippet{Label: strings.TrimSpace(newSnipLabel.Get()), Text: strings.TrimSpace(newSnipText.Get())})
																			newSnipLabel.Set("")
																			newSnipText.Set("")
																			a.notifyToast("已添加常用短语")
																		}),
																	},
																},
															},
														},
													},
												},
												core.Each(
													func() []Snippet { return a.snippets.Get() },
													func(s Snippet) string { return s.ID },
													func(cx *core.Scope, item func() Snippet, _ func() int) core.Node {
														sn0 := item()
														return core.View{
															Children: []core.Node{
																InsetDivider(cx, a, 14),
																core.View{
																	Style: core.Style{
																		Direction:      core.Row,
																		AlignItems:     core.AlignCenter,
																		JustifyContent: core.JustifySpaceBetween,
																		Gap:            Tokens.Space2,
																		Padding:        core.Symmetric(core.Points(10), core.Points(14)),
																	},
																	Children: []core.Node{
																		core.View{
																			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
																			Children: []core.Node{
																				core.Text{
																					ContentFunc: func() string { return item().Label },
																					StyleFunc: func() core.Style {
																						pal := a.paletteOf(cx)
																						return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
																					},
																				},
																				core.Text{
																					MaxLines:    1,
																					ContentFunc: func() string { return item().Text },
																					StyleFunc: func() core.Style {
																						pal := a.paletteOf(cx)
																						return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
																					},
																				},
																			},
																		},
																		ActionButton(cx, a, "snippet-del-"+sn0.ID, "删除", ActionDanger, func() {
																			a.DeleteSnippet(item().ID)
																		}),
																	},
																},
															},
														}
													},
												),
											}),
										},
									},

									// 5. App Updates, About & Runtime Design Tokens Gallery
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "应用更新与关于" }, nil),
											GroupCard(cx, a, "", []core.Node{
												core.View{
													Style: core.Style{
														Direction:      core.Row,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifySpaceBetween,
														Gap:            Tokens.Space3,
														Padding:        core.Symmetric(core.Points(12), core.Points(14)),
													},
													Children: []core.Node{
														core.View{
															Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
															Children: []core.Node{
																core.Text{
																	ContentFunc: func() string {
																		st := updater.Status(cx)
																		switch st.Phase {
																		case updater.PhaseDownloading:
																			if frac := st.Fraction(); frac > 0 {
																				return fmt.Sprintf("正在下载更新 · %.0f%%", frac*100)
																			}
																			return "正在下载更新…"
																		case updater.PhaseInstalling:
																			return "正在拉起系统安装器…"
																		}
																		if st.Available != "" {
																			return "发现新版本 v" + strings.TrimPrefix(st.Available, "v")
																		}
																		return "检查应用更新"
																	},
																	StyleFunc: func() core.Style {
																		pal := a.paletteOf(cx)
																		return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
																	},
																},
																core.Text{
																	ContentFunc: func() string {
																		st := updater.Status(cx)
																		if st.Available != "" {
																			return "已通过 Ed25519 签名验证，点击右侧立即升级"
																		}
																		return "自动从 GitHub Releases 检查并安装签名版本"
																	},
																	StyleFunc: func() core.Style {
																		pal := a.paletteOf(cx)
																		return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontCaption}
																	},
																},
															},
														},
														ActionButtonFunc(cx, a, "settings-check-update-btn", func() string {
															st := updater.Status(cx)
															if st.Phase == updater.PhaseDownloading {
																return "下载中…"
															}
															if st.Phase == updater.PhaseInstalling {
																return "安装中…"
															}
															if st.Available != "" {
																return "立即更新"
															}
															return "检查更新"
														}, ActionSecondary, func() {
															a.CheckForAppUpdates(cx, false)
														}),
													},
												},
												InsetDivider(cx, a, 14),
												toggleRow("settings-auto-update", "自动检查新版本", "后台静默检查 GitHub Releases 新版本并提醒更新",
													func() bool { return updater.Status(cx).AutomaticChecks },
													func(v bool) {
														if cx != nil {
															updater.SetAutomaticChecks(cx, v)
														}
													},
												),
												InsetDivider(cx, a, 14),
												core.View{
													Style: core.Style{Padding: core.Symmetric(core.Points(12), core.Points(14))},
													Children: []core.Node{
														core.Text{
															Content: "herdr-nsgo v" + strings.TrimPrefix(appBuildVersion, "v") + " · 纯 Go + NSGo Quiet Studio 原生客户端",
															StyleFunc: func() core.Style {
																pal := a.paletteOf(cx)
																return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption}
															},
														},
													},
												},
												InsetDivider(cx, a, 14),
												SheetListRow(cx, a, "settings-tokens-btn", "设计系统 Tokens 图鉴", "Quiet Studio", iconPanels, false, true, func() {
													u.subPage.Set("tokens")
												}),
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

// tokensGalleryScreen renders the runtime Quiet Studio Design Tokens & Live Component Slice Gallery.
func (u *uiController) tokensGalleryScreen(cx *core.Scope) core.Node {
	a := u.app
	catalog := TokenCatalog()

	dotSample := func(label string, colorFn func(Palette) core.Color) core.Node {
		return core.View{
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6)},
			Children: []core.Node{
				core.View{
					StyleFunc: func() core.Style {
						return core.Style{
							Width:        core.Pt(8),
							Height:       core.Pt(8),
							CornerRadius: 4,
							Background:   colorFn(a.paletteOf(cx)),
						}
					},
				},
				core.Text{
					Content: label,
					StyleFunc: func() core.Style {
						return core.Style{Color: a.paletteOf(cx).TextSecondary, FontSize: Tokens.FontMonoMeta}
					},
				},
			},
		}
	}

	previewSlice := core.View{
		TestID: "tokens-gallery-preview",
		Style:  core.Style{Gap: Tokens.Space2},
		Children: []core.Node{
			SectionHeader(cx, a, func() string { return "组件实时切片" }, nil),
			GroupCard(cx, a, "", []core.Node{
				core.View{
					Style: core.Style{
						Direction: core.Row,
						Gap:       Tokens.Space2,
						Padding:   core.Symmetric(core.Points(12), core.Points(14)),
					},
					Children: []core.Node{
						ActionButton(cx, a, "gallery-btn-primary", "主操作", ActionPrimary, nil),
						ActionButton(cx, a, "gallery-btn-secondary", "次操作", ActionSecondary, nil),
						ActionButton(cx, a, "gallery-btn-danger", "危险操作", ActionDanger, nil),
					},
				},
				InsetDivider(cx, a, 14),
				core.View{
					Style: core.Style{
						Direction: core.Row,
						Gap:       Tokens.Space4,
						Padding:   core.Symmetric(core.Points(12), core.Points(14)),
					},
					Children: []core.Node{
						dotSample("运行中", func(p Palette) core.Color { return p.Running }),
						dotSample("需要你", func(p Palette) core.Color { return p.Warn }),
						dotSample("异常", func(p Palette) core.Color { return p.Danger }),
						dotSample("空闲", func(p Palette) core.Color { return p.Fill }),
					},
				},
				InsetDivider(cx, a, 14),
				SheetListRow(cx, a, "gallery-sample-row", "分组列表行示例", "48px", iconTerminal, false, true, nil),
			}),
		},
	}

	tokenRows := make([]core.Node, 0, len(catalog)*2)
	for i, entry := range catalog {
		if i > 0 {
			tokenRows = append(tokenRows, InsetDivider(cx, a, 14))
		}
		e := entry
		tokenRows = append(tokenRows, core.View{
			Style: core.Style{
				Direction:  core.Row,
				AlignItems: core.AlignCenter,
				Gap:        Tokens.Space3,
				Padding:    core.Symmetric(core.Points(11), core.Points(14)),
			},
			Children: func() []core.Node {
				var kids []core.Node
				if e.Swatch != nil {
					kids = append(kids, core.View{
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							vtPal := a.vtPaletteOf(cx)
							return core.Style{
								FlexShrink:   0,
								Width:        core.Pt(24),
								Height:       core.Pt(24),
								CornerRadius: 12,
								Background:   e.Swatch(pal, vtPal),
								BorderWidth:  1,
								BorderColor:  pal.Border,
							}
						},
					})
				}
				kids = append(kids, core.View{
					Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
					Children: []core.Node{
						core.View{
							Style: core.Style{Direction: core.Row, JustifyContent: core.JustifySpaceBetween, Gap: Tokens.Space2},
							Children: []core.Node{
								core.Text{
									Content: e.Name,
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{Color: pal.Text, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold, FontFamily: core.FontMonospace}
									},
								},
								core.Text{
									Content: e.Group,
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta}
									},
								},
							},
						},
						core.Text{
							Content: fmt.Sprintf("Light: %s  ·  Dark: %s", e.Light, e.Dark),
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
							},
						},
						core.Text{
							Content: e.Use,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontCaption}
							},
						},
					},
				})
				return kids
			}(),
		})
	}

	return core.View{
		TestID: "screen-tokens",
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
			TopHeader(cx, a, func() string { return "设计系统 Tokens" },
				IconTap(cx, a, "tokens-back-btn", "返回", iconBack, func() {
					u.subPage.Set("settings")
				}),
				nil,
			),
			core.Scroll{
				EdgeEffects: core.EdgeEffects{Top: core.EdgeEffectSoft, Bottom: core.EdgeEffectSoft},
				Style:       core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Width:      core.Pct(100),
							AlignItems: core.AlignCenter,
							PaddingEnv: core.EnvEdges{Bottom: core.EnvSafeArea},
						},
						Children: []core.Node{
							core.View{
								Style: core.Style{
									Width:    core.Pct(100),
									MaxWidth: core.Pt(640),
									Padding:  core.All(Tokens.PagePadX),
									Gap:      Tokens.SectionGap,
								},
								Children: []core.Node{
									previewSlice,
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return fmt.Sprintf("全部设计 Tokens · %d", len(catalog)) }, nil),
											GroupCard(cx, a, "", tokenRows),
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
