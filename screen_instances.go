package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// instancesScreen renders the Quiet Studio multi-instance CorePool management tab:
// clean grouped cards, online emerald latency/count, TOFU shield line, quiet footer actions, and unified SheetModals.
func (u *uiController) instancesScreen(cx *core.Scope, wide bool) core.Node {
	a := u.app

	headerRight := []core.Node{
		IconTap(cx, a, "instances-qr-btn", "扫码配对", iconQR, func() {
			u.openQRPairing(cx)
		}),
		IconTap(cx, a, "instances-add-btn", "手动添加实例", iconPlus, func() {
			u.editingHost.Set(Host{UseLocalKey: true})
			u.showAddInstance.Set(true)
		}),
	}
	if !wide {
		headerRight = append(headerRight, IconTap(cx, a, "instances-settings-btn", "设置", iconSettings, func() {
			u.subPage.Set("settings")
		}))
	}

	bottomPad := core.Points(96)
	if wide {
		bottomPad = Tokens.Space6
	}

	return core.View{
		TestID: "screen-instances",
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
			TopHeader(cx, a, func() string { return "实例" }, nil, headerRight),
			ToastBanner(cx, a),
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
							Gap: Tokens.Space3,
						},
						Children: []core.Node{
							core.Show{
								When: func() bool { return len(a.instances.Get()) == 0 },
								Then: func(cx *core.Scope) core.Node {
									return EmptyStateCard(cx, a,
										"尚未添加任何 herdr-core 实例",
										"扫码配对或手动填写 SSH 主机信息，即可同时连接多台独立运行的开发机。",
										"instances-empty-add", "添加第一台机器", func() {
											u.editingHost.Set(Host{UseLocalKey: true})
											u.showAddInstance.Set(true)
										},
									)
								},
							},
							core.Each(
								func() []InstanceSnapshot { return a.instances.Get() },
								func(s InstanceSnapshot) string { return s.InstanceID },
								func(cx *core.Scope, item func() InstanceSnapshot, _ func() int) core.Node {
									return u.instanceCard(cx, item)
								},
							),
						},
					},
				},
			},
		},
	}
}

