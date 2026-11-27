package main

import (
	"fmt"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestModelPickerLongListKeepsApplyPinnedInViewportAndSupportsSearch(t *testing.T) {
	key := TermKey{InstanceID: "mac", Addr: "t1"}
	models := make([]AgentModelOption, 0, 24)
	for i := 0; i < 24; i++ {
		models = append(models, AgentModelOption{
			ID:          fmt.Sprintf("magpie/auto/model-%02d", i),
			Label:       fmt.Sprintf("Model %02d · routing group", i),
			Detail:      "magpie",
			DirectValue: fmt.Sprintf("magpie/auto/model-%02d", i),
		})
	}
	models = append(models, AgentModelOption{
		ID:          "magpie/codex/gpt-6.2-astra",
		Label:       "GPT 6.2 Astra",
		Detail:      "magpie",
		DirectValue: "magpie/codex/gpt-6.2-astra",
	})

	var uiRef *uiController
	var appRef *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, _ := testSeededApp(cx)
		appRef = a
		snaps := a.instances.Get()
		snaps[1].Terminals = []Terminal{{
			Address:    "t1",
			Name:       "pi-work",
			Cwd:        "/repo",
			State:      "idle",
			Foreground: &Foreground{PID: 400, Name: "pi", Cwd: "/repo"},
		}}
		a.SeedSnapshots(snaps)
		a.snapshots.SaveCatalog("mac", "pi", AgentCatalog{Models: models})
		node, ctrl := rootWithController(cx, a)
		uiRef = ctrl
		return node
	}, nsgotest.Size(390, 844))

	uiRef.openSession(appRef.cx, key)
	h.Settle()
	h.Press("id:chat-model-selector")
	h.Settle()

	h.Find("id:chat-model-sheet")
	h.Find("id:model-search-input")

	// Tap first option -> apply button MUST be pinned inside the 844pt viewport (not buried below 25 rows!).
	h.Press("id:model-option-magpie/auto/model-00")
	h.Settle()
	applyBtn := h.Find("id:model-apply")
	b := applyBtn.Bounds()
	if b.Origin.Y+b.Size.Height > 844 {
		t.Fatalf("expected pinned model-apply button inside 844pt viewport, got bounds=%+v", b)
	}

	// Search filters long Pi list cleanly to GPT 6.2 Astra.
	h.Type("id:model-search-input", "astra")
	h.Settle()
	h.Find("id:model-option-magpie/codex/gpt-6.2-astra")
}
