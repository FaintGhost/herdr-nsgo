package main

import (
	"context"
	"strings"
	"testing"
)

// Regression tests for every Critical & Important finding from the Whole-Branch Review.

func TestParseInteractivePromptIgnoresScrollbackListsAndPromptLines(t *testing.T) {
	// Real terminal.read(lines=0) routinely contains earlier markdown lists, checkboxes,
	// and previous ❯ user prompts above the active bottom permission dialog!
	screen := strings.Join([]string{
		"❯ Please refactor the auth module",
		"Here is the plan:",
		"1. Update token refresh",
		"2. Add [ ] unit tests",
		"3. Deploy to staging",
		"────────────────────────────────────────",
		"Tool execution requires confirmation",
		"run_command: git push origin main",
		"❯ 1. Allow once",
		"  2. Allow for this session",
		"  3. Always allow (save to config)",
		"  4. Deny",
		"↑/↓ to navigate · Enter to select · Esc to cancel",
	}, "\n")

	p, ok := ParseInteractivePrompt(screen, "antigravity", "permission")
	if !ok {
		t.Fatal("expected bottom Agy permission dialog to parse cleanly")
	}
	if p.Kind != PromptKindPermission {
		t.Fatalf("scrollback checkbox must not misclassify permission as %v", p.Kind)
	}
	if len(p.Options) != 4 || p.Cursor != 0 {
		t.Fatalf("expected strictly 4 bottom options with cursor 0, got cursor=%d len=%d opts=%+v", p.Cursor, len(p.Options), p.Options)
	}
	if p.Options[0].Label != "Allow once" || p.Options[3].Label != "Deny" {
		t.Fatalf("unexpected bottom options: %+v", p.Options)
	}
}

func TestParseInteractivePromptSingleQuestionMultiSelectNotMisclassifiedAsReview(t *testing.T) {
	screen := strings.Join([]string{
		"Which environments should we target?",
		"❯ 1. [x] Staging",
		"  2. [ ] Production",
		"  3. Other",
		"  Submit answers",
		"Space to toggle · Enter to select · Esc to cancel",
	}, "\n")

	p, ok := ParseInteractivePrompt(screen, "claude", "question")
	if !ok {
		t.Fatal("expected single-question multi-select prompt to parse")
	}
	if p.Kind != PromptKindQuestion || !p.MultiSelect {
		t.Fatalf("expected PromptKindQuestion+MultiSelect (NOT PromptKindReview), got kind=%v multi=%v", p.Kind, p.MultiSelect)
	}
}

func TestDriveInteractivePromptPiSingleSelectWithoutOSCStatusDoesNotDrift(t *testing.T) {
	// Pi rpiv-ask-user-question single-select whose title does NOT end in '?' and Status == nil.
	piScreen := strings.Join([]string{
		"Select target deployment strategy",
		"❯ 1. Blue-green rollout",
		"  2. Canary 10%",
		"  3. Type something.",
		"Enter to select · Esc to cancel",
	}, "\n")
	// InspectPrompt passes statusKind="question" when pq != nil.
	parsed, ok := ParseInteractivePrompt(piScreen, "pi", "question")
	if !ok || parsed.Kind != PromptKindQuestion {
		t.Fatalf("expected PromptKindQuestion, got ok=%v kind=%v", ok, parsed.Kind)
	}

	tr := &fakePromptTransport{
		term:   Terminal{Address: "t5", State: "running", Status: nil},
		screen: piScreen,
	}
	if err := DriveInteractivePrompt(context.Background(), tr, "t5", PromptSubmitRequest{
		Expected:    parsed,
		OptionIndex: 1,
	}); err != nil {
		t.Fatalf("expected JSONL-triggered Pi single-select to match fingerprint without drift, got: %v", err)
	}
}

func TestExtractPendingQuestionStopsAtNewerUserOrAssistantTurn(t *testing.T) {
	items := []ChatTurnItem{
		{
			ID:   "tg-old",
			Role: RoleToolGroup,
			Tools: []ChatToolCall{
				{ID: "tc1", Name: "ask_user_question", HasResult: false, RawInput: `{"questions":[{"question":"Old cancelled?"}]}`},
			},
		},
		{
			ID:   "u-next",
			Role: RoleUser,
			Text: "Never mind, skip that",
		},
		{
			ID:   "a-next",
			Role: RoleAssistant,
			Text: "Okay, moving on.",
		},
	}
	if _, ok := ExtractPendingQuestionFromItems(items); ok {
		t.Fatal("old cancelled question before newer user/assistant turns must NOT be treated as active")
	}
}

func TestPrefetchTranscriptPreservesPiHeaderEffort(t *testing.T) {
	key := TermKey{InstanceID: "mac", Addr: "t6"}
	path := "/Users/dev/.pi/agent/sessions/--repo--/2026-11-24_pref.jsonl"
	lines := []string{
		`{"type":"session","version":3,"id":"s1","cwd":"/repo"}`,
		`{"type":"thinking_level_change","id":"th1","thinkingLevel":"high"}`,
		`{"type":"model_change","id":"m1","provider":"magpie","modelId":"remote-magpie/codex/gpt-6.2-astra"}`,
	}
	for i := 0; i < 270; i++ {
		lines = append(lines, `{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":"step"}]}}`)
	}
	lines = append(lines, `{"type":"message","id":"a1","message":{"role":"assistant","model":"remote-magpie/codex/gpt-6.2-astra","content":[{"type":"text","text":"prefetched ok"}]}}`)

	fb := &fakeTranscriptBackend{
		terms: map[TermKey]AggregatedTerminal{
			key: {Key: key, Terminal: Terminal{Address: "t6", Cwd: "/repo", Argv: []string{"pi", "--session", path}}},
		},
		files: map[string][]string{"::" + path: lines},
	}
	resolver := NewTranscriptResolver()
	pref, ok := PrefetchTranscript(context.Background(), key, fb.terms[key].Terminal, fb, resolver, nil)
	if !ok {
		t.Fatal("expected PrefetchTranscript to succeed")
	}
	if len(pref.ParsedItems) == 0 || pref.ParsedItems[len(pref.ParsedItems)-1].Effort != "high" {
		t.Fatalf("expected prefetched Pi items to inherit header effort=high, got %+v", pref.ParsedItems[len(pref.ParsedItems)-1])
	}
}
