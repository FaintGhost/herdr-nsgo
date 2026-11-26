package main

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// Forward this preview's TCP streams over the same authenticated SSH client as
// hc connect. The listener and all accepted streams belong to the open sheet.
func openPreview(ctx context.Context, port string, dial func(context.Context, string, string) (net.Conn, error)) (string, error) {
	n, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("请输入 1 到 65535 之间的端口")
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(n))
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	context.AfterFunc(ctx, func() { ln.Close() })
	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer local.Close()
				open, cancel := context.WithTimeout(ctx, 10*time.Second)
				remote, err := dial(open, "tcp", addr)
				cancel()
				if err != nil {
					return
				}
				defer remote.Close()
				stop := context.AfterFunc(ctx, func() { remote.Close(); local.Close() })
				defer stop()
				go func() {
					_, _ = io.Copy(remote, local)
					if half, ok := remote.(interface{ CloseWrite() error }); ok {
						_ = half.CloseWrite()
					}
				}()
				_, _ = io.Copy(local, remote)
			}()
		}
	}()
	return "http://" + ln.Addr().String(), nil
}
func browserPreviewSheet(cx *core.Scope, a *App, key TermKey, close func()) core.Node {
	port := core.NewSignal(cx, "3000")
	url := core.NewSignal(cx, "")
	feedback := core.NewSignal(cx, "")
	var cancel context.CancelFunc
	open := func() {
		c := a.pool.ClientFor(key.InstanceID)
		if c == nil {
			feedback.Set("实例未连接")
			return
		}
		conn, ok := c.rw.(*conn)
		if !ok || conn.dialPreview == nil {
			feedback.Set("当前连接不能转发端口")
			return
		}
		if cancel != nil {
			cancel()
		}
		var ctx context.Context
		ctx, cancel = context.WithCancel(cx.Context())
		s, err := openPreview(ctx, port.Get(), conn.dialPreview)
		if err != nil {
			feedback.Set(err.Error())
			return
		}
		feedback.Set("")
		url.Set(s)
	}
	return SheetModal(cx, a, "browser-preview", "主机网页预览", "preview-close", close, []core.Node{
		core.View{
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2},
			Children: []core.Node{
				ui.Input{
					TestID:      "preview-port",
					Placeholder: "HTTP 端口（如 3000）",
					Value:       port.Get,
					OnChange:    port.Set,
					StyleFunc: func() core.Style {
						pal := a.paletteOf(cx)
						return core.Style{
							FlexGrow:     1,
							MinHeight:    core.Pt(Tokens.SizeTouch),
							Background:   pal.Surface,
							Color:        pal.Text,
							CornerRadius: Tokens.RadiusCtrl,
							BorderWidth:  1,
							BorderColor:  pal.Border,
						}
					},
				},
				ActionButton(cx, a, "preview-open", "打开", ActionPrimary, open),
			},
		},
		core.Show{
			When: func() bool { return strings.TrimSpace(feedback.Get()) != "" },
			Then: func(cx *core.Scope) core.Node {
				return core.Text{
					ContentFunc: feedback.Get,
					StyleFunc: func() core.Style {
						return core.Style{Color: a.paletteOf(cx).Danger, FontSize: Tokens.FontCaption}
					},
				}
			},
		},
		core.Show{When: func() bool { return url.Get() != "" }, Then: func(cx *core.Scope) core.Node {
			return core.View{
				StyleFunc: func() core.Style {
					pal := a.paletteOf(cx)
					return core.Style{
						MinHeight:    core.Pt(420),
						CornerRadius: Tokens.RadiusCard,
						BorderWidth:  1,
						BorderColor:  pal.Border,
						Overflow:     core.Hidden,
					}
				},
				Children: []core.Node{
					core.WebView{
						TestID:     "preview-web",
						Label:      "主机网页",
						SourceFunc: func() core.WebSource { return core.WebSrc(url.Get()) },
						Incognito:  true,
						PinchZoom:  true,
						Style:      core.Style{FlexGrow: 1, MinHeight: core.Pt(420)},
						OnError:    func(err error) { feedback.Set("网页加载失败：" + err.Error()) },
					},
				},
			}
		}},
	})
}
