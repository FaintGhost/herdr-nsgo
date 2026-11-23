package main

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// TermKey globally identifies a terminal across multiple concurrent herdr-core instances.
type TermKey struct {
	InstanceID string
	Addr       string
}

func (k TermKey) String() string {
	if k.InstanceID == "" {
		return k.Addr
	}
	return k.InstanceID + "::" + k.Addr
}

// AggregatedTerminal pairs a terminal with its owning instance and relay metadata.
type AggregatedTerminal struct {
	Key          TermKey
	InstanceName string
	CoreID       string
	Relayed      bool
	RelayVia     string
	Terminal     Terminal
}

// InstanceSnapshot is a read-only view of one core instance's live state.
type InstanceSnapshot struct {
	InstanceID   string
	InstanceName string
	Target       string
	User         string
	CoreID       string
	CoreVersion  string
	State        string // disconnected, connecting, connected, error
	Online       bool
	Problem      string
	LatencyMS    int64
	Machines     []MachineView
	Templates    []Template
	Terminals    []Terminal
}

// InstanceState is an alias exposed to UI layers.
type InstanceState = InstanceSnapshot

// AggregateTerminals merges terminals across all instances and deduplicates federated
// machine proxies against directly connected cores by CoreInfo.ID / MachineView.ID.
// Direct online connections always win; if a direct core is offline, its relayed view
// through another online core transparently takes over with Relayed=true.
func AggregateTerminals(snaps []InstanceSnapshot) []AggregatedTerminal {
	onlineDirectCoreIDs := make(map[string]bool)
	for _, s := range snaps {
		if s.Online && s.CoreID != "" {
			onlineDirectCoreIDs[s.CoreID] = true
		}
	}

	var out []AggregatedTerminal
	seen := make(map[string]bool)

	for _, s := range snaps {
		machineIDByName := make(map[string]string, len(s.Machines))
		for _, m := range s.Machines {
			if m.Name != "" && m.ID != "" {
				machineIDByName[m.Name] = m.ID
			}
		}

		for _, t := range s.Terminals {
			relayed := false
			relayVia := ""
			if prefix, _, hasSlash := strings.Cut(t.Address, "/"); hasSlash {
				relayed = true
				relayVia = s.InstanceName
				// If this federated peer core is already directly connected & online, suppress duplicate!
				if peerCoreID, ok := machineIDByName[prefix]; ok && onlineDirectCoreIDs[peerCoreID] {
					continue
				}
			}
			// If this instance is offline, skip its stale entries if another online core already shows them
			// via direct or relay; otherwise keep stale entries visible while reconnecting.
			key := TermKey{InstanceID: s.InstanceID, Addr: t.Address}
			if seen[key.String()] {
				continue
			}
			// Also suppress stale offline direct core's terminal when an online relay has it.
			if !s.Online && s.CoreID != "" {
				hasOnlineRelay := false
				for _, other := range snaps {
					if !other.Online || other.InstanceID == s.InstanceID {
						continue
					}
					for _, m := range other.Machines {
						if m.ID == s.CoreID {
							hasOnlineRelay = true
							break
						}
					}
					if hasOnlineRelay {
						break
					}
				}
				if hasOnlineRelay {
					continue
				}
			}

			seen[key.String()] = true
			out = append(out, AggregatedTerminal{
				Key:          key,
				InstanceName: s.InstanceName,
				CoreID:       s.CoreID,
				Relayed:      relayed,
				RelayVia:     relayVia,
				Terminal:     t,
			})
		}
	}

	// Deterministic priority sort: NeedsYou first, then working, then idle/live, then finished; tie-break by CreatedMS desc.
	sort.SliceStable(out, func(i, j int) bool {
		ri := termSortRank(out[i].Terminal)
		rj := termSortRank(out[j].Terminal)
		if ri != rj {
			return ri < rj
		}
		if out[i].Terminal.CreatedMS != out[j].Terminal.CreatedMS {
			return out[i].Terminal.CreatedMS > out[j].Terminal.CreatedMS
		}
		return out[i].Key.String() < out[j].Key.String()
	})
	return out
}