func (u *uiController) instanceCard(cx *core.Scope, itemFn func() InstanceSnapshot) core.Node {
	a := u.app
	initial := itemFn()
	id := initial.InstanceID

	findTOFU := func(hostID string) string {
		for _, rec := range a.tofuRecords.Get() {
			if rec.HostID == hostID {
				return rec.Fingerprint
			}
		}
		return ""
	}

	quietAction := func(testID, label string, svg *core.SVG, danger bool, onPress func()) core.Node {
		return core.View{
			TestID: testID,
			Role:   core.RoleButton,
			OnPress: func(core.PressEvent) {
				playSelectionHaptic(cx)
				if onPress != nil {
					onPress()
				}
			},
			Style: core.Style{
				Direction:  core.Row,
				AlignItems: core.AlignCenter,
				Gap:        core.Points(5),
				MinHeight:  core.Pt(32),
				Padding:    core.Symmetric(core.Points(4), core.Points(4)),
			},
			Children: []core.Node{
				core.Icon{
					SVG: svg,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.TextSecondary
						if danger {
							fg = pal.Danger
						}
						return core.Style{Width: core.Pt(14), Height: core.Pt(14), Color: fg}
					},
				},
				core.Text{
					Content: label,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.TextSecondary
						if danger {
							fg = pal.Danger
						}
						return core.Style{
							Color:      fg,
							FontSize:   Tokens.FontCaption,
							FontWeight: core.FontWeightMedium,
						}
					},
				},
			},
		}
	}

	return GroupCard(cx, a, "instance-card-"+id, []core.Node{
		core.View{
			Style: core.Style{
				Padding: core.All(core.Points(14)),
				Gap:     Tokens.Space2,
			},
			Children: []core.Node{
				// Top line: 8px semantic dot + Instance name + right mono latency/count
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
								FlexShrink: 1,
								MinWidth:   core.Pt(0),
							},
							Children: []core.Node{
								core.View{
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										s := itemFn()
										c := pal.Fill
										if s.Online {
											c = pal.Running
										} else if s.State == stateFailed {
											c = pal.Danger
										} else if s.State == stateDialing {
											c = pal.Warn
										}
										return core.Style{
											FlexShrink:   0,
											Width:        core.Pt(8),
											Height:       core.Pt(8),
											CornerRadius: 4,
											Background:   c,
										}
									},
								},
								core.Text{
									MaxLines:    1,
									ContentFunc: func() string { return itemFn().InstanceName },
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{
											FlexShrink: 1,
											MinWidth:   core.Pt(0),
											Color:      pal.Text,
											FontSize:   Tokens.FontRowTitle,
											FontWeight: core.FontWeightMedium,
										}
									},
								},
							},
						},
						core.Text{
							ContentFunc: func() string {
								s := itemFn()
								if s.Online {
									if s.LatencyMS > 0 {
										return fmt.Sprintf("%dms · %d 会话", s.LatencyMS, len(s.Terminals))
									}
									return fmt.Sprintf("在线 · %d 会话", len(s.Terminals))
								}
								if s.State == stateDialing {
									return "连接中…"
								}
								return "离线"
							},
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								s := itemFn()
								fg := pal.TextMuted
								if s.Online {
									fg = pal.Running
								} else if s.State == stateFailed {
									fg = pal.Danger
								} else if s.State == stateDialing {
									fg = pal.Warn
								}
								return core.Style{
									FlexShrink: 0,
									Color:      fg,
									FontSize:   Tokens.FontMonoMeta,
									FontFamily: core.FontMonospace,
									FontWeight: core.FontWeightMedium,
								}
							},
						},
					},
				},
				// Target + CoreID metadata
				core.Text{
					MaxLines: 1,
					ContentFunc: func() string {
						s := itemFn()
						parts := []string{}
						if s.User != "" && s.Target != "" {
							parts = append(parts, s.User+"@"+s.Target)
						} else if s.Target != "" {
							parts = append(parts, s.Target)
						}
						if s.CoreID != "" {
							parts = append(parts, "core:"+s.CoreID)
						}
						if len(s.Machines) > 0 {
							parts = append(parts, fmt.Sprintf("联邦 +%d", len(s.Machines)))
						}
						return strings.Join(parts, " · ")
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
				// TOFU Fingerprint shield line
				core.Show{
					When: func() bool { return findTOFU(itemFn().InstanceID) != "" },
					Then: func(cx *core.Scope) core.Node {
						return core.View{
							Style: core.Style{
								Direction:  core.Row,
								AlignItems: core.AlignCenter,
								Gap:        core.Points(5),
							},
							Children: []core.Node{
								core.Icon{
									SVG: iconShield,
									StyleFunc: func() core.Style {
										return core.Style{Width: core.Pt(13), Height: core.Pt(13), FlexShrink: 0, Color: a.paletteOf(cx).TextMuted}
									},
								},
								core.Text{
									MaxLines: 1,
									ContentFunc: func() string {
										fp := findTOFU(itemFn().InstanceID)
										if len(fp) > 28 {
											fp = fp[:28] + "…"
										}
										return "TOFU " + fp
									},
									StyleFunc: func() core.Style {
										pal := a.paletteOf(cx)
										return core.Style{
											FlexShrink: 1,
											MinWidth:   core.Pt(0),
											Color:      pal.TextMuted,
											FontSize:   Tokens.FontMonoMeta,
											FontFamily: core.FontMonospace,
										}
									},
								},
							},
						}
					},
				},
				// Error problem banner if present
				core.Show{
					When: func() bool { return itemFn().Problem != "" },
					Then: func(cx *core.Scope) core.Node {
						return core.Text{
							ContentFunc: func() string { return itemFn().Problem },
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{
									Color:    pal.Danger,
									FontSize: Tokens.FontCaption,
								}
							},
						}
					},
				},
			},
		},
		// Quiet bottom hairline footer with text+icon actions
		core.View{
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Direction:      core.Row,
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifySpaceBetween,
					Borders:        core.Edges{Top: 1},
					BorderColor:    pal.Separator,
					Padding:        core.Symmetric(Tokens.Space1, core.Points(12)),
				}
			},
			Children: []core.Node{
				core.View{
					Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space3},
					Children: []core.Node{
						quietAction("inst-reconnect-"+id, "重连", iconRefresh, false, func() {
							a.ReconnectInstance(itemFn().InstanceID)
						}),
						quietAction("inst-edit-"+id, "编辑", iconPencil, false, func() {
							cur := itemFn()
							h := Host{ID: cur.InstanceID, Label: cur.InstanceName, Target: cur.Target, User: cur.User, UseLocalKey: true}
							for _, saved := range a.Hosts() {
								if saved.EffectiveID() == cur.InstanceID {
									h = saved
									break
								}
							}
							u.editingHost.Set(h)
							u.showAddInstance.Set(true)
						}),
					},
				},
				quietAction("inst-delete-"+id, "删除", iconTrash, true, func() {
					a.RemoveHost(itemFn().InstanceID)
				}),
			},
		},
	})
}

