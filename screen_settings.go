package main

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// settingsScreen renders the full v1.0 Settings page (Appearance, Terminal, Security & TOFU,
// Local ed25519 Key, Notifications, Snippets, and Runtime Design Tokens Gallery).
func (u *uiController) settingsScreen(cx *core.Scope) core.Node {
	a := u.app
	newSnipLabel := core.NewSignal(cx, "")
	newSnipText := core.NewSignal(cx, "")

	card := func(children ...core.Node) core.Node {
		return core.View{
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Background:   pal.Surface,
					CornerRadius: Tokens.RadiusCard,
					BorderWidth:  1,
					BorderColor:  pal.Border,
					Padding:      core.All(Tokens.Space3),
					Gap:          Tokens.Space3,
				}
			},
			Children: children,
		}
	}

	toggleRow := func(testID, title, subtitle string, checked func() bool, onChange func(bool)) core.Node {
		return core.View{
			Style: core.Style{
				Direction:      core.Row,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifySpaceBetween,
				Gap:            Tokens.Space3,
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{FlexGrow: 1, Gap: core.Points(2)},
					Children: []core.Node{
						core.Text{
							Content: title,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightSemibold}
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
				Background: pal.Bg,
			}
		},
		Children: []core.Node{
			TopHeader(cx, a, func() string { return "设置" },
				ActionButton(cx, a, "settings-back-btn", "返回", ActionGhost, func() {
					u.subPage.Set("")
				}),
				nil,
			),
			ToastBanner(cx, a),
			core.Scroll{
				Style: core.Style{FlexGrow: 1},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Padding: core.All(Tokens.PagePadX),
							Gap:     Tokens.Space4,
						},
						Children: []core.Node{
							// 1. Appearance
							SectionHeader(cx, a, func() string { return "外观与主题" }, nil),
							card(
								core.View{
									Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
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
								toggleRow("settings-vt-dark", "终端画布始终暗色", "亮色 App 主题下仍使用 #070A0D 暗色 VT 画布",
									func() bool { return a.vtThemeMode.Get() == VTAlwaysDark },
									func(v bool) {
										if v {
											a.SetVTThemeMode(VTAlwaysDark)
										} else {
											a.SetVTThemeMode(VTFollowTheme)
										}
									},
								),
							),

							// 2. Terminal behavior
							SectionHeader(cx, a, func() string { return "终端与交互" }, nil),
							card(
								core.View{
									Style: core.Style{
										Direction:      core.Row,
										AlignItems:     core.AlignCenter,
										JustifyContent: core.JustifySpaceBetween,
									},
									Children: []core.Node{
										core.Text{
											ContentFunc: func() string { return fmt.Sprintf("终端字号：%.0f px", a.vtFontSize.Get()) },
											StyleFunc: func() core.Style {
												pal := a.paletteOf(cx)
												return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightSemibold}
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
								toggleRow("settings-observe-default", "默认观察模式打开终端", "查看会话时不改变桌面端 PTY 尺寸，发送输入或点「适配列宽」才 RESIZE",
									a.observeDefault.Get, a.SetObserveDefault,
								),
								toggleRow("settings-frame-cache", "内存 LRU 帧缓存（最多 24 会话）", "切回最近查看过的会话时 0ms 瞬开首帧",
									a.frameCacheOn.Get, a.SetFrameCacheOn,
								),
								toggleRow("settings-notify", "Agent 阻塞/权限系统通知", "当任意实例出现「等你审批」或「出错」时弹出本地通知",
									a.notifyAlerts.Get, func(v bool) {
										a.SetNotifyAlerts(v)
										if v && cx != nil {
											cx.RequestNotificationPermission(nil)
										}
									},
								),
							),

							// 3. SSH Key & Security (TOFU)
							SectionHeader(cx, a, func() string { return "本机 SSH 密钥与 TOFU 指纹" }, nil),
							card(
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
										return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
									},
								},
								core.View{
									Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
									Children: []core.Node{
										ActionButton(cx, a, "settings-copy-pub-btn", "复制 authorized_keys 命令", ActionSecondary, func() {
											if cx != nil {
												cx.SetClipboard(AuthorizedKeysCommand(a.localPubKey.Get()))
											}
											a.notifyToast("已复制公钥授权命令")
										}),
										ActionButton(cx, a, "settings-rotate-key-btn", "重生成密钥", ActionDanger, func() {
											if err := a.RegenerateLocalKey(); err != nil {
												a.notifyToast("重生成失败：" + err.Error())
											} else {
												a.notifyToast("已生成新 ed25519 密钥对，请重新授权到远端")
											}
										}),
									},
								},
								core.Show{
									When: func() bool { return len(a.tofuRecords.Get()) > 0 },
									Then: func(cx *core.Scope) core.Node {
										return core.View{
											Style: core.Style{Gap: Tokens.Space2},
											Children: []core.Node{
												core.Text{
													Content: "已信任主机指纹 (TOFU)",
													StyleFunc: func() core.Style {
														pal := a.paletteOf(cx)
														return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold}
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
																	Padding:        core.All(Tokens.Space2),
																	Background:     pal.SurfaceRaised,
																	CornerRadius:   Tokens.RadiusCtrl,
																}
															},
															Children: []core.Node{
																core.View{
																	Style: core.Style{FlexGrow: 1, Gap: core.Points(2)},
																	Children: []core.Node{
																		core.Text{
																			ContentFunc: func() string { return item().HostID },
																			StyleFunc: func() core.Style {
																				pal := a.paletteOf(cx)
																				return core.Style{Color: pal.Text, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold}
																			},
																		},
																		core.Text{
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
										}
									},
								},
							),

							// 4. Snippets quick add
							SectionHeader(cx, a, func() string { return "常用短语 (Snippets)" }, nil),
							card(
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
									Placeholder: "发送内容（例如 go test -race ./...）",
									Value:       newSnipText.Get,
									OnChange:    newSnipText.Set,
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{Width: core.Pct(100), MinHeight: core.Pt(Tokens.SizeTouch), Background: pal.SurfaceRaised, Color: pal.Text, CornerRadius: Tokens.RadiusCtrl, BorderColor: pal.Border, BorderWidth: 1}
									},
								},
								ActionButton(cx, a, "snippet-add-btn", "添加短语", ActionSecondary, func() {
									if strings.TrimSpace(newSnipText.Get()) == "" {
										return
									}
									a.SaveSnippet(Snippet{Label: strings.TrimSpace(newSnipLabel.Get()), Text: strings.TrimSpace(newSnipText.Get())})
									newSnipLabel.Set("")
									newSnipText.Set("")
									a.notifyToast("已添加常用短语")
								}),
							),

							// 5. About & Runtime Design Tokens Gallery
							SectionHeader(cx, a, func() string { return "关于与设计系统" }, nil),
							card(
								core.Text{
									Content: "herdr-nsgo v1.0 · 纯 Go + NSGo 原生多实例 herdr-core 客户端",
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption}
									},
								},
								ActionButton(cx, a, "settings-tokens-btn", "查看设计系统 Tokens 图鉴", ActionSecondary, func() {
									u.subPage.Set("tokens")
								}),
							),
						},
					},
				},
			},
		},
	}
}

