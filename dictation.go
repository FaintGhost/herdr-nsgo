package main

import (
	"github.com/nsgo-dev/nsgo/core"
	"strings"
)

// Keep recognition in the sheet. Closing it discards its transcription and
// aborts the microphone session through the scope's cleanup.
func dictationSheet(cx *core.Scope, a *App, key TermKey, close func()) core.Node {
	transcript := core.NewSignal(cx, "")
	listening := core.NewSignal(cx, true)
	status := core.NewSignal(cx, "正在启动麦克风…")
	session, err := cx.StartSpeech(core.SpeechOptions{Interim: true, OnStart: func() { status.Set("正在听写…") }, OnResult: func(s string, _ bool) { transcript.Set(s) }, OnEnd: func(s string, err error) {
		listening.Set(false)
		transcript.Set(s)
		if err != nil {
			status.Set("听写结束：" + err.Error())
		} else if strings.TrimSpace(s) == "" {
			status.Set("没有识别到语音")
		} else {
			status.Set("识别结果")
		}
	}})
	if err != nil {
		status.Set(err.Error())
		listening.Set(false)
	}
	return SheetModal(cx, a, "dictation-sheet", "语音输入", "dictation-close", close, []core.Node{
		GroupCard(cx, a, "", []core.Node{
			core.View{
				Style: core.Style{Padding: core.All(core.Points(14)), Gap: Tokens.Space2},
				Children: []core.Node{
					core.View{
						Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2},
						Children: []core.Node{
							core.View{
								StyleFunc: func() core.Style {
									pal := a.paletteOf(cx)
									bg := pal.Fill
									if listening.Get() {
										bg = pal.Running
									}
									return core.Style{Width: core.Pt(8), Height: core.Pt(8), CornerRadius: 4, Background: bg}
								},
							},
							core.Text{
								ContentFunc: status.Get,
								StyleFunc: func() core.Style {
									return core.Style{Color: a.paletteOf(cx).TextSecondary, FontSize: Tokens.FontCaption}
								},
							},
						},
					},
					core.Text{
						ContentFunc: transcript.Get,
						StyleFunc: func() core.Style {
							return core.Style{Color: a.paletteOf(cx).Text, FontSize: Tokens.FontBody, LineHeight: core.Points(22)}
						},
					},
				},
			},
		}),
		core.Show{When: listening.Get, Then: func(cx *core.Scope) core.Node {
			return ActionButton(cx, a, "dictation-stop", "结束听写", ActionSecondary, func() {
				if session != nil {
					session.Stop()
					status.Set("正在完成识别…")
				}
			})
		}},
		core.Show{When: func() bool { return !listening.Get() && strings.TrimSpace(transcript.Get()) != "" }, Then: func(cx *core.Scope) core.Node {
			return ActionButton(cx, a, "dictation-insert", "加入草稿", ActionPrimary, func() {
				draft := a.Compose(key.String())
				if draft != "" && !strings.HasSuffix(draft, "\n") {
					draft += "\n"
				}
				a.SetCompose(key.String(), draft+strings.TrimSpace(transcript.Get()))
				close()
			})
		}},
	})
}
