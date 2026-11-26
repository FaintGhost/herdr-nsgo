package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLiveMobilePicker(t *testing.T) {
	binary := os.Getenv("HERDR_TEST_PI_BINARY")
	if binary == "" {
		t.Skip("set HERDR_TEST_PI_BINARY for an isolated Pi selector")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	rw, err := dialLocal()
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(rw)
	defer c.Close()
	if _, err = c.HelloFramed(ctx); err != nil {
		t.Fatal(err)
	}
	term, err := c.Run(ctx, RunParams{Argv: []string{binary, "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates"}, Cwd: t.TempDir(), NoNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 4*time.Second)
		defer stop()
		_, _ = c.Kill(clean, term.Address)
		_ = c.Rm(clean, term.Address)
	}()
	r := remotePicker{client: c, kind: "pi", addr: term.Address}
	for {
		out, err := c.Read(ctx, term.Address, 0)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.Text, "ctrl+o") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	p, err := r.open(ctx, true)
	if err != nil {
		t.Fatal("thinking", err)
	}
	if len(p.rows) < 2 {
		t.Fatal(p)
	}
	old := p.rows[p.cursor]
	target := p.rows[(p.cursor+1)%len(p.rows)]
	if _, err = r.apply(ctx, p, target); err != nil {
		t.Fatal(err)
	}
	p, err = r.open(ctx, true)
	if err != nil || p.rows[p.cursor] != target {
		t.Fatalf("thinking wanted %s, got %+v, %v", target, p, err)
	}
	if _, err = r.apply(ctx, p, old); err != nil {
		t.Fatal(err)
	}
	p, err = r.open(ctx, false)
	if err != nil || len(p.rows) < 2 {
		t.Fatalf("model open: %+v %v", p, err)
	}
	target = p.rows[(p.cursor+1)%len(p.rows)]
	if _, err = r.apply(ctx, p, target); err != nil {
		t.Fatal("apply model", err)
	}
	p, err = r.open(ctx, false)
	if err != nil || !strings.HasPrefix(p.rows[p.cursor], strings.Split(target, " (")[0]+" ") {
		t.Fatalf("expected model %q, got %+v, %v", target, p, err)
	}
	r.cleanup()
	t.Log("verified explicit model and effort selections in a disposable session")
}

func TestClaudeEffortSlider(t *testing.T) {
	p, ok := parsePickerPage(" Effort\n Faster                             Smarter\n ────────────────────▲─────────────────────\n low     medium     high     xhigh      max\n ←/→ to adjust · s to use this session only · Enter to save · Esc to cancel", "claude")
	if !ok || !p.effort || !p.sessionKey || p.current != "high" || p.rows[p.cursor] != "high" {
		t.Fatal(p)
	}
}

func TestCodexModelAndEffortMenus(t *testing.T) {
	p, ok := parsePickerPage("Select Model and Effort\n› 1. GPT-6.2-Sol    Fast\n  2. GPT-6.2-Astra   Other\n↓\n enter select · esc back", "codex")
	if !ok || p.effort || !p.more || p.rows[1] != "GPT-6.2-Astra" {
		t.Fatal(p)
	}
	p, ok = parsePickerPage("Select Reasoning Level for GPT-6.2-Astra\n› 1. Low (default)    Fast\n  2. High     More reasoning\n enter default · s session · esc back", "codex")
	if !ok || !p.effort || !p.sessionKey || p.rows[1] != "High" {
		t.Fatal(p)
	}
}

type existingMenuTransport struct {
	screen string
	keys   []string
	sends  int
}

func (m *existingMenuTransport) Read(context.Context, string, int) (ReadResult, error) {
	return ReadResult{Text: m.screen}, nil
}
func (m *existingMenuTransport) Send(context.Context, string, string) (SendResult, error) {
	m.sends++
	return SendResult{}, nil
}
func (m *existingMenuTransport) Keys(_ context.Context, _ string, keys ...string) (KeysResult, error) {
	m.keys = append(m.keys, keys...)
	m.screen = ""
	return KeysResult{}, nil
}
func TestReopenAndCancelExistingPicker(t *testing.T) {
	m := &existingMenuTransport{screen: "Thinking Level\nShift+Tab cycles thinking levels in-session\n→ low   light\n✓ high   deep\nEnter to select · Escape/Ctrl+C to cancel"}
	r := remotePicker{client: m, kind: "pi"}
	p, err := r.open(t.Context(), true)
	if err != nil || p.current != "high" || len(m.keys) != 0 || m.sends != 0 {
		t.Fatalf("open selected highlighted Low: %+v, %v", p, err)
	}
	r.cleanup()
	if !slices.Equal(m.keys, []string{"esc"}) {
		t.Fatalf("cancel sent %v", m.keys)
	}
}

func TestPiSelectionScreen(t *testing.T) {
	p, ok := parsePickerPage("Thinking Level\nShift+Tab cycles thinking levels in-session\nlow   light\n→ ✓ high   deep\nEnter to select · Escape/Ctrl+C to cancel", "pi")
	if !ok || !slices.Equal(p.rows, []string{"low", "high"}) || p.cursor != 1 {
		t.Fatal(p)
	}
	if _, ok = parsePickerPage("Trust this directory?\n› 1. Yes\n2. No\nEnter to confirm, Esc to cancel", "codex"); ok {
		t.Fatal("accepted trust prompt")
	}
}