func termSortRank(t Terminal) int {
	switch t.StateWord() {
	case "blocked":
		return 0
	case "error":
		return 1
	case "working":
		return 2
	case "live", "idle":
		return 3
	case "done":
		return 4
	default:
		if t.Finished() {
			return 5
		}
		return 3
	}
}

// FrameCache is a thread-safe bounded LRU cache of initial/last VT paint bytes per terminal.
type FrameCache struct {
	mu    sync.Mutex
	cap   int
	order *list.List
	items map[TermKey]*list.Element
}

type frameEntry struct {
	key  TermKey
	data []byte
}

// NewFrameCache returns a FrameCache holding at most capacity sessions (default 24).
func NewFrameCache(capacity int) *FrameCache {
	if capacity <= 0 {
		capacity = 24
	}
	return &FrameCache{
		cap:   capacity,
		order: list.New(),
		items: make(map[TermKey]*list.Element),
	}
}

// Get returns a copy of cached VT bytes and promotes key to MRU.
func (fc *FrameCache) Get(key TermKey) ([]byte, bool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	el, ok := fc.items[key]
	if !ok {
		return nil, false
	}
	fc.order.MoveToFront(el)
	src := el.Value.(*frameEntry).data
	out := make([]byte, len(src))
	copy(out, src)
	return out, true
}

// Put stores a copy of VT bytes for key, evicting the least recently used session when full.
func (fc *FrameCache) Put(key TermKey, data []byte) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	if el, ok := fc.items[key]; ok {
		el.Value.(*frameEntry).data = cp
		fc.order.MoveToFront(el)
		return
	}
	el := fc.order.PushFront(&frameEntry{key: key, data: cp})
	fc.items[key] = el
	for fc.order.Len() > fc.cap {
		tail := fc.order.Back()
		if tail == nil {
			break
		}
		fc.order.Remove(tail)
		delete(fc.items, tail.Value.(*frameEntry).key)
	}
}

// Len reports how many sessions are cached.
func (fc *FrameCache) Len() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.order.Len()
}

// DialFunc opens a raw JSON-RPC stream to a core host.
type DialFunc func(Host) (io.ReadWriteCloser, error)

// CorePool maintains concurrent framed + events.subscribe connections to multiple herdr-core instances.
type CorePool struct {
	dial       DialFunc
	onChange   func()
	onNeedsYou func(AggregatedTerminal)

	mu      sync.Mutex
	workers map[string]*coreWorker
	order   []string
	closed  bool
}

type coreWorker struct {
	pool   *CorePool
	host   Host
	cancel context.CancelFunc
	wakeCh chan struct{}

	mu          sync.Mutex
	client      *Client
	coreID      string
	coreName    string
	coreVersion string
	state       string
	problem     string
	latencyMS   int64
	machines    []MachineView
	templates   []Template
	terminals   map[string]Terminal
	termOrder   []string
	notified    map[string]string // addr -> last notified state+msg key
}

// NewCorePool creates a concurrent multi-instance connection pool.
func NewCorePool(dial DialFunc, onChange func()) *CorePool {
	return &CorePool{
		dial:     dial,
		onChange: onChange,
		workers:  make(map[string]*coreWorker),
	}
}

// OnNeedsYou registers a callback triggered when any terminal transitions into blocked/error.
func (p *CorePool) OnNeedsYou(fn func(AggregatedTerminal)) {
	p.mu.Lock()
	p.onNeedsYou = fn
	p.mu.Unlock()
}

