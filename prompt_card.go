package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
)

// ChatInteractivePromptCard renders the full-featured multi-option permission card (perm-stack)
// or interactive AskUserQuestion / Review card (q-inline-card) anchored at the bottom of Chat View.
// When live screen parsing is unavailable (offline/unrecognized/auth), it degrades safely to InlineApprovalCard.
func ChatInteractivePromptCard(
	cx *core.Scope,
	a *App,
	itemFn func() AggregatedTerminal,
	chatCtrl *ChatSessionController,
	onOpenVT func(),
) core.Node {
	busy := core.NewSignal(cx, false)
	checkedSig := core.NewSignalFunc(cx, map[int]bool{}, nil)
	otherDraft := core.NewSignal(cx, "")
	expandedOtherIdx := core.NewSignal(cx, -1)
	lastFP := ""

	// Trigger live TUI inspection whenever this active session enters blocked or has an unanswered question tool call.
	cx.Effect(func() {
		it := itemFn()
		var pqPtr *PendingQuestion
		if chatCtrl != nil {
			if pq, ok := chatCtrl.PendingQuestionTool(); ok {
				pqCopy := pq
				pqPtr = &pqCopy
			}
		}
		if it.Terminal.StateWord() == "blocked" || pqPtr != nil {
			a.InspectPrompt(it.Key, it.Terminal, pqPtr, false)
		} else if it.Key != (TermKey{}) {
			a.ClearPromptCache(it.Key)
		}
	})

	// Synchronize local multi-select checkboxes and clear inline draft whenever the remote screen fingerprint changes.
	cx.Effect(func() {
		key := itemFn().Key
		p, ok, _ := a.CachedPrompt(key)
		if !ok {
			if lastFP != "" {
				lastFP = ""
				checkedSig.Set(map[int]bool{})
				otherDraft.Set("")
				expandedOtherIdx.Set(-1)
			}
			return
		}
		if p.Fingerprint != lastFP {
			lastFP = p.Fingerprint
			next := make(map[int]bool, len(p.Options))
			for _, o := range p.Options {
				if o.Checked {
					next[o.Index] = true
				}
			}
			checkedSig.Set(next)
			otherDraft.Set("")
			expandedOtherIdx.Set(-1)
		}
	})

	return core.Show{
		When: func() bool {
			_, ok, loading := a.CachedPrompt(itemFn().Key)
			return ok || loading
		},
		Then: func(cx *core.Scope) core.Node {
			return core.Show{
				When: func() bool {
					_, ok, _ := a.CachedPrompt(itemFn().Key)
					return ok
				},
				Then: func(cx *core.Scope) core.Node {
					it0 := itemFn()
					keyStr := it0.Key.String()
					promptFn := func() InteractivePrompt {
						p, _, _ := a.CachedPrompt(itemFn().Key)
						return p
					}
					return core.View{
						TestID: "chat-prompt-card-" + keyStr,
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							p := promptFn()
							bc := pal.WarnSoft
							if p.Kind == PromptKindQuestion || p.Kind == PromptKindReview {
								bc = pal.Border
							}
							return core.Style{
								Background:   pal.Surface,
								CornerRadius: Tokens.RadiusCard,
								BorderWidth:  1,
								BorderColor:  bc,
								Padding:      core.All(core.Points(13)),
								Gap:          core.Points(10),
							}
						},
						Children: []core.Node{
							// Header row: icon + title + live badge / tabs
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
											Gap:        core.Points(6),
											FlexGrow:   1,
											FlexShrink: 1,
											MinWidth:   core.Pt(0),
										},
										Children: []core.Node{
											HarnessBrandIcon(cx, a, func() Terminal { return itemFn().Terminal }, "", 15),
											core.Text{
												MaxLines: 1,
												ContentFunc: func() string {
													p := promptFn()
													switch p.Kind {
													case PromptKindReview:
														return "确认提交回答"
													case PromptKindQuestion:
														if h := harnessDisplayName(p.Harness); h != "" {
															return h + " 向你提问"
														}
														return "等待你回答"
													default:
														if strings.TrimSpace(p.Title) != "" {
															return p.Title
														}
														return "请求执行权限"
													}
												},
												StyleFunc: func() core.Style {
													return core.Style{
														FlexShrink: 1,
														MinWidth:   core.Pt(0),
														Color:      a.paletteOf(cx).Text,
														FontSize:   core.Points(14),
														FontWeight: core.FontWeightSemibold,
													}
												},
											},
											core.View{
												StyleFunc: func() core.Style {
													return core.Style{
														FlexShrink:   0,
														Padding:      core.Symmetric(core.Points(2), core.Points(7)),
														CornerRadius: Tokens.RadiusPill,
														Background:   a.paletteOf(cx).RunningSoft,
													}
												},
												Children: []core.Node{
													core.Text{
														MaxLines: 1,
														ContentFunc: func() string {
															p := promptFn()
															if p.Kind == PromptKindQuestion {
																if p.MultiSelect {
																	return "多选"
																}
																return "单选"
															}
															if p.Kind == PromptKindReview {
																return "复核"
															}
															return fmt.Sprintf("实时 %d 项", len(p.Options))
														},
														StyleFunc: func() core.Style {
															return core.Style{
																Color:      a.paletteOf(cx).Running,
																FontSize:   core.Points(11),
																FontFamily: core.FontMonospace,
																FontWeight: core.FontWeightMedium,
															}
														},
													},
												},
											},
										},
									},
									core.Show{
										When: func() bool { return promptFn().Kind == PromptKindPermission },
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												StyleFunc: func() core.Style {
													return core.Style{
														FlexShrink:   0,
														Padding:      core.Symmetric(core.Points(2), core.Points(8)),
														CornerRadius: Tokens.RadiusPill,
														Background:   a.paletteOf(cx).WarnSoft,
													}
												},
												Children: []core.Node{
													core.Text{
														Content: "待审批",
														StyleFunc: func() core.Style {
															return core.Style{
																Color:      a.paletteOf(cx).Warn,
																FontSize:   core.Points(11),
																FontWeight: core.FontWeightSemibold,
															}
														},
													},
												},
											}
										},
									},
								},
							},
							// Multi-question tab strip (when present)
							core.Show{
								When: func() bool { return len(promptFn().Tabs) >= 2 },
								Then: func(cx *core.Scope) core.Node {
									return core.Scroll{
										Horizontal:    true,
										HideIndicator: true,
										Children: []core.Node{
											core.View{
												Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6)},
												Children: []core.Node{
													core.Each(
														func() []string { return promptFn().Tabs },
														func(lbl string) string { return lbl },
														func(cx *core.Scope, tabFn func() string, idxFn func() int) core.Node {
															return base.Button{
																TestID: fmt.Sprintf("prompt-tab-%s-%d", keyStr, idxFn()),
																Label:  tabFn(),
																OnPress: func(core.PressEvent) {
																	if busy.Get() {
																		return
																	}
																	p := promptFn()
																	delta := idxFn() - p.ActiveTab
																	if delta == 0 || !p.CanSwitchTab {
																		return
																	}
																	playSelectionHaptic(cx)
																	busy.Set(true)
																	a.SubmitPromptChoice(itemFn().Key, PromptSubmitRequest{
																		Expected: p,
																		TabDelta: delta,
																	}, func(error) { busy.Set(false) })
																},
																StyleFunc: func() core.Style {
																	pal := a.paletteOf(cx)
																	p := promptFn()
																	bg := pal.SurfaceRaised
																	if idxFn() == p.ActiveTab {
																		bg = pal.Accent
																	}
																	return core.Style{
																		Padding:      core.Symmetric(core.Points(3), core.Points(9)),
																		CornerRadius: Tokens.RadiusPill,
																		Background:   bg,
																	}
																},
																Children: []core.Node{
																	core.Text{
																		ContentFunc: tabFn,
																		StyleFunc: func() core.Style {
																			pal := a.paletteOf(cx)
																			fg := pal.TextMuted
																			if idxFn() == promptFn().ActiveTab {
																				fg = pal.AccentFg
																			}
																			return core.Style{
																				Color:      fg,
																				FontSize:   core.Points(11.5),
																				FontWeight: core.FontWeightMedium,
																			}
																		},
																	},
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
							// Question headline (for Question kind)
							core.Show{
								When: func() bool {
									p := promptFn()
									return p.Kind == PromptKindQuestion && strings.TrimSpace(p.Title) != ""
								},
								Then: func(cx *core.Scope) core.Node {
									return core.Text{
										ContentFunc: func() string { return promptFn().Title },
										StyleFunc: func() core.Style {
											return core.Style{
												Color:      a.paletteOf(cx).Text,
												FontSize:   core.Points(14),
												FontWeight: core.FontWeightSemibold,
											}
										},
									}
								},
							},
							// Monospace command / review detail preview
							core.Show{
								When: func() bool {
									p := promptFn()
									if strings.TrimSpace(p.Detail) != "" {
										return true
									}
									return p.Kind == PromptKindPermission && strings.TrimSpace(firstLine(itemFn().Terminal.Status, itemFn().Terminal)) != ""
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Background:   pal.Bg,
												CornerRadius: core.Points(8),
												BorderWidth:  1,
												BorderColor:  pal.Separator,
												Padding:      core.Symmetric(Tokens.Space2, core.Points(10)),
											}
										},
										Children: []core.Node{
											core.Text{
												MaxLines: 4,
												ContentFunc: func() string {
													p := promptFn()
													if strings.TrimSpace(p.Detail) != "" {
														return p.Detail
													}
													it := itemFn()
													return firstLine(it.Terminal.Status, it.Terminal)
												},
												StyleFunc: func() core.Style {
													return core.Style{
														Color:      a.paletteOf(cx).TextSecondary,
														FontSize:   Tokens.FontMonoMeta,
														FontFamily: core.FontMonospace,
													}
												},
											},
										},
									}
								},
							},
							// Vertical numbered / checkbox option rows + inline Other input
							renderPromptOptionList(cx, a, "", keyStr, promptFn, itemFn, busy, checkedSig, otherDraft, expandedOtherIdx),
							// Multi-select / multi-question footer action bar
							core.Show{
								When: func() bool {
									p := promptFn()
									return p.Kind == PromptKindQuestion && (p.MultiSelect || p.CanSwitchTab)
								},
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2},
										Children: []core.Node{
											core.Show{
												When: func() bool { return promptFn().CanSwitchTab },
												Then: func(cx *core.Scope) core.Node {
													return base.Button{
														TestID: "prompt-tab-prev-" + keyStr,
														Label:  "上一题",
														OnPress: func(core.PressEvent) {
															if busy.Get() {
																return
															}
															playSelectionHaptic(cx)
															busy.Set(true)
															a.SubmitPromptChoice(itemFn().Key, PromptSubmitRequest{
																Expected: promptFn(),
																TabDelta: -1,
															}, func(error) { busy.Set(false) })
														},
														StyleFunc: func() core.Style {
															pal := a.paletteOf(cx)
															return core.Style{
																Height:         core.Pt(38),
																Padding:        core.Symmetric(0, Tokens.Space3),
																CornerRadius:   Tokens.RadiusCtrl,
																Background:     pal.SurfaceRaised,
																BorderWidth:    1,
																BorderColor:    pal.Border,
																AlignItems:     core.AlignCenter,
																JustifyContent: core.JustifyCenter,
															}
														},
														Children: []core.Node{
															core.Text{
																Content: "上一题",
																StyleFunc: func() core.Style {
																	return core.Style{
																		Color:      a.paletteOf(cx).Text,
																		FontSize:   core.Points(13),
																		FontWeight: core.FontWeightMedium,
																	}
																},
															},
														},
													}
												},
											},
											core.Show{
												When: func() bool { return promptFn().MultiSelect },
												Then: func(cx *core.Scope) core.Node {
													return base.Button{
														TestID: "prompt-multi-submit-" + keyStr,
														Label:  "提交回答",
														OnPress: func(core.PressEvent) {
															if busy.Get() {
																return
															}
															playSelectionHaptic(cx)
															var selected []int
															for idx, on := range checkedSig.Get() {
																if on {
																	selected = append(selected, idx)
																}
															}
															sort.Ints(selected)
															busy.Set(true)
															a.SubmitPromptChoice(itemFn().Key, PromptSubmitRequest{
																Expected:        promptFn(),
																SelectedIndices: selected,
																CustomText:      otherDraft.Get(),
																IsMultiSubmit:   true,
																RequireBlocked:  itemFn().Terminal.StateWord() == "blocked",
															}, func(error) { busy.Set(false) })
														},
														StyleFunc: func() core.Style {
															pal := a.paletteOf(cx)
															return core.Style{
																FlexGrow:       1,
																Height:         core.Pt(38),
																Padding:        core.Symmetric(0, Tokens.Space3),
																CornerRadius:   Tokens.RadiusCtrl,
																Background:     pal.Accent,
																AlignItems:     core.AlignCenter,
																JustifyContent: core.JustifyCenter,
															}
														},
														Children: []core.Node{
															core.Text{
																ContentFunc: func() string {
																	n := 0
																	for _, on := range checkedSig.Get() {
																		if on {
																			n++
																		}
																	}
																	if strings.TrimSpace(otherDraft.Get()) != "" {
																		n++
																	}
																	if n > 0 {
																		return fmt.Sprintf("提交 %d 项回答", n)
																	}
																	return "提交回答"
																},
																StyleFunc: func() core.Style {
																	return core.Style{
																		Color:      a.paletteOf(cx).AccentFg,
																		FontSize:   core.Points(13.5),
																		FontWeight: core.FontWeightSemibold,
																	}
																},
															},
														},
													}
												},
											},
										},
									}
								},
							},
							// Card footer: verification note / spinner + 1-tap Refresh & Terminal link
							core.View{
								Style: core.Style{
									Direction:      core.Row,
									AlignItems:     core.AlignCenter,
									JustifyContent: core.JustifySpaceBetween,
									Gap:            Tokens.Space2,
									Padding:        core.Edges{Top: core.Points(2)},
								},
								Children: []core.Node{
									core.View{
										Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(6), FlexShrink: 1, MinWidth: core.Pt(0)},
										Children: []core.Node{
											core.Show{
												When: busy.Get,
												Then: func(cx *core.Scope) core.Node {
													return ui.Spinner{Size: 12, Variant: ui.SpinnerSpokes, Label: "提交中", Tint: func() core.Color { return a.paletteOf(cx).Running }}
												},
											},
											core.Text{
												MaxLines: 1,
												ContentFunc: func() string {
													if busy.Get() {
														return "正在核对并发送到远端…"
													}
													return "发送前自动复核远端状态"
												},
												StyleFunc: func() core.Style {
													return core.Style{
														Color:    a.paletteOf(cx).TextMuted,
														FontSize: core.Points(11.5),
													}
												},
											},
										},
									},
									core.View{
										Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2, FlexShrink: 0},
										Children: []core.Node{
											base.Button{
												TestID: "chat-prompt-refresh",
												Label:  "刷新选项",
												OnPress: func(core.PressEvent) {
													playSelectionHaptic(cx)
													var pqPtr *PendingQuestion
													if chatCtrl != nil {
														if pq, ok := chatCtrl.PendingQuestionTool(); ok {
															pqCopy := pq
															pqPtr = &pqCopy
														}
													}
													a.InspectPrompt(itemFn().Key, itemFn().Terminal, pqPtr, true)
												},
												Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(4), Padding: core.Symmetric(core.Points(2), core.Points(4))},
												Children: []core.Node{
													ui.Icon(lucide.RefreshCw, 12, func() core.Color { return a.paletteOf(cx).TextMuted }),
													core.Text{
														Content: "刷新",
														StyleFunc: func() core.Style {
															return core.Style{Color: a.paletteOf(cx).TextMuted, FontSize: core.Points(12)}
														},
													},
												},
											},
											base.Button{
												TestID: "chat-prompt-open-vt",
												Label:  "进终端",
												OnPress: func(core.PressEvent) {
													playSelectionHaptic(cx)
													if onOpenVT != nil {
														onOpenVT()
													}
												},
												Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(4), Padding: core.Symmetric(core.Points(2), core.Points(4))},
												Children: []core.Node{
													ui.Icon(lucide.Terminal, 12, func() core.Color { return a.paletteOf(cx).TextSecondary }),
													core.Text{
														Content: "进终端",
														StyleFunc: func() core.Style {
															return core.Style{Color: a.paletteOf(cx).TextSecondary, FontSize: core.Points(12), FontWeight: core.FontWeightMedium}
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
				},
				Else: func(cx *core.Scope) core.Node {
					// Initial live terminal.read in flight: quiet loading state + safe Terminal fallback.
					return core.View{
						TestID: "chat-prompt-loading-" + itemFn().Key.String(),
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Direction:      core.Row,
								AlignItems:     core.AlignCenter,
								JustifyContent: core.JustifySpaceBetween,
								Gap:            Tokens.Space2,
								Background:     pal.Surface,
								CornerRadius:   Tokens.RadiusCard,
								BorderWidth:    1,
								BorderColor:    pal.WarnSoft,
								Padding:        core.Symmetric(core.Points(12), core.Points(14)),
							}
						},
						Children: []core.Node{
							core.View{
								Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2},
								Children: []core.Node{
									ui.Spinner{Size: 14, Variant: ui.SpinnerSpokes, Label: "读取实时选项", Tint: func() core.Color { return a.paletteOf(cx).Warn }},
									core.Text{
										Content: "正在读取终端实时选项…",
										StyleFunc: func() core.Style {
											return core.Style{Color: a.paletteOf(cx).TextSecondary, FontSize: Tokens.FontCaption}
										},
									},
								},
							},
							base.Button{
								TestID: "chat-prompt-loading-vt",
								Label:  "进终端",
								OnPress: func(core.PressEvent) {
									if onOpenVT != nil {
										onOpenVT()
									}
								},
								Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(4)},
								Children: []core.Node{
									ui.Icon(lucide.Terminal, 13, func() core.Color { return a.paletteOf(cx).TextSecondary }),
									core.Text{
										Content: "进终端",
										StyleFunc: func() core.Style {
											return core.Style{Color: a.paletteOf(cx).TextSecondary, FontSize: Tokens.FontCaption, FontWeight: core.FontWeightMedium}
										},
									},
								},
							},
						},
					}
				},
			}
		},
		Else: func(cx *core.Scope) core.Node {
			// When a live check already completed and failed to parse (or JSONL-triggered Pi question),
			// strictly degrade to safe "进入终端处理" + "重试读取" (zero blind Allow/Deny buttons).
			return core.Show{
				When: func() bool {
					return a.PromptInspectChecked(itemFn().Key) || (chatCtrl != nil && func() bool { _, ok := chatCtrl.PendingQuestionTool(); return ok }())
				},
				Then: func(cx *core.Scope) core.Node {
					keyStr := itemFn().Key.String()
					return core.View{
						TestID: "chat-prompt-fallback-" + keyStr,
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Background:   pal.Surface,
								CornerRadius: Tokens.RadiusCard,
								BorderWidth:  1,
								BorderColor:  pal.WarnSoft,
								Padding:      core.All(core.Points(13)),
								Gap:          core.Points(10),
							}
						},
						Children: []core.Node{
							core.Text{
								ContentFunc: func() string {
									it := itemFn()
									if line := firstLine(it.Terminal.Status, it.Terminal); line != "" {
										return line
									}
									return "终端正在等待交互，未能自动解析当前选项"
								},
								StyleFunc: func() core.Style {
									return core.Style{Color: a.paletteOf(cx).TextSecondary, FontSize: Tokens.FontCaption}
								},
							},
							core.View{
								Style: core.Style{Direction: core.Row, Gap: Tokens.Space2},
								Children: []core.Node{
									base.Button{
										TestID: "chat-prompt-retry-" + keyStr,
										Label:  "重试读取",
										OnPress: func(core.PressEvent) {
											playSelectionHaptic(cx)
											var pqPtr *PendingQuestion
											if chatCtrl != nil {
												if pq, ok := chatCtrl.PendingQuestionTool(); ok {
													pqCopy := pq
													pqPtr = &pqCopy
												}
											}
											a.InspectPrompt(itemFn().Key, itemFn().Terminal, pqPtr, true)
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexGrow:       1,
												Height:         core.Pt(36),
												CornerRadius:   Tokens.RadiusCtrl,
												Background:     pal.SurfaceRaised,
												BorderWidth:    1,
												BorderColor:    pal.Border,
												AlignItems:     core.AlignCenter,
												JustifyContent: core.JustifyCenter,
											}
										},
										Children: []core.Node{
											core.Text{
												Content: "重试读取",
												StyleFunc: func() core.Style {
													return core.Style{Color: a.paletteOf(cx).Text, FontSize: core.Points(13), FontWeight: core.FontWeightMedium}
												},
											},
										},
									},
									base.Button{
										TestID: "attention-open-" + keyStr,
										Label:  "进入终端处理",
										OnPress: func(core.PressEvent) {
											playSelectionHaptic(cx)
											if onOpenVT != nil {
												onOpenVT()
											}
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexGrow:       1,
												Height:         core.Pt(36),
												CornerRadius:   Tokens.RadiusCtrl,
												Background:     pal.Accent,
												AlignItems:     core.AlignCenter,
												JustifyContent: core.JustifyCenter,
											}
										},
										Children: []core.Node{
											core.Text{
												Content: "进入终端处理",
												StyleFunc: func() core.Style {
													return core.Style{Color: a.paletteOf(cx).AccentFg, FontSize: core.Points(13), FontWeight: core.FontWeightMedium}
												},
											},
										},
									},
								},
							},
						},
					}
				},
				Else: func(cx *core.Scope) core.Node {
					return InlineApprovalCard(cx, a, itemFn, func(TermKey) {
						if onOpenVT != nil {
							onOpenVT()
						}
					})
				},
			}
		},
	}
}

