package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

type pickerPage struct {
	rows       []string
	cursor     int
	effort     bool
	sessionKey bool
	more       bool
	current    string
}

var numberedChoice = regexp.MustCompile(`^\d+[.)]\s+(.+)$`)
var columnGap = regexp.MustCompile(`\s{2,}`)
var piPageNumber = regexp.MustCompile(`(?m)^\s*\(\d+/\d+\)\s*$`)
var sliderLabel = regexp.MustCompile(`[a-z]+`)
var piEffortChoice = regexp.MustCompile(`^(off|minimal|low|medium|high|xhigh|max)\s{2,}`)

// Only interpret recognized selector screens. A transcript or a permission prompt must
// never become the source of a confirmation key.
func parsePickerPage(screen, kind string) (pickerPage, bool) {
	p := pickerPage{cursor: -1}
	low := strings.ToLower(screen)
	p.sessionKey = strings.Contains(low, "s to use this session only") || strings.Contains(low, "s session")
	p.more = piPageNumber.MatchString(screen) || strings.Contains(screen, "\n↓") || strings.Contains(screen, "\n↑")
	if kind == "claude" && strings.Contains(low, "←/→ to adjust") && strings.Contains(low, "esc to cancel") && strings.Contains(low, "smarter") {
		lines := strings.Split(screen, "\n")
		for i, line := range lines {
			mark := strings.IndexRune(line, '▲')
			if mark < 0 || i+1 >= len(lines) {
				continue
			}
			col := len([]rune(line[:mark]))
			labels := lines[i+1]
			best := 1 << 30
			for _, span := range sliderLabel.FindAllStringIndex(labels, -1) {
				word := labels[span[0]:span[1]]
				if !slices.Contains([]string{"off", "auto", "low", "medium", "high", "xhigh", "max"}, word) {
					return p, false
				}
				distance := (span[0]+span[1]-1)/2 - col
				if distance < 0 {
					distance = -distance
				}
				if distance < best {
					best = distance
					p.cursor = len(p.rows)
					p.current = word
				}
				p.rows = append(p.rows, word)
			}
			p.effort = true
			return p, p.cursor >= 0
		}
		return p, false
	}
	if kind == "pi" {
		if !strings.Contains(low, "enter to select") || !strings.Contains(low, "to cancel") {
			return p, false
		}
		p.effort = strings.Contains(screen, "Thinking Level") && strings.Contains(low, "cycles thinking levels")
		if !p.effort && !strings.Contains(screen, "Model Name:") && !strings.Contains(low, "select model") {
			return p, false
		}
	} else {
		p.effort = strings.Contains(low, "select reasoning effort") || strings.Contains(low, "select reasoning level") || strings.Contains(low, "select effort")
		if !p.effort && !strings.Contains(low, "select model") && !strings.Contains(low, "select a model") {
			return p, false
		}
		if !strings.Contains(low, "esc") {
			return p, false
		}
	}
	for line := range strings.SplitSeq(screen, "\n") {
		s := strings.TrimSpace(line)
		active := strings.HasPrefix(s, "→") || strings.HasPrefix(s, "❯") || strings.HasPrefix(s, "›")
		if active {
			s = strings.TrimSpace(strings.TrimLeft(s, "→❯›"))
		}
		marked := strings.HasPrefix(s, "✓") || strings.Contains(s, " ✔") || strings.Contains(s, " (current)")
		s = strings.TrimSpace(strings.TrimPrefix(s, "✓"))
		if kind == "pi" {
			if p.effort {
				m := piEffortChoice.FindStringSubmatch(s)
				if m == nil {
					continue
				}
				s = m[1]
			} else {
				// Pi renders the ID, model description, then the provider in square brackets.
				if !strings.Contains(s, " [") || !strings.Contains(s, "]") {
					continue
				}
				f := strings.Fields(s)
				if len(f) < 2 || strings.ContainsAny(f[0], "[]()") {
					continue
				}
				s = strings.TrimSpace(strings.Split(s, " · default")[0])
			}
		} else if kind == "claude" && strings.TrimSpace(strings.TrimLeft(s, "▸▾ ")) == "More models" {
			s = "More models"
		} else {
			m := numberedChoice.FindStringSubmatch(s)
			if m == nil {
				continue
			}
			s = strings.TrimSpace(columnGap.Split(m[1], 2)[0])
			s = strings.TrimSpace(strings.ReplaceAll(s, " ✔", ""))
		}
		if active {
			p.cursor = len(p.rows)
		}
		if marked {
			p.current = s
		}
		p.rows = append(p.rows, s)
	}
	return p, p.cursor >= 0 && len(p.rows) > 0
}