// SetHosts reconciles the pool's running workers against hosts.
func (p *CorePool) SetHosts(hosts []Host) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	desired := make(map[string]Host, len(hosts))
	nextOrder := make([]string, 0, len(hosts))
	for _, h := range hosts {
		id := h.EffectiveID()
		desired[id] = h
		nextOrder = append(nextOrder, id)
	}

	// Stop removed or changed workers.
	var toStop []*coreWorker
	for id, w := range p.workers {
		h, keep := desired[id]
		if !keep || !h.IsEnabled() || hostChanged(w.host, h) {
			toStop = append(toStop, w)
			delete(p.workers, id)
		}
	}

	// Start new enabled workers.
	var toStart []*coreWorker
	for _, id := range nextOrder {
		h := desired[id]
		if !h.IsEnabled() {
			continue
		}
		if _, exists := p.workers[id]; !exists {
			ctx, cancel := context.WithCancel(context.Background())
			w := &coreWorker{
				pool:      p,
				host:      h,
				cancel:    cancel,
				wakeCh:    make(chan struct{}, 1),
				state:     stateDialing,
				terminals: make(map[string]Terminal),
				notified:  make(map[string]string),
			}
			p.workers[id] = w
			toStart = append(toStart, w)
			go w.run(ctx)
		}
	}
	p.order = nextOrder
	p.mu.Unlock()

	for _, w := range toStop {
		w.stop()
	}
	if len(toStop) > 0 || len(toStart) > 0 {
		p.emitChange()
	}
}

func hostChanged(a, b Host) bool {
	return a.Target != b.Target || a.User != b.User || a.Password != b.Password || a.Key != b.Key || a.PinnedFP != b.PinnedFP || a.Label != b.Label
}

// WakeAll immediately resets backoff and reconnects any offline workers (e.g. foreground resume).
func (p *CorePool) WakeAll() {
	p.mu.Lock()
	workers := make([]*coreWorker, 0, len(p.workers))
	for _, w := range p.workers {
		workers = append(workers, w)
	}
	p.mu.Unlock()
	for _, w := range workers {
		select {
		case w.wakeCh <- struct{}{}:
		default:
		}
	}
}

