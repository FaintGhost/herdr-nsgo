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
	"sync/atomic"
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

	// Track which peer CoreIDs actually have active relayed terminals on any online core.
	relayedActiveCoreIDs := make(map[string]bool)
	for _, s := range snaps {
		if !s.Online {
			continue
		}
		machineIDByName := make(map[string]string, len(s.Machines))
		for _, m := range s.Machines {
			if m.Name != "" && m.ID != "" {
				machineIDByName[m.Name] = m.ID
			}
		}
		for _, t := range s.Terminals {
			if prefix, _, hasSlash := strings.Cut(t.Address, "/"); hasSlash {
				if peerCoreID, ok := machineIDByName[prefix]; ok && peerCoreID != "" {
					relayedActiveCoreIDs[peerCoreID] = true
				}
			}
		}
	}

	var out []AggregatedTerminal
	seen := make(map[string]bool)
	seenRelayPeer := make(map[string]bool)

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
			peerCoreID := ""
			if prefix, subAddr, hasSlash := strings.Cut(t.Address, "/"); hasSlash {
				relayed = true
				relayVia = s.InstanceName
				if pid, ok := machineIDByName[prefix]; ok && pid != "" {
					peerCoreID = pid
					// If this federated peer core is already directly connected & online, suppress duplicate!
					if onlineDirectCoreIDs[peerCoreID] {
						continue
					}
					// If another online core already relayed this same (peerCoreID, subAddr), deduplicate!
					relayDedupeKey := peerCoreID + "::" + subAddr
					if seenRelayPeer[relayDedupeKey] {
						continue
					}
					seenRelayPeer[relayDedupeKey] = true
				}
			}
			key := TermKey{InstanceID: s.InstanceID, Addr: t.Address}
			if seen[key.String()] {
				continue
			}
			// Suppress stale offline direct core's terminal ONLY if an online relay actually carries terminals for it.
			if !s.Online && s.CoreID != "" && relayedActiveCoreIDs[s.CoreID] {
				continue
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

// alertTracker computes deduplicated NeedsYou and completion-report notification diffs over AggregateTerminals snapshots.
// First sight of an instance/terminal seeds silently so reconnecting doesn't spam stale alerts.
type alertTracker struct {
	mu        sync.Mutex
	seeded    bool
	seen      map[string]string // canonical terminal identity -> last status signature
	lastState map[string]string // canonical terminal identity -> previous StateWord
}

func newAlertTracker() *alertTracker {
	return &alertTracker{
		seen:      make(map[string]string),
		lastState: make(map[string]string),
	}
}

// isExplicitCompletionTerminal reports whether a terminal carries an explicit agent completion report.
func isExplicitCompletionTerminal(t Terminal) bool {
	if t.NeedsYou() {
		return false
	}
	if t.Status != nil && t.Status.State == "done" {
		return true
	}
	return t.Finished() && IsAgentTerminal(t)
}

// completionSignature uniquely identifies one completion event so reading/acknowledging it stays dismissed
// until the terminal runs a newer turn or reports a new completion message.
func completionSignature(t Terminal) string {
	var statusMS, finMS uint64
	if t.StatusMS != nil {
		statusMS = *t.StatusMS
	}
	if t.FinishedMS != nil {
		finMS = *t.FinishedMS
	}
	msg := ""
	if t.Status != nil {
		msg = strings.TrimSpace(t.Status.Msg)
	}
	return fmt.Sprintf("done:%d:%d:%s:%s", statusMS, finMS, t.StateWord(), msg)
}

func (tr *alertTracker) diff(snaps []InstanceSnapshot, agg []AggregatedTerminal) []AggregatedTerminal {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	active := make(map[string]bool, len(agg))
	var alerts []AggregatedTerminal
	for _, it := range agg {
		id := it.Key.String()
		if it.CoreID != "" && !it.Relayed {
			id = it.CoreID + "::" + it.Key.Addr
		}
		active[id] = true
		curState := it.Terminal.StateWord()
		prevState := tr.lastState[id]

		switch {
		case it.Terminal.NeedsYou():
			msg := ""
			kind := ""
			if it.Terminal.Status != nil {
				msg = it.Terminal.Status.Msg
				kind = it.Terminal.Status.Kind
			}
			sig := fmt.Sprintf("%s:%s:%s", curState, kind, msg)
			if tr.seen[id] != sig {
				tr.seen[id] = sig
				if tr.seeded {
					alerts = append(alerts, it)
				}
			}
		case isExplicitCompletionTerminal(it.Terminal) || (prevState == "working" && (curState == "idle" || curState == "done")):
			sig := completionSignature(it.Terminal)
			if tr.seen[id] != sig {
				tr.seen[id] = sig
				if tr.seeded {
					alerts = append(alerts, it)
				}
			}
		case curState == "working":
			delete(tr.seen, id)
		}
		tr.lastState[id] = curState
	}
	for id := range tr.seen {
		if !active[id] {
			delete(tr.seen, id)
		}
	}
	for id := range tr.lastState {
		if !active[id] {
			delete(tr.lastState, id)
		}
	}
	tr.seeded = true
	return alerts
}

// SessionSortMode controls how active sessions are ordered on the Home screen.
type SessionSortMode string

const (
	SortActiveNewest SessionSortMode = "active_newest" // 最近活跃（新→旧）
	SortActiveOldest SessionSortMode = "active_oldest" // 最早活跃（旧→新）
	SortManual       SessionSortMode = "manual"        // 手动拖拽排序
)

func (m SessionSortMode) Label() string {
	switch m {
	case SortActiveOldest:
		return "最早活跃"
	case SortManual:
		return "手动排序"
	default:
		return "最近活跃"
	}
}

// GroupActiveMS returns the latest activity timestamp (ms) across a parent session and its sub-sessions.
func GroupActiveMS(g SessionGroup, live map[TermKey]uint64) uint64 {
	best := g.Primary.Terminal.ActiveMS()
	if live != nil {
		if ts := live[g.Primary.Key]; ts > best {
			best = ts
		}
	}
	for _, ch := range g.Children {
		if ts := ch.Terminal.ActiveMS(); ts > best {
			best = ts
		}
		if live != nil {
			if ts := live[ch.Key]; ts > best {
				best = ts
			}
		}
	}
	return best
}

// SortSessionGroups orders session groups by active time (newest/oldest) or persisted manual drag order.
func SortSessionGroups(groups []SessionGroup, mode SessionSortMode, manualOrder []string, live map[TermKey]uint64) []SessionGroup {
	if len(groups) <= 1 {
		return groups
	}
	out := make([]SessionGroup, len(groups))
	copy(out, groups)

	switch mode {
	case SortActiveOldest:
		sort.SliceStable(out, func(i, j int) bool {
			ai := GroupActiveMS(out[i], live)
			aj := GroupActiveMS(out[j], live)
			if ai != aj {
				return ai < aj
			}
			if out[i].Primary.Terminal.CreatedMS != out[j].Primary.Terminal.CreatedMS {
				return out[i].Primary.Terminal.CreatedMS < out[j].Primary.Terminal.CreatedMS
			}
			return out[i].Primary.Key.String() < out[j].Primary.Key.String()
		})
	case SortManual:
		rank := make(map[string]int, len(manualOrder))
		for idx, k := range manualOrder {
			if _, exists := rank[k]; !exists {
				rank[k] = idx
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			ki := out[i].Primary.Key.String()
			kj := out[j].Primary.Key.String()
			ri, oki := rank[ki]
			rj, okj := rank[kj]
			if oki && okj {
				return ri < rj
			}
			// Brand-new sessions not yet placed manually float cleanly to the top by newest activity.
			if oki != okj {
				return !oki
			}
			ai := GroupActiveMS(out[i], live)
			aj := GroupActiveMS(out[j], live)
			if ai != aj {
				return ai > aj
			}
			return ki < kj
		})
	default: // SortActiveNewest
		sort.SliceStable(out, func(i, j int) bool {
			ai := GroupActiveMS(out[i], live)
			aj := GroupActiveMS(out[j], live)
			if ai != aj {
				return ai > aj
			}
			if out[i].Primary.Terminal.CreatedMS != out[j].Primary.Terminal.CreatedMS {
				return out[i].Primary.Terminal.CreatedMS > out[j].Primary.Terminal.CreatedMS
			}
			return out[i].Primary.Key.String() < out[j].Primary.Key.String()
		})
	}
	return out
}

// ReorderKeySlice moves fromKey to the position of toKey (before or after), seeding from baseOrder.
func ReorderKeySlice(baseOrder []string, fromKey, toKey string, placeAfter bool) []string {
	if fromKey == "" || toKey == "" || fromKey == toKey {
		return append([]string(nil), baseOrder...)
	}
	seen := make(map[string]bool, len(baseOrder)+2)
	clean := make([]string, 0, len(baseOrder)+2)
	for _, k := range baseOrder {
		if k != "" && !seen[k] {
			seen[k] = true
			clean = append(clean, k)
		}
	}
	if !seen[fromKey] {
		clean = append(clean, fromKey)
	}
	if !seen[toKey] {
		clean = append(clean, toKey)
	}
	without := make([]string, 0, len(clean)-1)
	for _, k := range clean {
		if k != fromKey {
			without = append(without, k)
		}
	}
	targetIdx := -1
	for i, k := range without {
		if k == toKey {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return append(without, fromKey)
	}
	insertAt := targetIdx
	if placeAfter {
		insertAt = targetIdx + 1
	}
	res := make([]string, 0, len(without)+1)
	res = append(res, without[:insertAt]...)
	res = append(res, fromKey)
	res = append(res, without[insertAt:]...)
	return res
}

// SessionGroup pairs a top-level parent terminal with any child sub-sessions spawned from it (via Terminal.Parent).
type SessionGroup struct {
	Primary           AggregatedTerminal
	Children          []AggregatedTerminal
	ActiveChildren    int
	AttentionChildren int
}

// GroupSessions nests live child sub-sessions (Terminal.Parent != nil) under their root parent session
// and moves all finished terminals (including exited child/grandchild sub-sessions) into the finished slice
// so active sub-session counts reflect live sub-sessions rather than accumulated historical exited commands.
func GroupSessions(items []AggregatedTerminal) (active []SessionGroup, finished []AggregatedTerminal) {
	byKey := make(map[TermKey]int, len(items))
	for i, it := range items {
		byKey[it.Key] = i
	}

	// Resolve root ancestor key for each item (guards against cycles).
	rootOf := func(it AggregatedTerminal) (TermKey, bool) {
		cur := it
		visited := 0
		for cur.Terminal.Parent != nil && *cur.Terminal.Parent != "" && visited < 8 {
			parentKey := TermKey{InstanceID: cur.Key.InstanceID, Addr: *cur.Terminal.Parent}
			idx, ok := byKey[parentKey]
			if !ok {
				break
			}
			cur = items[idx]
			visited++
		}
		if cur.Key == it.Key {
			return TermKey{}, false
		}
		return cur.Key, true
	}

	childrenByRoot := make(map[TermKey][]AggregatedTerminal)
	isChild := make(map[TermKey]bool)
	var finishedChildren []AggregatedTerminal
	for _, it := range items {
		if rootKey, ok := rootOf(it); ok {
			isChild[it.Key] = true
			if it.Terminal.Finished() {
				finishedChildren = append(finishedChildren, it)
				continue
			}
			childrenByRoot[rootKey] = append(childrenByRoot[rootKey], it)
		}
	}

	for _, it := range items {
		if isChild[it.Key] {
			continue
		}
		kids := childrenByRoot[it.Key]
		if it.Terminal.Finished() && len(kids) == 0 {
			finished = append(finished, it)
			continue
		}
		activeKids := 0
		attnKids := 0
		for _, ch := range kids {
			if ch.Terminal.StateWord() == "working" || ch.Terminal.NeedsYou() {
				activeKids++
			}
			if ch.Terminal.NeedsYou() {
				attnKids++
			}
		}
		active = append(active, SessionGroup{
			Primary:           it,
			Children:          kids,
			ActiveChildren:    activeKids,
			AttentionChildren: attnKids,
		})
	}
	finished = append(finished, finishedChildren...)
	return active, finished
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
	pool         *CorePool
	host         Host
	cancel       context.CancelFunc
	wakeCh       chan struct{}
	resyncCh     chan struct{}
	resetBackoff atomic.Bool

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
	revision    uint64 // Changes to terminal membership or state since the last RPC began.
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
				resyncCh:  make(chan struct{}, 1),
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
	return a.Target != b.Target || a.User != b.User || a.Password != b.Password || a.Key != b.Key || a.PinnedFP != b.PinnedFP || a.Label != b.Label || a.UseLocalKey != b.UseLocalKey || a.CredRev != b.CredRev
}

// WakeAll immediately resets backoff, wakes offline workers, and 500ms-pings online workers
// so half-open mobile TCP sockets after sleep or Wi-Fi↔5G handoff reconnect cleanly.
func (p *CorePool) WakeAll() {
	p.mu.Lock()
	workers := make([]*coreWorker, 0, len(p.workers))
	for _, w := range p.workers {
		workers = append(workers, w)
	}
	p.mu.Unlock()
	for _, w := range workers {
		w.resetBackoff.Store(true)
		select {
		case w.wakeCh <- struct{}{}:
		default:
		}
		w.mu.Lock()
		c := w.client
		w.mu.Unlock()
		if c != nil {
			w.triggerResync()
			go func(client *Client) {
				if !client.Ping(500 * time.Millisecond) {
					_ = client.Close()
				}
			}(c)
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

// RefreshInstance reconciles a stale address without dropping open streams.
func (p *CorePool) RefreshInstance(id string) {
	p.mu.Lock()
	w := p.workers[id]
	p.mu.Unlock()
	if w != nil {
		w.triggerResync()
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
		var initMu sync.Mutex
		var pending []Event
		ready := false
		c.OnEvent(func(ev Event) {
			initMu.Lock()
			defer initMu.Unlock()
			if !ready {
				pending = append(pending, ev)
				return
			}
			w.applyEvent(ctx, c, ev)
		})

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

		// Fetch initial machines (short 3s budget) before first emit so cross-core dedup has MachineView.ID immediately.
		mctx, mcancel := context.WithTimeout(ctx, 3*time.Second)
		initMachines, _ := c.Machines(mctx)
		mcancel()

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
		if initMachines != nil {
			w.machines = initMachines
		}
		w.replaceTerminalsLocked(sub.Terminals)
		w.mu.Unlock()
		initMu.Lock()
		for _, ev := range pending {
			w.applyEvent(ctx, c, ev)
		}
		pending = nil
		ready = true
		initMu.Unlock()
		w.pool.emitChange()

		// Fetch templates (and retry machines if initial call timed out) asynchronously.
		go w.loadMetadata(ctx, c)

		// Dedicated per-connection async resync loop so Client.readLoop NEVER blocks on RPCs.
		connCtx, connCancel := context.WithCancel(ctx)
		go func(cctx context.Context, client *Client) {
			for {
				select {
				case <-cctx.Done():
					return
				case <-client.Closed():
					return
				case <-w.resyncCh:
					w.resync(cctx, client)
				}
			}
		}(connCtx, c)

		// Wait until connection drops or context canceled.
		select {
		case <-ctx.Done():
			connCancel()
			_ = c.Close()
			return
		case <-c.Closed():
			connCancel()
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
	if w.resetBackoff.Swap(false) {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-w.wakeCh:
		w.resetBackoff.Store(false)
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

func (w *coreWorker) triggerResync() {
	select {
	case w.resyncCh <- struct{}{}:
	default:
	}
}

func (w *coreWorker) applyEvent(ctx context.Context, c *Client, ev Event) {
	w.mu.Lock()
	if w.client != c {
		w.mu.Unlock()
		return
	}
	w.revision++
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
			w.triggerResync()
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
			var payload struct {
				Status *Status `json:"status"`
				State  string  `json:"state"`
			}
			if json.Unmarshal(ev.Data, &payload) != nil {
				w.mu.Unlock()
				w.triggerResync()
				return
			}
			st := payload.Status
			if st == nil && payload.State != "" {
				st = new(Status)
				_ = json.Unmarshal(ev.Data, st)
			}
			if st == nil || st.State == "" {
				w.mu.Unlock()
				w.triggerResync()
				return
			}
			t.Status = st
			w.terminals[ev.Terminal] = t
		} else {
			w.mu.Unlock()
			w.triggerResync()
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
		w.triggerResync()
		return
	default:
		if ev.Terminal != "" {
			revision := w.revision
			w.mu.Unlock()
			go w.refreshOne(ctx, c, ev.Terminal, revision)
			return
		}
	}
	w.mu.Unlock()

	w.pool.emitChange()
}

func (w *coreWorker) refreshOne(ctx context.Context, c *Client, addr string, revision uint64) {
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	t, err := c.Get(rctx, addr)
	if err != nil {
		return
	}
	w.mu.Lock()
	if w.client != c {
		w.mu.Unlock()
		return
	}
	if w.revision != revision {
		w.mu.Unlock()
		w.triggerResync()
		return
	}
	if _, ok := w.terminals[addr]; !ok {
		w.termOrder = append(w.termOrder, addr)
	}
	w.terminals[addr] = t
	w.revision++
	w.mu.Unlock()
	w.pool.emitChange()
}

func (w *coreWorker) resync(ctx context.Context, c *Client) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	w.mu.Lock()
	revision := w.revision
	w.mu.Unlock()
	list, err := c.List(rctx, true)
	if err != nil {
		return
	}
	machines, _ := c.Machines(rctx)
	w.mu.Lock()
	if w.client != c {
		w.mu.Unlock()
		return
	}
	if revision != w.revision {
		w.mu.Unlock()
		w.triggerResync()
		return
	}
	w.revision++
	w.replaceTerminalsLocked(list.Terminals)
	if machines != nil {
		w.machines = machines
	}
	w.mu.Unlock()
	w.pool.emitChange()
}
