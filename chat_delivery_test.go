package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatDeliveryAfterUserEcho(t *testing.T) {
	c := NewChatSessionController(nil, TermKey{}, nil, nil, nil)
	defer c.Close()
	c.source = SourceJSONL
	c.lastLine = 40
	id := c.beginChatSend("看这里", string(chatQueue))
	term := Terminal{State: "idle"}
	if c.activity(term) != "正在发送" {
		t.Fatal(c.activity(term))
	}
	c.confirmSubmission(id, chatQueue)
	if !strings.Contains(c.activity(term), "等待") {
		t.Fatal(c.activity(term))
	}
	c.parsedItems = []ChatTurnItem{{ID: "u41", Role: RoleUser, Text: "看这里", SourceLine: 41}}
	c.rebuildMergedLocked()
	if len(c.optimistic) != 0 || !c.shouldPollWorking() || c.Items()[0].DeliveryMode != "排队" {
		t.Fatal("user echo stopped reply tracking or lost the selected mode")
	}
	c.parsedItems = append(c.parsedItems, ChatTurnItem{ID: "t42", Role: RoleThinking, SourceLine: 42})
	c.rebuildMergedLocked()
	if c.activity(term) != "正在思考" {
		t.Fatal(c.activity(term))
	}
	c.parsedItems = append(c.parsedItems, ChatTurnItem{ID: "a43", Role: RoleAssistant, Text: "收到", SourceLine: 43})
	c.rebuildMergedLocked()
	c.confirmSubmission(id, chatQueue)
	if c.activity(term) != "" || c.shouldPollWorking() {
		t.Fatal("late ack resurrected old turn")
	}
}

func TestPiSkillManifestOnlyUsesSystemMessages(t *testing.T) {
	const manifest = `<available_skills><skill><name>render-report</name><description>Render &amp; verify</description></skill></available_skills>`
	got := piSkillManifest([]string{`{"type":"message","message":{"role":"system","sections":{"skills":"` + manifest + `"}}}`, `{"type":"message","message":{"role":"user","content":"<available_skills></available_skills>"}}`})
	if len(got) != 1 || got[0].Name != "skill:render-report" || got[0].Description != "Render & verify" {
		t.Fatalf("skills=%v", got)
	}
}

func TestCommandDiscoveryPerAgent(t *testing.T) {
	for _, tc := range []struct{ kind, dir, want string }{{"pi", ".pi/skills", "skill:inspect"}, {"claude", ".claude/skills", "inspect"}, {"codex", ".agents/skills", "$inspect"}} {
		t.Run(tc.kind, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, tc.dir, "inspect")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: inspect\ndescription: Inspect project\n---\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "sh", "-c", readAgentCommands)
			cmd.Dir = base
			cmd.Env = []string{"HOME=" + base, "PATH=" + os.Getenv("PATH"), "HC_AGENT_KIND=" + tc.kind, "HC_FALLBACK_SKILLS=1"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(out))
			}
			var entries []slashEntry
			if json.Unmarshal(out, &entries) != nil || len(entries) != 1 || entries[0].Name != tc.want {
				t.Fatalf("commands=%s", out)
			}
		})
	}
}

func TestAgentControls(t *testing.T) {
	for _, tc := range []struct{ kind, effort, queue, steer string }{{"pi", "/thinking", "\x1b\r", ""}, {"claude", "/effort", "", "\x18\x13"}, {"codex", "/model", "\t", ""}} {
		if agentSelectorCommand(tc.kind, false) != "/model" || agentSelectorCommand(tc.kind, true) != tc.effort || alternateSubmitKey(tc.kind, chatQueue) != tc.queue || alternateSubmitKey(tc.kind, chatSteer) != tc.steer {
			t.Errorf("incorrect controls for %s", tc.kind)
		}
	}
}

func TestLivePiFollowUpGesture(t *testing.T) {
	if os.Getenv("HERDR_TEST_GESTURE") != "1" {
		t.Skip("opt-in disposable PTY receiver")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	rw, err := dialLocal()
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(rw)
	defer client.Close()
	if _, err = client.HelloFramed(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const script = `import os,sys,tty,pathlib
tty.setraw(0)
p=pathlib.Path(sys.argv[1]);(p/'ready').touch();b=b''
while not b.endswith(b'\x1b\r'): b+=os.read(0,4096)
(p/'result').write_bytes(b)`
	term, err := client.Run(ctx, RunParams{Argv: []string{"python3", "-c", script, dir, "pi"}, NoNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = client.Kill(ctx, term.Address); _ = client.Rm(ctx, term.Address) }()
	for {
		if _, err = os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = client.submitChat(ctx, term.Address, "一\n二", "pi", chatQueue); err != nil {
		t.Fatal(err)
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "result"))
		if err == nil {
			if string(b) != "\x1b[200~一\n二\x1b[201~\x1b\r" {
				t.Fatalf("unexpected input %q", b)
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