// tokensGalleryScreen renders the runtime Design System Token inspection view.
func (u *uiController) tokensGalleryScreen(cx *core.Scope) core.Node {
	a := u.app
	catalog := TokenCatalog()

	rows := make([]core.Node, 0, len(catalog))
	for _, entry := range catalog {
		e := entry
		rows = append(rows, core.View{
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Background:   pal.Surface,
					CornerRadius: Tokens.RadiusCtrl,
					BorderWidth:  1,
					BorderColor:  pal.Border,
					Padding:      core.All(Tokens.Space3),
					Gap:          core.Points(2),
				}
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{Direction: core.Row, JustifyContent: core.JustifySpaceBetween},
					Children: []core.Node{
						core.Text{
							Content: e.Name,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{Color: pal.Accent, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold, FontFamily: core.FontMonospace}
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
						return core.Style{Color: pal.Text, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
					},
				},
				core.Text{
					Content: e.Use,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption}
					},
				},
			},
		})
	}

	return core.View{
		TestID: "screen-tokens",
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexGrow:   1,
				Background: pal.Bg,
			}
		},
		Children: []core.Node{
			TopHeader(cx, a, func() string { return "设计系统 Tokens" },
				ActionButton(cx, a, "tokens-back-btn", "返回", ActionGhost, func() {
					u.subPage.Set("settings")
				}),
				nil,
			),
			core.Scroll{
				Style: core.Style{FlexGrow: 1},
				Children: []core.Node{
					core.View{
						Style:    core.Style{Padding: core.All(Tokens.PagePadX), Gap: Tokens.Space2},
						Children: rows,
					},
				},
			},
		},
	}
}
