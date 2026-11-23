package main

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// accent is Moshi's single emphasis colour: everything else is neutral.
func accent() core.Color { return core.RGB(0x00e676) }

// ---------------------------------------------------------------------------
// home

func (a *App) homePage(cx *core.Scope, nav *base.Nav[Route]) base.NavPage {
	colors := ui.ColorsOf(cx)
	return ui.NavPage(cx, base.NavPage{
		Title:      "herdr",
		HeaderStyle: core.Style{Background: colors.Background()},
		HeaderRight: ui.Button{
			Text:    "收件箱",
			Variant: ui.ButtonGhost,
			OnPress: func() { nav.Push(Route{Kind: "inbox"}) },
		},
		Children: []core.Node{a.homeBody(cx, nav)},
		StyleFunc: func() core.Style {
			return core.Style{Background: colors.Background()}
		},
	}, ui.NavHeader{Title: "herdr"})
}

func (a *App) homeBody(cx *core.Scope, nav *base.Nav[Route]) core.Node {
	return ui.ScrollView{
		Style: core.Style{Width: core.Pct(100), Height: core.Pct(100)},
		ContentStyle: core.Style{
			Padding: core.All(16), Gap: 20, Width: core.Pct(100),
		},
		Children: []core.Node{
			a.connBanner(cx),
			a.statusLine(cx),
			a.attentionSection(cx, nav),
			a.sessionsSection(cx, nav),
			a.machinesSection(cx, nav),
			a.discoverSection(cx, nav),
			core.View{Style: core.Style{Height: core.Pt(core.Points(24))}},
		},
	}
}

// connBanner is the dependency banner: a missing connection, or a failure
// with its repair, never a bare error.
func (a *App) connBanner(cx *core.Scope) core.Node {
	colors := ui.ColorsOf(cx)
	return core.View{
		StyleFunc: func() core.Style {
			state := a.conn.Get()
			problem := a.problem.Get()
			if state == stateOnline && problem == "" {
				return core.Style{Display: core.DisplayNone}
			}
			return core.Style{
				Padding: core.All(14), Gap: 6,
				CornerRadius: core.Points(14),
				BorderWidth:  core.Points(1),
				BorderColor:  accent(),
				Background:   colors.Surface(),
			}
		},
		Children: []core.Node{
			core.Text{
				ContentFunc: func() string {
					switch a.conn.Get() {
					case stateDialing:
						return "正在连接…"
					case stateOnline:
						return "连接中断"
					}
					return "还没连上核心"
				},
				StyleFunc: func() core.Style {
					return core.Style{FontSize: core.Points(15), FontWeight: core.FontWeightSemibold, Color: colors.Text()}
				},
			},
			core.Text{
				ContentFunc: func() string {
					if p := a.problem.Get(); p != "" {
						return p
					}
					if a.conn.Get() == stateDialing {
						return "走 SSH 跑 `hc connect`，通常几秒。"
					}
					return "添加一台装了 herdr-core 的机器，就能看到上面所有终端。"
				},
				StyleFunc: func() core.Style {
					return core.Style{FontSize: core.Points(13), LineHeight: core.Points(19), Color: colors.Muted()}
				},
			},
			core.View{
				Style: core.Style{Padding: core.Symmetric(core.Points(6), core.Points(0))},
				Children: []core.Node{ui.Button{
					Text:    "重连",
					Variant: ui.ButtonTinted,
					OnPress: func() { a.Reconnect() },
				}},
			},
		},
	}
}

// statusLine says which core answered and how much it has, so a silent empty
// list never passes for a working connection.
func (a *App) statusLine(cx *core.Scope) core.Node {
	colors := ui.ColorsOf(cx)
	return core.Text{
		ContentFunc: func() string {
			if a.conn.Get() != stateOnline {
				return ""
			}
			name := a.coreName.Get()
			if name == "" {
				name = "核心"
			}
			return fmt.Sprintf("已连接 %s · %d 个终端 · %d 台机器", name, len(a.terms.Get()), len(a.machines.Get()))
		},
		StyleFunc: func() core.Style {
			s := core.Style{FontSize: core.Points(12), Color: colors.Muted()}
			if a.conn.Get() != stateOnline {
				s.Display = core.DisplayNone
			}
			return s
		},
	}
}

