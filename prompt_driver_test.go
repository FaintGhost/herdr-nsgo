package main

import (
	"context"
	"slices"
	"strings"
	"testing"
)

type fakePromptTransport struct {
	term       Terminal
	screen     string
	keysSent   [][]string
	forcedSent []string
	onAction   func()
}

func (f *fakePromptTransport) Get(_ context.Context, _ string) (Terminal, error) {
	return f.term, nil
}

func (f *fakePromptTransport) Read(_ context.Context, _ string, _ int) (ReadResult, error) {
	return ReadResult{Text: f.screen}, nil
}

func (f *fakePromptTransport) Keys(_ context.Context, _ string, keys ...string) (KeysResult, error) {
	f.keysSent = append(f.keysSent, append([]string(nil), keys...))
	if f.onAction != nil {
		f.onAction()
	}
	return KeysResult{}, nil
}

func (f *fakePromptTransport) SendForced(_ context.Context, _ string, text string) (SendResult, error) {
	f.forcedSent = append(f.forcedSent, text)
	if f.onAction != nil {
		f.onAction()
	}
	return SendResult{}, nil
}

func TestDrivePromptSingleOptionCursorDeltaAndFingerprintGuard(t *testing.T) {
	agyScreen := strings.Join([]string{
		"Tool execution requires confirmation",
		"run_command: git push origin main",
		"❯ 1. Allow once",
		"  2. Allow for this session",
		"  3. Always allow (save to config)",
		"  4. Deny",
		"↑/↓ to navigate · Enter to select · Esc to cancel",
	}, "\n")
	parsed, ok := ParseInteractivePrompt(agyScreen, "antigravity", "permission")
	if !ok {
		t.Fatal("failed to parse Agy screen")
	}

	tr := &fakePromptTransport{
		term:   Terminal{Address: "t1", State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "antigravity"}},
		screen: agyScreen,
	}

	// Select option 3 (index 2: Always allow) -> down, down, enter.
	err := DriveInteractivePrompt(context.Background(), tr, "t1", PromptSubmitRequest{
		Expected:    parsed,
		OptionIndex: 2,
	})
	if err != nil {
		t.Fatalf("DriveInteractivePrompt failed: %v", err)
	}
	flat := flattenKeyBatches(tr.keysSent)
	want := []string{"down", "down", "enter"}
	if !slices.Equal(flat, want) {
		t.Fatalf("expected keys %v, got %v", want, flat)
	}

	// Now mutate screen -> fingerprint guard MUST abort and send zero keys!
	tr.keysSent = nil
	tr.screen = strings.ReplaceAll(agyScreen, "Always allow (save to config)", "Different option")
	err = DriveInteractivePrompt(context.Background(), tr, "t1", PromptSubmitRequest{
		Expected:    parsed,
		OptionIndex: 2,
	})
	if err == nil {
		t.Fatal("expected error when screen fingerprint drifted")
	}
	if len(tr.keysSent) != 0 {
		t.Fatalf("expected zero keys sent after drift, got %v", tr.keysSent)
	}
}

func TestDrivePromptInlineNeedsTextSendsForcedTextWithoutBlankEnter(t *testing.T) {
	claudeScreen := strings.Join([]string{
		"Bash command",
		"  npm install",
		"Do you want to proceed?",
		"❯ 1. Yes",
		"  2. Yes, and don't ask again for npm commands",
		"  3. No, and tell Claude what to do differently (esc)",
	}, "\n")
	parsed, ok := ParseInteractivePrompt(claudeScreen, "claude", "permission")
	if !ok {
		t.Fatal("failed to parse Claude screen")
	}

	tr := &fakePromptTransport{
		term:   Terminal{Address: "t1", State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "claude"}},
		screen: claudeScreen,
	}
	err := DriveInteractivePrompt(context.Background(), tr, "t1", PromptSubmitRequest{
		Expected:    parsed,
		OptionIndex: 2,
		CustomText:  "use pnpm instead",
	})
	if err != nil {
		t.Fatalf("DriveInteractivePrompt custom text failed: %v", err)
	}
	flat := flattenKeyBatches(tr.keysSent)
	// Must move down twice WITHOUT pressing Enter before typing!
	wantKeys := []string{"down", "down"}
	if !slices.Equal(flat, wantKeys) {
		t.Fatalf("expected keys %v (no blank enter), got %v", wantKeys, flat)
	}
	if !slices.Equal(tr.forcedSent, []string{"use pnpm instead"}) {
		t.Fatalf("expected SendForced with custom text, got %v", tr.forcedSent)
	}
}

func TestDrivePromptMultiSelectTogglesDiffAndSubmitsViaNext(t *testing.T) {
	multiScreen := strings.Join([]string{
		"Feature Type  [Testing]  Submit",
		"Which checks should we run?",
		"❯ 1. [x] Unit tests",
		"  2. [ ] Integration tests",
		"  3. Type something.",
		"  Next",
		"Space to toggle · Tab to switch · Esc to cancel",
	}, "\n")
	parsed, ok := ParseInteractivePrompt(multiScreen, "pi", "question")
	if !ok {
		t.Fatal("failed to parse Pi multi-select screen")
	}

	// Simulate Pi session where Status == nil (no OSC 7501 blocked, triggered by JSONL).
	tr := &fakePromptTransport{
		term:   Terminal{Address: "t5", State: "running", Status: nil},
		screen: multiScreen,
	}

	// Want ONLY index 1 checked (uncheck 0, check 1, then move to Next at index 3 and Enter).
	err := DriveInteractivePrompt(context.Background(), tr, "t5", PromptSubmitRequest{
		Expected:        parsed,
		SelectedIndices: []int{1},
		IsMultiSubmit:   true,
	})
	if err != nil {
		t.Fatalf("multi-select submit failed: %v", err)
	}
	flat := flattenKeyBatches(tr.keysSent)
	want := []string{"space", "down", "space", "down", "down", "enter"}
	if !slices.Equal(flat, want) {
		t.Fatalf("expected multi-select keys %v, got %v", want, flat)
	}
}

func TestDrivePromptTabSwitchSendsShiftTabOrTab(t *testing.T) {
	multiScreen := strings.Join([]string{
		"Feature Type  [Testing]  Submit",
		"Which checks should we run?",
		"❯ 1. [x] Unit tests",
		"  2. [ ] Integration tests",
		"  Next",
		"Space to toggle · Tab to switch · Esc to cancel",
	}, "\n")
	parsed, ok := ParseInteractivePrompt(multiScreen, "pi", "question")
	if !ok {
		t.Fatal("failed to parse screen")
	}
	tr := &fakePromptTransport{
		term:   Terminal{Address: "t5", State: "running"},
		screen: multiScreen,
	}
	if err := DriveInteractivePrompt(context.Background(), tr, "t5", PromptSubmitRequest{
		Expected: parsed,
		TabDelta: -1,
	}); err != nil {
		t.Fatalf("TabDelta -1 failed: %v", err)
	}
	if flat := flattenKeyBatches(tr.keysSent); !slices.Equal(flat, []string{"shift+tab"}) {
		t.Fatalf("expected [shift+tab], got %v", flat)
	}
}

func flattenKeyBatches(batches [][]string) []string {
	var out []string
	for _, b := range batches {
		out = append(out, b...)
	}
	return out
}
