package main

import (
	"context"
	"strings"
	"testing"
)

func TestPiLongSessionReadsHeaderThinkingLevelAndModelContextWindow(t *testing.T) {
	key := TermKey{InstanceID: "mac", Addr: "t5"}
	path := "/Users/dev/.pi/agent/sessions/--repo--/2026-11-24_s1.jsonl"

	// Build a Pi session longer than the 250-line tail window where thinking_level_change="high"
	// and model_change="remote-magpie/codex/gpt-6.2-astra" live at lines 2-4!
	lines := []string{
		`{"type":"session","version":3,"id":"s1","cwd":"/repo"}`,
		`{"type":"model_change","id":"m1","provider":"magpie","modelId":"remote-magpie/auto/deepseek-flash"}`,
		`{"type":"thinking_level_change","id":"th1","thinkingLevel":"high"}`,
		`{"type":"model_change","id":"m2","provider":"magpie","modelId":"remote-magpie/codex/gpt-6.2-astra"}`,
	}
	for i := 0; i < 270; i++ {
		lines = append(lines, `{"type":"message","id":"u`+string(rune('a'+(i%26)))+`","message":{"role":"user","content":[{"type":"text","text":"ping"}]}}`)
	}
	lines = append(lines,
		`{"type":"message","id":"a-last","message":{"role":"assistant","model":"remote-magpie/codex/gpt-6.2-astra","usage":{"input":136000,"cacheRead":0,"output":200},"content":[{"type":"text","text":"done"}]}}`,
	)

	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			key: {
				Key: key,
				Terminal: Terminal{
					Address:    "t5",
					Cwd:        "/repo",
					Argv:       []string{"pi", "--session", path},
					Foreground: &Foreground{PID: 700, Name: "pi", Cwd: "/repo"},
				},
			},
		},
		files: map[string][]string{
			"::" + path: lines,
		},
	}

	resolver := NewTranscriptResolver()
	ctrl := NewChatSessionController(nil, key, fb, resolver, nil)
	defer ctrl.Close()

	// Register a catalog window lookup providing 272000 for remote-magpie/codex/gpt-6.2-astra.
	ctrl.SetContextWindowLookup(func(model string) int {
		if strings.Contains(model, "gpt-6.2-astra") {
			return 272_000
		}
		return 0
	})

	if err := ctrl.RefreshSync(context.Background()); err != nil {
		t.Fatalf("RefreshSync failed: %v", err)
	}

	items := ctrl.Items()
	if len(items) == 0 {
		t.Fatal("expected parsed Pi items")
	}
	last := items[len(items)-1]
	if last.Effort != "high" {
		t.Fatalf("expected header thinking_level_change effort=high inherited by tail items, got %q", last.Effort)
	}

	usage := ctrl.ContextUsage()
	if !usage.Valid() {
		t.Fatal("expected valid ContextUsage")
	}
	if usage.WindowTokens != 272_000 {
		t.Fatalf("expected WindowTokens=272000 from catalog lookup, got %d", usage.WindowTokens)
	}
	if got := usage.DetailLabel(); got != "上下文 136k / 272k · 50%" {
		t.Fatalf("expected DetailLabel '上下文 136k / 272k · 50%%', got %q", got)
	}
}

func TestCatalogContextWindowLookupMatchesPrefixedAndBareModels(t *testing.T) {
	a := &App{snapshots: NewSnapshotStore(nil)}
	a.snapshots.SaveCatalog("mac", "pi", AgentCatalog{
		Models: []AgentModelOption{
			{ID: "magpie/codex/gpt-6.2-astra", Label: "GPT 6.2 Astra", ContextWindow: 272_000},
			{ID: "magpie/auto/claude-opus-5-5", Label: "Opus 5.5", ContextWindow: 1_000_000},
		},
	})

	if got := a.LookupModelContextWindow("mac", "pi", "remote-magpie/codex/gpt-6.2-astra"); got != 272_000 {
		t.Fatalf("expected 272000 for prefixed model match, got %d", got)
	}
	if got := a.LookupModelContextWindow("mac", "pi", "gpt-6.2-astra"); got != 272_000 {
		t.Fatalf("expected 272000 for bare suffix match, got %d", got)
	}
	if got := estimateContextWindow("gpt-5.4", 100_000); got != 272_000 {
		t.Fatalf("expected 272000 fallback window for gpt-5.4/codex, got %d", got)
	}
}
