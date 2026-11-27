package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestCorePoolDeduplicatesDirectOverRelay(t *testing.T) {
	// Snapshot with two cores:
	// - mac-mini (coreID="core-mac") online, federates machine "kr" (MachineView.ID="core-kr")
	//   and lists both local "t1" and relayed "kr/t7".
	// - kr (coreID="core-kr") online directly, lists "t7" (same underlying terminal!).
	snaps := []InstanceSnapshot{
		{
			InstanceID:   "mac-mini",
			InstanceName: "mac-mini",
			CoreID:       "core-mac",
			Online:       true,
			Machines:     []MachineView{{Name: "kr", ID: "core-kr", Host: "100.97.151.80"}},
			Terminals: []Terminal{
				{Address: "t1", Name: "herdr-nsgo-dev", State: "idle"},
				{Address: "kr/t7", Name: "wms-token", State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "claude"}},
			},
		},
		{
			InstanceID:   "kr",
			InstanceName: "kr",
			CoreID:       "core-kr",
			Online:       true,
			Terminals: []Terminal{
				{Address: "t7", Name: "wms-token", State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "claude"}},
			},
		},
	}

	got := AggregateTerminals(snaps)
	if len(got) != 2 {
		t.Fatalf("expected 2 deduplicated terminals, got %d: %+v", len(got), got)
	}
	var foundT7 *AggregatedTerminal
	for i := range got {
		if got[i].Terminal.Name == "wms-token" {
			foundT7 = &got[i]
		}
	}
	if foundT7 == nil {
		t.Fatal("missing wms-token after deduplication")
	}
	if foundT7.Key.InstanceID != "kr" || foundT7.Key.Addr != "t7" || foundT7.Relayed {
		t.Errorf("expected direct kr/t7 (Relayed=false), got %+v", *foundT7)
	}

	// Now take direct kr offline: mac-mini's relayed kr/t7 MUST reappear marked Relayed=true!
	snaps[1].Online = false
	gotOffline := AggregateTerminals(snaps)
	if len(gotOffline) != 2 {
		t.Fatalf("expected 2 terminals with relay fallback, got %d: %+v", len(gotOffline), gotOffline)
	}
	foundT7 = nil
	for i := range gotOffline {
		if gotOffline[i].Terminal.Name == "wms-token" {
			foundT7 = &gotOffline[i]
		}
	}
	if foundT7 == nil {
		t.Fatal("missing wms-token when direct kr offline")
	}
	if foundT7.Key.InstanceID != "mac-mini" || foundT7.Key.Addr != "kr/t7" || !foundT7.Relayed {
		t.Errorf("expected relayed fallback via mac-mini, got %+v", *foundT7)
	}
}

func TestGroupSessionsNestsChildrenAndSeparatesFinished(t *testing.T) {
	p1 := "kr/t1d"
	items := []AggregatedTerminal{
		{Key: TermKey{InstanceID: "kr", Addr: "kr/t1d"}, InstanceName: "kr", Terminal: Terminal{Address: "kr/t1d", Name: "main-agent", State: "live"}},
		{Key: TermKey{InstanceID: "kr", Addr: "kr/t1m"}, InstanceName: "kr", Terminal: Terminal{Address: "kr/t1m", Name: "child-1", Parent: &p1, State: "finished"}},
		{Key: TermKey{InstanceID: "kr", Addr: "kr/t1n"}, InstanceName: "kr", Terminal: Terminal{Address: "kr/t1n", Name: "child-2", Parent: &p1, State: "working"}},
		{Key: TermKey{InstanceID: "kr", Addr: "kr/t1p"}, InstanceName: "kr", Terminal: Terminal{Address: "kr/t1p", Name: "child-3", Parent: &p1, State: "live", Status: &Status{State: "done"}}},
		{Key: TermKey{InstanceID: "kr", Addr: "t3"}, InstanceName: "kr", Terminal: Terminal{Address: "t3", Name: "old-zsh", State: "finished"}},
	}

	active, finished := GroupSessions(items)
	if len(active) != 1 {
		t.Fatalf("expected 1 active root group, got %d: %+v", len(active), active)
	}
	if active[0].Primary.Key.Addr != "kr/t1d" || len(active[0].Children) != 2 || active[0].ActiveChildren != 1 {
		t.Errorf("unexpected root group: %+v", active[0])
	}
	if len(finished) != 2 {
		t.Errorf("expected 2 finished terminals (t3 + finished child kr/t1m), got %+v", finished)
	}
}

