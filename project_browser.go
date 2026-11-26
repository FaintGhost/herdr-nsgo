package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

// Pass the path as argv. The host's current directory is the session root on
// both direct and federated cores. Page by entries or bytes before encoding.
const browseProject = `import json, os, sys
from pathlib import Path
root = Path.cwd().resolve()
p = (root / sys.argv[1]).resolve()
if not p.is_relative_to(root): sys.exit('路径超出会话目录')
offset = int(sys.argv[2])
if p.is_dir():
    hidden = {'.git', 'node_modules', '__pycache__', '.venv', '.next', 'DerivedData'}
    with os.scandir(p) as entries:
        rows = sorted(((e.name, e.is_dir(follow_symlinks=False)) for e in entries if e.name not in hidden and not e.is_symlink() and (e.is_dir(follow_symlinks=False) or e.is_file(follow_symlinks=False))), key=lambda x: (not x[1], x[0].casefold(), x[0]))
    print(json.dumps(dict(directory=True, entries=[dict(name=n, directory=d) for n,d in rows[offset:offset+60]], next=offset+60 if len(rows)>offset+60 else 0), ensure_ascii=False))
elif p.is_file():
    with p.open('rb') as f:
        f.seek(offset)
        data=f.read(16388)
    end=min(16384,len(data))
    while end<len(data) and end>0 and data[end]&0xc0==0x80: end-=1
    if not end and data or b'\0' in data: sys.exit('此文件不能作为文本预览')
    try: text=data[:end].decode('utf-8')
    except UnicodeDecodeError: sys.exit('此文件不是 UTF-8 文本')
    print(json.dumps(dict(text=text, next=offset+end if len(data)>end else 0), ensure_ascii=False))
else: sys.exit('文件已移动或无法访问')`

type projectEntry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
}
type projectPage struct {
	Directory bool           `json:"directory"`
	Entries   []projectEntry `json:"entries"`
	Next      int            `json:"next"`
	Text      string         `json:"text"`
}
type projectLocation struct {
	relative, view string
	offset         int
}