type pickerTransport interface {
	Read(context.Context, string, int) (ReadResult, error)
	Send(context.Context, string, string) (SendResult, error)
	Keys(context.Context, string, ...string) (KeysResult, error)
}

type remotePicker struct {
	client     pickerTransport
	addr, kind string
	mu         sync.Mutex
	opened     bool
}

func (r *remotePicker) read(ctx context.Context) (pickerPage, bool, error) {
	v, err := r.client.Read(ctx, r.addr, 0)
	p, ok := parsePickerPage(v.Text, r.kind)
	return p, ok, err
}
func (r *remotePicker) wait(ctx context.Context, accept func(pickerPage, bool) bool) (pickerPage, error) {
	for {
		p, ok, err := r.read(ctx)
		if err != nil {
			return p, err
		}
		if accept(p, ok) {
			return p, nil
		}
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case <-time.After(90 * time.Millisecond):
		}
	}
}
func (r *remotePicker) open(ctx context.Context, effort bool) (pickerPage, error) {
	result, err := r.client.Read(ctx, r.addr, 0)
	if err != nil {
		return pickerPage{}, err
	}
	if p, ok := parsePickerPage(result.Text, r.kind); ok {
		r.opened = true // Adopt an existing selector, including one left by the previous client.
		if r.kind == "codex" || p.effort == effort {
			return p, nil
		}
		if _, err = r.client.Keys(ctx, r.addr, "esc"); err != nil {
			return p, err
		}
		if _, err = r.wait(ctx, func(_ pickerPage, ok bool) bool { return !ok }); err != nil {
			return p, err
		}
	} else if looksLikeOpenDialog(result.Text) {
		return pickerPage{}, errors.New("远端已有待处理的对话框，不能在此时打开设置")
	}
	r.opened = true
	if _, err := r.client.Send(ctx, r.addr, agentSelectorCommand(r.kind, effort)); err != nil {
		return pickerPage{}, err
	}
	return r.wait(ctx, func(p pickerPage, ok bool) bool { return ok })
}
func looksLikeOpenDialog(screen string) bool {
	s := strings.ToLower(screen)
	return strings.Contains(s, "esc to cancel") || strings.Contains(s, "enter to select") || strings.Contains(s, "esc close") || strings.Contains(s, "esc back") || strings.Contains(s, "esc to go back")
}

func (r *remotePicker) move(ctx context.Context, p pickerPage, key string) (pickerPage, error) {
	if _, err := r.client.Keys(ctx, r.addr, key); err != nil {
		return p, err
	}
	return r.wait(ctx, func(next pickerPage, ok bool) bool {
		return ok && next.effort == p.effort && next.rows[next.cursor] != p.rows[p.cursor]
	})
}
func (r *remotePicker) apply(ctx context.Context, shown pickerPage, target string) (pickerPage, error) {
	p, ok, err := r.read(ctx)
	if err != nil {
		return p, err
	}
	if !ok || p.effort != shown.effort {
		return p, errors.New("远端选项已变化，请重新打开")
	}
	for range len(shown.rows) + 1 {
		idx := slices.Index(p.rows, target)
		if idx < 0 {
			return p, errors.New("所选项已不在当前列表，请重新选择")
		}
		if idx == p.cursor {
			confirm := "enter"
			if p.sessionKey && !strings.HasPrefix(target, "More ") {
				confirm = "s"
			}
			if _, err = r.client.Keys(ctx, r.addr, confirm); err != nil {
				return p, err
			}
			next, err := r.wait(ctx, func(next pickerPage, ok bool) bool {
				if strings.HasPrefix(target, "More ") {
					return ok && (!slices.Equal(p.rows, next.rows) || next.effort != p.effort)
				}
				if r.kind == "codex" && !p.effort {
					return ok && next.effort
				}
				return !ok || next.effort != p.effort
			})
			if err == nil && next.cursor < 0 {
				r.opened = false
			}
			return next, err
		}
		key := "down"
		if idx < p.cursor {
			key = "up"
		}
		if r.kind == "claude" && p.effort {
			if key == "up" {
				key = "left"
			} else {
				key = "right"
			}
		}
		p, err = r.move(ctx, p, key)
		if err != nil {
			return p, err
		}
	}
	return p, errors.New("未能定位所选项")
}
func (r *remotePicker) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opened {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p, ok, err := r.read(ctx)
	for steps := 0; steps < 3 && err == nil && ok; steps++ {
		if _, err = r.client.Keys(ctx, r.addr, "esc"); err != nil {
			return
		}
		old := p
		p, err = r.wait(ctx, func(next pickerPage, visible bool) bool {
			return !visible || next.effort != old.effort || !slices.Equal(next.rows, old.rows)
		})
		ok = p.cursor >= 0
	}
}

