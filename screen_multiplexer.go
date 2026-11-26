package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

func (u *uiController) muxPicker(cx *core.Scope, instance, machine, initialDir string, back func()) core.Node {
	a := u.app
	life := cx.Context()
	backend := a.TranscriptBackend()
	kind := core.NewSignal(cx, "tmux")
	rows := core.NewSignalFunc(cx, []muxSession(nil), nil)
	ready := core.NewSignal(cx, false)
	opening := core.NewSignal(cx, false)
	status := core.NewSignal(cx, "")
	query := core.NewSignal(cx, "")
	name := core.NewSignal(cx, "")
	cwd := core.NewSignal(cx, initialDir)
	var request uint64
	load := func(next string) {
		if opening.Get() {
			return
		}
		request++
		id := request
		if kind.Get() != next {
			query.Set("")
		}
		kind.Set(next)
		ready.Set(false)
		rows.Set(nil)
		status.Set("正在读取会话…")
		go func() {
			ctx, cancel := context.WithTimeout(life, 9*time.Second)
			defer cancel()
			items, err := listMuxSessions(ctx, backend, instance, machine, next)
			cx.Post(func() {
				if id != request {
					return
				}
				if err != nil {
					status.Set(err.Error())
					return
				}
				rows.Set(items)
				ready.Set(true)
				if len(items) == 0 {
					status.Set("主机上没有会话")
				} else {
					status.Set(fmt.Sprintf("%d 个会话", len(items)))
				}
			})
		}()
	}
	enter := func(key TermKey) { u.sessionModes[key] = "vt"; u.showNewSession.Set(false); u.openSession(cx, key) }
	connect := func(s muxSession, create bool) {
		if opening.Get() || !ready.Get() {
			return
		}
		k := kind.Get()
		params, err := muxRunParams(k, machine, strings.TrimSpace(cwd.Get()), s, create)
		if err != nil {
			status.Set(err.Error())
			return
		}
		opening.Set(true)
		status.Set("正在接入会话…")
		go func() {
			ctx, cancel := context.WithTimeout(life, 15*time.Second)
			defer cancel()
			key, err := a.connectMux(ctx, instance, params, create)
			cx.Post(func() {
				opening.Set(false)
				if err != nil {
					status.Set(explain(err))
					return
				}
				enter(key)
			})
		}()
	}
	load("tmux")
	var tabs []core.Node
	for _, k := range []string{"tmux", "zellij"} {
		tabs = append(tabs, FilterPill(cx, a, "mux-kind-"+k, func() string { return k }, func() bool { return kind.Get() == k }, nil, func() { load(k) }))
	}
	styledInput := func(testID, placeholder string, val func() string, onChange func(string)) core.Node {
		return ui.Input{
			TestID:      testID,
			Placeholder: placeholder,
			Value:       val,
			OnChange:    onChange,
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				return core.Style{
					Width:        core.Pct(100),
					MinHeight:    core.Pt(Tokens.SizeTouch),
					Background:   pal.Surface,
					Color:        pal.Text,
					CornerRadius: Tokens.RadiusCtrl,
					BorderColor:  pal.Border,
					BorderWidth:  1,
				}
			},
		}
	}
	host := instance
	if machine != "" {
		host += " / " + machine
	}
	filteredSessions := func() []muxSession {
		q := strings.ToLower(strings.TrimSpace(query.Get()))
		all := rows.Get()
		if q == "" {
			return all
		}
		var found []muxSession
		for _, s := range all {
			if strings.Contains(strings.ToLower(s.name), q) {
				found = append(found, s)
			}
		}
		return found
	}
	sheet := SheetModal(cx, a, "mux-picker", "主机会话 · "+host, "mux-back", back, []core.Node{
		core.View{
			Style: core.Style{
				Direction:      core.Row,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifySpaceBetween,
				Gap:            Tokens.Space2,
			},
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, Gap: Tokens.Space2}, Children: tabs},
				IconTap(cx, a, "mux-refresh", "刷新", iconRefresh, func() { load(kind.Get()) }),
			},
		},
		core.Text{
			ContentFunc: status.Get,
			StyleFunc: func() core.Style {
				return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: Tokens.FontCaption}
			},
		},
		styledInput("mux-search", "搜索会话", query.Get, query.Set),
		core.Show{
			When: func() bool { return len(filteredSessions()) > 0 },
			Then: func(cx *core.Scope) core.Node {
				return GroupCard(cx, a, "", []core.Node{
					core.Each(filteredSessions, func(s muxSession) string { return s.target }, func(cx *core.Scope, item func() muxSession, idx func() int) core.Node {
						return core.View{
							Children: []core.Node{
								core.Show{
									When: func() bool { return idx() > 0 },
									Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 46) },
								},
								SheetListRowFunc(cx, a, "mux-session-"+item().target, func() string {
									s := item()
									if s.resumable {
										return s.name + " · 可恢复"
									}
									return s.name
								}, nil, iconTerminal, false, true, func() { connect(item(), false) }),
							},
						}
					}),
				})
			},
		},
		core.Show{When: ready.Get, Then: func(cx *core.Scope) core.Node {
			return GroupCard(cx, a, "", []core.Node{
				core.View{
					Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space2},
					Children: []core.Node{
						styledInput("mux-name", "新会话名称", name.Get, name.Set),
						styledInput("mux-cwd", "新会话工作目录（可选）", cwd.Get, cwd.Set),
						ActionButton(cx, a, "mux-create", "创建或接入", ActionPrimary, func() { connect(muxSession{name: strings.TrimSpace(name.Get())}, true) }),
					},
				},
			})
		}},
	})
	sheet.CloseOnSwipe = false
	return sheet
}

func muxControls(cx *core.Scope, a *App, kind string, key TermKey, close func()) core.Node {
	var rows []core.Node
	life := cx.Context()
	busy := false
	for i, action := range muxShortcuts(kind) {
		if i > 0 {
			rows = append(rows, InsetDivider(cx, a, 46))
		}
		act := action
		rows = append(rows, SheetListRow(cx, a, "mux-key-"+act.id, act.label, act.binding, iconTerminal, false, false, func() {
			if busy {
				return
			}
			c := a.pool.ClientFor(key.InstanceID)
			if c == nil {
				a.notifyToast("实例未连接")
				return
			}
			busy = true
			go func() {
				ctx, cancel := context.WithTimeout(life, 5*time.Second)
				defer cancel()
				err := sendMuxShortcut(ctx, c, key.Addr, act)
				cx.Post(func() {
					busy = false
					if err != nil {
						a.notifyToast(explain(err))
						return
					}
					close()
				})
			}()
		}))
	}
	sheet := SheetModal(cx, a, "mux-controls", kind+" · 默认快捷键", "mux-controls-close", close, []core.Node{
		GroupCard(cx, a, "", rows),
	})
	sheet.CloseOnSwipe = false
	return sheet
}