func loadProjectPage(ctx context.Context, b TranscriptBackend, t AggregatedTerminal, loc projectLocation) (projectPage, error) {
	var p projectPage
	cwd := t.Terminal.CurrentDir()
	if cwd == "" {
		return p, errors.New("会话尚未报告工作目录")
	}
	argv := []string{"python3", "-c", browseProject, loc.relative, fmt.Sprint(loc.offset)}
	if loc.view != "files" {
		argv = []string{"git", "--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--no-color"}
		if loc.view == "staged" {
			argv = append(argv, "--cached")
		}
		argv = append(argv, "--", ".")
	}
	on, _ := SplitMachineAddr(t.Key.Addr)
	out, err := b.MachineExec(ctx, t.Key.InstanceID, ExecParams{On: on, Cwd: cwd, Argv: argv, Timeout: 8})
	if err != nil {
		return p, err
	}
	if out.Code == nil || *out.Code != 0 {
		msg := strings.TrimSpace(out.StderrText())
		if msg == "" {
			msg = "主机读取失败"
		}
		return p, errors.New(msg)
	}
	if loc.view == "files" {
		if out.Truncated {
			return p, errors.New("主机输出超过传输上限")
		}
		err = json.Unmarshal([]byte(out.StdoutText()), &p)
		return p, err
	}
	p.Text = out.StdoutText()
	if out.Truncated {
		p.Text += "\n（差异超过传输上限，显示部分内容）"
	}
	if p.Text == "" {
		p.Text = "没有差异"
	}
	return p, nil
}
func projectSheet(cx *core.Scope, a *App, t AggregatedTerminal, close func()) core.Node {
	life := cx.Context()
	backend := a.TranscriptBackend()
	loc := core.NewSignal(cx, projectLocation{relative: ".", view: "files"})
	page := core.NewSignalFunc(cx, projectPage{}, nil)
	msg := core.NewSignal(cx, "")
	pending := core.NewSignal(cx, false)
	var history []projectLocation
	var generation uint64
	load := func(next projectLocation) {
		generation++
		id := generation
		loc.Set(next)
		pending.Set(true)
		page.Set(projectPage{})
		msg.Set("读取中…")
		go func() {
			ctx, cancel := context.WithTimeout(life, 12*time.Second)
			defer cancel()
			p, err := loadProjectPage(ctx, backend, t, next)
			cx.Post(func() {
				if generation != id {
					return
				}
				pending.Set(false)
				msg.Set("")
				if err != nil {
					msg.Set(err.Error())
					return
				}
				page.Set(p)
			})
		}()
	}
	visit := func(next projectLocation) { history = append(history, loc.Get()); load(next) }
	load(loc.Get())
	label := func(f func() string) core.Node {
		return core.Text{ContentFunc: f, Selectable: core.SelectableOn, StyleFunc: func() core.Style {
			return core.Style{Color: a.paletteOf(cx).Text, FontFamily: core.FontMonospace, FontSize: Tokens.FontMonoMeta}
		}}
	}
	var tabs []core.Node
	for _, tab := range []struct{ key, title string }{{"files", "文件"}, {"changes", "工作区"}, {"staged", "暂存区"}} {
		tabs = append(tabs, FilterPill(cx, a, "project-"+tab.key, func() string { return tab.title }, func() bool { return loc.Get().view == tab.key }, nil, func() { history = nil; load(projectLocation{relative: ".", view: tab.key}) }))
	}
	return SheetModal(cx, a, "project-sheet", "会话文件与差异", "project-close", close, []core.Node{
		core.View{
			Style: core.Style{
				Direction:      core.Row,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifySpaceBetween,
				Gap:            Tokens.Space2,
			},
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, Gap: Tokens.Space2}, Children: tabs},
				IconTap(cx, a, "project-refresh", "刷新", iconRefresh, func() { load(loc.Get()) }),
			},
		},
		core.Text{
			MaxLines: 1,
			ContentFunc: func() string {
				return path.Join(t.Terminal.CurrentDir(), loc.Get().relative)
			},
			StyleFunc: func() core.Style {
				return core.Style{Color: a.paletteOf(cx).TextMuted, FontFamily: core.FontMonospace, FontSize: Tokens.FontMonoMeta}
			},
		},
		core.Show{When: func() bool { _ = loc.Get(); _ = pending.Get(); return len(history) > 0 }, Then: func(cx *core.Scope) core.Node {
			return GroupCard(cx, a, "", []core.Node{
				SheetListRow(cx, a, "project-back", "返回上一级", "", iconBack, false, false, func() {
					last := len(history) - 1
					previous := history[last]
					history = history[:last]
					load(previous)
				}),
			})
		}},
		core.View{
			TestID: "project-scroll",
			Style:  core.Style{Gap: Tokens.Space2},
			Children: []core.Node{
				core.Show{
					When: func() bool {
						if msg.Get() != "" {
							return true
						}
						p := page.Get()
						return (p.Directory && len(p.Entries) == 0) || p.Text != ""
					},
					Then: func(cx *core.Scope) core.Node {
						return GroupCard(cx, a, "", []core.Node{
							core.View{
								Style: core.Style{Padding: core.All(core.Points(12))},
								Children: []core.Node{
									label(func() string {
										if s := msg.Get(); s != "" {
											return s
										}
										p := page.Get()
										if p.Directory && len(p.Entries) == 0 {
											return "空目录"
										}
										return p.Text
									}),
								},
							},
						})
					},
				},
				core.Show{
					When: func() bool { return len(page.Get().Entries) > 0 },
					Then: func(cx *core.Scope) core.Node {
						return GroupCard(cx, a, "", []core.Node{
							core.Each(func() []projectEntry { return page.Get().Entries }, func(e projectEntry) string { return e.Name }, func(cx *core.Scope, item func() projectEntry, idx func() int) core.Node {
								e0 := item()
								ic := iconFileCode
								if e0.Directory {
									ic = iconFolder
								}
								return core.View{
									Children: []core.Node{
										core.Show{
											When: func() bool { return idx() > 0 },
											Then: func(cx *core.Scope) core.Node { return InsetDivider(cx, a, 46) },
										},
										SheetListRowFunc(cx, a, "project-entry-"+e0.Name, func() string {
											e := item()
											if e.Directory {
												return e.Name + "/"
											}
											return e.Name
										}, nil, ic, false, e0.Directory, func() {
											visit(projectLocation{view: "files", relative: path.Join(loc.Get().relative, item().Name)})
										}),
									},
								}
							}),
						})
					},
				},
			},
		},
		core.Show{When: func() bool { return page.Get().Next != 0 }, Then: func(cx *core.Scope) core.Node {
			return ActionButton(cx, a, "project-next", "下一页", ActionSecondary, func() { next := loc.Get(); next.offset = page.Get().Next; visit(next) })
		}},
	})
}
