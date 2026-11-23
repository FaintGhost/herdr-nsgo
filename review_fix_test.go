package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"golang.org/x/crypto/ssh"
)

// Critical 1: a "finished" event must not freeze the connection reader while the pool resyncs.
func TestPoolResyncDoesNotBlockReader(t *testing.T) {
	var pushEvent atomic.Value // func(Event)
	dialer := func(h Host) (io.ReadWriteCloser, error) {
		cli, srv := net.Pipe()
		fakeFramedServer(t, srv, func(method string, params json.RawMessage, writeFrame func(uint32, FrameKind, []byte)) (any, error) {
			switch method {
			case "events.subscribe":
				ch := uint32(1)
				pushEvent.Store(func(ev Event) {
					raw, _ := json.Marshal(ev)
					writeFrame(ch, FrameData, raw)
				})
				return SubscribeResult{Channel: &ch, Seq: 1, Terminals: []Terminal{{Address: "t1", Name: "one", State: "working"}}}, nil
			case "terminal.list":
				return ListResult{Terminals: []Terminal{
					{Address: "t1", Name: "one", State: "working"},
					{Address: "t2", Name: "two", State: "idle"},
				}}, nil
			case "machine.list":
				return MachineListResult{}, nil
			case "templates.list":
				return TemplatesResult{}, nil
			}
			return map[string]any{}, nil
		}, nil)
		return cli, nil
	}
	pool := NewCorePool(dialer, func() {})
	defer pool.Close()
	pool.SetHosts([]Host{{ID: "kr", Label: "kr", Target: "kr:22", User: "root"}})

	deadline := time.Now().Add(3 * time.Second)
	for pushEvent.Load() == nil || len(pool.Terminals()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("never subscribed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pushEvent.Load().(func(Event))(Event{Type: "finished", Terminal: "t1", Seq: 2})

	deadline = time.Now().Add(2 * time.Second)
	for len(pool.Terminals()) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("resync did not land within 2s (reader blocked?): %d terminals", len(pool.Terminals()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Critical 2: the Ghostty bridge's Resize callback must never need SessionTerminal.mu.
func TestGhosttyBridgeResizeDoesNotTakeSessionLock(t *testing.T) {
	st := NewSessionTerminal(nil, TermKey{InstanceID: "kr", Addr: "t1"}, 80, 24, true, nil, false)
	defer st.Close()
	bridge := newSessionGhosttyBridge(st)
	st.mu.Lock()
	done := make(chan struct{})
	go func() {
		_ = bridge.Resize(58, 24)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		st.mu.Unlock()
		t.Fatal("bridge.Resize blocked on SessionTerminal.mu (deadlock under ghostty.Resize)")
	}
	st.mu.Unlock()
}

// Critical 3: with the local key enabled, the user's password must still be offered.
func TestAuthForOffersKeyAndPassword(t *testing.T) {
	priv, _, err := GenerateED25519Key()
	if err != nil {
		t.Fatal(err)
	}
	methods, err := authFor(Host{Key: priv, Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 {
		t.Fatalf("want public key + password auth methods, got %d", len(methods))
	}
	var _ ssh.AuthMethod = methods[1]
}

// Important: two online cores relaying the same offline peer must not double-list it.
func TestAggregateDedupesTwoRelaysOfOfflinePeer(t *testing.T) {
	relay := func(id string) InstanceSnapshot {
		return InstanceSnapshot{
			InstanceID: id, InstanceName: id, CoreID: "core-" + id, Online: true,
			Machines:  []MachineView{{Name: "kr", ID: "core-kr"}},
			Terminals: []Terminal{{Address: "kr/t7", Name: "wms", State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "claude"}}},
		}
	}
	snaps := []InstanceSnapshot{relay("a"), relay("b"), {InstanceID: "kr", InstanceName: "kr", CoreID: "core-kr", Online: false}}
	got := AggregateTerminals(snaps)
	if len(got) != 1 {
		t.Fatalf("want 1 deduplicated relayed row, got %d: %+v", len(got), got)
	}
}

// Important: an offline direct core keeps its cached rows when the "relay" lists the machine but no terminals of it.
func TestAggregateKeepsOfflineDirectWhenRelayHasNothing(t *testing.T) {
	snaps := []InstanceSnapshot{
		{InstanceID: "mac", InstanceName: "mac", CoreID: "core-mac", Online: true, Machines: []MachineView{{Name: "kr", ID: "core-kr"}}},
		{InstanceID: "kr", InstanceName: "kr", CoreID: "core-kr", Online: false, Terminals: []Terminal{{Address: "t7", Name: "wms", State: "idle"}}},
	}
	got := AggregateTerminals(snaps)
	if len(got) != 1 || got[0].Key.InstanceID != "kr" {
		t.Fatalf("offline direct rows hidden by an empty relay: %+v", got)
	}
}

// Important: notifications are computed once over deduplicated rows, seeded silently on first sight.
func TestAlertTrackerDedupAndSeeding(t *testing.T) {
	blocked := func(msg string) Terminal {
		return Terminal{Address: "t7", Name: "wms", State: "blocked", Status: &Status{State: "blocked", Kind: "permission", App: "claude", Msg: msg}}
	}
	snaps := func(msg string) []InstanceSnapshot {
		relayed := blocked(msg)
		relayed.Address = "kr/t7"
		return []InstanceSnapshot{
			{InstanceID: "mac", InstanceName: "mac", CoreID: "core-mac", Online: true, Machines: []MachineView{{Name: "kr", ID: "core-kr"}}, Terminals: []Terminal{relayed}},
			{InstanceID: "kr", InstanceName: "kr", CoreID: "core-kr", Online: true, Terminals: []Terminal{blocked(msg)}},
		}
	}
	tr := newAlertTracker()
	s1 := snaps("Allow push?")
	if got := tr.diff(s1, AggregateTerminals(s1)); len(got) != 0 {
		t.Fatalf("first sight must seed silently, got %d alerts", len(got))
	}
	s2 := snaps("Allow rm -rf build?")
	got := tr.diff(s2, AggregateTerminals(s2))
	if len(got) != 1 || got[0].Key.InstanceID != "kr" {
		t.Fatalf("want exactly one alert via direct kr, got %+v", got)
	}
	if again := tr.diff(s2, AggregateTerminals(s2)); len(again) != 0 {
		t.Fatalf("same prompt re-alerted: %+v", again)
	}
}

// Important: pairing payloads with unsafe or out-of-range fields are rejected.
func TestParsePairingRejectsUnsafeInputs(t *testing.T) {
	for _, bad := range []string{
		`{"v":1,"host":"   ","user":"a"}`,
		`{"v":1,"host":"h","port":70000}`,
		`{"v":1,"host":"h","port":-1}`,
		`{"v":2,"host":"h"}`,
		"herdr://pair?v=abc&host=h",
		"herdr://pair?v=9&host=h",
		"herdr://pair?v=1&host=evil%20host",
		"herdr://pair?v=1&host=h&user=a%40b",
		"herdr://pair?v=1&host=h/x",
		"herdr://pair?v=1&host=h&fp=MD5:aa",
		"herdr://pair?v=1&host=h%0a",
	} {
		if _, err := ParsePairingPayload(bad); err == nil {
			t.Errorf("ParsePairingPayload(%q) unexpectedly succeeded", bad)
		}
	}
	if p, err := ParsePairingPayload(`{"v":1,"host":" box.local ","user":" dev "}`); err != nil || p.Host != "box.local" || p.User != "dev" {
		t.Errorf("JSON fields not trimmed: %+v %v", p, err)
	}
}

// Important: UI badge / soft-tint pairings meet WCAG AA as actually composited.
func TestPaletteCompositeContrast(t *testing.T) {
	for _, p := range []Palette{LightPalette(), DarkPalette()} {
		pairs := []struct {
			label  string
			fg, bg core.Color
		}{
			{"finished badge", p.TextSecondary, p.Fill},
			{"danger on danger-soft", p.Danger, Composite(p.DangerSoft, p.Surface)},
			{"warn on warn-soft", p.Warn, Composite(p.WarnSoft, p.Surface)},
			{"accent on accent-soft", p.Accent, Composite(p.AccentSoft, p.Surface)},
			{"text on raised", p.Text, p.SurfaceRaised},
			{"text-secondary on raised", p.TextSecondary, p.SurfaceRaised},
		}
		for _, pr := range pairs {
			if r := ContrastRatio(pr.fg, pr.bg); r < 4.5 {
				t.Errorf("dark=%v %s: %.2f < 4.5", p.Dark, pr.label, r)
			}
		}
	}
}

// Important: colon-form truecolor SGR (38:2::R:G:B) keeps its channels.
func TestVTScreenColonTruecolor(t *testing.T) {
	s := NewVTScreen(20, 2)
	s.Write([]byte("\x1b[38:2::10:20:30mX"))
	c := s.Rows()[0][0]
	if c.Style.FG.Kind != VTColorRGB || c.Style.FG.R != 10 || c.Style.FG.G != 20 || c.Style.FG.B != 30 || c.Style.Underline {
		t.Fatalf("colon truecolor misparsed: %+v", c.Style)
	}
}

// Important: unique OSC 8 links must not grow memory without bound.
func TestVTScreenOSCLinksBounded(t *testing.T) {
	s := NewVTScreen(20, 2)
	for i := 0; i < 1000; i++ {
		s.Write([]byte("\x1b]8;;https://e.x/" + strings.Repeat("a", i%7) + string(rune('a'+i%26)) + itoa(i) + "\x07x\x1b]8;;\x07"))
	}
	s.mu.RLock()
	n := len(s.oscLinkSeen)
	s.mu.RUnlock()
	if n > 200 {
		t.Fatalf("oscLinkSeen grew to %d", n)
	}
}

// Important: leaving observe re-attaches as an active viewer, and input typed meanwhile is not lost.
func TestSessionReattachOnObserveExit(t *testing.T) {
	st := NewSessionTerminal(nil, TermKey{InstanceID: "kr", Addr: "t1"}, 140, 40, true, nil, false)
	defer st.Close()
	var calls []bool
	st.SetReattach(func(observe bool, cols, rows int) { calls = append(calls, observe) })
	_ = st.SendInput([]byte("ls\r"), 12)
	if len(calls) != 1 || calls[0] != false {
		t.Fatalf("SendInput in observe must reattach active once, got %v", calls)
	}
	if st.Observe() {
		t.Fatal("still observing after input")
	}
	st.mu.Lock()
	pending := len(st.pendingInput)
	st.mu.Unlock()
	if pending != 1 {
		t.Fatalf("input must be queued until the active attach lands, got %d", pending)
	}
	st.SetObserve(true, 12)
	if len(calls) != 2 || calls[1] != true {
		t.Fatalf("returning to observe must reattach observe, got %v", calls)
	}
}

// Important: a reachable core that rejects the ping method is still alive.
func TestPingTreatsRPCErrorAsAlive(t *testing.T) {
	cli, srv := net.Pipe()
	fakeFramedServer(t, srv, func(method string, params json.RawMessage, writeFrame func(uint32, FrameKind, []byte)) (any, error) {
		return nil, errors.New("method not found")
	}, nil)
	c := NewClient(cli)
	defer c.Close()
	if !c.Ping(time.Second) {
		t.Fatal("RPC error reply means the link is alive")
	}
}

// Important: editing credentials or the local-key toggle restarts the worker.
func TestHostChangedSeesCredentialEdits(t *testing.T) {
	a := Host{ID: "kr", Target: "kr:22", User: "root"}
	b := a
	b.UseLocalKey = true
	if !hostChanged(a, b) {
		t.Error("UseLocalKey toggle not detected")
	}
	c := a
	c.CredRev = 7
	if !hostChanged(a, c) {
		t.Error("credential revision not detected")
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