func TestGroupSessionsExcludesFinishedChildrenAndGrandchildren(t *testing.T) {
	rootAddr := "kr/t11f"
	exitZero := 0
	items := []AggregatedTerminal{
		{
			Key:          TermKey{InstanceID: "mac", Addr: rootAddr},
			InstanceName: "mac",
			Terminal:     Terminal{Address: rootAddr, Name: "main-session", State: "live", Status: &Status{State: "done"}},
		},
	}

	// 6 live direct sub-sessions (4 done + 2 working), mirroring real hc ls output.
	liveChildAddrs := []string{"kr/t139", "kr/t13a", "kr/t13b", "kr/t13c", "kr/t13d", "kr/t13e"}
	for i, addr := range liveChildAddrs {
		st := "done"
		if i < 2 {
			st = "working"
		}
		p := rootAddr
		items = append(items, AggregatedTerminal{
			Key:          TermKey{InstanceID: "mac", Addr: addr},
			InstanceName: "mac",
			Terminal: Terminal{
				Address: addr,
				Name:    fmt.Sprintf("sub-%d", i),
				Parent:  &p,
				State:   "live",
				Status:  &Status{State: st},
			},
		})
	}

	// 2 finished direct children (one with a stale blocked status).
	for i := range 2 {
		p := rootAddr
		var stale *Status
		if i == 0 {
			stale = &Status{State: "blocked", Kind: "permission", App: "claude"}
		}
		items = append(items, AggregatedTerminal{
			Key:          TermKey{InstanceID: "mac", Addr: fmt.Sprintf("kr/tdead%d", i)},
			InstanceName: "mac",
			Terminal: Terminal{
				Address:  fmt.Sprintf("kr/tdead%d", i),
				Name:     fmt.Sprintf("dead-direct-%d", i),
				Parent:   &p,
				State:    "finished",
				ExitCode: &exitZero,
				Status:   stale,
			},
		})
	}

	// 68 finished grandchildren (short-lived bash/go commands spawned by the 6 live sub-sessions).
	for i := range 68 {
		parentAddr := liveChildAddrs[i%len(liveChildAddrs)]
		items = append(items, AggregatedTerminal{
			Key:          TermKey{InstanceID: "mac", Addr: fmt.Sprintf("kr/tcmd%d", i)},
			InstanceName: "mac",
			Terminal: Terminal{
				Address:  fmt.Sprintf("kr/tcmd%d", i),
				Name:     fmt.Sprintf("drain-test-%d", i),
				Parent:   &parentAddr,
				State:    "finished",
				ExitCode: &exitZero,
			},
		})
	}

	// Edge cases:
	// 1. Live grandchild under a finished intermediate child still attaches to live root.
	deadMid := "kr/tdead0"
	items = append(items, AggregatedTerminal{
		Key:          TermKey{InstanceID: "mac", Addr: "kr/tlive-grand"},
		InstanceName: "mac",
		Terminal:     Terminal{Address: "kr/tlive-grand", Name: "live-grandchild", Parent: &deadMid, State: "live", Status: &Status{State: "idle"}},
	})
	// 2. Finished root with a live child stays in active so the live child is not hidden.
	deadRoot := "kr/told-root"
	items = append(items,
		AggregatedTerminal{
			Key:          TermKey{InstanceID: "mac", Addr: deadRoot},
			InstanceName: "mac",
			Terminal:     Terminal{Address: deadRoot, Name: "old-root", State: "finished", ExitCode: &exitZero},
		},
		AggregatedTerminal{
			Key:          TermKey{InstanceID: "mac", Addr: "kr/told-root-live-kid"},
			InstanceName: "mac",
			Terminal:     Terminal{Address: "kr/told-root-live-kid", Name: "orphan-survivor", Parent: &deadRoot, State: "live", Status: &Status{State: "idle"}},
		},
	)
	// 3. Finished root with only a finished child -> both appear in finished once (no duplicates).
	deadPairRoot := "kr/tpair-root"
	items = append(items,
		AggregatedTerminal{
			Key:          TermKey{InstanceID: "mac", Addr: deadPairRoot},
			InstanceName: "mac",
			Terminal:     Terminal{Address: deadPairRoot, Name: "pair-root", State: "finished", ExitCode: &exitZero},
		},
		AggregatedTerminal{
			Key:          TermKey{InstanceID: "mac", Addr: "kr/tpair-kid"},
			InstanceName: "mac",
			Terminal:     Terminal{Address: "kr/tpair-kid", Name: "pair-kid", Parent: &deadPairRoot, State: "finished", ExitCode: &exitZero},
		},
	)

	active, finished := GroupSessions(items)
	if len(active) != 2 {
		t.Fatalf("expected 2 active groups (kr/t11f + old-root with live child), got %d", len(active))
	}

	var mainGroup *SessionGroup
	for i := range active {
		if active[i].Primary.Key.Addr == rootAddr {
			mainGroup = &active[i]
		}
	}
	if mainGroup == nil {
		t.Fatalf("missing root group %s", rootAddr)
	}
	// 6 live direct children + 1 live grandchild = 7 (NOT 77!).
	if len(mainGroup.Children) != 7 {
		t.Fatalf("expected 7 live descendants (6 direct + 1 live grandchild), got %d", len(mainGroup.Children))
	}
	if mainGroup.ActiveChildren != 2 {
		t.Fatalf("expected ActiveChildren=2 (only working/needs-you children), got %d", mainGroup.ActiveChildren)
	}
	if mainGroup.AttentionChildren != 0 {
		t.Fatalf("expected AttentionChildren=0 (stale blocked status on finished child must not count), got %d", mainGroup.AttentionChildren)
	}

	// Verify dead-direct-0 with stale blocked status reports NeedsYou()==false.
	for _, it := range finished {
		if it.Key.Addr == "kr/tdead0" && it.Terminal.NeedsYou() {
			t.Fatalf("finished terminal with stale blocked status must not report NeedsYou()==true")
		}
	}

	// 2 dead direct + 68 dead grandchildren + 1 pair-root + 1 pair-kid = 72 unique finished terminals.
	if len(finished) != 72 {
		t.Fatalf("expected 72 finished terminals, got %d", len(finished))
	}
	seen := make(map[TermKey]bool, len(finished))
	for _, it := range finished {
		if seen[it.Key] {
			t.Fatalf("duplicate finished terminal %v", it.Key)
		}
		seen[it.Key] = true
	}
}

