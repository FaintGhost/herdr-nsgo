package main

import (
	"strings"
	"testing"
)

func TestParseInteractivePromptAgyFourOptions(t *testing.T) {
	screen := strings.Join([]string{
		"Tool execution requires confirmation",
		"run_command: git push origin main && gh run list -L 2",
		"",
		"❯ 1. Allow once",
		"  2. Allow for this session",
		"  3. Always allow (save to config)",
		"  4. Deny",
		"",
		"↑/↓ to navigate · Enter to select · Esc to cancel",
	}, "\n")

	p, ok := ParseInteractivePrompt(screen, "antigravity", "permission")
	if !ok {
		t.Fatal("expected Agy 4-option permission prompt to parse")
	}
	if p.Kind != PromptKindPermission || p.Cursor != 0 || len(p.Options) != 4 {
		t.Fatalf("unexpected Agy prompt: kind=%v cursor=%d opts=%+v", p.Kind, p.Cursor, p.Options)
	}
	if p.Options[0].Tone != TonePrimary || p.Options[1].Tone != ToneNeutral || p.Options[2].Tone != ToneNeutral || p.Options[3].Tone != ToneDanger {
		t.Fatalf("unexpected option tones: %+v", p.Options)
	}
	if p.Options[0].KeyHint != "1" || p.Options[3].KeyHint != "4" {
		t.Fatalf("unexpected key hints: %+v", p.Options)
	}
	if !strings.Contains(p.Detail, "git push origin main") {
		t.Fatalf("expected command detail preserved, got %q", p.Detail)
	}
	if p.Fingerprint == "" {
		t.Fatal("expected non-empty screen fingerprint")
	}
}

func TestParseInteractivePromptClaudeThreeOptionsNeedsText(t *testing.T) {
	screen := strings.Join([]string{
		"Bash command",
		"  go test -race ./...",
		"Do you want to proceed?",
		"❯ 1. Yes",
		"  2. Yes, and don't ask again for go test commands in /repo",
		"  3. No, and tell Claude what to do differently (esc)",
	}, "\n")

	p, ok := ParseInteractivePrompt(screen, "claude", "permission")
	if !ok {
		t.Fatal("expected Claude 3-option permission prompt to parse")
	}
	if len(p.Options) != 3 || p.Cursor != 0 {
		t.Fatalf("expected 3 options cursor 0, got cursor=%d opts=%+v", p.Cursor, p.Options)
	}
	if p.Options[0].Tone != TonePrimary || p.Options[1].Tone != ToneNeutral || p.Options[2].Tone != ToneDanger {
		t.Fatalf("unexpected Claude tones: %+v", p.Options)
	}
	if !p.Options[2].NeedsText || !p.Options[2].InlineTextMode {
		t.Fatalf("expected Claude option 3 to require inline custom text, got %+v", p.Options[2])
	}
}

func TestParseInteractivePromptPiRpivAskUserQuestionMultiAndSubmitTab(t *testing.T) {
	multiScreen := strings.Join([]string{
		"Feature Type  [Testing]  Submit",
		"Which checks should we run?",
		"❯ 1. [x] Unit tests",
		"     Fast local package tests",
		"  2. [ ] Integration tests",
		"     End-to-end SSH checks",
		"  3. Type something.",
		"  Next",
		"Space to toggle · Tab to switch · Esc to cancel",
	}, "\n")

	p, ok := ParseInteractivePrompt(multiScreen, "pi", "question")
	if !ok {
		t.Fatal("expected Pi rpiv-ask-user-question multi-select prompt to parse")
	}
	if p.Kind != PromptKindQuestion || !p.MultiSelect || !p.CanSwitchTab {
		t.Fatalf("expected multi-select tabbed question, got %+v", p)
	}
	if len(p.Options) != 4 {
		t.Fatalf("expected 4 rows (2 checkboxes + Type something + Next), got %d: %+v", len(p.Options), p.Options)
	}
	if !p.Options[0].Checked || p.Options[1].Checked {
		t.Fatalf("expected option 0 checked and option 1 unchecked, got %+v", p.Options)
	}
	if !p.Options[2].NeedsText || !p.Options[2].InlineTextMode {
		t.Fatalf("expected Type something row to be NeedsText+InlineTextMode, got %+v", p.Options[2])
	}
	if !p.Options[3].IsSubmit {
		t.Fatalf("expected Next row to be IsSubmit, got %+v", p.Options[3])
	}

	reviewScreen := strings.Join([]string{
		"Feature Type  Testing  [Submit]",
		"Review your answers",
		"  Feature Type: Bug fix",
		"  Testing: Unit tests",
		"❯ Submit answers",
		"  Cancel",
		"Enter to confirm · Tab to switch · Esc to cancel",
	}, "\n")
	rev, ok := ParseInteractivePrompt(reviewScreen, "pi", "question")
	if !ok {
		t.Fatal("expected Submit review screen to parse")
	}
	if rev.Kind != PromptKindReview || len(rev.Options) != 2 || !rev.Options[0].IsSubmit || rev.Options[1].Tone != ToneDanger {
		t.Fatalf("unexpected review prompt: %+v", rev)
	}
}

func TestEnrichPromptFromJSONLAndPendingQuestionDetection(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"s1","cwd":"/repo"}`,
		`{"type":"message","id":"a1","message":{"role":"assistant","model":"gpt-5.4","content":[{"type":"toolCall","id":"tc1","name":"ask_user_question","arguments":{"questions":[{"question":"Which caching layer should we use?","multiSelect":true,"options":[{"label":"In-memory LRU","description":"Zero dependencies, fast"},{"label":"SQLite","description":"Persists across restarts"}]}]}}]}}`,
	}
	items := ParseTranscriptLinesAt(FormatPi, lines, 1)
	pq, ok := ExtractPendingQuestionFromItems(items)
	if !ok {
		t.Fatal("expected unanswered ask_user_question tool call detected from JSONL items")
	}
	if len(pq.Questions) != 1 || !pq.Questions[0].MultiSelect || pq.Questions[0].Question != "Which caching layer should we use?" {
		t.Fatalf("unexpected PendingQuestion: %+v", pq)
	}

	tuiScreen := strings.Join([]string{
		"Which caching layer should we use?",
		"❯ 1. [ ] In-memory LRU",
		"  2. [ ] SQLite",
		"  3. Type something.",
		"  Next",
		"Enter to select · Esc to cancel",
	}, "\n")
	p, ok := ParseInteractivePrompt(tuiScreen, "pi", "question")
	if !ok {
		t.Fatal("expected TUI screen parsed")
	}
	enriched := EnrichPromptFromJSONL(p, pq)
	if enriched.Options[0].Description != "Zero dependencies, fast" || enriched.Options[1].Description != "Persists across restarts" {
		t.Fatalf("expected descriptions enriched from JSONL, got %+v", enriched.Options)
	}
}