func harnessDisplayName(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "claude":
		return "Claude"
	case "antigravity":
		return "Antigravity"
	case "codex":
		return "Codex"
	case "pi":
		return "Pi"
	case "omp":
		return "OMP"
	default:
		return ""
	}
}

// renderPromptOptionList renders the vertical compact option stack shared by Chat Full cards
// and expanded Home/Inbox cards.
func renderPromptOptionList(
	cx *core.Scope,
	a *App,
	idPrefix string,
	keyStr string,
	promptFn func() InteractivePrompt,
	itemFn func() AggregatedTerminal,
	busy *core.Signal[bool],
	checkedSig *core.Signal[map[int]bool],
	otherDraft *core.Signal[string],
	expandedOtherIdx *core.Signal[int],
) core.Node {
	return core.View{
		Style: core.Style{Gap: core.Points(6)},
		Children: []core.Node{
			core.Each(
				func() []PromptOption {
					p := promptFn()
					// In multi-select questions, hide the sentinel Next row because the bottom primary Submit button drives it.
					if p.Kind == PromptKindQuestion && p.MultiSelect {
						var vis []PromptOption
						for _, o := range p.Options {
							if o.IsSubmit {
								continue
							}
							vis = append(vis, o)
						}
						return vis
					}
					return p.Options
				},
				func(o PromptOption) string { return fmt.Sprintf("%d:%s", o.Index, o.Label) },
				func(cx *core.Scope, optFn func() PromptOption, _ func() int) core.Node {
					opt0 := optFn()
					optIdx := opt0.Index
					p0 := promptFn()

					// In Question mode, render the NeedsText ("Other" / "Type something.") row directly as an inline input!
					if opt0.NeedsText && p0.Kind == PromptKindQuestion {
						return renderInlineOtherInputRow(cx, a, idPrefix, keyStr, opt0, promptFn, itemFn, busy, checkedSig, otherDraft)
					}

					isChecked := func() bool {
						return checkedSig != nil && checkedSig.Get()[optIdx]
					}
					rowNode := base.Button{
						TestID: fmt.Sprintf("%sprompt-opt-%s-%d", idPrefix, keyStr, optIdx),
						Label:  opt0.Label,
						OnPress: func(core.PressEvent) {
							if busy.Get() {
								return
							}
							playSelectionHaptic(cx)
							p := promptFn()
							opt := optFn()
							if p.Kind == PromptKindQuestion && p.MultiSelect {
								cur := checkedSig.Get()
								next := make(map[int]bool, len(cur)+1)
								for k, v := range cur {
									next[k] = v
								}
								next[opt.Index] = !next[opt.Index]
								checkedSig.Set(next)
								return
							}
							if opt.NeedsText {
								animateSpringLayout(cx)
								if expandedOtherIdx.Get() == opt.Index {
									expandedOtherIdx.Set(-1)
								} else {
									expandedOtherIdx.Set(opt.Index)
								}
								return
							}
							busy.Set(true)
							a.SubmitPromptChoice(itemFn().Key, PromptSubmitRequest{
								Expected:       p,
								OptionIndex:    opt.Index,
								RequireBlocked: itemFn().Terminal.StateWord() == "blocked",
							}, func(error) { busy.Set(false) })
						},
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							p := promptFn()
							opt := optFn()
							bg := pal.SurfaceRaised
							bc := pal.Border
							bw := core.Points(1)
							if p.Kind == PromptKindQuestion && p.MultiSelect && isChecked() {
								bg = pal.RunningSoft
								bc = pal.Running
							} else {
								switch opt.Tone {
								case TonePrimary:
									bg = pal.Accent
									bc = pal.Accent
								case ToneDanger:
									bg = pal.DangerSoft
									bc = core.Transparent
									bw = 0
								}
							}
							return core.Style{
								Direction:    core.Row,
								AlignItems:   core.AlignCenter,
								Gap:          core.Points(10),
								MinHeight:    core.Pt(40),
								Padding:      core.Symmetric(core.Points(8), core.Points(11)),
								CornerRadius: Tokens.RadiusCtrl,
								Background:   bg,
								BorderWidth:  bw,
								BorderColor:  bc,
							}
						},
						Children: []core.Node{
							core.Show{
								When: func() bool {
									p := promptFn()
									return p.Kind == PromptKindQuestion && p.MultiSelect
								},
								Then: func(cx *core.Scope) core.Node {
									return core.Show{
										When: isChecked,
										Then: func(cx *core.Scope) core.Node {
											return ui.Icon(lucide.SquareCheck, 16, func() core.Color { return a.paletteOf(cx).Running })
										},
										Else: func(cx *core.Scope) core.Node {
											return ui.Icon(lucide.Square, 16, func() core.Color { return a.paletteOf(cx).TextMuted })
										},
									}
								},
								Else: func(cx *core.Scope) core.Node {
									return core.Show{
										When: func() bool { return optFn().KeyHint != "" },
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													opt := optFn()
													bg := pal.Bg
													bc := pal.Border
													bw := core.Points(1)
													if opt.Tone == TonePrimary {
														bg = pal.AccentHover
														bc = core.Transparent
														bw = 0
													} else if opt.Tone == ToneDanger {
														bg = core.Transparent
														bc = pal.Danger
													}
													return core.Style{
														Width:          core.Pt(20),
														Height:         core.Pt(20),
														FlexShrink:     0,
														CornerRadius:   core.Points(5),
														Background:     bg,
														BorderWidth:    bw,
														BorderColor:    bc,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifyCenter,
													}
												},
												Children: []core.Node{
													core.Text{
														ContentFunc: func() string { return optFn().KeyHint },
														StyleFunc: func() core.Style {
															pal := a.paletteOf(cx)
															fg := pal.TextMuted
															switch optFn().Tone {
															case TonePrimary:
																fg = pal.AccentFg
															case ToneDanger:
																fg = pal.Danger
															}
															return core.Style{
																Color:      fg,
																FontSize:   core.Points(11),
																FontFamily: core.FontMonospace,
																FontWeight: core.FontWeightSemibold,
															}
														},
													},
												},
											}
										},
									}
								},
							},
							core.View{
								Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
								Children: []core.Node{
									core.Text{
										ContentFunc: func() string { return optFn().Label },
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											fg := pal.Text
											switch optFn().Tone {
											case TonePrimary:
												fg = pal.AccentFg
											case ToneDanger:
												fg = pal.Danger
											}
											return core.Style{
												Color:      fg,
												FontSize:   core.Points(13.5),
												FontWeight: core.FontWeightMedium,
											}
										},
									},
									core.Show{
										When: func() bool { return strings.TrimSpace(optFn().Description) != "" },
										Then: func(cx *core.Scope) core.Node {
											return core.Text{
												ContentFunc: func() string { return optFn().Description },
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													fg := pal.TextMuted
													if optFn().Tone == TonePrimary {
														fg = pal.AccentFg
													}
													return core.Style{
														Color:    fg,
														FontSize: core.Points(11.5),
													}
												},
											}
										},
									},
								},
							},
						},
					}

					// For Permission NeedsText options (e.g. Claude option 3), expand inline custom input below the tapped row.
					if opt0.NeedsText {
						return core.View{
							Style: core.Style{Gap: core.Points(6)},
							Children: []core.Node{
								rowNode,
								core.Show{
									When: func() bool { return expandedOtherIdx.Get() == optIdx },
									Then: func(cx *core.Scope) core.Node {
										return renderInlineOtherInputRow(cx, a, idPrefix, keyStr, opt0, promptFn, itemFn, busy, checkedSig, otherDraft)
									},
								},
							},
						}
					}
					return rowNode
				},
			),
		},
	}
}

