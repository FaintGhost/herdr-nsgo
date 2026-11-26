package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestMuxPickerNavigation(t *testing.T) {
	b := &fakeTranscriptBackend{execHandler: func(_ string, p ExecParams) (ExecResult, error) {
		s := "$2|project one\n"
		if slices.Contains(p.Argv, "zellij") {
			s = "daily work [Created 2h ago] \narchive [Created 1d ago] (EXITED - attach to resurrect)\n"
		}
		return ExecResult{Code: new(int32(0)), Stdout: base64.StdEncoding.EncodeToString([]byte(s))}, nil
	}}
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		app, node := testSeededApp(cx)
		app.SetTranscriptBackend(b)
		return node
	}, nsgotest.Size(390, 844))
	h.Press("id:fab-new-session")
	h.Press("id:new-host-mux")
	wait := func(selector string) {
		t.Helper()
		for end := time.Now().Add(time.Second); time.Now().Before(end); {
			h.Settle()
			if len(h.Query(selector)) == 1 {
				return
			}
			time.Sleep(time.Millisecond)
		}
		h.Find(selector)
	}
	wait("text:project one")
	h.Press("id:mux-kind-zellij")
	wait("text:archive · 可恢复")
	if len(h.Query("text:project one")) != 0 {
		t.Fatal("old list survived source change")
	}
	h.Press("id:mux-session-daily work")
	wait("text:目标实例未在线")
	h.Press("id:mux-back")
	h.Find("id:sheet-new-session")
	h.Find("id:tpl-chip-claude")
}

func TestMuxRunAndReuse(t *testing.T) {
	s := muxSession{name: "-a $(echo OOPS)", target: "$12"}
	for _, k := range []string{"tmux", "zellij"} {
		params, err := muxRunParams(k, "peer", "/project", s, false)
		if err != nil || params.On != "peer" || params.Cwd != "" {
			t.Fatal(params, err)
		}
		last := params.Argv[len(params.Argv)-1]
		if (k == "tmux" && last != s.target) || (k == "zellij" && last != s.name) {
			t.Fatal(params)
		}
		if strings.Contains(params.Argv[2], s.name) {
			t.Fatal("interpolated host name")
		}
		rows := []Terminal{{Address: "t1", Labels: params.Labels}, {Address: "peer/t2", ExitCode: new(0), Labels: params.Labels}, {Address: "peer/t3", Labels: params.Labels}}
		if key, ok := existingMuxClient(rows, "host", params); !ok || key != (TermKey{"host", "peer/t3"}) {
			t.Fatal(key, ok)
		}
		for _, a := range muxShortcuts(k) {
			if _, err := terminalBinding(a.binding); err != nil {
				t.Fatal(a, err)
			}
		}
	}
}

// A disposable Linux container supplies the two host binaries. The normal
// Herdr RPC still starts a real PTY and receives the same input as the app.
func TestLiveMuxLifecycle(t *testing.T) {
	container := os.Getenv("HERDR_TEST_MUX_CONTAINER")
	if container == "" {
		t.Skip("set HERDR_TEST_MUX_CONTAINER to a disposable container with tmux and zellij")
	}
	rw, err := dialLocal()
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(rw)
	defer c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	defer cancel()
	if _, err = c.HelloFramed(ctx); err != nil {
		t.Fatal(err)
	}
	backend := &fakeTranscriptBackend{execHandler: func(_ string, p ExecParams) (ExecResult, error) {
		p.Argv = append([]string{"docker", "exec", container}, p.Argv...)
		return c.MachineExec(ctx, p)
	}}
	for _, k := range []string{"tmux", "zellij"} {
		t.Run(k, func(t *testing.T) {
			name := fmt.Sprintf("nsgo-%d", time.Now().UnixNano())
			var session = muxSession{name: name}
			for _, create := range []bool{true, false} {
				p, err := muxRunParams(k, "", "", session, create)
				if err != nil {
					t.Fatal(err)
				}
				p.Argv = append([]string{"docker", "exec", "-it", "-e", "TERM=xterm-256color", container}, p.Argv...)
				res, err := c.Run(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _, _ = c.Kill(ctx, res.Address); _ = c.Rm(ctx, res.Address) }()
				stream, _, err := c.Attach(ctx, AttachParams{Terminal: res.Address, Cols: 100, Rows: 32})
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				deadline := time.Now().Add(9 * time.Second)
				for {
					v, err := c.Read(ctx, res.Address, 0)
					if err != nil {
						t.Fatal(err)
					}
					if k == "tmux" && strings.Contains(v.Text, "/#") || k == "zellij" && strings.Contains(v.Text, "Tab #1") {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("no remote screen: %q", v.Text)
					}
					time.Sleep(80 * time.Millisecond)
				}
				for _, sc := range muxShortcuts(k) {
					if sc.id == "detach" {
						if err := sendMuxShortcut(ctx, c, res.Address, sc); err != nil {
							t.Fatal(err)
						}
					}
				}
				deadline = time.Now().Add(6 * time.Second)
				for {
					r, err := c.List(ctx, true)
					if err != nil {
						t.Fatal(err)
					}
					idx := slices.IndexFunc(r.Terminals, func(v Terminal) bool { return v.Address == res.Address })
					if idx < 0 || r.Terminals[idx].Finished() {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("client did not detach")
					}
					time.Sleep(80 * time.Millisecond)
				}
				items, err := listMuxSessions(ctx, backend, "local", "", k)
				if err != nil {
					t.Fatal(err)
				}
				idx := slices.IndexFunc(items, func(v muxSession) bool { return v.name == name })
				if idx < 0 {
					t.Fatalf("detached host session missing: %v", items)
				}
				session = items[idx]
			}
		})
	}
}
