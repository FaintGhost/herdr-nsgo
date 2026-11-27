package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"
)

// sessionScreen renders the unified Session view with seamless 0ms switching between
// Chat View (official NSGo ui/chat + ui/diff + Moshi Floating Glass Composer) and real 2D VT Terminal View:
//   - 44px compact safe-area header with 1-tap Quick Session Switcher, fixed 聊天|终端 segmented pill, search, tools
//   - Collapsible search bar (filters Chat cards in chat mode; jumps VT matches in vt mode)
//   - Active viewport (Chat View or VT Canvas, both kept mounted for <1ms visibility swap)
//   - Mode-adaptive bottom composer: Floating Liquid-Glass multiline composer in Chat mode (with >_ quick VT switch,
//     expandable tall editor, quote preview, and swipe-down keyboard dismiss) vs KeycapBar + terminal dock in VT mode.
func (u *uiController) sessionScreen(cx *core.Scope, showBack bool) core.Node {
	a := u.app
	showSearch := core.NewSignal(cx, false)
	previewKey := core.NewSignal(cx, TermKey{})
	muxKey := core.NewSignal(cx, TermKey{})
	projectKey := core.NewSignal(cx, TermKey{})
	showAttachments := core.NewSignal(cx, false)
	var attachmentKey TermKey
	showVoice := core.NewSignal(cx, TermKey{})
	showSnippets := core.NewSignal(cx, false)
	showTools := core.NewSignal(cx, false)
	showSwitcher := core.NewSignal(cx, false)

	ctrlArmed := core.NewSignal(cx, false)
	ctrlLocked := core.NewSignal(cx, false)
	var lastCtrlTap time.Time

	composerExpanded := core.NewSignal(cx, true)
	composerTall := core.NewSignal(cx, false)
	composerInset := core.NewSignal(cx, core.Points(138))
	composerHeight := core.NewSignal(cx, core.Points(22))
	quotedText := core.NewSignal(cx, "")

	// Reset transient per-session UI state whenever the active session switches while sessionScreen stays mounted.
	lastSessionKey := u.activeSession.GetUntracked()
	cx.Effect(func() {
		next := u.activeSession.Get()
		if next != lastSessionKey {
			lastSessionKey = next
			quotedText.Set("")
			composerTall.Set(false)
			showSearch.Set(false)
			ctrlArmed.Set(false)
			ctrlLocked.Set(false)
		}
	})

	curKey := func() TermKey { return u.activeSession.Get() }
	curTerm := func() AggregatedTerminal {
		t, _ := a.FindTerminal(curKey())
		return t
	}
	curST := func() *SessionTerminal { return u.activeTerm.Get() }
	curChat := func() *ChatSessionController { return u.activeChat.Get() }
	isChatMode := func() bool { return u.sessionMode.Get() == "chat" }

	isCtrlActive := func() bool { return ctrlArmed.Get() || ctrlLocked.Get() }

	sendKeyOrBytes := func(logicalKey string, rawBytes []byte) {
		st := curST()
		if st != nil && st.Attached() {
			_ = st.SendInput(rawBytes, a.vtFontSize.Get())
			return
		}
		a.PressTo(curKey(), logicalKey)
	}

	buildFinalSendText := func(raw string) string {
		q := strings.TrimSpace(quotedText.Get())
		if q == "" {
			return raw
		}
		var qb strings.Builder
		for _, ln := range strings.Split(singleLineClip(q, 220), "\n") {
			qb.WriteString("> ")
			qb.WriteString(ln)
			qb.WriteByte('\n')
		}
		qb.WriteByte('\n')
		qb.WriteString(raw)
		return qb.String()
	}

	modeFor := func(kind string, term Terminal) chatSubmitMode {
		// Wait/Steer queue semantics only apply while the Agent is actively busy ("working").
		// When the Agent is idle or done, send normally without showing "等待中" / "插队".
		if term.StateWord() != "working" {
			return chatNormal
		}
		return a.EffectiveSendMode(kind)
	}
	sendComposerText := func() {
		k := curKey()
		term := curTerm().Terminal
		kind := DetectHarnessKind(term)
		mode := modeFor(kind, term)
		draft := a.Compose(k.String())
		raw := strings.TrimSpace(draft)
		if strings.HasPrefix(raw, "/") {
			mode = chatNormal
		}
		if isChatMode() && a.chatImages.Get()[k].name != "" {
			chatCtrl := curChat()
			quote := quotedText.Get()
			a.sendChatImage(k, buildFinalSendText(raw), kind, mode, func(message string) {
				if a.Compose(k.String()) == draft {
					a.SetCompose(k.String(), "")
				}
				if curKey() == k && quotedText.Get() == quote {
					quotedText.Set("")
				}
				if chatCtrl != nil && !chatCtrl.isClosed() {
					id := chatCtrl.beginChatSend(message, string(mode))
					chatCtrl.confirmSubmission(id, mode)
					chatCtrl.RefreshAsync()
				}
			})
			return
		}
		if raw == "" {
			return
		}
		text := buildFinalSendText(raw)
		quotedText.Set("")
		composerTall.Set(false)

		// In Chat View, ALWAYS route through RPC terminal.send so the background
		// SessionTerminal stays in observe=true and never shrinks a 140-col desktop PTY!
		if isChatMode() {
			chatCtrl := curChat()
			var optID string
			if chatCtrl != nil {
				optID = chatCtrl.beginChatSend(text, string(mode))
			}
			a.SetCompose(k.String(), "")
			playSelectionHaptic(cx)
			// If a test injected a fake TranscriptBackend without a live RPC client, keep the optimistic turn cleanly.
			if a.clientForKey(k) == nil && a.transcriptBackend != nil {
				return
			}
			a.sendChatWithMode(k, text, kind, mode, func(err error) {
				if err != nil {
					if chatCtrl != nil {
						chatCtrl.RemoveOptimistic(optID)
					}
					a.post(func() {
						if strings.TrimSpace(a.Compose(k.String())) == "" {
							a.setCompose(k.String(), raw)
						}
					})
					return
				}
				if chatCtrl != nil {
					chatCtrl.confirmSubmission(optID, mode)
					chatCtrl.RefreshAsync()
				}
			})
			return
		}
		st := curST()
		if st != nil && st.Attached() {
			if err := st.SendInput([]byte(text+"\r"), a.vtFontSize.Get()); err == nil {
				a.SetCompose(k.String(), "")
				return
			}
		}
		a.SendTo(k, text)
	}

	keycaps := []KeycapItem{
		{TestID: "keycap-esc", Label: "Esc", OnPress: func() { sendKeyOrBytes("esc", []byte{0x1b}) }},
		{TestID: "keycap-tab", Label: "Tab", OnPress: func() { sendKeyOrBytes("tab", []byte{'\t'}) }},
		{
			TestID: "keycap-ctrl",
			LabelFunc: func() string {
				if ctrlLocked.Get() {
					return "Ctrl🔒"
				}
				return "Ctrl"
			},
			Active: isCtrlActive,
			OnPress: func() {
				now := time.Now()
				if ctrlLocked.Get() {
					ctrlLocked.Set(false)
					ctrlArmed.Set(false)
					return
				}
				if ctrlArmed.Get() && now.Sub(lastCtrlTap) < 480*time.Millisecond {
					ctrlLocked.Set(true)
					ctrlArmed.Set(false)
					a.notifyToast("Ctrl 已双击锁定（再点一次解除）")
					return
				}
				ctrlArmed.Set(!ctrlArmed.Get())
				lastCtrlTap = now
			},
		},
		{TestID: "keycap-up", Label: "↑", OnPress: func() { sendKeyOrBytes("up", []byte("\x1b[A")) }},
		{TestID: "keycap-down", Label: "↓", OnPress: func() { sendKeyOrBytes("down", []byte("\x1b[B")) }},
		{TestID: "keycap-left", Label: "←", OnPress: func() { sendKeyOrBytes("left", []byte("\x1b[D")) }},
		{TestID: "keycap-right", Label: "→", OnPress: func() { sendKeyOrBytes("right", []byte("\x1b[C")) }},
		{TestID: "keycap-enter", Label: "↵", OnPress: func() { sendKeyOrBytes("enter", []byte{'\r'}) }},
		{
			TestID: "keycap-rightclick",
			Label:  "右键",
			Active: func() bool {
				st := curST()
				return st != nil && st.RightClickArmed()
			},
			OnPress: func() {
				st := curST()
				if st == nil {
					return
				}
				if st.ToggleRightClickArmed() {
					a.notifyToast("已武装右键：点按终端任意位置呼出 herdr 菜单")
				} else {
					a.notifyToast("已取消右键武装")
				}
			},
		},
		{TestID: "keycap-ctrlc", Label: "^C", Danger: true, OnPress: func() { sendKeyOrBytes("ctrl+c", []byte{0x03}) }},
		{TestID: "keycap-ctrld", Label: "^D", OnPress: func() { sendKeyOrBytes("ctrl+d", []byte{0x04}) }},
		{TestID: "keycap-ctrll", Label: "^L", OnPress: func() { sendKeyOrBytes("ctrl+l", []byte{0x0c}) }},
		{
			TestID: "keycap-composer",
			Label:  "⌨",
			Active: composerExpanded.Get,
			OnPress: func() {
				animateSpringLayout(cx)
				composerExpanded.Set(!composerExpanded.Get())
			},
		},
	}

	var headerControls []core.Node
	if showBack {
		headerControls = append(headerControls, IconTap(cx, a, "session-back-btn", "返回", iconBack, u.closeSession))
	}
	headerControls = append(headerControls, base.Button{
		TestID:    "session-switcher-btn",
		LabelFunc: func() string { return "切换会话 · " + curTerm().Terminal.Label() },
		OnPress:   func(core.PressEvent) { showSwitcher.Set(true) },
		Style: core.Style{
			FlexGrow:   1,
			FlexShrink: 1,
			MinWidth:   core.Pt(0),
			MinHeight:  core.Pt(Tokens.SizeTouch),
			Direction:  core.Row,
			AlignItems: core.AlignCenter,
			Gap:        core.Points(6),
			Padding:    core.Symmetric(0, core.Points(4)),
		},
		Children: []core.Node{
			core.View{
				Style: core.Style{
					FlexShrink:     0,
					Width:          core.Pt(12),
					Height:         core.Pt(12),
					AlignItems:     core.AlignCenter,
					JustifyContent: core.JustifyCenter,
				},
				Children: []core.Node{
					core.Show{
						When: func() bool {
							ch := curChat()
							return isChatMode() && ch != nil && ch.IsActivelySyncing()
						},
						Then: func(cx *core.Scope) core.Node {
							return ui.Spinner{
								TestID: "session-header-sync-spinner",
								Size:   12,
								Label:  "同步中",
							}
						},
						Else: func(cx *core.Scope) core.Node {
							return core.View{
								StyleFunc: func() core.Style {
									return core.Style{
										Width:        core.Pt(8),
										Height:       core.Pt(8),
										CornerRadius: 4,
										Background:   stateDotColor(a.paletteOf(cx), curTerm().Terminal.StateWord()),
									}
								},
							}
						},
					},
				},
			},
			HarnessBrandIcon(cx, a, func() Terminal { return curTerm().Terminal }, "", 15),
			core.Text{
				ContentFunc: func() string { return curTerm().Terminal.Label() },
				MaxLines:    1,
				Style: core.Style{
					FlexShrink: 1,
					MinWidth:   core.Pt(0),
					FontSize:   Tokens.FontSession,
					FontWeight: core.FontWeightSemibold,
				},
				StyleFunc: ui.Colored(func() core.Color { return a.paletteOf(cx).Text }),
			},
			ui.Icon(lucide.ChevronDown, 13, func() core.Color { return a.paletteOf(cx).TextMuted }),
		},
	})

	modeSegmentPill := core.View{
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bw := core.Points(0)
			if pal.Dark {
				bw = 1
			}
			return core.Style{
				FlexShrink:   0,
				Direction:    core.Row,
				AlignItems:   core.AlignCenter,
				Background:   pal.SurfaceRaised,
				CornerRadius: Tokens.RadiusPill,
				BorderWidth:  bw,
				BorderColor:  pal.Border,
				Padding:      core.All(core.Points(3)),
				Gap:          core.Points(2),
			}
		},
		Children: []core.Node{
			sessionModeSegmentBtn(cx, a, "session-mode-chat", "聊天", isChatMode, func() {
				u.setSessionMode("chat")
			}),
			sessionModeSegmentBtn(cx, a, "session-mode-vt", "终端", func() bool { return !isChatMode() }, func() {
				u.setSessionMode("vt")
			}),
		},
	}

	// Shared single-instance input editor (`id:session-composer`) used in both Floating Chat Glass Card and VT Dock
	// so there is never a duplicate TestID while switching modes in <1ms!
	composerEditor := func(cx *core.Scope) core.Node {
		return base.Input{
			TestID:         "session-composer",
			Label:          "发消息或指令",
			Placeholder:    "发消息或指令…",
			Multiline:      true,
			SubmitBehavior: core.SubmitBehaviorSubmit,
			Value: func() string {
				return a.Compose(curKey().String())
			},
			OnChange: func(txt string) {
				if !isChatMode() && isCtrlActive() && len(txt) == 1 {
					ch := strings.ToLower(txt)[0]
					if ch >= 'a' && ch <= 'z' {
						if !ctrlLocked.Get() {
							ctrlArmed.Set(false)
						}
						sendKeyOrBytes("ctrl+"+string(ch), []byte{ch - 'a' + 1})
						return
					}
				}
				a.SetCompose(curKey().String(), txt)
			},
			OnPaste: func(ev core.PasteEvent) {
				if !isChatMode() {
					return
				}
				for _, file := range ev.Files {
					if strings.HasPrefix(file.MIME, "image/") {
						a.attachChatImage(curKey(), file.Name, file.MIME, file.Data)
					}
				}
			},
			OnSubmit: func(string) {
				sendComposerText()
			},
			OnContentSizeChange: func(sz core.Size) {
				composerHeight.Set(sz.Height)
			},
			Style: core.Style{
				MinWidth:          core.Pt(0),
				MaxWidth:          core.Pct(100),
				AlignSelf:         core.AlignStretch,
				Overflow:          core.Hidden,
				FontSize:          Tokens.FontBody,
				LineHeight:        core.Points(20),
				TextAlignVertical: core.TextAlignVerticalTop,
			},
			StyleFunc: func() core.Style {
				pal := a.paletteOf(cx)
				if !isChatMode() {
					return core.Style{
						MinHeight:        core.Pt(Tokens.SizeTouch),
						MaxHeight:        core.Pt(64),
						Padding:          core.Symmetric(core.Points(9), Tokens.Space3),
						Background:       pal.SurfaceRaised,
						Color:            pal.Text,
						PlaceholderColor: pal.TextMuted,
						CaretColor:       pal.Accent,
						CornerRadius:     Tokens.RadiusCtrl,
						BorderColor:      pal.Border,
						BorderWidth:      1,
					}
				}
				maxLinesH := core.Points(112)
				minH := core.Points(36)
				if composerTall.Get() {
					maxLinesH = 220
					minH = 148
				}
				// Reserve the header, composer controls and safe area while the IME is open.
				available := cx.Env().Window().Height - cx.Env().KeyboardHeight() - cx.Env().SafeArea().Top - cx.Env().SafeArea().Bottom
				maxLinesH = min(maxLinesH, max(36, available-150))
				minH = min(minH, maxLinesH)
				curH := min(maxLinesH, max(minH, composerHeight.Get()+8))
				return core.Style{
					Height:           core.Pt(curH),
					MinHeight:        core.Pt(minH),
					Padding:          core.Symmetric(core.Points(4), core.Points(6)),
					Background:       core.Transparent,
					Color:            pal.Text,
					PlaceholderColor: pal.TextMuted,
					CaretColor:       pal.Accent,
				}
			},
		}
	}

	pickPhoto := func(key TermKey, chatMode bool) {
		if cx == nil {
			return
		}
		cx.PickImages(core.PickOptions{Limit: 1, MaxDimension: 2400}, func(files []core.PickedFile) {
			if len(files) == 0 {
				return
			}
			f := files[0]
			if chatMode {
				a.attachChatImage(key, f.Name, f.MIME, f.Data)
				return
			}
			a.UploadFileTo(key, f.Name, f.Data)
		})
	}

	uploadAction := func() {
		key := curKey()
		if !isChatMode() {
			pickPhoto(key, false)
			return
		}
		attachmentKey = key
		cx.DismissKeyboard()
		showAttachments.Set(true)
	}
	selector := core.NewSignal(cx, "")
	var selectorKey TermKey
	openAgentSelector := func(effort bool) {
		selectorKey = curKey()
		cx.DismissKeyboard()
		if effort {
			selector.Set("思考强度")
		} else {
			selector.Set("模型")
		}
	}
	sendOrInterruptNode := func(cx *core.Scope) core.Node {
		return core.Show{
			When: func() bool {
				return isChatMode() && a.chatImages.Get()[curKey()].name == "" &&
					curTerm().Terminal.StateWord() == "working" &&
					strings.TrimSpace(a.Compose(curKey().String())) == ""
			},
			Then: func(cx *core.Scope) core.Node {
				return base.Button{
					TestID: "session-interrupt-btn",
					Label:  "中断",
					OnPress: func(core.PressEvent) {
						playSelectionHaptic(cx)
						a.InterruptTo(curKey())
					},
					Style: core.Style{
						Width:          core.Pt(34),
						Height:         core.Pt(34),
						FlexShrink:     0,
						CornerRadius:   17,
						AlignItems:     core.AlignCenter,
						JustifyContent: core.JustifyCenter,
					},
					StyleFunc: func() core.Style {
						return core.Style{Background: a.paletteOf(cx).Accent}
					},
					Children: []core.Node{
						ui.Icon(lucide.Square, 13, func() core.Color { return a.paletteOf(cx).AccentFg }),
					},
				}
			},
			Else: func(cx *core.Scope) core.Node {
				if !isChatMode() {
					return base.Button{
						TestID:  "session-send-btn",
						Label:   "发送",
						OnPress: func(core.PressEvent) { playSelectionHaptic(cx); sendComposerText() },
						Style: core.Style{
							Width:          core.Pt(34),
							Height:         core.Pt(34),
							FlexShrink:     0,
							CornerRadius:   17,
							AlignItems:     core.AlignCenter,
							JustifyContent: core.JustifyCenter,
						},
						StyleFunc: func() core.Style { return core.Style{Background: a.paletteOf(cx).Accent} },
						Children:  []core.Node{ui.Icon(lucide.ArrowUp, 17, func() core.Color { return a.paletteOf(cx).AccentFg })},
					}
				}
				empty := func() bool {
					return strings.TrimSpace(a.Compose(curKey().String())) == "" && a.chatImages.Get()[curKey()].name == ""
				}
				return base.Button{
					TestID:   "session-send-btn",
					Label:    "发送",
					Disabled: empty,
					OnPress:  func(core.PressEvent) { sendComposerText() },
					Style: core.Style{
						Width:          core.Pt(34),
						Height:         core.Pt(34),
						FlexShrink:     0,
						CornerRadius:   17,
						AlignItems:     core.AlignCenter,
						JustifyContent: core.JustifyCenter,
					},
					StyleFunc: func() core.Style {
						p := a.paletteOf(cx)
						if empty() {
							return core.Style{Background: p.Fill}
						}
						return core.Style{Background: p.Accent}
					},
					Children: []core.Node{ui.Icon(lucide.ArrowUp, 17, func() core.Color {
						p := a.paletteOf(cx)
						if empty() {
							return p.TextMuted
						}
						return p.AccentFg
					})},
				}
			},
		}
	}

	latestTranscriptModel := func() string {
		ch := curChat()
		if ch == nil {
			return ""
		}
		if ch.rev != nil {
			_ = ch.rev.Get()
		}
		items := ch.Items()
		for i := len(items) - 1; i >= 0; i-- {
			if m, _ := splitModelAndEffort(items[i].Model); m != "" {
				return m
			}
		}
		return ""
	}
	latestTranscriptEffort := func() string {
		ch := curChat()
		if ch == nil {
			return ""
		}
		if ch.rev != nil {
			_ = ch.rev.Get()
		}
		items := ch.Items()
		for i := len(items) - 1; i >= 0; i-- {
			if e := strings.TrimSpace(items[i].Effort); e != "" {
				return e
			}
			if _, e := splitModelAndEffort(items[i].Model); e != "" {
				return e
			}
		}
		return ""
	}
	activeModelChipLabel := func() string {
		if a.catalogRev != nil {
			_ = a.catalogRev.Get()
		}
		k := curKey()
		live := latestTranscriptModel()
		kind := DetectHarnessKind(curTerm().Terminal)
		catDef := a.InstantCatalogFor(k.InstanceID, kind, "").DefaultModel
		if m := a.SessionModel(k); m != "" {
			// If the optimistic selection is a bare Claude family alias ("opus"/"sonnet"/"haiku")
			// and the live transcript or host catalog knows the exact version ("claude-opus-5-5"),
			// display the specific version ("Opus 5.5") instead of bare "Opus".
			lowM := strings.ToLower(strings.TrimSpace(m))
			if lowM == "opus" || lowM == "sonnet" || lowM == "haiku" || lowM == "default" {
				if live != "" && (lowM == "default" || strings.Contains(strings.ToLower(live), lowM)) {
					return ShortModelLabel(live)
				}
				if catDef != "" && (lowM == "default" || strings.Contains(strings.ToLower(catDef), lowM)) {
					return ShortModelLabel(catDef)
				}
			}
			return ShortModelLabel(m)
		}
		if live != "" {
			return ShortModelLabel(live)
		}
		if catDef != "" {
			return ShortModelLabel(catDef)
		}
		return "模型"
	}
	activeEffortChipLabel := func() string {
		if a.catalogRev != nil {
			_ = a.catalogRev.Get()
		}
		k := curKey()
		if e := a.SessionEffort(k); e != "" {
			return formatEffortChipLabel(e)
		}
		if e := latestTranscriptEffort(); e != "" {
			return formatEffortChipLabel(e)
		}
		kind := DetectHarnessKind(curTerm().Terminal)
		if cat := a.InstantCatalogFor(k.InstanceID, kind, ""); cat.DefaultEffort != "" {
			return formatEffortChipLabel(cat.DefaultEffort)
		}
		if def := defaultEffortForKind(kind); def != "" {
			return formatEffortChipLabel(def)
		}
		return "思考强度"
	}

	return core.View{
		TestID: "screen-session",
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			return core.Style{
				FlexGrow:   1,
				FlexShrink: 1,
				MinHeight:  core.Pt(0),
				Position:   core.Relative,
				Background: pal.Bg,
			}
		},
		Children: []core.Node{
			// 48px single flex-row Quiet Studio session header (Search moved into Tools sheet; fixed controls across modes).
			core.View{
				Style: core.Style{
					PaddingEnv: core.EnvEdges{Top: core.EnvSafeArea},
					Padding:    core.Symmetric(Tokens.Space1, Tokens.Space2),
					Borders:    core.Edges{Bottom: 1},
				},
				StyleFunc: func() core.Style {
					p := a.paletteOf(cx)
					return core.Style{Background: p.Surface, BorderColor: p.Separator}
				},
				Children: []core.Node{
					core.View{
						Style: core.Style{
							Direction:  core.Row,
							AlignItems: core.AlignCenter,
							Gap:        Tokens.Space1,
							MinHeight:  core.Pt(Tokens.SizeTopbar),
						},
						Children: append(headerControls,
							modeSegmentPill,
							IconTap(cx, a, "session-tools-btn", "更多工具", iconMore, func() { showTools.Set(true) }),
						),
					},
				},
			},
			SyncStatusBanner(cx, a),
			// Collapsible in-session search bar (mode-aware)
			core.Show{
				When: showSearch.Get,
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						StyleFunc: func() core.Style {
							pal := a.paletteOf(cx)
							return core.Style{
								Direction:   core.Row,
								AlignItems:  core.AlignCenter,
								Gap:         Tokens.Space2,
								Padding:     core.Symmetric(Tokens.Space2, Tokens.Space3),
								Background:  pal.Surface,
								Borders:     core.Edges{Bottom: 1},
								BorderColor: pal.Separator,
							}
						},
						Children: []core.Node{
							core.View{
								Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
								Children: []core.Node{
									ui.Input{
										TestID:      "session-search-input",
										Placeholder: "搜索当前会话内容…",
										Value: func() string {
											if isChatMode() {
												if ch := curChat(); ch != nil && ch.searchQuery != nil {
													return ch.searchQuery.Get()
												}
												return ""
											}
											st := curST()
											if st == nil || st.searchQuery == nil {
												return ""
											}
											return st.searchQuery.Get()
										},
										OnChange: func(q string) {
											if isChatMode() {
												if ch := curChat(); ch != nil && ch.searchQuery != nil {
													ch.searchQuery.Set(q)
												}
												return
											}
											if st := curST(); st != nil {
												st.SetSearch(q)
											}
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												Width:        core.Pct(100),
												MinHeight:    core.Pt(Tokens.SizeKey),
												Background:   pal.SurfaceRaised,
												Color:        pal.Text,
												CornerRadius: Tokens.RadiusKey,
												BorderColor:  pal.Border,
												BorderWidth:  1,
											}
										},
									},
								},
							},
							core.Show{
								When: func() bool { return !isChatMode() },
								Then: func(cx *core.Scope) core.Node {
									return core.View{
										Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2},
										Children: []core.Node{
											core.Text{
												ContentFunc: func() string {
													st := curST()
													if st == nil {
														return "0"
													}
													m := st.SearchMatches()
													if len(m) == 0 {
														return "0"
													}
													idx := 0
													if st.searchIdx != nil {
														idx = st.searchIdx.Get()
													}
													return fmt.Sprintf("%d/%d", idx+1, len(m))
												},
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
												},
											},
											ActionButton(cx, a, "search-prev-btn", "↑", ActionSecondary, func() {
												if st := curST(); st != nil {
													st.PrevSearchMatch()
												}
											}),
											ActionButton(cx, a, "search-next-btn", "↓", ActionSecondary, func() {
												if st := curST(); st != nil {
													st.NextSearchMatch()
												}
											}),
										},
									}
								},
							},
						},
					}
				},
			},
			// Main session body: BOTH Chat View and Terminal View stay mounted AND laid out in memory
			// (Absolute full-bleed + VisibilityHidden), so toggling 聊天 <-> 终端 runs ZERO Yoga re-layout
			// and ZERO view remount — flipping native View visibility in <1ms!
			core.View{
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Position: core.Relative, Overflow: core.Hidden},
				Children: []core.Node{
					// Layer 1: Chat View (uses DisplayNone when in VT mode so Android View.GONE never blocks VT touches)
					core.View{
						StyleFunc: func() core.Style {
							if !isChatMode() {
								return core.Style{Display: core.DisplayNone}
							}
							return core.Style{
								Position:      core.Absolute,
								Top:           core.Pt(0),
								Right:         core.Pt(0),
								Bottom:        core.Pt(0),
								Left:          core.Pt(0),
								PointerEvents: core.PointerEventsAuto,
								ZIndex:        2,
							}
						},
						Children: []core.Node{
							core.Each(
								func() []*ChatSessionController {
									ch := curChat()
									if ch == nil {
										return nil
									}
									return []*ChatSessionController{ch}
								},
								func(ch *ChatSessionController) *ChatSessionController { return ch },
								func(cx *core.Scope, item func() *ChatSessionController, _ func() int) core.Node {
									return RenderChatSessionWithCallbacks(
										cx,
										a,
										item(),
										curTerm,
										func() { u.setSessionMode("vt") },
										func(targetKey TermKey) { u.openSession(cx, targetKey) },
										func(replyText string) {
											quotedText.Set(replyText)
											a.notifyToast("已引用回复到输入框")
										}, composerInset.Get,
									)
								},
							),
						},
					},
					// Layer 2: Terminal View + dedicated VT sub-bar (uses DisplayNone when in Chat mode so Android View.GONE never blocks Chat long-press text selection!)
					core.View{
						StyleFunc: func() core.Style {
							if isChatMode() {
								return core.Style{Display: core.DisplayNone}
							}
							return core.Style{
								Position:      core.Absolute,
								Top:           core.Pt(0),
								Right:         core.Pt(0),
								Bottom:        core.Pt(0),
								Left:          core.Pt(0),
								PointerEvents: core.PointerEventsAuto,
								ZIndex:        2,
							}
						},
						Children: []core.Node{
							// Dedicated VT sub-status bar: shows PTY cols×rows + Observe/Fit toggle without touching TopHeader!
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Direction:      core.Row,
										AlignItems:     core.AlignCenter,
										JustifyContent: core.JustifySpaceBetween,
										Gap:            Tokens.Space2,
										Padding:        core.Symmetric(core.Points(5), Tokens.PagePadX),
										Background:     pal.Surface,
										Borders:        core.Edges{Bottom: 1},
										BorderColor:    pal.Separator,
									}
								},
								Children: []core.Node{
									core.Text{
										MaxLines: 1,
										ContentFunc: func() string {
											st := curST()
											cols, rows := 0, 0
											if st != nil {
												cols, rows = st.Screen().Size()
											}
											if cols <= 0 {
												cols = curTerm().Terminal.Cols
												rows = curTerm().Terminal.Rows
											}
											addr := curKey().Addr
											if cols > 0 && rows > 0 {
												if addr != "" {
													return fmt.Sprintf("%s · %d×%d", addr, cols, rows)
												}
												return fmt.Sprintf("%d×%d", cols, rows)
											}
											return addr
										},
										StyleFunc: func() core.Style {
											pal := a.paletteOf(cx)
											return core.Style{
												FlexGrow:   1,
												FlexShrink: 1,
												MinWidth:   core.Pt(0),
												Color:      pal.TextMuted,
												FontSize:   Tokens.FontMonoMeta,
												FontFamily: core.FontMonospace,
											}
										},
									},
									FilterPill(cx, a, "session-fit-toggle", func() string {
										st := curST()
										if st == nil || (st.observeSig != nil && st.observeSig.Get()) || (st.observeSig == nil && st.Observe()) {
											return "观察"
										}
										return "已适配"
									}, func() bool {
										st := curST()
										return st != nil && !st.Observe()
									}, nil, func() {
										st := curST()
										if st == nil {
											return
										}
										nextObserve := !st.Observe()
										st.SetObserve(nextObserve, a.vtFontSize.Get())
										if nextObserve {
											a.notifyToast("已切换为观察模式")
										} else {
											a.notifyToast("已适配窗口尺寸")
										}
									}),
								},
							},
							core.View{
								Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Position: core.Relative, Overflow: core.Hidden},
								Children: []core.Node{
									core.Each(
										func() []*SessionTerminal {
											st := curST()
											if st == nil {
												return nil
											}
											return []*SessionTerminal{st}
										},
										func(st *SessionTerminal) *SessionTerminal { return st },
										func(cx *core.Scope, item func() *SessionTerminal, _ func() int) core.Node {
											return RenderSessionTerminalConfigured(
												cx,
												item(),
												func() Palette { return a.paletteOf(cx) },
												func() VTPalette { return a.vtPaletteOf(cx) },
												a.vtFontSize.Get,
												a.MonoFontFamily,
												func() bool { return !isChatMode() },
											)
										},
									),
									core.Show{
										When: func() bool { return curST() == nil },
										Then: func(cx *core.Scope) core.Node {
											return RenderSessionTerminal(
												cx,
												nil,
												func() Palette { return a.paletteOf(cx) },
												func() VTPalette { return a.vtPaletteOf(cx) },
												a.vtFontSize.Get,
											)
										},
									},
									core.Show{
										When: func() bool {
											st := curST()
											return st != nil && st.scrolledUp != nil && st.scrolledUp.Get()
										},
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												Style: core.Style{
													Position: core.Absolute,
													Right:    core.Pt(14),
													Bottom:   core.Pt(14),
												},
												Children: []core.Node{
													ActionButton(cx, a, "vt-scroll-bottom-fab", "↓ 底部", ActionPrimary, func() {
														if st := curST(); st != nil {
															st.ScrollToBottom()
														}
													}),
												},
											}
										},
									},
								},
							},
						},
					},
					// Floating Quiet Studio Chat Composer: centered at ChatMaxWidth on wide screens, zero drag-handle clutter.
					core.View{
						PointerEvents: core.PointerEventsBoxNone,
						OnLayout: func(b core.Bounds) {
							if b.Size.Height > 0 {
								composerInset.Set(b.Size.Height + 16)
							}
						},
						StyleFunc: func() core.Style {
							if !isChatMode() {
								return core.Style{Display: core.DisplayNone}
							}
							return core.Style{
								Position:   core.Absolute,
								Left:       core.Pt(12),
								Right:      core.Pt(12),
								Bottom:     core.Pt(8),
								AlignItems: core.AlignCenter,
								PaddingEnv: core.EnvEdges{Bottom: core.EnvKeyboard},
								ZIndex:     5,
							}
						},
						Children: []core.Node{
							core.View{
								TestID: "chat-glass-composer",
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									safeBottom := core.Points(0)
									if cx.Env().KeyboardHeight() == 0 {
										safeBottom = max(0, cx.Env().SafeArea().Bottom-4)
									}
									return core.Style{
										Width:        core.Pct(100),
										MaxWidth:     core.Pt(Tokens.ChatMaxWidth),
										Background:   pal.GlassSurface,
										CornerRadius: core.Points(22),
										BorderWidth:  1,
										BorderColor:  pal.GlassBorder,
										Padding:      core.Symmetric(core.Points(10), core.Points(12)),
										Margin:       core.Edges{Bottom: safeBottom},
										Gap:          core.Points(6),
									}
								},
								Children: []core.Node{
									// Optional Quoted Reply chip (commentOnSelection)
									core.Show{
										When: func() bool { return strings.TrimSpace(quotedText.Get()) != "" },
										Then: func(cx *core.Scope) core.Node {
											return core.View{
												TestID: "composer-quote-banner",
												StyleFunc: func() core.Style {
													pal := a.paletteOf(cx)
													return core.Style{
														Direction:    core.Row,
														AlignItems:   core.AlignCenter,
														Gap:          Tokens.Space2,
														Background:   pal.AccentSoft,
														CornerRadius: Tokens.RadiusKey,
														Borders:      core.Edges{Left: 3},
														BorderColor:  pal.Accent,
														Padding:      core.Symmetric(core.Points(4), core.Points(8)),
													}
												},
												Children: []core.Node{
													core.Text{
														MaxLines:    1,
														ContentFunc: func() string { return "引用: " + singleLineClip(quotedText.Get(), 64) },
														StyleFunc: func() core.Style {
															return core.Style{
																FlexGrow:   1,
																FlexShrink: 1,
																MinWidth:   core.Pt(0),
																Color:      a.paletteOf(cx).TextSecondary,
																FontSize:   Tokens.FontMonoMeta,
															}
														},
													},
													core.View{
														TestID: "composer-quote-clear",
														Role:   core.RoleButton,
														OnPress: func(core.PressEvent) {
															quotedText.Set("")
														},
														Style: core.Style{Padding: core.Symmetric(core.Points(2), core.Points(6))},
														Children: []core.Node{
															core.Icon{
																SVG: iconClose,
																StyleFunc: func() core.Style {
																	return core.Style{Width: core.Pt(14), Height: core.Pt(14), Color: a.paletteOf(cx).TextMuted}
																},
															},
														},
													},
												},
											}
										},
									},
									core.Show{
										When: func() bool { return isChatMode() && a.chatImages.Get()[curKey()].name != "" },
										Then: func(cx *core.Scope) core.Node {
											return core.Dynamic(func(cx *core.Scope) core.Node {
												key := curKey()
												draft := a.chatImages.Get()[key]
												chip := chat.Attachment{Label: draft.name}
												if !draft.document {
													chip.Image = core.ImageSource{Data: draft.data}
												}
												if !draft.sending {
													chip.RemoveLabel = "移除附件"
													chip.OnRemove = func() {
														if !a.chatImages.Get()[key].sending {
															a.setChatImage(key, chatImageDraft{})
														}
													}
												}
												children := []core.Node{chip}
												if draft.sending {
													children = append(children, ui.Spinner{Size: 16, Label: "正在发送附件"})
												}
												return core.View{TestID: "chat-image-attachment", Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2}, Children: children}
											})
										},
									},
									slashCommandBar(cx, a, func() string { return a.Compose(curKey().String()) }, curTerm, curChat, func() bool { return isChatMode() && quotedText.Get() == "" }),
									core.Show{When: isChatMode, Then: composerEditor},
									core.Show{When: isChatMode, Then: func(cx *core.Scope) core.Node {
										return core.View{
											PanActivation: core.PanActivation{ActiveOffsetY: 12, FailOffsetX: 16},
											OnPan: func(ev core.PanEvent) {
												if ev.Phase != core.GestureEnded {
													return
												}
												if ev.Translation.Y > 26 {
													if composerTall.Get() {
														animateSpringLayout(cx)
														composerTall.Set(false)
													} else if cx != nil {
														cx.DismissKeyboard()
													}
												} else if ev.Translation.Y < -32 && !composerTall.Get() {
													animateSpringLayout(cx)
													playSelectionHaptic(cx)
													composerTall.Set(true)
												}
											},
											Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space1},
											Children: []core.Node{
												base.Button{
													TestID:  "session-upload-btn",
													Label:   "更多输入操作",
													OnPress: func(core.PressEvent) { uploadAction() },
													Style: core.Style{
														Width:          core.Pt(32),
														Height:         core.Pt(32),
														FlexShrink:     0,
														CornerRadius:   16,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifyCenter,
													},
													StyleFunc: func() core.Style {
														return core.Style{Background: a.paletteOf(cx).SurfaceRaised}
													},
													Children: []core.Node{
														ui.Icon(lucide.Plus, 16, func() core.Color { return a.paletteOf(cx).Text }),
													},
												},
												base.Button{
													TestID: "composer-voice-btn",
													Label:  "语音输入",
													OnPress: func(core.PressEvent) {
														playSelectionHaptic(cx)
														showVoice.Set(curKey())
													},
													Style: core.Style{
														Width:          core.Pt(32),
														Height:         core.Pt(32),
														FlexShrink:     0,
														CornerRadius:   16,
														AlignItems:     core.AlignCenter,
														JustifyContent: core.JustifyCenter,
													},
													StyleFunc: func() core.Style {
														return core.Style{Background: a.paletteOf(cx).SurfaceRaised}
													},
													Children: []core.Node{
														ui.Icon(lucide.Mic, 15, func() core.Color { return a.paletteOf(cx).TextSecondary }),
													},
												},
												core.Show{When: func() bool { return supportsModelEffortPicker(DetectHarnessKind(curTerm().Terminal)) }, Then: func(cx *core.Scope) core.Node {
													return core.Scroll{
														Horizontal:    true,
														HideIndicator: true,
														Style:         core.Style{FlexShrink: 1, MinWidth: core.Pt(0)},
														Children: []core.Node{
															core.View{
																Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: core.Points(4)},
																Children: []core.Node{
																	chat.Chip{TestID: "chat-model-selector", Text: "模型", TextFunc: activeModelChipLabel, Caret: true, OnPress: func() { openAgentSelector(false) }},
																	chat.Chip{TestID: "chat-effort-selector", Text: "思考强度", TextFunc: activeEffortChipLabel, Caret: true, OnPress: func() { openAgentSelector(true) }},
																},
															},
														},
													}
												}},
												core.View{Style: core.Style{FlexGrow: 1}},
												core.Show{
													When: func() bool {
														ch := curChat()
														return ch != nil && ch.ContextUsage().Valid()
													},
													Then: func(cx *core.Scope) core.Node {
														ringTint := func() core.Color {
															pal := a.paletteOf(cx)
															ch := curChat()
															if ch == nil {
																return pal.TextMuted
															}
															ratio := ch.ContextUsage().Ratio()
															switch {
															case ratio >= 0.90:
																return pal.Danger
															case ratio >= 0.75:
																return pal.Warn
															default:
																return pal.Running
															}
														}
														return base.Button{
															TestID: "chat-context-ring",
															LabelFunc: func() string {
																if ch := curChat(); ch != nil {
																	if lbl := ch.ContextUsage().DetailLabel(); lbl != "" {
																		return lbl
																	}
																}
																return "上下文用量"
															},
															OnPress: func(core.PressEvent) {
																playSelectionHaptic(cx)
																if ch := curChat(); ch != nil {
																	if lbl := ch.ContextUsage().DetailLabel(); lbl != "" {
																		a.notifyToast(lbl)
																	}
																}
															},
															Style: core.Style{
																Width:          core.Pt(28),
																Height:         core.Pt(28),
																FlexShrink:     0,
																CornerRadius:   14,
																AlignItems:     core.AlignCenter,
																JustifyContent: core.JustifyCenter,
															},
															Children: []core.Node{
																ui.ProgressRing{
																	Size:      20,
																	LineWidth: 2.4,
																	Value: func() float32 {
																		ch := curChat()
																		if ch == nil {
																			return 0
																		}
																		u := ch.ContextUsage()
																		if !u.Valid() {
																			return 0
																		}
																		pct := u.Percent()
																		if pct < 3 {
																			return 3
																		}
																		return pct
																	},
																	Tint: ringTint,
																},
															},
														}
													},
												},
												renderDeliveryIndicator(cx, a, func() string {
													if c := curChat(); c != nil {
														return c.activity(curTerm().Terminal)
													}
													return ""
												}),
												sendOrInterruptNode(cx),
											},
										}
									}},
								},
							},
						},
					},
				},
			},
			// Terminal-mode bottom dock (KeycapBar + compact terminal composer; hidden in Chat mode!)
			core.View{
				StyleFunc: func() core.Style {
					if isChatMode() {
						return core.Style{Display: core.DisplayNone}
					}
					pal := a.paletteOf(cx)
					return core.Style{
						Display:    core.DisplayFlex,
						Background: pal.Surface,
						PaddingEnv: core.EnvEdges{Bottom: core.EnvKeyboard},
					}
				},
				Children: []core.Node{
					core.Show{When: func() bool { return supportedMux(curTerm().Terminal.Labels[muxKindLabel]) }, Then: func(cx *core.Scope) core.Node {
						return KeycapBar(cx, a, append([]KeycapItem{{TestID: "keycap-mux", LabelFunc: func() string { return curTerm().Terminal.Labels[muxKindLabel] }, OnPress: func() { muxKey.Set(curKey()) }}}, keycaps...))
					}, Else: func(cx *core.Scope) core.Node { return KeycapBar(cx, a, keycaps) }},
					core.Show{
						When: func() bool { return !isChatMode() && composerExpanded.Get() },
						Then: func(cx *core.Scope) core.Node {
							return core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									return core.Style{
										Direction:   core.Row,
										AlignItems:  core.AlignCenter,
										Gap:         Tokens.Space2,
										Padding:     core.Symmetric(Tokens.Space2, Tokens.Space3),
										PaddingEnv:  core.EnvEdges{Bottom: core.EnvSafeArea},
										Background:  pal.Surface,
										Borders:     core.Edges{Top: 1},
										BorderColor: pal.Separator,
									}
								},
								Children: []core.Node{
									IconButton(cx, a, "session-upload-btn", "上传图片", iconImage, false, uploadAction),
									IconButton(cx, a, "session-snippets-btn", "常用短语", iconSnippets, false, func() {
										showSnippets.Set(true)
									}),
									core.View{
										Style:    core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
										Children: []core.Node{composerEditor(cx)},
									},
									sendOrInterruptNode(cx),
								},
							}
						},
					},
				},
			},
			core.Show{When: func() bool { return selector.Get() != "" && selectorKey == curKey() && isChatMode() }, Then: func(cx *core.Scope) core.Node {
				return mobileAgentPicker(cx, a, curTerm(), latestTranscriptModel(), selector.Get() == "思考强度", func() { selector.Set("") })
			}},
			// Quiet Studio Quick Session Switcher Sheet (grouped GroupCards: 需要你 -> 运行中 -> 空闲与在线)
			core.Show{
				When: showSwitcher.Get,
				Then: func(cx *core.Scope) core.Node {
					all := a.aggTerms.Get()
					var waiting, working, idle []AggregatedTerminal
					for _, it := range all {
						if it.Terminal.StateWord() == "finished" {
							continue
						}
						switch {
						case it.Terminal.NeedsYou():
							waiting = append(waiting, it)
						case it.Terminal.StateWord() == "working":
							working = append(working, it)
						default:
							idle = append(idle, it)
						}
					}
					var body []core.Node
					appendGroup := func(title string, list []AggregatedTerminal) {
						if len(list) == 0 {
							return
						}
						tCopy := fmt.Sprintf("%s · %d", title, len(list))
						body = append(body, SectionHeader(cx, a, func() string { return tCopy }, nil))
						var rows []core.Node
						for i, entry := range list {
							if i > 0 {
								rows = append(rows, InsetDivider(cx, a, 34))
							}
							it := entry
							isCur := it.Key == curKey()
							rows = append(rows, core.View{
								TestID: "switcher-item-" + it.Key.String(),
								Role:   core.RoleButton,
								OnPress: func(core.PressEvent) {
									playSelectionHaptic(cx)
									showSwitcher.Set(false)
									if it.Key != curKey() {
										u.openSession(cx, it.Key)
									}
								},
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									bg := core.Transparent
									if isCur {
										bg = pal.AccentSoft
									}
									return core.Style{
										Direction:      core.Row,
										AlignItems:     core.AlignCenter,
										JustifyContent: core.JustifySpaceBetween,
										Gap:            Tokens.Space3,
										Background:     bg,
										Padding:        core.Symmetric(core.Points(11), core.Points(14)),
									}
								},
								Children: []core.Node{
									core.View{
										StyleFunc: func() core.Style {
											return core.Style{
												FlexShrink:   0,
												Width:        core.Pt(8),
												Height:       core.Pt(8),
												CornerRadius: 4,
												Background:   stateDotColor(a.paletteOf(cx), it.Terminal.StateWord()),
											}
										},
									},
									core.View{
										Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: core.Points(2)},
										Children: []core.Node{
											core.Text{
												MaxLines: 1,
												Content:  it.Terminal.Label(),
												StyleFunc: func() core.Style {
													return core.Style{
														Color:      a.paletteOf(cx).Text,
														FontSize:   Tokens.FontRowTitle,
														FontWeight: core.FontWeightMedium,
													}
												},
											},
											core.Text{
												MaxLines: 1,
												Content:  fmt.Sprintf("%s · %s · %s", it.InstanceName, it.Key.Addr, shortPath(it.Terminal.CurrentDir())),
												StyleFunc: func() core.Style {
													return core.Style{
														Color:      a.paletteOf(cx).TextMuted,
														FontSize:   Tokens.FontMonoMeta,
														FontFamily: core.FontMonospace,
													}
												},
											},
										},
									},
									func() core.Node {
										if isCur {
											return core.Icon{
												SVG: iconCheck,
												StyleFunc: func() core.Style {
													return core.Style{Width: core.Pt(16), Height: core.Pt(16), FlexShrink: 0, Color: a.paletteOf(cx).Text}
												},
											}
										}
										return StatusBadge(cx, a, func() string { return it.Terminal.StateWord() })
									}(),
								},
							})
						}
						body = append(body, GroupCard(cx, a, "", rows))
					}
					appendGroup("需要你", waiting)
					appendGroup("运行中", working)
					appendGroup("空闲与在线", idle)
					if len(body) == 0 {
						body = append(body, EmptyStateCard(cx, a, "暂无其他活跃会话", "你可以在首页新建 Claude / Codex 或终端会话。", "", "", nil))
					}
					return SheetModal(cx, a, "sheet-session-switcher", "快速切换会话", "sheet-session-switcher-close", func() {
						showSwitcher.Set(false)
					}, body)
				},
			},
			core.Show{When: func() bool { return showAttachments.Get() && attachmentKey == curKey() && isChatMode() }, Then: func(sheet *core.Scope) core.Node {
				key := attachmentKey
				close := func() { showAttachments.Set(false) }
				expandLabel := "展开长文编辑"
				if composerTall.Get() {
					expandLabel = "收起长文编辑"
				}
				return SheetModal(sheet, a, "sheet-attachments", "输入工具", "attachments-close", close, []core.Node{
					GroupCard(sheet, a, "", []core.Node{
						SheetListRow(sheet, a, "attachment-photo", "相册", "", lucide.Images(), false, false, func() { close(); pickPhoto(key, true) }),
						InsetDivider(sheet, a, 46),
						SheetListRow(sheet, a, "attachment-camera", "拍照", "", lucide.Camera(), false, false, func() {
							close()
							cx.TakePhoto(core.PhotoOptions{MaxDimension: 2400}, func(f *core.PickedFile, err error) {
								if err != nil {
									a.notifyToast("无法拍照：" + err.Error())
									return
								}
								if f != nil {
									a.attachChatImage(key, f.Name, f.MIME, f.Data)
								}
							})
						}),
						InsetDivider(sheet, a, 46),
						SheetListRow(sheet, a, "attachment-clipboard", "剪贴板图片", "", lucide.Clipboard(), false, false, func() {
							close()
							cx.ReadClipboardImage(func(im core.ClipboardImage, ok bool, err error) {
								if err != nil {
									a.notifyToast(err.Error())
									return
								}
								if !ok {
									a.notifyToast("剪贴板中没有图片")
									return
								}
								a.attachChatImage(key, "clipboard.png", "image/png", im.PNG)
							})
						}),
						InsetDivider(sheet, a, 46),
						SheetListRow(sheet, a, "attachment-document", "选择文件", "", lucide.File(), false, false, func() {
							close()
							cx.PickFiles(core.PickFilesOptions{}, func(files []core.File, err error) {
								if err != nil {
									a.notifyToast(err.Error())
									return
								}
								if len(files) == 0 {
									return
								}
								f := files[0]
								go func() {
									data, err := readPickedAttachment(f)
									a.post(func() {
										if err != nil {
											a.notifyToast(err.Error())
											return
										}
										a.attachChatImage(key, f.Name, f.MIME, data)
									})
								}()
							})
						}),
					}),
					GroupCard(sheet, a, "", []core.Node{
						SheetListRow(sheet, a, "session-snippets-btn", "语音与常用短语", "", lucide.TextQuote(), false, true, func() { close(); showSnippets.Set(true) }),
						InsetDivider(sheet, a, 46),
						SheetListRow(sheet, a, "session-expand-composer-btn", expandLabel, "", lucide.Maximize2(), false, false, func() { close(); animateSpringLayout(cx); composerTall.Set(!composerTall.Get()) }),
					}),
				})
			}},
			core.Show{When: func() bool { return showVoice.Get() != (TermKey{}) && showVoice.Get() == curKey() }, Then: func(cx *core.Scope) core.Node {
				return dictationSheet(cx, a, curKey(), func() { showVoice.Set(TermKey{}) })
			}},
			core.Show{When: func() bool { return muxKey.Get() != (TermKey{}) && muxKey.Get() == curKey() && !isChatMode() }, Then: func(cx *core.Scope) core.Node {
				return muxControls(cx, a, curTerm().Terminal.Labels[muxKindLabel], curKey(), func() { muxKey.Set(TermKey{}) })
			}},
			// Snippets bottom sheet
			core.Show{
				When: showSnippets.Get,
				Then: func(cx *core.Scope) core.Node {
					return SheetModal(cx, a, "sheet-snippets", "语音与常用短语", "sheet-snippets-close", func() {
						showSnippets.Set(false)
					}, []core.Node{
						GroupCard(cx, a, "", []core.Node{
							SheetListRow(cx, a, "start-dictation", "语音输入", "", iconMic, false, true, func() { showSnippets.Set(false); showVoice.Set(curKey()) }),
						}),
						GroupCard(cx, a, "", []core.Node{
							core.Each(
								func() []Snippet {
									list := a.snippets.Get()
									if !isChatMode() {
										return list
									}
									var items []Snippet
									for _, v := range list {
										if !v.Binding {
											items = append(items, v)
										}
									}
									return items
								},
								func(s Snippet) string { return s.ID },
								func(cx *core.Scope, item func() Snippet, idx func() int) core.Node {
									sn0 := item()
									return core.View{
										Children: []core.Node{
											core.Show{
												When: func() bool { return idx() > 0 },
												Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 14) },
											},
											core.View{
												TestID: "snippet-item-" + sn0.ID,
												Role:   core.RoleButton,
												OnPress: func(core.PressEvent) {
													playSelectionHaptic(cx)
													sn := item()
													if sn.Binding {
														seq, err := terminalBinding(sn.Text)
														if err != nil {
															a.notifyToast(err.Error())
															return
														}
														st := curST()
														if st == nil || !st.Attached() {
															a.notifyToast("终端尚未接入")
															return
														}
														if err := st.SendInput(seq, a.vtFontSize.Get()); err != nil {
															a.notifyToast(err.Error())
															return
														}
													} else {
														a.SetCompose(curKey().String(), sn.Text)
													}
													showSnippets.Set(false)
												},
												Style: core.Style{
													Padding: core.Symmetric(core.Points(11), core.Points(14)),
													Gap:     core.Points(2),
												},
												Children: []core.Node{
													core.Text{
														ContentFunc: func() string { return item().Label },
														StyleFunc: func() core.Style {
															pal := a.paletteOf(cx)
															return core.Style{Color: pal.Text, FontSize: Tokens.FontRowTitle, FontWeight: core.FontWeightMedium}
														},
													},
													core.Text{
														ContentFunc: func() string { return item().Text },
														StyleFunc: func() core.Style {
															pal := a.paletteOf(cx)
															return core.Style{Color: pal.TextMuted, FontSize: Tokens.FontMonoMeta, FontFamily: core.FontMonospace}
														},
													},
												},
											},
										},
									}
								},
							),
						}),
					})
				},
			},
			core.Show{When: func() bool { return projectKey.Get() != (TermKey{}) && projectKey.Get() == curKey() }, Then: func(cx *core.Scope) core.Node {
				return projectSheet(cx, a, curTerm(), func() { projectKey.Set(TermKey{}) })
			}},
			core.Show{When: func() bool { return previewKey.Get() != (TermKey{}) && previewKey.Get() == curKey() }, Then: func(cx *core.Scope) core.Node {
				return browserPreviewSheet(cx, a, curKey(), func() { previewKey.Set(TermKey{}) })
			}},
			// Quiet Studio Tools / Links / Lifecycle bottom sheet (GroupCard + SheetListRow, Search entry included)
			core.Show{
				When: showTools.Get,
				Then: func(cx *core.Scope) core.Node {
					var links []string
					if st := curST(); st != nil {
						links = st.Screen().ExtractLinks()
					}
					primaryRows := []core.Node{
						SheetListRow(cx, a, "session-search-btn", "搜索当前会话", "", iconSearch, false, false, func() {
							showTools.Set(false)
							animateSpringLayout(cx)
							showSearch.Set(!showSearch.Get())
						}),
					}
					if on, _ := SplitMachineAddr(curKey().Addr); on == "" {
						primaryRows = append(primaryRows,
							InsetDivider(cx, a, 46),
							SheetListRow(cx, a, "tool-preview", "预览主机网页", "", iconGlobe, false, true, func() { showTools.Set(false); previewKey.Set(curKey()) }),
						)
					}
					primaryRows = append(primaryRows,
						InsetDivider(cx, a, 46),
						SheetListRow(cx, a, "tool-project", "会话文件与 Git 差异", "", iconFolderGt, false, true, func() { showTools.Set(false); projectKey.Set(curKey()) }),
						InsetDivider(cx, a, 46),
						SheetListRow(cx, a, "tool-copy-text", "复制干净屏幕文本", "", iconCopy, false, false, func() {
							if st := curST(); st != nil && cx != nil {
								clean := StripHerdrScrollbar(st.Screen().PlainText())
								cx.SetClipboard(clean)
								a.notifyToast("已复制干净终端文本到剪贴板")
							}
							showTools.Set(false)
						}),
						InsetDivider(cx, a, 46),
						SheetListRow(cx, a, "tool-pick-file", "上传本地文件到工作目录", "", iconUpload, false, false, func() {
							showTools.Set(false)
							if cx == nil {
								return
							}
							cx.PickFiles(core.PickFilesOptions{}, func(files []core.File, err error) {
								if err != nil || len(files) == 0 {
									return
								}
								f := files[0]
								if f.Path == "" {
									a.notifyToast("无法读取所选文件路径")
									return
								}
								data, rerr := os.ReadFile(f.Path)
								if rerr != nil {
									a.notifyToast("读取文件失败：" + rerr.Error())
									return
								}
								a.UploadFileTo(curKey(), f.Name, data)
							})
						}),
					)
					dangerRows := []core.Node{
						SheetListRow(cx, a, "tool-interrupt", "发送中断 (Interrupt)", "", iconStop, false, false, func() {
							a.InterruptTo(curKey())
							showTools.Set(false)
						}),
						InsetDivider(cx, a, 46),
						SheetListRow(cx, a, "tool-kill", "终止终端 (Kill)", "", iconOctagonX, true, false, func() {
							a.KillTerminal(curKey())
							showTools.Set(false)
						}),
						InsetDivider(cx, a, 46),
						SheetListRow(cx, a, "tool-rm", "清理已结束终端 (Rm)", "", iconTrash, true, false, func() {
							a.RemoveTerminal(curKey())
							showTools.Set(false)
							u.closeSession()
						}),
					}
					body := []core.Node{
						GroupCard(cx, a, "", primaryRows),
						GroupCard(cx, a, "", dangerRows),
					}
					if len(links) > 0 {
						body = append(body, SectionHeader(cx, a, func() string { return "识别到的链接与路径" }, nil))
						var linkRows []core.Node
						for i, l := range links {
							if i > 0 {
								linkRows = append(linkRows, InsetDivider(cx, a, 46))
							}
							link := l
							ic := iconLink
							if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
								ic = iconExtLink
							}
							linkRows = append(linkRows, SheetListRow(cx, a, fmt.Sprintf("tool-link-%d", i), link, "", ic, false, false, func() {
								if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
									if cx != nil {
										cx.OpenURL(link, nil)
									}
								} else if cx != nil {
									cx.SetClipboard(link)
									a.notifyToast("已复制路径：" + link)
								}
								showTools.Set(false)
							}))
						}
						body = append(body, GroupCard(cx, a, "", linkRows))
					}
					return SheetModal(cx, a, "sheet-session-tools", "会话工具与链接", "sheet-session-tools-close", func() {
						showTools.Set(false)
					}, body)
				},
			},
		},
	}
}

func sessionModeSegmentBtn(cx *core.Scope, a *App, testID, label string, activeFn func() bool, onPress func()) core.Node {
	scale := newPressScale(cx)
	return core.View{
		TestID:      testID,
		Role:        core.RoleButton,
		OnPressDown: func(core.PressEvent) { pressSpringTo(cx, scale, 0.95) },
		OnPressUp:   func(core.PressEvent) { pressSpringTo(cx, scale, 1.0) },
		OnPress: func(core.PressEvent) {
			pressSpringTo(cx, scale, 1.0)
			if onPress != nil {
				onPress()
			}
		},
		Style: core.Style{Animated: pressAnimStyle(scale)},
		StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			bg := core.Transparent
			if activeFn() {
				bg = pal.Surface
			}
			return core.Style{
				FlexShrink:     0,
				MinHeight:      core.Pt(26),
				Padding:        core.Symmetric(core.Points(3), core.Points(11)),
				CornerRadius:   Tokens.RadiusPill,
				Background:     bg,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
			}
		},
		Children: []core.Node{
			core.Text{
				Content: label,
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					fg := pal.TextSecondary
					if activeFn() {
						fg = pal.Text
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