// attentionSection is the inbox at the top of home: the terminals that wait
// on a person come before everything else, or nothing appears.
func (a *App) attentionSection(cx *core.Scope, nav *base.Nav[Route]) core.Node {
	rows := core.Each(
		func() []Terminal { return a.Attention() },
		func(t Terminal) string { return t.Address },
		func(cx *core.Scope, item func() Terminal, _ func() int) core.Node {
			return a.terminalRow(cx, nav, item(), true)
		},
	)
	return core.View{
		StyleFunc: func() core.Style {
			if len(a.Attention()) == 0 {
				return core.Style{Display: core.DisplayNone}
			}
			return core.Style{Gap: 8}
		},
		Children: []core.Node{
			core.Text{
				ContentFunc: func() string { return fmt.Sprintf("需要你 · %d", len(a.Attention())) },
				StyleFunc: func() core.Style {
					return core.Style{FontSize: core.Points(12), FontWeight: core.FontWeightSemibold, Color: ui.ColorsOf(cx).Muted(), Padding: core.Symmetric(core.Points(4), core.Points(0))}
				},
			},
			rows,
		},
	}
}

func (a *App) sessionsSection(cx *core.Scope, nav *base.Nav[Route]) core.Node {
	rows := core.Each(
		func() []Terminal { return a.terms.Get() },
		func(t Terminal) string { return t.Address },
		func(cx *core.Scope, item func() Terminal, _ func() int) core.Node {
			return a.terminalRow(cx, nav, item(), false)
		},
	)
	empty := core.View{
		StyleFunc: func() core.Style {
			if len(a.terms.Get()) > 0 {
				return core.Style{Display: core.DisplayNone}
			}
			return core.Style{}
		},
		Children: []core.Node{emptyCard(cx,
			"还没有活跃会话",
			"连上之后，这里会列出每个终端：状态、目录、最后一句输出。",
			"添加机器",
			func() { nav.Push(Route{Kind: "add"}) },
		)},
	}
	return core.View{Style: core.Style{Gap: 8}, Children: []core.Node{
		sectionTitle(cx, "会话"), empty, rows,
	}}
}

func (a *App) machinesSection(cx *core.Scope, nav *base.Nav[Route]) core.Node {
	rows := core.Each(
		func() []MachineView { return a.machines.Get() },
		func(m MachineView) string { return m.Name },
		func(cx *core.Scope, item func() MachineView, _ func() int) core.Node {
			m := item()
			label := m.Name
			if m.Host != "" && m.Host != m.Name {
				label = m.Name + " · " + m.Host
			}
			return ui.Button{Text: label, Variant: ui.ButtonSecondary, OnPress: func() {}}
		},
	)
	empty := core.View{
		StyleFunc: func() core.Style {
			if len(a.machines.Get()) > 0 {
				return core.Style{Display: core.DisplayNone}
			}
			return core.Style{}
		},
		Children: []core.Node{emptyCard(cx,
			"还没有机器",
			"一台机器一条 SSH 连接。连上后它的终端会并进上面的会话列表。",
			"连接一台机器",
			func() { nav.Push(Route{Kind: "add"}) },
		)},
	}
	return core.View{Style: core.Style{Gap: 8}, Children: []core.Node{
		sectionTitle(cx, "机器"), empty, rows,
	}}
}

