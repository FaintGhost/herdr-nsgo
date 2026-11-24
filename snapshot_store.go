package main

import (
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

const (
	snapIndexStorageKey = "herdr-nsgo.snap.index.v1"
	chatSnapKeyPrefix   = "herdr-nsgo.chatsnap.v1."
	vtSnapKeyPrefix     = "herdr-nsgo.vtsnap.v1."

	maxSnapKeys          = 24
	maxSnapChatItems     = 40
	maxSnapItemTextBytes = 12 * 1024
	maxSnapVTBytes       = 32 * 1024
)

// ChatSnapshot stores a compact, bounded local snapshot of a session's structured conversation
// so opening any previously visited session renders on Frame 0 (0ms) before SSH/RPC catches up.
type ChatSnapshot struct {
	Source       TranscriptSource `json:"source"`
	Format       TranscriptFormat `json:"format,omitempty"`
	ResolvedPath string           `json:"resolved_path,omitempty"`
	OnMachine    string           `json:"on_machine,omitempty"`
	Items        []ChatTurnItem   `json:"items"`
	UpdatedMS    int64            `json:"updated_ms"`
}

// SnapshotStore persists bounded LRU per-session Chat and VT snapshots in NSGo local storage
// (with an in-memory mirror for fast synchronous Frame-0 reads and nil-scope unit tests).
type SnapshotStore struct {
	cx      *core.Scope
	mu      sync.Mutex
	order   []string // MRU at end
	chatMem map[string]ChatSnapshot
	vtMem   map[string][]byte
}

// NewSnapshotStore initializes a SnapshotStore and loads the persisted LRU key index if cx != nil.
func NewSnapshotStore(cx *core.Scope) *SnapshotStore {
	s := &SnapshotStore{
		cx:      cx,
		chatMem: make(map[string]ChatSnapshot),
		vtMem:   make(map[string][]byte),
	}
	if cx != nil {
		if raw, ok := cx.Storage().GetItem(snapIndexStorageKey); ok && raw != "" {
			var keys []string
			if json.Unmarshal([]byte(raw), &keys) == nil {
				s.order = keys
			}
		}
	}
	return s
}

func (s *SnapshotStore) touchLocked(k string) (evicted []string) {
	kept := s.order[:0]
	for _, existing := range s.order {
		if existing != k {
			kept = append(kept, existing)
		}
	}
	kept = append(kept, k)
	if len(kept) > maxSnapKeys {
		cut := len(kept) - maxSnapKeys
		evicted = append(evicted, kept[:cut]...)
		kept = append([]string(nil), kept[cut:]...)
	}
	s.order = kept
	return evicted
}

func (s *SnapshotStore) persistIndexAndEvict(rawIdx string, evicted []string) {
	if s.cx == nil {
		return
	}
	st := s.cx.Storage()
	for _, ek := range evicted {
		st.RemoveItem(chatSnapKeyPrefix + ek)
		st.RemoveItem(vtSnapKeyPrefix + ek)
	}
	if rawIdx != "" {
		st.SetItem(snapIndexStorageKey, rawIdx)
	}
}

func compactSnapshotItems(items []ChatTurnItem) []ChatTurnItem {
	if len(items) == 0 {
		return nil
	}
	if len(items) > maxSnapChatItems {
		items = items[len(items)-maxSnapChatItems:]
	}
	out := make([]ChatTurnItem, 0, len(items))
	for _, it := range items {
		if it.Optimistic {
			continue
		}
		cp := it
		cp.Text = utf8SafePrefix(cp.Text, maxSnapItemTextBytes)
		cp.Blocks = nil // Rebuilt on demand or rendered directly via TextView Markdown
		if len(cp.Tools) > 0 {
			tools := make([]ChatToolCall, len(cp.Tools))
			for i, tc := range cp.Tools {
				tc.InputSummary = utf8SafePrefix(tc.InputSummary, 2048)
				tc.Output = utf8SafePrefix(tc.Output, 4096)
				tc.DiffOld = utf8SafePrefix(tc.DiffOld, 4096)
				tc.DiffNew = utf8SafePrefix(tc.DiffNew, 4096)
				tc.DiffPatch = utf8SafePrefix(tc.DiffPatch, 6144)
				tools[i] = tc
			}
			cp.Tools = tools
		}
		out = append(out, cp)
	}
	return out
}

// SaveChat stores the latest structured turns for key in memory and persistent storage.
func (s *SnapshotStore) SaveChat(key TermKey, snap ChatSnapshot) {
	if s == nil || key == (TermKey{}) {
		return
	}
	compacted := compactSnapshotItems(snap.Items)
	if len(compacted) == 0 {
		return
	}
	snap.Items = compacted
	if snap.UpdatedMS <= 0 {
		snap.UpdatedMS = time.Now().UnixMilli()
	}
	ks := key.String()

	s.mu.Lock()
	s.chatMem[ks] = snap
	evicted := s.touchLocked(ks)
	for _, ek := range evicted {
		delete(s.chatMem, ek)
		delete(s.vtMem, ek)
	}
	idxBytes, _ := json.Marshal(s.order)
	s.mu.Unlock()

	if s.cx != nil {
		raw, err := json.Marshal(snap)
		if err == nil {
			writeFn := func() {
				s.persistIndexAndEvict(string(idxBytes), evicted)
				s.cx.Storage().SetItem(chatSnapKeyPrefix+ks, string(raw))
			}
			s.cx.Post(writeFn)
		}
	}
}

// LoadChat synchronously retrieves a session's cached ChatSnapshot for 0ms Frame-0 rendering.
func (s *SnapshotStore) LoadChat(key TermKey) (ChatSnapshot, bool) {
	if s == nil || key == (TermKey{}) {
		return ChatSnapshot{}, false
	}
	ks := key.String()

	s.mu.Lock()
	if hit, ok := s.chatMem[ks]; ok && len(hit.Items) > 0 {
		s.mu.Unlock()
		return hit, true
	}
	s.mu.Unlock()

	if s.cx == nil {
		return ChatSnapshot{}, false
	}
	raw, ok := s.cx.Storage().GetItem(chatSnapKeyPrefix + ks)
	if !ok || raw == "" {
		return ChatSnapshot{}, false
	}
	var snap ChatSnapshot
	if json.Unmarshal([]byte(raw), &snap) != nil || len(snap.Items) == 0 {
		return ChatSnapshot{}, false
	}
	s.mu.Lock()
	s.chatMem[ks] = snap
	s.mu.Unlock()
	return snap, true
}

// SaveVT persists the tail raw VT screen bytes for key so cold-start Terminal View also opens in 0ms.
func (s *SnapshotStore) SaveVT(key TermKey, raw []byte) {
	if s == nil || key == (TermKey{}) || len(raw) == 0 {
		return
	}
	if len(raw) > maxSnapVTBytes {
		raw = raw[len(raw)-maxSnapVTBytes:]
	}
	cp := append([]byte(nil), raw...)
	ks := key.String()

	s.mu.Lock()
	s.vtMem[ks] = cp
	evicted := s.touchLocked(ks)
	for _, ek := range evicted {
		delete(s.chatMem, ek)
		delete(s.vtMem, ek)
	}
	idxBytes, _ := json.Marshal(s.order)
	s.mu.Unlock()

	if s.cx != nil {
		encoded := base64.StdEncoding.EncodeToString(cp)
		s.cx.Post(func() {
			s.persistIndexAndEvict(string(idxBytes), evicted)
			s.cx.Storage().SetItem(vtSnapKeyPrefix+ks, encoded)
		})
	}
}

// LoadVT synchronously loads persisted VT tail bytes when in-memory FrameCache misses on cold start.
func (s *SnapshotStore) LoadVT(key TermKey) ([]byte, bool) {
	if s == nil || key == (TermKey{}) {
		return nil, false
	}
	ks := key.String()

	s.mu.Lock()
	if hit, ok := s.vtMem[ks]; ok && len(hit) > 0 {
		out := append([]byte(nil), hit...)
		s.mu.Unlock()
		return out, true
	}
	s.mu.Unlock()

	if s.cx == nil {
		return nil, false
	}
	enc, ok := s.cx.Storage().GetItem(vtSnapKeyPrefix + ks)
	if !ok || enc == "" {
		return nil, false
	}
	dec, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(dec) == 0 {
		return nil, false
	}
	s.mu.Lock()
	s.vtMem[ks] = dec
	s.mu.Unlock()
	return append([]byte(nil), dec...), true
}