// ReconnectInstance forces a fresh dial for one instance.
func (p *CorePool) ReconnectInstance(instanceID string) {
	p.mu.Lock()
	w := p.workers[instanceID]
	p.mu.Unlock()
	if w == nil {
		return
	}
	w.mu.Lock()
	c := w.client
	w.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

// ClientFor returns the active *Client for instanceID, if online.
func (p *CorePool) ClientFor(instanceID string) *Client {
	p.mu.Lock()
	w := p.workers[instanceID]
	p.mu.Unlock()
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client
}

// Snapshots returns all configured instances' live snapshots in order.
func (p *CorePool) Snapshots() []InstanceSnapshot {
	p.mu.Lock()
	workers := make([]*coreWorker, 0, len(p.order))
	for _, id := range p.order {
		if w, ok := p.workers[id]; ok {
			workers = append(workers, w)
		}
	}
	p.mu.Unlock()

	out := make([]InstanceSnapshot, 0, len(workers))
	for _, w := range workers {
		out = append(out, w.snapshot())
	}
	return out
}

// Terminals returns the deduplicated, priority-sorted terminal list across all instances.
func (p *CorePool) Terminals() []AggregatedTerminal {
	return AggregateTerminals(p.Snapshots())
}

// Close shuts down every worker cleanly.
func (p *CorePool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	workers := make([]*coreWorker, 0, len(p.workers))
	for _, w := range p.workers {
		workers = append(workers, w)
	}
	p.workers = make(map[string]*coreWorker)
	p.mu.Unlock()

	for _, w := range workers {
		w.stop()
	}
}

func (p *CorePool) emitChange() {
	if p.onChange != nil {
		p.onChange()
	}
}

func (w *coreWorker) stop() {
	w.cancel()
	w.mu.Lock()
	c := w.client
	w.client = nil
	w.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (w *coreWorker) snapshot() InstanceSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	name := w.host.Label
	if name == "" {
		name = w.coreName
	}
	if name == "" {
		name = w.host.EffectiveID()
	}
	terms := make([]Terminal, 0, len(w.termOrder))
	for _, addr := range w.termOrder {
		if t, ok := w.terminals[addr]; ok {
			terms = append(terms, t)
		}
	}
	machines := append([]MachineView(nil), w.machines...)
	templates := append([]Template(nil), w.templates...)
	return InstanceSnapshot{
		InstanceID:   w.host.EffectiveID(),
		InstanceName: name,
		Target:       w.host.Target,
		User:         w.host.User,
		CoreID:       w.coreID,
		CoreVersion:  w.coreVersion,
		State:        w.state,
		Online:       w.state == stateOnline,
		Problem:      w.problem,
		LatencyMS:    w.latencyMS,
		Machines:     machines,
		Templates:    templates,
		Terminals:    terms,
	}
}

func (w *coreWorker) run(ctx context.Context) {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	attempt := 0

	for {
		if ctx.Err() != nil {
			return
		}
		w.setState(stateDialing, "")
		start := time.Now()
		rw, err := w.pool.dial(w.host)
		if err != nil {
			w.setState(stateFailed, explain(err))
			if !w.sleepOrWake(ctx, backoffs[min(attempt, len(backoffs)-1)]) {
				return
			}
			attempt++
			continue
		}

		c := NewClient(rw)
		c.OnEvent(func(ev Event) { w.applyEvent(ctx, c, ev) })

		hctx, hcancel := context.WithTimeout(ctx, 20*time.Second)
		hello, err := c.HelloFramed(hctx)
		hcancel()
		if err != nil {
			_ = c.Close()
			w.setState(stateFailed, explain(err))
			if !w.sleepOrWake(ctx, backoffs[min(attempt, len(backoffs)-1)]) {
				return
			}
			attempt++
			continue
		}

		latency := time.Since(start).Milliseconds()
		sctx, scancel := context.WithTimeout(ctx, 15*time.Second)
		sub, subErr := c.Subscribe(sctx)
		scancel()
		if subErr != nil {
			_ = c.Close()
			w.setState(stateFailed, explain(subErr))
			if !w.sleepOrWake(ctx, backoffs[min(attempt, len(backoffs)-1)]) {
				return
			}
			attempt++
			continue
		}

		// Connected & subscribed!
		attempt = 0
		w.mu.Lock()
		w.client = c
		w.coreID = hello.Core.ID
		w.coreName = hello.Core.Name
		w.coreVersion = hello.Version
		w.latencyMS = latency
		w.state = stateOnline
		w.problem = ""
		w.replaceTerminalsLocked(sub.Terminals)
		w.mu.Unlock()
		w.pool.emitChange()

		// Fetch machines & templates asynchronously without blocking UI snapshot.
		go w.loadMetadata(ctx, c)

		// Wait until connection drops or context canceled.
		select {
		case <-ctx.Done():
			_ = c.Close()
			return
		case <-c.Closed():
			w.mu.Lock()
			if w.client == c {
				w.client = nil
			}
			w.state = stateFailed
			w.problem = "与核心的连接断了，正在自动重连。"
			w.mu.Unlock()
			w.pool.emitChange()
			if !w.sleepOrWake(ctx, backoffs[0]) {
				return
			}
		}
	}
}

func (w *coreWorker) loadMetadata(ctx context.Context, c *Client) {
	mctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	machines, _ := c.Machines(mctx)
	templates, _ := c.Templates(mctx)
	w.mu.Lock()
	if machines != nil {
		w.machines = machines
	}
	if templates != nil {
		w.templates = templates
	}
	w.mu.Unlock()
	w.pool.emitChange()
}

func (w *coreWorker) setState(st, problem string) {
	w.mu.Lock()
	w.state = st
	w.problem = problem
	w.mu.Unlock()
	w.pool.emitChange()
}

func (w *coreWorker) sleepOrWake(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-w.wakeCh:
		return true
	case <-t.C:
		return true
	}
}

