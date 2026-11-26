package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiTranscriptMatchesExactWorkspace(t *testing.T) {
	home := t.TempDir()
	folder := filepath.Join(home, ".pi", "agent", "sessions", "--repo-my app.v2--")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "date_abc-123.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := &fakeTranscriptBackend{execHandler: func(_ string, p ExecParams) (ExecResult, error) {
		cmd := exec.CommandContext(t.Context(), "sh", "-c", p.Line)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		for k, v := range p.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		out, err := cmd.Output()
		return ExecResult{Stdout: base64.StdEncoding.EncodeToString(out)}, err
	}}
	for _, tc := range []struct{ cwd, id, want string }{{"/repo/my app.v2", "", path}, {"/repo/my app.v2", "absent-123", ""}, {"/repo/other", "", ""}} {
		term := Terminal{Cwd: tc.cwd, Argv: []string{"pi"}}
		if tc.id != "" {
			term.Argv = append(term.Argv, "--session", tc.id)
		}
		got, _ := NewTranscriptResolver().Resolve(t.Context(), TermKey{Addr: "t1"}, term, backend)
		if got.Path != tc.want {
			t.Errorf("cwd=%q session=%q resolved %q, want %q", tc.cwd, tc.id, got.Path, tc.want)
		}
	}
}

func TestPiTurnsIdentifyTheirAgent(t *testing.T) {
	format := TranscriptFormat("pi")
	line := `{"type":"message","id":"a1","message":{"role":"assistant","content":[{"type":"text","text":"回复"}]}}`
	items := ParseTranscriptLines(format, []string{line})
	if len(items) != 1 || items[0].App != "pi" || items[0].Text != "回复" {
		t.Fatalf("Pi turn = %+v", items)
	}
}

func TestUnknownRPCFailureShowsActualOperation(t *testing.T) {
	err := &RPCError{Method: "terminal.put_finish", Code: -32603, Message: "disk full"}
	got := explain(err)
	if !strings.Contains(got, "terminal.put_finish") || !strings.Contains(got, "disk full") {
		t.Fatalf("missing server diagnostic: %s", got)
	}
}