// terminalRow is one session: a state dot, its name, its directory, its word.
func (a *App) terminalRow(cx *core.Scope, nav *base.Nav[Route], t Terminal, attention bool) core.Node {
	colors := ui.ColorsOf(cx)
	return core.View{
		OnPress: func(core.PressEvent) { nav.Push(Route{Kind: "session", Addr: t.Address}) },
		Style: core.Style{
			Padding: core.All(14), Gap: 4,
			CornerRadius: core.Points(14),
			Background:   colors.Surface(),
			BorderWidth:  core.Points(1),
			BorderColor: func() core.Color {
				if attention {
					return accent()
				}
				return colors.Border()
			}(),
		},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
				core.View{Style: core.Style{Width: core.Pt(8), Height: core.Pt(8), CornerRadius: core.Points(4), Background: stateColor(cx, t)}},
				core.Text{
					Content: t.Label(),
					StyleFunc: func() core.Style {
						return core.Style{FontSize: core.Points(15), FontWeight: core.FontWeightSemibold, Color: colors.Text(), FlexGrow: 1}
					},
				},
				core.Text{
					Content: stateWord(t),
					StyleFunc: func() core.Style {
						return core.Style{FontSize: core.Points(12), Color: stateColor(cx, t)}
					},
				},
			}},
			core.Text{
				Content:     detailLine(t),
				MaxLines:    1,
				StyleFunc: func() core.Style {
					return core.Style{FontSize: core.Points(12), Color: colors.Muted()}
				},
			},
		},
	}
}

// discoverSection is Moshi's "发现": name each capability and where it lives,
// instead of writing a manual.
func (a *App) discoverSection(cx *core.Scope, nav *base.Nav[Route]) core.Node {
	rows := []core.Node{sectionTitle(cx, "发现 herdr")}
	for _, d := range []struct{ name, where string }{
		{"终端 / 聊天视图", "会话 → 一屏文字 + 一个输入框"},
		{"收件箱与审批", "右上角收件箱 → 只列等你的终端"},
		{"一次性按键", "会话底部 → Enter / Esc / Ctrl+C / ↑ / ↓"},
		{"机器与终端", "首页 → 机器 · 会话（machine/t7 可跨机器）"},
	} {
		d := d
		rows = append(rows, core.View{
			Style: core.Style{Padding: core.All(12), Gap: 2, CornerRadius: core.Points(12),
				Background: ui.ColorsOf(cx).Surface()},
			Children: []core.Node{
				core.Text{Content: d.name, Style: core.Style{FontSize: core.Points(14), FontWeight: core.FontWeightSemibold, Color: ui.ColorsOf(cx).Text()}},
				core.Text{Content: d.where, Style: core.Style{FontSize: core.Points(12), Color: ui.ColorsOf(cx).Muted()}},
			},
		})
	}
	return core.View{Style: core.Style{Gap: 8}, Children: rows}
}

// ---------------------------------------------------------------------------
// session

func (a *App) sessionPage(cx *core.Scope, nav *base.Nav[Route], addr string) base.NavPage {
	colors := ui.ColorsOf(cx)
	t, _ := a.Terminal(addr)
	cx.Effect(func() {
		a.Watch(addr)
		cx.Cleanup(func() { a.Unwatch(addr) })
	})
	return ui.NavPage(cx, base.NavPage{
		Title:           t.Label(),
		HeaderBackTitle: "返回",
		HeaderStyle:     core.Style{Background: colors.Background()},
		HeaderRight: core.Text{
			ContentFunc: func() string {
				cur, _ := a.Terminal(addr)
				return stateWord(cur)
			},
			StyleFunc: func() core.Style {
				cur, _ := a.Terminal(addr)
				return core.Style{FontSize: core.Points(13), Color: stateColor(cx, cur)}
			},
		},
		OnDismiss: func() { a.Unwatch(addr) },
		StyleFunc: func() core.Style { return core.Style{Background: colors.Background()} },
		Children:  []core.Node{a.sessionBody(cx, addr)},
	}, ui.NavHeader{Title: t.Label()})
}