type pickerChoice struct {
	ID          string
	Label       string
	Detail      string
	DirectValue string
}

func mobileAgentPicker(cx *core.Scope, a *App, t AggregatedTerminal, liveModel string, effort bool, closeSheet func()) core.Node {
	kind := DetectHarnessKind(t.Terminal)
	title := "模型"
	if effort {
		title = "思考强度"
	}
	c := a.pool.ClientFor(t.Key.InstanceID)
	r := &remotePicker{client: c, addr: t.Key.Addr, kind: kind}
	life := cx.Context()

	// 0ms instant options from persistent cache + built-in presets + live transcript model.
	catalog := core.NewSignalFunc(cx, a.InstantCatalogFor(t.Key.InstanceID, kind, liveModel), nil)
	tuiMode := core.NewSignal(cx, false)
	busy := core.NewSignal(cx, false)
	refreshing := core.NewSignal(cx, false)
	notice := core.NewSignal(cx, "")
	chosen := core.NewSignal(cx, "")
	chosenLabel := core.NewSignal(cx, "")
	page := core.NewSignalFunc(cx, pickerPage{cursor: -1}, nil)

	// Silently refresh host model metadata in the background via fast read-only MachineExec (zero TUI disruption).
	if c != nil {
		refreshing.Set(true)
		a.PrefetchAgentCatalog(t.Key, t.Terminal, func(updated AgentCatalog) {
			if life.Err() != nil {
				return
			}
			refreshing.Set(false)
			if liveModel != "" && updated.DefaultModel == "" {
				updated.DefaultModel = liveModel
			}
			catalog.Set(updated)
		})
		// Clear subtle refreshing indicator if prefetch skipped/cached or timed out.
		time.AfterFunc(1200*time.Millisecond, func() {
			if life.Err() == nil {
				cx.Post(func() { refreshing.Set(false) })
			}
		})
	}

	currentValue := func() string {
		if tuiMode.Get() {
			return page.Get().current
		}
		if effort {
			if s := a.SessionEffort(t.Key); s != "" {
				return s
			}
			return catalog.Get().DefaultEffort
		}
		if s := a.SessionModel(t.Key); s != "" {
			return s
		}
		if liveModel != "" {
			return liveModel
		}
		return catalog.Get().DefaultModel
	}

	isCurrentChoice := func(ch pickerChoice) bool {
		cur := strings.ToLower(strings.TrimSpace(currentValue()))
		if cur == "" {
			return false
		}
		idLow := strings.ToLower(ch.ID)
		valLow := strings.ToLower(ch.DirectValue)
		if cur == idLow || cur == valLow || cur == strings.ToLower(ch.Label) {
			return true
		}
		if !effort && kind == "claude" && (idLow == "opus" || idLow == "sonnet" || idLow == "haiku") {
			return strings.Contains(cur, idLow)
		}
		return false
	}

	choices := func() []pickerChoice {
		if tuiMode.Get() {
			rows := page.Get().rows
			out := make([]pickerChoice, 0, len(rows))
			for _, row := range rows {
				lbl, det := row, ""
				if effort || page.Get().effort {
					lbl, det = EffortDisplay(row)
				}
				out = append(out, pickerChoice{ID: row, Label: lbl, Detail: det, DirectValue: row})
			}
			return out
		}
		cat := catalog.Get()
		if effort {
			effs := cat.Efforts
			if kind == "codex" {
				curMod := strings.ToLower(strings.TrimSpace(currentValue()))
				if m := a.SessionModel(t.Key); m != "" {
					curMod = strings.ToLower(m)
				} else if liveModel != "" {
					curMod = strings.ToLower(liveModel)
				} else if cat.DefaultModel != "" {
					curMod = strings.ToLower(cat.DefaultModel)
				}
				for _, m := range cat.Models {
					if strings.EqualFold(m.ID, curMod) && len(m.Efforts) > 0 {
						effs = m.Efforts
						break
					}
				}
			}
			out := make([]pickerChoice, 0, len(effs))
			for _, lv := range effs {
				lbl, det := EffortDisplay(lv)
				out = append(out, pickerChoice{ID: lv, Label: lbl, Detail: det, DirectValue: lv})
			}
			return out
		}
		out := make([]pickerChoice, 0, len(cat.Models))
		for _, m := range cat.Models {
			dv := m.DirectValue
			if dv == "" {
				dv = m.ID
			}
			lbl := m.Label
			if lbl == "" {
				lbl = m.ID
			}
			out = append(out, pickerChoice{ID: m.ID, Label: lbl, Detail: m.Detail, DirectValue: dv})
		}
		return out
	}

	runTUI := func(fn func(context.Context) (pickerPage, error), applying bool) {
		if busy.Get() {
			return
		}
		if c == nil {
			notice.Set("实例离线，连接后可同步远端菜单")
			return
		}
		tuiMode.Set(true)
		busy.Set(true)
		notice.Set("")
		go func() {
			r.mu.Lock()
			ctx, cancel := context.WithTimeout(life, 15*time.Second)
			p, err := fn(ctx)
			cancel()
			r.mu.Unlock()
			cx.Post(func() {
				if life.Err() != nil {
					return
				}
				busy.Set(false)
				if err != nil {
					page.Set(pickerPage{cursor: -1})
					notice.Set("未能读取实时菜单：" + err.Error())
					chosen.Set("")
					return
				}
				if applying && p.cursor < 0 {
					field := "模型"
					if page.Get().effort {
						field = "思考强度"
						a.SetSessionEffort(t.Key, chosen.Get())
					} else {
						a.SetSessionModel(t.Key, chosen.Get())
					}
					cx.Announce(field + "已提交：" + chosen.Get())
					a.notifyToast("已切换" + field + "：" + chosen.Get())
					closeSheet()
					return
				}
				page.Set(p)
				chosen.Set("")
			})
		}()
	}

	applyDirect := func(targetID, targetLabel string) {
		if busy.Get() {
			return
		}
		if c == nil {
			notice.Set("实例离线，请待恢复连接后再切换")
			return
		}
		cmd, ok := DirectApplyCommand(kind, effort, targetID)
		if !ok {
			runTUI(func(ctx context.Context) (pickerPage, error) { return r.open(ctx, effort) }, false)
			return
		}
		busy.Set(true)
		notice.Set("")
		go func() {
			ctx, cancel := context.WithTimeout(life, 10*time.Second)
			defer cancel()
			_, err := c.Send(ctx, t.Key.Addr, cmd)
			cx.Post(func() {
				if life.Err() != nil {
					return
				}
				busy.Set(false)
				if err != nil {
					notice.Set("切换失败：" + explain(err))
					return
				}
				display := targetLabel
				if display == "" {
					display = targetID
				}
				if effort {
					a.SetSessionEffort(t.Key, targetID)
					cx.Announce("思考强度已切换：" + display)
					a.notifyToast("已切换思考强度：" + display)
				} else {
					a.SetSessionModel(t.Key, targetID)
					cx.Announce("模型已切换：" + display)
					a.notifyToast("已切换模型：" + ShortModelLabel(display))
				}
				closeSheet()
			})
		}()
	}

	cx.Cleanup(func() {
		if c != nil {
			go r.cleanup()
		}
	})

	statusRow := core.View{
		Style: core.Style{
			Direction:      core.Row,
			AlignItems:     core.AlignCenter,
			JustifyContent: core.JustifySpaceBetween,
			Gap:            Tokens.Space2,
		},
		Children: []core.Node{
			core.View{
				Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2, FlexShrink: 1, MinWidth: core.Pt(0)},
				Children: []core.Node{
					core.Show{
						When: func() bool { return busy.Get() || refreshing.Get() },
						Then: func(cx *core.Scope) core.Node {
							return ui.Spinner{Size: 13, Variant: ui.SpinnerSpokes, Label: "同步中", Tint: func() core.Color { return a.paletteOf(cx).Running }}
						},
					},
					core.Text{
						MaxLines: 1,
						ContentFunc: func() string {
							if busy.Get() {
								if tuiMode.Get() {
									return "正在同步终端实时菜单…"
								}
								return "正在极速切换…"
							}
							if notice.Get() != "" {
								return notice.Get()
							}
							if chosen.Get() != "" {
								lbl := chosenLabel.Get()
								if lbl == "" {
									lbl = chosen.Get()
								}
								return "待应用 · " + lbl
							}
							if cur := currentValue(); cur != "" {
								return "当前 · " + ShortModelLabel(cur)
							}
							return "点选一项即可快速切换"
						},
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							fg := pal.TextSecondary
							if notice.Get() != "" {
								fg = pal.Danger
							}
							return core.Style{FontSize: Tokens.FontCaption, Color: fg, FlexShrink: 1, MinWidth: core.Pt(0)}
						},
					},
				},
			},
			core.Show{
				When: func() bool { return c != nil && !busy.Get() },
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						TestID: "model-sync-tui",
						Role:   core.RoleButton,
						OnPress: func(core.PressEvent) {
							playSelectionHaptic(cx)
							runTUI(func(ctx context.Context) (pickerPage, error) { return r.open(ctx, effort) }, false)
						},
						Style: core.Style{
							Direction:  core.Row,
							AlignItems: core.AlignCenter,
							Gap:        core.Points(4),
							Padding:    core.Symmetric(core.Points(4), core.Points(6)),
						},
						Children: []core.Node{
							core.Icon{
								SVG: iconRefresh,
								StyleFunc: func() core.Style {
									return core.Style{Width: core.Pt(13), Height: core.Pt(13), Color: a.paletteOf(cx).TextMuted}
								},
							},
							core.Text{
								Content: "终端菜单",
								StyleFunc: func() core.Style {
									return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: Tokens.FontMonoMeta, FontWeight: core.FontWeightMedium}
								},
							},
						},
					}
				},
			},
		},
	}

	options := core.Show{
		When: func() bool { return len(choices()) > 0 },
		Then: func(cx *core.Scope) core.Node {
			return GroupCard(cx, a, "model-options", []core.Node{
				core.Each(choices, func(ch pickerChoice) string { return ch.ID }, func(cx *core.Scope, itemFn func() pickerChoice, idx func() int) core.Node {
					ch0 := itemFn()
					return core.View{
						Children: []core.Node{
							core.Show{
								When: func() bool { return idx() > 0 },
								Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 14) },
							},
							core.View{
								Role:     core.RoleButton,
								TestID:   "model-option-" + ch0.ID,
								Disabled: busy.Get,
								OnPress: func(core.PressEvent) {
									playSelectionHaptic(cx)
									ch := itemFn()
									chosen.Set(ch.DirectValue)
									chosenLabel.Set(ch.Label)
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									ch := itemFn()
									bg := core.Transparent
									if chosen.Get() == ch.DirectValue {
										bg = pal.AccentSoft
									}
									return core.Style{
										Direction:      core.Row,
										AlignItems:     core.AlignCenter,
										JustifyContent: core.JustifySpaceBetween,
										Gap:            Tokens.Space2,
										MinHeight:      core.Pt(46),
										Padding:        core.Symmetric(core.Points(10), core.Points(14)),
										Background:     bg,
									}
								},
								Children: []core.Node{
									core.View{
										Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
										Children: []core.Node{
											core.Text{
												MaxLines:    1,
												ContentFunc: func() string { return itemFn().Label },
												StyleFunc: func() core.Style {
													return core.Style{
														FontSize:   Tokens.FontRowTitle,
														FontWeight: core.FontWeightMedium,
														Color:      a.paletteOf(cx).Text,
													}
												},
											},
											core.Show{
												When: func() bool { return itemFn().Detail != "" },
												Then: func(cx *core.Scope) core.Node {
													return core.Text{
														MaxLines:    1,
														ContentFunc: func() string { return itemFn().Detail },
														StyleFunc: func() core.Style {
															return core.Style{
																FontSize: Tokens.FontMonoMeta,
																Color:    a.paletteOf(cx).TextMuted,
															}
														},
													}
												},
											},
										},
									},
									core.Show{
										When: func() bool {
											ch := itemFn()
											return chosen.Get() == ch.DirectValue || (chosen.Get() == "" && isCurrentChoice(ch))
										},
										Then: func(cx *core.Scope) core.Node {
											return core.Icon{
												SVG: iconCheck,
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													fg := pal.Text
													if chosen.Get() == "" {
														fg = pal.Running
													}
													return core.Style{Width: core.Pt(16), Height: core.Pt(16), FlexShrink: 0, Color: fg}
												},
											}
										},
									},
								},
							},
						},
					}
				}),
			})
		},
		Else: func(cx *core.Scope) core.Node {
			return GroupCard(cx, a, "", []core.Node{
				core.View{
					Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space2},
					Children: []core.Node{
						core.Show{
							When: func() bool { return busy.Get() || refreshing.Get() },
							Then: func(cx *core.Scope) core.Node {
								return ui.Skeleton{Lines: 3, Label: "正在读取模型列表"}
							},
							Else: func(cx *core.Scope) core.Node {
								return core.Text{
									Content: "当前未发现本地模型缓存，可点上方「终端菜单」从远端拉取完整列表。",
									StyleFunc: func() core.Style {
										return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: Tokens.FontCaption}
									},
								}
							},
						},
					},
				},
			})
		},
	}

	paging := core.Show{When: func() bool { return tuiMode.Get() && page.Get().more && !busy.Get() }, Then: func(cx *core.Scope) core.Node {
		var buttons []core.Node
		for _, key := range []string{"up", "down"} {
			label := "下一页"
			if key == "up" {
				label = "上一页"
			}
			buttons = append(buttons, ActionButton(cx, a, "model-page-"+key, label, ActionSecondary, func() {
				runTUI(func(ctx context.Context) (pickerPage, error) {
					p, ok, err := r.read(ctx)
					if err != nil {
						return p, err
					}
					if !ok {
						return p, errors.New("选择器已关闭")
					}
					old := p.rows
					for range len(old) + 1 {
						p, err = r.move(ctx, p, key)
						if err != nil || !slices.Equal(old, p.rows) {
							return p, err
						}
					}
					return p, nil
				}, false)
			}))
		}
		return core.View{Style: core.Style{Direction: core.Row, Gap: Tokens.Space2}, Children: buttons}
	}}

	apply := core.Show{When: func() bool { return chosen.Get() != "" && !busy.Get() }, Then: func(cx *core.Scope) core.Node {
		return ActionButton(cx, a, "model-apply", "应用", ActionPrimary, func() {
			target, lbl := chosen.Get(), chosenLabel.Get()
			if tuiMode.Get() {
				shown := page.Get()
				runTUI(func(ctx context.Context) (pickerPage, error) {
					return r.apply(ctx, shown, target)
				}, true)
				return
			}
			applyDirect(target, lbl)
		})
	}}

	label := fmt.Sprintf("%s · %s", title, kind)
	return SheetModal(cx, a, "chat-model-sheet", label, "chat-model-close", closeSheet, []core.Node{
		statusRow,
		options,
		paging,
		apply,
		core.Show{When: func() bool { return tuiMode.Get() && !busy.Get() && notice.Get() != "" && c != nil }, Then: func(cx *core.Scope) core.Node {
			return ActionButton(cx, a, "model-reload", "重新读取菜单", ActionSecondary, func() {
				runTUI(func(ctx context.Context) (pickerPage, error) { return r.open(ctx, effort) }, false)
			})
		}},
	})
}
