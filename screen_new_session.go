package main

import (
	"context"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// newSessionSheet renders the New Session bottom sheet: select target online instance,
// optional federated machine (p.On), template from templates.list (or Shell), cwd/name/prompt, and run.
func (u *uiController) newSessionSheet(cx *core.Scope) core.Node {
	a := u.app
	snaps := a.instances.Get()
	defaultInst := a.activeInstanceFilter.Get()
	if defaultInst == "" && len(snaps) > 0 {
		for _, s := range snaps {
			if s.Online {
				defaultInst = s.InstanceID
				break
			}
		}
		if defaultInst == "" {
			defaultInst = snaps[0].InstanceID
		}
	}

	selectedInst := core.NewSignal(cx, defaultInst)
	selectedMachine := core.NewSignal(cx, "") // "" = local on target instance
	selectedTpl := core.NewSignal(cx, "shell")
	cwd := core.NewSignal(cx, "")
	name := core.NewSignal(cx, "")
	prompt := core.NewSignal(cx, "")
	errMsg := core.NewSignal(cx, "")
	submitting := core.NewSignal(cx, false)

	syncDefaultTemplate := func(instID string) {
		for _, s := range a.instances.Get() {
			if s.InstanceID == instID {
				if len(s.Templates) > 0 {
					selectedTpl.Set(s.Templates[0].Name)
				} else {
					selectedTpl.Set("shell")
				}
				return
			}
		}
		selectedTpl.Set("shell")
	}
	syncDefaultTemplate(defaultInst)

	currentTemplates := func() []Template {
		id := selectedInst.Get()
		for _, s := range a.instances.Get() {
			if s.InstanceID == id {
				return s.Templates
			}
		}
		return nil
	}

	currentMachines := func() []MachineView {
		id := selectedInst.Get()
		for _, s := range a.instances.Get() {
			if s.InstanceID == id {
				return s.Machines
			}
		}
		return nil
	}

	inputField := func(testID, label, placeholder string, val func() string, onChange func(string)) core.Node {
		return core.View{
			Style: core.Style{Gap: core.Points(4)},
			Children: []core.Node{
				core.Text{
					Content: label,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold}
					},
				},
				ui.Input{
					TestID:      testID,
					Placeholder: placeholder,
					Value:       val,
					OnChange:    onChange,
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

	return SheetModal(cx, a, "sheet-new-session", "新建会话", "sheet-new-session-close", func() {
		u.showNewSession.Set(false)
	}, []core.Node{
		// Target instance chips
		core.View{
			Style: core.Style{Gap: Tokens.Space1},
			Children: []core.Node{
				core.Text{
					Content: "目标实例",
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold}
					},
				},
				core.Scroll{
					Horizontal:    true,
					NoShrink:      true,
					HideIndicator: true,
					Children: []core.Node{
						core.View{
							Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
							Children: []core.Node{
								core.Each(
									func() []InstanceSnapshot { return a.instances.Get() },
									func(s InstanceSnapshot) string { return s.InstanceID },
									func(cx *core.Scope, item func() InstanceSnapshot, _ func() int) core.Node {
										s0 := item()
										return FilterPill(cx, a, "new-inst-"+s0.InstanceID, func() string {
											return item().InstanceName
										}, func() bool {
											return selectedInst.Get() == item().InstanceID
										}, func() bool {
											return item().Online
										}, func() {
											id := item().InstanceID
											selectedInst.Set(id)
											selectedMachine.Set("")
											syncDefaultTemplate(id)
										})
									},
								),
							},
						},
					},
				},
			},
		},
		// Optional federated remote machine selector (when instance has Machines)
		core.Show{
			When: func() bool { return len(currentMachines()) > 0 },
			Then: func(cx *core.Scope) core.Node {
				return core.View{
					Style: core.Style{Gap: Tokens.Space1},
					Children: []core.Node{
						core.Text{
							Content: "运行机器（可选联邦节点）",
							StyleFunc: func() core.Style {
								pal := a.paletteOf(cx)
								return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold}
							},
						},
						core.Scroll{
							Horizontal:    true,
							HideIndicator: true,
							Children: []core.Node{
								core.View{
									Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
									Children: []core.Node{
										FilterPill(cx, a, "new-machine-local", func() string { return "本机" }, func() bool {
											return selectedMachine.Get() == ""
										}, nil, func() { selectedMachine.Set("") }),
										core.Each(
											currentMachines,
											func(m MachineView) string { return m.Name },
											func(cx *core.Scope, item func() MachineView, _ func() int) core.Node {
												m0 := item()
												return FilterPill(cx, a, "new-machine-"+m0.Name, func() string {
													return item().Name
												}, func() bool {
													return selectedMachine.Get() == item().Name
												}, nil, func() {
													selectedMachine.Set(item().Name)
												})
											},
										),
									},
								},
							},
						},
					},
				}
			},
		},
		// Template / Shell chips
		core.View{
			Style: core.Style{Gap: Tokens.Space1},
			Children: []core.Node{
				core.Text{
					Content: "启动模板",
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{Color: pal.TextSecondary, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightSemibold}
					},
				},
				core.Scroll{
					Horizontal:    true,
					NoShrink:      true,
					HideIndicator: true,
					Children: []core.Node{
						core.View{
							Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
							Children: []core.Node{
								core.Each(
									currentTemplates,
									func(t Template) string { return t.Name },
									func(cx *core.Scope, item func() Template, _ func() int) core.Node {
										t0 := item()
										return FilterPill(cx, a, "tpl-chip-"+t0.Name, func() string {
											return item().Name
										}, func() bool {
											return selectedTpl.Get() == item().Name
										}, nil, func() {
											selectedTpl.Set(item().Name)
										})
									},
								),
								FilterPill(cx, a, "tpl-chip-shell", func() string { return "Shell" }, func() bool {
									return selectedTpl.Get() == "shell"
								}, nil, func() {
									selectedTpl.Set("shell")
								}),
							},
						},
					},
				},
			},
		},
		inputField("new-session-cwd", "工作目录（可选）", "/path/to/workspace", cwd.Get, cwd.Set),
		inputField("new-session-name", "会话名称（可选）", "例如 fix-auth", name.Get, name.Set),
		inputField("new-session-prompt", "首条指令或任务（可选）", "例如 帮我跑一遍测试并修复报错", prompt.Get, prompt.Set),
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
		ActionButtonFunc(cx, a, "new-session-submit", func() string {
			if submitting.Get() {
				return "正在创建…"
			}
			return "启动终端"
		}, ActionPrimary, func() {
			inst := selectedInst.Get()
			if inst == "" {
				errMsg.Set("请先选择一个在线实例。")
				return
			}
			p := RunParams{
				Cwd:  strings.TrimSpace(cwd.Get()),
				Name: strings.TrimSpace(name.Get()),
				On:   selectedMachine.Get(),
			}
			if tpl := selectedTpl.Get(); tpl != "" && tpl != "shell" {
				p.Argv = []string{tpl}
				if pr := strings.TrimSpace(prompt.Get()); pr != "" {
					p.Argv = append(p.Argv, pr)
				}
			} else if pr := strings.TrimSpace(prompt.Get()); pr != "" {
				p.Line = pr
			} else {
				p.Argv = []string{"sh", "-l"}
			}
			submitting.Set(true)
			errMsg.Set("")
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				key, err := a.CreateTerminal(ctx, inst, p)
				a.post(func() {
					submitting.Set(false)
					if err != nil {
						errMsg.Set(explain(err))
						return
					}
					u.showNewSession.Set(false)
					u.openSession(cx, key)
				})
			}()
		}),
	})
}