// addInstanceModal renders the manual SSH instance creation / edit form with live connection test.
func (u *uiController) addInstanceModal(cx *core.Scope) core.Node {
	a := u.app
	initial := u.editingHost.Get()
	label := core.NewSignal(cx, initial.Label)
	target := core.NewSignal(cx, initial.Target)
	user := core.NewSignal(cx, initial.User)
	password := core.NewSignal(cx, "")
	privKey := core.NewSignal(cx, "")
	useLocalKey := core.NewSignal(cx, initial.UseLocalKey || initial.Target == "")
	pinnedFP := core.NewSignal(cx, initial.PinnedFP)
	testStatus := core.NewSignal(cx, "")
	testingConn := core.NewSignal(cx, false)

	buildHost := func() Host {
		h := Host{
			ID:          initial.ID,
			Label:       strings.TrimSpace(label.Get()),
			Target:      strings.TrimSpace(target.Get()),
			User:        strings.TrimSpace(user.Get()),
			Password:    password.Get(),
			Key:         strings.TrimSpace(privKey.Get()),
			UseLocalKey: useLocalKey.Get(),
			PinnedFP:    strings.TrimSpace(pinnedFP.Get()),
		}
		if h.Label == "" {
			h.Label = h.Target
		}
		return h
	}

	field := func(testID, title, placeholder string, val func() string, onChange func(string), secure bool) core.Node {
		return core.View{
			Style: core.Style{Gap: core.Points(4)},
			Children: []core.Node{
				core.Text{
					Content: title,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{
							Color:      pal.TextSecondary,
							FontSize:   Tokens.FontCaption,
							FontWeight: core.FontWeightSemibold,
						}
					},
				},
				ui.Input{
					TestID:      testID,
					Placeholder: placeholder,
					Value:       val,
					OnChange:    onChange,
					Masked:      secure,
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
		}
	}

	return SheetModal(cx, a, "instance-form", "添加 / 编辑 SSH 实例", "instance-form-close", func() {
		u.showAddInstance.Set(false)
	}, []core.Node{
		field("form-label", "实例备注名", "例如 mac-mini / kr-prod", label.Get, label.Set, false),
		field("form-target", "主机地址（支持 Tailscale / 局域网 IP）", "100.97.151.80:22", target.Get, target.Set, false),
		field("form-user", "SSH 用户名", "root 或当前系统用户名", user.Get, user.Set, false),
		core.View{
			Style: core.Style{
				Direction:      core.Row,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifySpaceBetween,
			},
			Children: []core.Node{
				core.Text{
					Content: "使用本机自动生成的 ed25519 密钥",
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{Color: pal.Text, FontSize: Tokens.FontBody}
					},
				},
				ui.Switch{
					TestID:   "form-use-local-key",
					Checked:  useLocalKey.Get,
					OnChange: useLocalKey.Set,
					Label:    "使用本机 ed25519 密钥",
				},
			},
		},
		field("form-password", "SSH 密码（可选，存入系统加密钥匙串）", "留空则仅使用密钥认证", password.Get, password.Set, true),
		field("form-privkey", "自定义 OpenSSH 私钥（可选，留空用本机密钥）", "-----BEGIN OPENSSH PRIVATE KEY-----", privKey.Get, privKey.Set, false),
		field("form-fp", "预置主机指纹（可选，TOFU 强校验）", "SHA256:...", pinnedFP.Get, pinnedFP.Set, false),
		core.Show{
			When: func() bool { return testStatus.Get() != "" },
			Then: func(cx *core.Scope) core.Node {
				return core.Text{
					ContentFunc: testStatus.Get,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						fg := pal.TextSecondary
						if strings.HasPrefix(testStatus.Get(), "成功") {
							fg = pal.Running
						} else if strings.HasPrefix(testStatus.Get(), "失败") {
							fg = pal.Danger
						}
						return core.Style{Color: fg, FontSize: Tokens.FontCaption}
					},
				}
			},
		},
		core.View{
			Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
			Children: []core.Node{
				core.View{
					Style: core.Style{FlexGrow: 1},
					Children: []core.Node{
						ActionButtonFunc(cx, a, "form-test-btn", func() string {
							if testingConn.Get() {
								return "测试中…"
							}
							return "测试连接"
						}, ActionSecondary, func() {
							h := buildHost()
							if h.Target == "" {
								testStatus.Set("失败：请先填写主机地址。")
								return
							}
							testingConn.Set(true)
							testStatus.Set("正在通过 SSH 连接并校验 herdr-core…")
							go func() {
								ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
								defer cancel()
								hello, ms, err := a.TestConnection(ctx, h)
								a.post(func() {
									testingConn.Set(false)
									if err != nil {
										testStatus.Set("失败：" + explain(err))
										return
									}
									name := hello.Core.Name
									if name == "" {
										name = h.Target
									}
									testStatus.Set(fmt.Sprintf("成功：已连上 %s（%s，%dms）", name, hello.Version, ms))
								})
							}()
						}),
					},
				},
				core.View{
					Style: core.Style{FlexGrow: 1},
					Children: []core.Node{
						ActionButton(cx, a, "form-save-btn", "保存并连接", ActionPrimary, func() {
							h := buildHost()
							if h.Target == "" {
								testStatus.Set("失败：主机地址不能为空。")
								return
							}
							a.SaveHost(h)
							u.showAddInstance.Set(false)
							a.notifyToast("已保存实例：" + h.Label)
						}),
					},
				},
			},
		},
	})
}