func (a *App) sessionBody(cx *core.Scope, addr string) core.Node {
	colors := ui.ColorsOf(cx)
	return core.View{
		Style: core.Style{Width: core.Pct(100), Height: core.Pct(100), Direction: core.Column},
		Children: []core.Node{
			ui.ScrollView{
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, FlexBasis: core.Pt(core.Points(0))},
				ContentStyle: core.Style{Padding: core.All(12)},
				Children: []core.Node{core.Text{
					ContentFunc: func() string {
						text := a.Text(addr)
						if strings.TrimSpace(text) == "" {
							return "正在读取这一屏…"
						}
						return text
					},
					StyleFunc: func() core.Style {
						return core.Style{
							FontFamily: core.FontMonospace,
							FontSize:   core.Points(12),
							LineHeight: core.Points(17),
							Color:      colors.Text(),
						}
					},
				}},
			},
			a.keyBar(cx, addr),
			a.composer(cx, addr),
		},
	}
}

// keyBar is the one-tap keys a phone otherwise cannot type.
func (a *App) keyBar(cx *core.Scope, addr string) core.Node {
	var keys []core.Node
	for _, k := range []struct{ label, key string }{
		{"Enter", "enter"},
		{"Esc", "esc"},
		{"Ctrl+C", "ctrl+c"},
		{"Tab", "tab"},
		{"↑", "up"},
		{"↓", "down"},
		{"Ctrl+D", "ctrl+d"},
	} {
		k := k
		keys = append(keys, ui.Button{
			Text:    k.label,
			Variant: ui.ButtonGhost,
			OnPress: func() { a.Press(addr, k.key) },
		})
	}
	return ui.ScrollView{
		Horizontal: true,
		Style:      core.Style{MaxHeight: core.Pt(core.Points(44))},
		ContentStyle: core.Style{
			Padding: core.Symmetric(8, 6), Gap: 6, Direction: core.Row, AlignItems: core.AlignCenter,
		},
		Children: keys,
	}
}

