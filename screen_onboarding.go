package main

import (
	"github.com/nsgo-dev/nsgo/core"
)

// onboardingScreen renders the 0-instance cold-start guide with QR pairing, manual SSH setup,
// one-click authorized_keys command copy, and troubleshooting checklist.
func (u *uiController) onboardingScreen(cx *core.Scope) core.Node {
	a := u.app
	showTroubleshoot := core.NewSignal(cx, false)

	stepRow := func(num, title, desc string) core.Node {
		return core.View{
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Direction:    core.Row,
					Gap:          Tokens.Space3,
					Padding:      core.All(Tokens.Space3),
					Background:   pal.Surface,
					CornerRadius: Tokens.RadiusCard,
					BorderWidth:  1,
					BorderColor:  pal.Border,
				}
			},
			Children: []core.Node{
				core.View{
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{
							Width:          core.Pt(26),
							Height:         core.Pt(26),
							CornerRadius:   13,
							Background:     pal.AccentSoft,
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
									Color:      pal.Accent,
									FontSize:   Tokens.FontCaption,
									FontWeight: core.FontWeightBold,
								}
							},
						},
					},
				},
				core.View{
					Style: core.Style{FlexGrow: 1, Gap: core.Points(3)},
					Children: []core.Node{
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
				Background: pal.Bg,
			}
		},
		Children: []core.Node{
			TopHeader(cx, a, func() string { return "herdr" }, nil, []core.Node{
				ActionButton(cx, a, "onboarding-settings-btn", "设置", ActionGhost, func() {
					u.subPage.Set("settings")
				}),
			}),
			core.Scroll{
				Style: core.Style{FlexGrow: 1},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Padding: core.All(Tokens.Space4),
							Gap:     Tokens.Space4,
						},
						Children: []core.Node{
							// Hero copy
							core.View{
								Style: core.Style{Gap: Tokens.Space2},
								Children: []core.Node{
									core.Text{
										Content: "连接你的第一台 herdr 机器",
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Color:      pal.Text,
												FontSize:   core.Points(22),
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
							// Primary CTAs
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
							// 3-step quick start
							SectionHeader(cx, a, func() string { return "1 分钟上手步骤" }, nil),
							core.View{
								Style: core.Style{Gap: Tokens.Space2},
								Children: []core.Node{
									stepRow("1", "在远端电脑运行 herdr-core", "确认已安装 hc 并在后台运行，且可通过 Tailscale 或局域网 SSH 登录。"),
									stepRow("2", "授权本机 ed25519 公钥", "点击下方一键复制命令，在远端执行即可免密连接；或直接在表单里使用密码。"),
									stepRow("3", "扫码或填写主机地址", "支持同时添加多台独立实例，客户端会自动按 CoreID 去重并保留直连优先。"),
								},
							},
							// Local ed25519 key copy helper
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Background:   pal.Surface,
										CornerRadius: Tokens.RadiusCard,
										BorderWidth:  1,
										BorderColor:  pal.Border,
										Padding:      core.All(Tokens.Space3),
										Gap:          Tokens.Space2,
									}
								},
								Children: []core.Node{
									core.Text{
										Content: "本机免密公钥命令",
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Color:      pal.Text,
												FontSize:   Tokens.FontCaption,
												FontWeight: core.FontWeightSemibold,
											}
										},
									},
									core.Text{
										ContentFunc: func() string {
											return AuthorizedKeysCommand(a.localPubKey.Get())
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Color:      pal.TextMuted,
												FontSize:   Tokens.FontMonoMeta,
												FontFamily: core.FontMonospace,
											}
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
							// Troubleshooting accordion
							ActionButtonFunc(cx, a, "onboarding-troubleshoot-btn", func() string {
								if showTroubleshoot.Get() {
									return "收起常见排障指南"
								}
								return "连不上？查看常见排障指南"
							}, ActionGhost, func() {
								showTroubleshoot.Set(!showTroubleshoot.Get())
							}),
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
	}
}