// qrPairingModal renders the QR camera scan trigger + paste URI/JSON fallback modal.
func (u *uiController) qrPairingModal(cx *core.Scope) core.Node {
	a := u.app
	rawInput := core.NewSignal(cx, "")
	errMsg := core.NewSignal(cx, "")

	applyRaw := func(raw string) {
		p, err := ParsePairingPayload(raw)
		if err != nil {
			errMsg.Set(err.Error())
			return
		}
		id := p.Label
		for _, existing := range a.Hosts() {
			if existing.EffectiveID() == id && existing.Target != p.Target() {
				id = p.Target()
				break
			}
		}
		h := Host{
			ID:          id,
			Label:       p.Label,
			Target:      p.Target(),
			User:        p.User,
			Key:         p.Key,
			Password:    p.Password,
			PinnedFP:    p.Fingerprint,
			UseLocalKey: p.Key == "" && p.Password == "",
		}
		a.SaveHost(h)
		// Seed instance snapshot immediately in UI state if offline/test mode.
		u.showQRModal.Set(false)
		a.notifyToast("已配对实例：" + h.Label)
	}

	return SheetModal(cx, a, "qr-modal", "扫码 / 粘贴配对", "qr-modal-close", func() {
		u.showQRModal.Set(false)
	}, []core.Node{
		core.Text{
			Content: "支持 herdr://pair?v=1&host=...&user=...&fp=SHA256:... 或紧凑 JSON 格式；带 fp 字段时将在首次握手前强制校验主机指纹。",
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption}
			},
		},
		ActionButton(cx, a, "qr-scan-camera-btn", "打开相机扫描二维码", ActionPrimary, func() {
			if cx == nil {
				return
			}
			cx.ScanCode(core.ScanOptions{}, func(code *core.ScannedCode, err error) {
				if err != nil {
					a.post(func() { errMsg.Set("无法打开相机：" + err.Error() + "，请直接粘贴下方配对码。") })
					return
				}
				if code == nil || strings.TrimSpace(code.Text) == "" {
					return
				}
				a.post(func() {
					rawInput.Set(code.Text)
					applyRaw(code.Text)
				})
			})
		}),
		ui.Input{
			TestID:      "qr-paste-input",
			Placeholder: "粘贴 herdr://pair?v=1&... 或 JSON",
			Value:       rawInput.Get,
			OnChange:    func(s string) { rawInput.Set(s); errMsg.Set("") },
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
		core.Show{
			When: func() bool { return errMsg.Get() != "" },
			Then: func(cx *core.Scope) core.Node {
				return core.Text{
					ContentFunc: errMsg.Get,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{Color: pal.Danger, FontSize: Tokens.FontCaption}
					},
				}
			},
		},
		ActionButton(cx, a, "qr-apply-btn", "解析并添加实例", ActionSecondary, func() {
			applyRaw(rawInput.Get())
		}),
	})
}

func (u *uiController) openQRPairing(cx *core.Scope) {
	u.showQRModal.Set(true)
}