func (w *coreWorker) replaceTerminalsLocked(list []Terminal) {
	w.terminals = make(map[string]Terminal, len(list))
	w.termOrder = make([]string, 0, len(list))
	for _, t := range list {
		w.terminals[t.Address] = t
		w.termOrder = append(w.termOrder, t.Address)
	}
}

func (w *coreWorker) applyEvent(ctx context.Context, c *Client, ev Event) {
	var alertTerm *AggregatedTerminal

	w.mu.Lock()
	switch ev.Type {
	case "created":
		var t Terminal
		if json.Unmarshal(ev.Data, &t) == nil && t.Address != "" {
			if _, ok := w.terminals[t.Address]; !ok {
				w.termOrder = append(w.termOrder, t.Address)
			}
			w.terminals[t.Address] = t
		} else {
			w.mu.Unlock()
			w.resync(ctx, c)
			return
		}
	case "removed":
		delete(w.terminals, ev.Terminal)
		filtered := w.termOrder[:0]
		for _, a := range w.termOrder {
			if a != ev.Terminal {
				filtered = append(filtered, a)
			}
		}
		w.termOrder = filtered
		delete(w.notified, ev.Terminal)
	case "status":
		if t, ok := w.terminals[ev.Terminal]; ok {
			var st Status
			if json.Unmarshal(ev.Data, &st) == nil && st.State != "" {
				t.Status = &st
				t.State = st.State
				w.terminals[ev.Terminal] = t
				if t.NeedsYou() {
					sig := fmt.Sprintf("%s:%s:%s", st.State, st.Kind, st.Msg)
					if w.notified[ev.Terminal] != sig {
						w.notified[ev.Terminal] = sig
						name := w.host.Label
						if name == "" {
							name = w.coreName
						}
						alertTerm = &AggregatedTerminal{
							Key:          TermKey{InstanceID: w.host.EffectiveID(), Addr: t.Address},
							InstanceName: name,
							CoreID:       w.coreID,
							Terminal:     t,
						}
					}
				} else {
					delete(w.notified, ev.Terminal)
				}
			}
		} else {
			w.mu.Unlock()
			w.resync(ctx, c)
			return
		}
	case "title":
		if t, ok := w.terminals[ev.Terminal]; ok {
			var title string
			if json.Unmarshal(ev.Data, &title) == nil {
				t.Title = title
				w.terminals[ev.Terminal] = t
			}
		}
	case "finished", "lost", "restarted", "missed":
		w.mu.Unlock()
		w.resync(ctx, c)
		return
	default:
		// turn, size, keep, labels, doc: refresh specific terminal if present
		if ev.Terminal != "" {
			w.mu.Unlock()
			go w.refreshOne(ctx, c, ev.Terminal)
			return
		}
	}
	w.mu.Unlock()

	w.pool.emitChange()
	if alertTerm != nil {
		w.pool.mu.Lock()
		fn := w.pool.onNeedsYou
		w.pool.mu.Unlock()
		if fn != nil {
			fn(*alertTerm)
		}
	}
}

func (w *coreWorker) refreshOne(ctx context.Context, c *Client, addr string) {
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	t, err := c.Get(rctx, addr)
	if err != nil {
		return
	}
	w.mu.Lock()
	if _, ok := w.terminals[addr]; !ok {
		w.termOrder = append(w.termOrder, addr)
	}
	w.terminals[addr] = t
	w.mu.Unlock()
	w.pool.emitChange()
}

func (w *coreWorker) resync(ctx context.Context, c *Client) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := c.List(rctx, true)
	if err != nil {
		return
	}
	machines, _ := c.Machines(rctx)
	w.mu.Lock()
	w.replaceTerminalsLocked(list.Terminals)
	if machines != nil {
		w.machines = machines
	}
	w.mu.Unlock()
	w.pool.emitChange()
}