func TestInlineApprovalWhitelist(t *testing.T) {
	cases := []struct {
		name      string
		term      Terminal
		wantAllow bool
	}{
		{
			name:      "claude permission blocked allowed",
			term:      Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "claude"}},
			wantAllow: true,
		},
		{
			name:      "codex permission blocked allowed",
			term:      Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "codex"}},
			wantAllow: true,
		},
		{
			name:      "antigravity permission allowed",
			term:      Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "antigravity"}},
			wantAllow: true,
		},
		{
			name:      "agy permission allowed",
			term:      Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "agy"}},
			wantAllow: true,
		},
		{
			name:      "unknown app permission rejected",
			term:      Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "mystery-cli"}},
			wantAllow: false,
		},
		{
			name:      "question kind never inline",
			term:      Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "question", App: "claude"}},
			wantAllow: false,
		},
		{
			name:      "auth kind never inline",
			term:      Terminal{State: "blocked", Status: &Status{State: "blocked", Kind: "auth", App: "claude"}},
			wantAllow: false,
		},
		{
			name:      "working state never inline",
			term:      Terminal{State: "working", Status: &Status{State: "working", Kind: "permission", App: "claude"}},
			wantAllow: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanInlineApprove(tc.term); got != tc.wantAllow {
				t.Errorf("CanInlineApprove() = %v, want %v", got, tc.wantAllow)
			}
			keys, ok := ApproveKeys(tc.term, true)
			if ok != tc.wantAllow {
				t.Errorf("ApproveKeys(allow=true) ok = %v, want %v", ok, tc.wantAllow)
			}
			if tc.wantAllow && len(keys) == 0 {
				t.Error("expected non-empty key sequence for allowed adapter")
			}
			denyKeys, okDeny := ApproveKeys(tc.term, false)
			if okDeny != tc.wantAllow {
				t.Errorf("ApproveKeys(allow=false) ok = %v, want %v", okDeny, tc.wantAllow)
			}
			if tc.wantAllow && len(denyKeys) == 0 {
				t.Error("expected non-empty deny key sequence for allowed adapter")
			}
		})
	}
}