func renderInlineOtherInputRow(
	cx *core.Scope,
	a *App,
	idPrefix string,
	keyStr string,
	opt PromptOption,
	promptFn func() InteractivePrompt,
	itemFn func() AggregatedTerminal,
	busy *core.Signal[bool],
	checkedSig *core.Signal[map[int]bool],
	otherDraft *core.Signal[string],
) core.Node {
	submitCustom := func() {
		if busy.Get() {
			return
		}
		p := promptFn()
		txt := strings.TrimSpace(otherDraft.Get())
		if txt == "" && opt.Tone != ToneDanger {
			a.notifyToast("请先输入内容")
			return
		}
		playSelectionHaptic(cx)
		busy.Set(true)
		req := PromptSubmitRequest{
			Expected:       p,
			OptionIndex:    opt.Index,
			CustomText:     txt,
			RequireBlocked: itemFn().Terminal.StateWord() == "blocked",
		}
		if p.Kind == PromptKindQuestion && p.MultiSelect {
			var selected []int
			if checkedSig != nil {
				for idx, on := range checkedSig.Get() {
					if on {
						selected = append(selected, idx)
					}
				}
			}
			sort.Ints(selected)
			req.SelectedIndices = selected
			req.IsMultiSubmit = true
		}
		a.SubmitPromptChoice(itemFn().Key, req, func(err error) {
			busy.Set(false)
			if err == nil {
				otherDraft.Set("")
			}
		})
	}

	placeholder := "其他：输入自定义说明…"
	if opt.Tone == ToneDanger {
		placeholder = "输入拒绝原因或替代指令（留空直接拒绝）…"
	}

	return core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				Direction:    core.Row,
				AlignItems:   core.AlignCenter,
				Gap:          core.Points(6),
				Background:   pal.Bg,
				BorderWidth:  1,
				BorderColor:  pal.Border,
				CornerRadius: Tokens.RadiusCtrl,
				Padding:      core.Edges{Top: core.Points(4), Right: core.Points(6), Bottom: core.Points(4), Left: core.Points(10)},
			}
		},
		Children: []core.Node{
			core.View{
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
				Children: []core.Node{
					ui.Input{
						TestID:      idPrefix + "prompt-other-input-" + keyStr,
						Placeholder: placeholder,
						Value:       otherDraft.Get,
						OnChange:    otherDraft.Set,
						OnSubmit:    func(string) { submitCustom() },
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Width:      core.Pct(100),
								MinHeight:  core.Pt(30),
								Background: core.Transparent,
								Color:      pal.Text,
								FontSize:   core.Points(13),
							}
						},
					},
				},
			},
			base.Button{
				TestID:  idPrefix + "prompt-other-submit-" + keyStr,
				Label:   "发送",
				OnPress: func(core.PressEvent) { submitCustom() },
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					bg := pal.Accent
					if opt.Tone == ToneDanger && strings.TrimSpace(otherDraft.Get()) == "" {
						bg = pal.DangerSoft
					}
					return core.Style{
						FlexShrink:     0,
						Height:         core.Pt(28),
						Padding:        core.Symmetric(0, core.Points(11)),
						CornerRadius:   core.Points(7),
						Background:     bg,
						AlignItems:     core.AlignCenter,
						JustifyContent: core.JustifyCenter,
					}
				},
				Children: []core.Node{
					core.Text{
						ContentFunc: func() string {
							if opt.Tone == ToneDanger && strings.TrimSpace(otherDraft.Get()) == "" {
								return "拒绝"
							}
							return "发送"
						},
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							fg := pal.AccentFg
							if opt.Tone == ToneDanger && strings.TrimSpace(otherDraft.Get()) == "" {
								fg = pal.Danger
							}
							return core.Style{
								Color:      fg,
								FontSize:   core.Points(12),
								FontWeight: core.FontWeightSemibold,
							}
						},
					},
				},
			},
		},
	}
}
