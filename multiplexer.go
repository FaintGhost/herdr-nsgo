package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

const muxKindLabel = "nsgo.mux"
const muxNameLabel = "nsgo.mux.session"

type muxSession struct {
	name, target string
	resumable    bool
}

// Use the same non-interactive PATH for discovery and attach. Session names
// are separate argv entries and never interpolated into this launcher.
func muxArgv(kind string, args ...string) []string {
	argv := []string{"sh", "-c", `export PATH="$PATH:$HOME/.local/bin:$HOME/.cargo/bin:/opt/homebrew/bin:/usr/local/bin" LC_ALL=C; exec "$@"`, "nsgo-mux", kind}
	return append(argv, args...)
}

func listMuxSessions(ctx context.Context, b TranscriptBackend, instance, machine, kind string) ([]muxSession, error) {
	var args []string
	switch kind {
	case "tmux":
		args = []string{"list-sessions", "-F", "#{session_id}|#{session_name}"}
	case "zellij":
		args = []string{"list-sessions", "--no-formatting"}
	default:
		return nil, errors.New("未知的多路复用器")
	}
	res, err := b.MachineExec(ctx, instance, ExecParams{On: machine, Argv: muxArgv(kind, args...), Timeout: 6})
	if err != nil {
		return nil, err
	}
	if res.Code == nil {
		return nil, errors.New("主机未返回退出状态")
	}
	if res.Truncated {
		return nil, errors.New("会话列表超过主机传输上限")
	}
	if *res.Code != 0 {
		detail := strings.TrimSpace(res.StderrText())
		if detail == "" {
			detail = strings.TrimSpace(res.StdoutText())
		}
		switch {
		case *res.Code == 127:
			return nil, fmt.Errorf("主机找不到 %s，请检查安装或 PATH", kind)
		case kind == "tmux" && (strings.HasPrefix(detail, "no server running on ") || strings.HasPrefix(detail, "error connecting to ") && strings.HasSuffix(detail, "(No such file or directory)")), kind == "zellij" && strings.EqualFold(strings.TrimSuffix(detail, "."), "No active zellij sessions found"):
			return nil, nil
		}
		return nil, fmt.Errorf("%s 列表读取失败 (%d)：%s", kind, *res.Code, detail)
	}
	var sessions []muxSession
	for line := range strings.SplitSeq(res.StdoutText(), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		var s muxSession
		var ok bool
		if kind == "tmux" {
			s.target, s.name, ok = strings.Cut(line, "|")
			if !ok || !strings.HasPrefix(s.target, "$") {
				return nil, errors.New("无法识别 tmux 会话列表")
			}
		} else {
			s.name, _, ok = strings.CutLast(line, " [Created ")
			if !ok {
				return nil, errors.New("无法识别 Zellij 会话列表")
			}
			s.target = s.name
			s.resumable = strings.Contains(line[len(s.name):], "EXITED")
		}
		if s.name != "" {
			sessions = append(sessions, s)
		}
	}
	return sessions, nil
}

func muxRunParams(kind, machine, cwd string, s muxSession, create bool) (RunParams, error) {
	if strings.TrimSpace(s.name) == "" || strings.IndexFunc(s.name, unicode.IsControl) >= 0 {
		return RunParams{}, errors.New("请输入有效的会话名称")
	}
	p := RunParams{On: machine, NoNotify: true, Labels: map[string]string{muxKindLabel: kind, muxNameLabel: s.name}}
	if create {
		p.Cwd = cwd
	}
	switch kind {
	case "tmux":
		if create {
			if strings.ContainsAny(s.name, ".:") {
				return RunParams{}, errors.New("tmux 会话名不能含句点或冒号")
			}
			p.Argv = muxArgv(kind, "new-session", "-A", "-s", s.name)
		} else {
			p.Argv = muxArgv(kind, "attach-session", "-t", s.target)
		}
	case "zellij":
		args := []string{"attach"}
		if create {
			args = append(args, "--create")
		}
		p.Argv = muxArgv(kind, append(args, "--", s.name)...)
	default:
		return RunParams{}, errors.New("未知的多路复用器")
	}
	return p, nil
}