func TestFrameCacheLRUEviction(t *testing.T) {
	fc := NewFrameCache(24)
	for i := 0; i < 30; i++ {
		k := TermKey{InstanceID: "kr", Addr: fmt.Sprintf("t%d", i)}
		fc.Put(k, []byte(fmt.Sprintf("frame-%d", i)))
	}
	if fc.Len() != 24 {
		t.Fatalf("Len() = %d, want 24", fc.Len())
	}
	// Oldest 0..5 evicted; 6..29 present.
	if _, ok := fc.Get(TermKey{InstanceID: "kr", Addr: "t0"}); ok {
		t.Error("expected t0 to be evicted")
	}
	// Touch t6 so it becomes MRU, then insert t30; t7 should be evicted, t6 retained.
	if b, ok := fc.Get(TermKey{InstanceID: "kr", Addr: "t6"}); !ok || string(b) != "frame-6" {
		t.Fatalf("expected t6 present, got %q ok=%v", b, ok)
	}
	fc.Put(TermKey{InstanceID: "kr", Addr: "t30"}, []byte("frame-30"))
	if _, ok := fc.Get(TermKey{InstanceID: "kr", Addr: "t6"}); !ok {
		t.Error("expected recently-read t6 to survive eviction")
	}
	if _, ok := fc.Get(TermKey{InstanceID: "kr", Addr: "t7"}); ok {
		t.Error("expected LRU t7 to be evicted")
	}
}

func TestPoolEventsUpdateWithoutPolling(t *testing.T) {
	var mu sync.Mutex
	var pushEvent func(Event)

	dialer := func(h Host) (io.ReadWriteCloser, error) {
		cli, srv := net.Pipe()
		fakeFramedServer(t, srv, func(method string, params json.RawMessage, writeFrame func(uint32, FrameKind, []byte)) (any, error) {
			switch method {
			case "events.subscribe":
				ch := uint32(1)
				mu.Lock()
				pushEvent = func(ev Event) {
					raw, _ := json.Marshal(ev)
					writeFrame(ch, FrameData, raw)
				}
				mu.Unlock()
				return SubscribeResult{
					Channel:   &ch,
					Seq:       1,
					Terminals: []Terminal{{Address: "t1", Name: "worker", State: "working"}},
				}, nil
			case "machine.list":
				return MachineListResult{}, nil
			case "templates.list":
				return TemplatesResult{Templates: []Template{{Name: "claude", Description: "Claude Code"}}}, nil
			}
			return map[string]any{}, nil
		}, nil)
		return cli, nil
	}

	updates := make(chan []AggregatedTerminal, 16)
	var pool *CorePool
	pool = NewCorePool(dialer, func() {
		updates <- pool.Terminals()
	})
	defer pool.Close()

	pool.SetHosts([]Host{{ID: "kr", Label: "kr", Target: "kr:22", User: "root"}})

	// Wait for initial snapshot from events.subscribe.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case list := <-updates:
			if len(list) == 1 && list[0].Terminal.StateWord() == "working" {
				goto pushBlocked
			}
		case <-deadline:
			t.Fatal("timeout waiting for initial subscribe snapshot")
		}
	}

pushBlocked:
	mu.Lock()
	fn := pushEvent
	mu.Unlock()
	if fn == nil {
		t.Fatal("pushEvent not registered")
	}
	data, _ := json.Marshal(map[string]any{"reported": true, "status": Status{State: "blocked", Kind: "permission", App: "claude", Msg: "Allow git push?"}})
	fn(Event{Type: "status", Terminal: "t1", Seq: 2, Data: data})

	deadline2 := time.After(3 * time.Second)
	for {
		select {
		case list := <-updates:
			if len(list) == 1 && list[0].Terminal.StateWord() == "blocked" && list[0].Terminal.NeedsYou() {
				return
			}
		case <-deadline2:
			t.Fatal("timeout waiting for real-time status event update")
		}
	}
}
