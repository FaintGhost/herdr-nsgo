package main

import (
	"github.com/nsgo-dev/nsgo/core"
)

// onboardingScreen renders the Quiet Studio 0-instance cold-start guide with QR pairing,
// manual SSH setup, grouped steps, and compact authorized_keys command box (centered max 520px).
func (u *uiController) onboardingScreen(cx *core.Scope) core.Node {
	a := u.app
	showTroubleshoot := core.NewSignal(cx, false)

	stepRow := func(num, title, desc string) core.Node {
		return core.View{
			Style: core.Style{
				Direction: core.Row,
				Gap:       Tokens.Space3,
				Padding:   core.Symmetric(core.Points(12), core.Points(14)),
			},
			Children: []core.Node{
				core.View{
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{
							FlexShrink:     0,
							Width:          core.Pt(24),
							Height:         core.Pt(24),
							CornerRadius:   12,
							Background:     pal.SurfaceRaised,
							AlignItems:     core.AlignCenter,
							JustifyContent: core.JustifyCenter,
						}
					},
					Children: []core.Node{
						core.Text{
							Content: num,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{
									Color:      pal.Text,
									FontSize:   Tokens.FontMonoMeta,
									FontWeight: core.FontWeightSemibold,
									FontFamily: core.FontMonospace,
								}
							},
						},
					},
				},
				core.View{
					Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
					Children: []core.Node{
						core.Text{
							Content: title,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{
									Color:      pal.Text,
									FontSize:   Tokens.FontRowTitle,
									FontWeight: core.FontWeightMedium,
								}
							},
						},
						core.Text{
							Content: desc,
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{
									Color:    pal.TextSecondary,
									FontSize: Tokens.FontCaption,
								}
							},
						},
					},
				},
			},
		}
	}

	return core.View{
		TestID: "screen-onboarding",
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
				IconTap(cx, a, "onboarding-settings-btn", "设置", iconSettings, func() {
					u.subPage.Set("settings")
				}),
			}),
			ToastBanner(cx, a),
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
									MaxWidth: core.Pt(520),
									Padding:  core.All(Tokens.PagePadX),
									Gap:      Tokens.SectionGap,
								},
								Children: []core.Node{
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											core.Text{
												Content: "连接你的第一台 herdr 机器",
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														Color:      pal.Text,
														FontSize:   Tokens.FontNav,
														FontWeight: core.FontWeightBold,
													}
												},
											},
											core.Text{
												Content: "直连多台运行 herdr-core 的开发机，在手机上实时看完整 VT 终端、审批 Agent 权限并随时下发任务。",
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														Color:    pal.TextSecondary,
														FontSize: Tokens.FontBody,
													}
												},
											},
										},
									},
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											ActionButton(cx, a, "onboarding-qr-btn", "扫码 / 粘贴配对码", ActionPrimary, func() {
												u.openQRPairing(cx)
											}),
											ActionButton(cx, a, "onboarding-manual-btn", "手动添加 SSH 机器", ActionSecondary, func() {
												u.editingHost.Set(Host{UseLocalKey: true})
												u.showAddInstance.Set(true)
											}),
										},
									},
									core.View{
										Style: core.Style{Gap: Tokens.Space2},
										Children: []core.Node{
											SectionHeader(cx, a, func() string { return "1 分钟上手步骤" }, nil),
											GroupCard(cx, a, "", []core.Node{
												stepRow("1", "在远端电脑运行 herdr-core", "确认已安装 hc 并在后台运行，且可通过 Tailscale 或局域网 SSH 登录。"),
												InsetDivider(cx, a, 50),
												stepRow("2", "授权本机 ed25519 公钥（或直接填密码）", "点击下方一键复制命令在远端执行即可免密连接；也可以直接在表单填 SSH 密码登录。"),
												InsetDivider(cx, a, 50),
												stepRow("3", "扫码或填写主机地址", "支持同时添加多台独立实例，客户端会自动按 CoreID 去重并保留直连优先。"),
											}),
										},
									},
									GroupCard(cx, a, "", []core.Node{
										core.View{
											Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space2},
											Children: []core.Node{
												core.Text{
													Content: "本机免密公钥命令",
													StyleFunc: func() core.Style {
														pal := a.paletteOf(cx)
														return core.Style{
															Color:      pal.Text,
															FontSize:   Tokens.FontCaption,
															FontWeight: core.FontWeightMedium,
														}
													},
												},
												core.View{
													StyleFunc: func() core.Style {
														return core.Style{
															Background:   a.paletteOf(cx).Bg,
															CornerRadius: Tokens.RadiusCtrl,
															Padding:      core.Symmetric(Tokens.Space2, Tokens.Space3),
														}
													},
													Children: []core.Node{
														core.Text{
															ContentFunc: func() string {
																return AuthorizedKeysCommand(a.localPubKey.Get())
															},
															StyleFunc: func() core.Style {
																pal := a.paletteOf(cx)
																return core.Style{
																	Color:      pal.TextSecondary,
																	FontSize:   Tokens.FontMonoMeta,
																	FontFamily: core.FontMonospace,
																}
															},
														},
													},
												},
												ActionButton(cx, a, "onboarding-copy-key-btn", "复制 authorized_keys 命令", ActionSecondary, func() {
													cmd := AuthorizedKeysCommand(a.localPubKey.Get())
													if cx != nil {
														cx.SetClipboard(cmd)
													}
													a.notifyToast("已复制公钥授权命令，到远端终端粘贴执行即可。")
												}),
											},
										},
									}),
									core.View{
										TestID: "onboarding-troubleshoot-btn",
										Role:   core.RoleButton,
										OnPress: func(core.PressEvent) {
											playSelectionHaptic(cx)
											showTroubleshoot.Set(!showTroubleshoot.Get())
										},
										Style: core.Style{
											Direction:  core.Row,
											AlignItems: core.AlignCenter,
											AlignSelf:  core.AlignStart,
											Gap:        core.Points(4),
											Padding:    core.Symmetric(core.Points(4), Tokens.Space1),
										},
										Children: []core.Node{
											core.Text{
												ContentFunc: func() string {
													if showTroubleshoot.Get() {
														return "收起常见排障指南"
													}
													return "连不上？查看常见排障指南"
												},
												StyleFunc: func() core.Style {
													return core.Style{
														Color:      a.paletteOf(cx).TextMuted,
														FontSize:   Tokens.FontCaption,
														FontWeight: core.FontWeightMedium,
													}
												},
											},
											core.Show{
												When: showTroubleshoot.Get,
												Then: func(cx *core.Scope) core.Node {
													return core.Icon{
														SVG: iconChevronU,
														StyleFunc: func() core.Style {
															return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
														},
													}
												},
												Else: func(cx *core.Scope) core.Node {
													return core.Icon{
														SVG: iconChevronD,
														StyleFunc: func() core.Style {
															return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
														},
													}
												},
											},
										},
									},
									core.Show{
										When: showTroubleshoot.Get,
										Then: func(cx *core.Scope) core.Node {
											return EmptyStateCard(cx, a,
												"排障检查清单",
												"1) Tailscale/局域网连通：确认手机与电脑在同一 Tailnet。\n"+
													"2) SSH 服务开启：macOS 开启「远程登录」，Linux 确认 sshd 在 22 端口运行。\n"+
													"3) PATH 中包含 hc：非交互式 SSH 需要将 ~/.local/bin 加入 ~/.zshenv 或 ~/.bashrc，或安装到 /usr/local/bin。",
												"", "", nil,
											)
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
