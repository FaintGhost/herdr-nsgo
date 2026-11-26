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

func mobileAgentPicker(cx *core.Scope, a *App, t AggregatedTerminal, effort bool, closeSheet func()) core.Node {
	kind := DetectHarnessKind(t.Terminal)
	title := "模型"
	if effort {
		title = "思考强度"
	}
	c := a.pool.ClientFor(t.Key.InstanceID)
	r := &remotePicker{client: c, addr: t.Key.Addr, kind: kind}
	life := cx.Context()
	busy := core.NewSignal(cx, false)
	notice := core.NewSignal(cx, "")
	chosen := core.NewSignal(cx, "")
	page := core.NewSignalFunc(cx, pickerPage{cursor: -1}, nil)
	run := func(fn func(context.Context) (pickerPage, error), applying bool) {
		if busy.Get() {
			return
		}
		if c == nil {
			notice.Set("实例离线，连接后可读取选项")
			return
		}
		busy.Set(true)
		notice.Set("")
		go func() {
			r.mu.Lock()
			ctx, cancel := context.WithTimeout(life, 20*time.Second)
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
					notice.Set("未能确认远端状态：" + err.Error())
					chosen.Set("")
					return
				}
				if applying && p.cursor < 0 {
					field := "模型"
					if page.Get().effort {
						field = "思考强度"
					}
					cx.Announce(field + "已提交：" + chosen.Get())
					closeSheet()
					return
				}
				page.Set(p)
				chosen.Set("")
			})
		}()
	}
	cx.Cleanup(func() {
		if c != nil {
			go r.cleanup()
		}
	})
	load := func() { run(func(ctx context.Context) (pickerPage, error) { return r.open(ctx, effort) }, false) }
	load()
	status := core.Text{
		ContentFunc: func() string {
			if busy.Get() {
				return "正在同步远端选项…"
			}
			if notice.Get() != "" {
				return notice.Get()
			}
			if chosen.Get() != "" {
				return "待应用 · " + chosen.Get()
			}
			if kind == "codex" && !page.Get().effort {
				return "先选模型，再选推理强度"
			}
			if page.Get().current != "" {
				return "当前 · " + page.Get().current
			}
			return "点选一项，再点应用"
		},
		StyleFunc: func() core.Style {
			return core.Style{FontSize: Tokens.FontCaption, Color: a.paletteOf(cx).TextSecondary}
		},
	}
	options := core.Show{
		When: func() bool { return len(page.Get().rows) > 0 },
		Then: func(cx *core.Scope) core.Node {
			return GroupCard(cx, a, "model-options", []core.Node{
				core.Each(func() []string { return page.Get().rows }, func(s string) string { return s }, func(cx *core.Scope, row func() string, idx func() int) core.Node {
					return core.View{
						Children: []core.Node{
							core.Show{
								When: func() bool { return idx() > 0 },
								Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 14) },
							},
							core.View{
								Role:     core.RoleButton,
								TestID:   "model-option-" + row(),
								Disabled: busy.Get,
								OnPress: func(core.PressEvent) {
									playSelectionHaptic(cx)
									chosen.Set(row())
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									bg := core.Transparent
									if chosen.Get() == row() {
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
									core.Text{
										ContentFunc: row,
										StyleFunc: func() core.Style {
											return core.Style{
												FlexGrow:   1,
												FlexShrink: 1,
												MinWidth:   core.Pt(0),
												FontSize:   Tokens.FontRowTitle,
												Color:      a.paletteOf(cx).Text,
											}
										},
									},
									core.Show{
										When: func() bool {
											r := row()
											return chosen.Get() == r || (chosen.Get() == "" && page.Get().current == r)
										},
										Then: func(cx *core.Scope) core.Node {
											return core.Icon{
												SVG: iconCheck,
												StyleFunc: func() core.Style {
													return core.Style{Width: core.Pt(16), Height: core.Pt(16), FlexShrink: 0, Color: a.paletteOf(cx).Text}
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
	}
	paging := core.Show{When: func() bool { return page.Get().more && !busy.Get() }, Then: func(cx *core.Scope) core.Node {
		var buttons []core.Node
		for _, key := range []string{"up", "down"} {
			label := "下一页"
			if key == "up" {
				label = "上一页"
			}
			buttons = append(buttons, ActionButton(cx, a, "model-page-"+key, label, ActionSecondary, func() {
				run(func(ctx context.Context) (pickerPage, error) {
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
			target, shown := chosen.Get(), page.Get()
			run(func(ctx context.Context) (pickerPage, error) {
				return r.apply(ctx, shown, target)
			}, true)
		})
	}}
	label := fmt.Sprintf("%s · %s", title, kind)
	return SheetModal(cx, a, "chat-model-sheet", label, "chat-model-close", closeSheet, []core.Node{
		status,
		options,
		paging,
		apply,
		core.Show{When: func() bool { return !busy.Get() && notice.Get() != "" && c != nil }, Then: func(cx *core.Scope) core.Node {
			return ActionButton(cx, a, "model-reload", "重新读取选项", ActionSecondary, load)
		}},
	})
}