func (a *App) connectMux(ctx context.Context, instance string, params RunParams, create bool) (TermKey, error) {
	c := a.pool.ClientFor(instance)
	if c == nil {
		return TermKey{}, errors.New("目标实例未在线")
	}
	existing, err := c.List(ctx, false)
	if err != nil {
		return TermKey{}, err
	}
	key, found := existingMuxClient(existing.Terminals, instance, params)
	if !found {
		res, err := c.Run(ctx, params)
		if err != nil {
			return TermKey{}, err
		}
		key = TermKey{instance, res.Address}
	}
	if create {
		err = waitMuxCreation(ctx, a.TranscriptBackend(), instance, params)
		if err != nil {
			return TermKey{}, fmt.Errorf("终端 %s：%w", key.Addr, err)
		}
	}
	return key, nil
}

func waitMuxCreation(ctx context.Context, b TranscriptBackend, instance string, p RunParams) error {
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	kind, name := p.Labels[muxKindLabel], p.Labels[muxNameLabel]
	for {
		sessions, err := listMuxSessions(ctx, b, instance, p.On, kind)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			return err
		}
		if slices.ContainsFunc(sessions, func(s muxSession) bool { return s.name == name && !s.resumable }) {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(350 * time.Millisecond):
		}
	}
	return fmt.Errorf("主机尚未列出 %s 会话 %q，请检查启动配置后刷新", kind, name)
}

func existingMuxClient(terminals []Terminal, instance string, params RunParams) (TermKey, bool) {
	for _, t := range terminals {
		on, _ := SplitMachineAddr(t.Address)
		if on == params.On && !t.Finished() && t.Labels[muxKindLabel] == params.Labels[muxKindLabel] && t.Labels[muxNameLabel] == params.Labels[muxNameLabel] {
			return TermKey{instance, t.Address}, true
		}
	}
	return TermKey{}, false
}

// A mode-changing prefix and its follow-up need distinct input events in
// Zellij. A single VT write may be interpreted as pasted text instead.
func sendMuxShortcut(ctx context.Context, c *Client, addr string, action muxShortcut) error {
	prefix, key, ok := strings.Cut(action.binding, ",")
	if !ok {
		return errors.New("无效按键序列")
	}
	prefix = "ctrl+" + strings.TrimPrefix(prefix, "C-")
	if _, err := c.Keys(ctx, addr, prefix); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(160 * time.Millisecond):
	}
	_, err := c.Keys(ctx, addr, key)
	return err
}

type muxShortcut struct{ id, label, binding string }

func muxShortcuts(kind string) []muxShortcut {
	switch kind {
	case "tmux":
		return []muxShortcut{{"prev", "上一窗口", "C-b,p"}, {"next", "下一窗口", "C-b,n"}, {"new", "新窗口", "C-b,c"}, {"split-h", "左右分屏", "C-b,%"}, {"split-v", "上下分屏", `C-b,"`}, {"pane", "下一窗格", "C-b,o"}, {"detach", "分离", "C-b,d"}}
	case "zellij":
		items := []muxShortcut{{"prev", "上一标签", "C-t,h"}, {"next", "下一标签", "C-t,l"}, {"new", "新标签", "C-t,n"}, {"pane", "新窗格", "C-p,n"}, {"split-v", "下方分屏", "C-p,d"}, {"detach", "分离", "C-o,d"}}
		for i := 1; i <= 9; i++ {
			n := fmt.Sprint(i)
			items = append(items, muxShortcut{"tab-" + n, "标签 " + n, "C-t," + n})
		}
		return items
	}
	return nil
}

func supportedMux(kind string) bool { return slices.Contains([]string{"tmux", "zellij"}, kind) }