// composer types into the session; the draft survives switching away.
func (a *App) composer(cx *core.Scope, addr string) core.Node {
	colors := ui.ColorsOf(cx)
	return core.View{
		StyleFunc: func() core.Style {
			return core.Style{
				Padding:     core.Symmetric(10, 12),
				Gap:         8,
				Direction:   core.Row,
				AlignItems:  core.AlignCenter,
				BorderWidth: core.Points(1),
				BorderColor: colors.Border(),
				Background:  colors.Surface(),
			}
		},
		Children: []core.Node{
			core.View{Style: core.Style{FlexGrow: 1}, Children: []core.Node{
				ui.Input{
					Value:       func() string { return a.Compose(addr) },
					OnChange:    func(s string) { a.SetCompose(addr, s) },
					Placeholder: "发给这个终端…",
					TestID:      "compose",
				},
			}},
			ui.Button{
				Text:    "发送",
				Variant: ui.ButtonPrimary,
				OnPress: func() {
					draft := a.Compose(addr)
					if strings.TrimSpace(draft) == "" {
						return
					}
					a.Send(addr, draft)
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// inbox

func (a *App) inboxPage(cx *core.Scope, nav *base.Nav[Route]) base.NavPage {
	colors := ui.ColorsOf(cx)
	return ui.NavPage(cx, base.NavPage{
		Title:           "收件箱",
		HeaderBackTitle: "首页",
		HeaderStyle:     core.Style{Background: colors.Background()},
		StyleFunc:       func() core.Style { return core.Style{Background: colors.Background()} },
		Children:        []core.Node{a.inboxBody(cx, nav)},
	}, ui.NavHeader{Title: "收件箱"})
}

func (a *App) inboxBody(cx *core.Scope, nav *base.Nav[Route]) core.Node {
	colors := ui.ColorsOf(cx)
	items := a.Attention()
	rows := []core.Node{
		ui.Input{
			Value:       func() string { return a.filter.Get() },
			OnChange:    a.filter.Set,
			Placeholder: "按名字、目录、地址筛选",
		},
	}
	if len(items) == 0 {
		rows = append(rows, emptyCard(cx,
			"没有等你的事",
			"agent 卡在要审批、要回答、或出错时才会出现在这里。平时它自己跑。",
			"看所有会话",
			func() { nav.Pop(1) },
		))
	}
	for _, t := range items {
		t := t
		rows = append(rows, core.View{
			OnPress: func(core.PressEvent) { nav.Push(Route{Kind: "session", Addr: t.Address}) },
			Style: core.Style{
				Padding: core.All(14), Gap: 4, CornerRadius: core.Points(14),
				Background: colors.Surface(), BorderWidth: core.Points(1), BorderColor: accent(),
			},
			Children: []core.Node{
				core.Text{
					ContentFunc: func() string { return t.Label() + " · " + attentionWhy(t) },
					Style:       core.Style{FontSize: core.Points(15), FontWeight: core.FontWeightSemibold, Color: colors.Text()},
				},
				core.Text{
					Content: firstLine(t.Status, t),
					Style:   core.Style{FontSize: core.Points(12), Color: colors.Muted()},
				},
			},
		})
	}
	return ui.ScrollView{
		Style:        core.Style{Width: core.Pct(100), Height: core.Pct(100)},
		ContentStyle: core.Style{Padding: core.All(16), Gap: 12},
		Children:     rows,
	}
}

// ---------------------------------------------------------------------------
// add a machine

func (a *App) addPage(cx *core.Scope, nav *base.Nav[Route]) base.NavPage {
	colors := ui.ColorsOf(cx)
	target := core.NewSignal(cx, "")
	user := core.NewSignal(cx, "")
	pass := core.NewSignal(cx, "")
	key := core.NewSignal(cx, "")

	submit := func() {
		if strings.TrimSpace(target.Get()) == "" {
			return
		}
		a.SaveHost(Host{
			Label:    target.Get(),
			Target:   strings.TrimSpace(target.Get()),
			User:     strings.TrimSpace(user.Get()),
			Password: pass.Get(),
			Key:      key.Get(),
		})
		nav.Pop(1)
	}

	return ui.NavPage(cx, base.NavPage{
		Title:           "添加机器",
		HeaderBackTitle: "首页",
		HeaderStyle:     core.Style{Background: colors.Background()},
		StyleFunc:       func() core.Style { return core.Style{Background: colors.Background()} },
		Children: []core.Node{ui.ScrollView{
			Style:        core.Style{Width: core.Pct(100), Height: core.Pct(100)},
			ContentStyle: core.Style{Padding: core.All(16), Gap: 16},
			Children: []core.Node{
				// Moshi's pattern: name the cost of each path, don't ask
				// "which do you prefer".
				optionCard(cx, "SSH 连接", "推荐 · 约1分钟",
					"填主机名、用户名和密码。它会在远端跑 `hc connect`，不需要开放端口。"),
				field(cx, "主机（host 或 host:port）", target.Get, target.Set),
				field(cx, "用户名", user.Get, user.Set),
				field(cx, "密码（用私钥时可留空）", pass.Get, pass.Set),
				ui.Input{
					Value:       key.Get,
					OnChange:    key.Set,
					Multiline:   true,
					Placeholder: "或粘贴整段 OpenSSH 私钥（含 BEGIN/END 行）",
					TestID:      "key",
				},
				ui.Button{Text: "连接", Variant: ui.ButtonPrimary, OnPress: submit},
				optionCard(cx, "本机调试", "只用于桌面端 · 需要本机装 herdr-core",
					"不用填表：直接在运行 herdr-nsgo 的这台机器上跑 `hc connect`。"),
				ui.Button{
					Text:    "用本机",
					Variant: ui.ButtonSecondary,
					OnPress: func() {
						a.SaveHost(Host{Label: "local", Target: "local"})
						nav.Pop(1)
					},
				},
				core.Text{
					Content: "密码只存在这台设备上，随连接一起用；后续版本改用密钥与扫码配对。",
					Style:   core.Style{FontSize: core.Points(12), Color: colors.Muted()},
				},
			},
		}},
	}, ui.NavHeader{Title: "添加机器"})
}

// ---------------------------------------------------------------------------
// pieces

func sectionTitle(cx *core.Scope, text string) core.Node {
	colors := ui.ColorsOf(cx)
	return core.Text{
		Content: text,
		Style: core.Style{
			FontSize: core.Points(12), FontWeight: core.FontWeightSemibold,
			Color: colors.Muted(),
		},
	}
}

// emptyCard is an empty state that says what will be here and what to do.
func emptyCard(cx *core.Scope, title, body, cta string, onPress func()) core.Node {
	colors := ui.ColorsOf(cx)
	return core.View{
		Style: core.Style{
			Padding: core.All(18), Gap: 8, CornerRadius: core.Points(16),
			BorderWidth: core.Points(1), BorderColor: colors.Border(),
			Background: colors.Surface(),
		},
		Children: []core.Node{
			core.Text{Content: title, Style: core.Style{FontSize: core.Points(16), FontWeight: core.FontWeightSemibold, Color: colors.Text()}},
			core.Text{Content: body, Style: core.Style{FontSize: core.Points(13), LineHeight: core.Points(19), Color: colors.Muted()}},
			core.View{
				Style:    core.Style{Padding: core.Symmetric(core.Points(6), core.Points(0)), Direction: core.Row},
				Children: []core.Node{ui.Button{Text: cta, Variant: ui.ButtonPrimary, OnPress: onPress}},
			},
		},
	}
}

func optionCard(cx *core.Scope, title, cost, body string) core.Node {
	colors := ui.ColorsOf(cx)
	return core.View{
		Style: core.Style{
			Padding: core.All(14), Gap: 4, CornerRadius: core.Points(14),
			Background: colors.Surface(),
		},
		Children: []core.Node{
			core.Text{Content: cost, Style: core.Style{FontSize: core.Points(11), FontWeight: core.FontWeightSemibold, Color: accent()}},
			core.Text{Content: title, Style: core.Style{FontSize: core.Points(15), FontWeight: core.FontWeightSemibold, Color: colors.Text()}},
			core.Text{Content: body, Style: core.Style{FontSize: core.Points(12), LineHeight: core.Points(18), Color: colors.Muted()}},
		},
	}
}

func field(cx *core.Scope, placeholder string, get func() string, set func(string)) core.Node {
	return ui.Input{Value: get, OnChange: set, Placeholder: placeholder}
}

func stateWord(t Terminal) string {
	switch t.StateWord() {
	case "live":
		// The terminal is alive but its program reports no status word.
		return "运行中"
	case "working":
		return "跑着"
	case "blocked":
		return "等你"
	case "idle":
		return "空闲"
	case "done":
		return "结束"
	case "error":
		return "出错"
	}
	return t.StateWord()
}

func stateColor(cx *core.Scope, t Terminal) core.Color {
	colors := ui.ColorsOf(cx)
	switch t.StateWord() {
	case "working":
		return accent()
	case "blocked", "error":
		return core.RGB(0xffb02e)
	case "done":
		return colors.Muted()
	}
	return colors.Faint()
}

func detailLine(t Terminal) string {
	if t.Status != nil && t.Status.Msg != "" {
		return t.Status.Msg
	}
	if t.Cwd != "" {
		return t.Cwd
	}
	return t.Address
}

func attentionWhy(t Terminal) string {
	switch t.StateWord() {
	case "blocked":
		if t.Status != nil && t.Status.Kind != "" {
			switch t.Status.Kind {
			case "permission":
				return "等审批"
			case "question":
				return "等你回答"
			case "auth":
				return "等你登录"
			}
			return "等你 · " + t.Status.Kind
		}
		return "等你"
	case "error":
		return "出错"
	}
	return t.StateWord()
}

func firstLine(st *Status, t Terminal) string {
	if st != nil {
		if st.Msg != "" {
			return st.Msg
		}
		if st.Title != "" {
			return st.Title
		}
	}
	return detailLine(t)
}
