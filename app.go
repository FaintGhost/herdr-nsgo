package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

// Connection states, as the UI shows them.
const (
	stateOff      = "disconnected"
	stateDialing  = "connecting"
	stateOnline   = "connected"
	stateFailed   = "error"
	stateReplaced = "connected"
)

const (
	hostsKey       = "herdr-nsgo.hosts.v2"
	legacyHostsKey = "herdr-nsgo.hosts"
	prefsKey       = "herdr-nsgo.prefs.v1"
	snippetsKey    = "herdr-nsgo.snippets.v1"
	localPrivKeyID = "herdr-nsgo.local.ed25519.priv"
	localPubKeyKey = "herdr-nsgo.local.ed25519.pub"
)

// Snippet is a reusable quick-send command or prompt template.
type Snippet struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Text  string `json:"text"`
}

// AppPrefs persists user appearance and terminal behavior settings.
type AppPrefs struct {
	ThemeMode      ThemeMode   `json:"theme_mode"`
	VTThemeMode    VTThemeMode `json:"vt_theme_mode"`
	VTFontSize     float32     `json:"vt_font_size"`
	ObserveDefault bool        `json:"observe_default"`
	FrameCacheOn   bool        `json:"frame_cache_on"`
	NotifyAlerts   bool        `json:"notify_alerts"`
}

func defaultPrefs() AppPrefs {
	return AppPrefs{
		ThemeMode:      ThemeLight,
		VTThemeMode:    VTFollowTheme,
		VTFontSize:     12,
		ObserveDefault: true,
		FrameCacheOn:   true,
		NotifyAlerts:   true,
	}
}

func defaultSnippets() []Snippet {
	return []Snippet{
		{ID: "git-status", Label: "git status", Text: "git status --short"},
		{ID: "git-diff", Label: "git diff", Text: "git diff --stat"},
		{ID: "continue", Label: "继续完成剩余步骤", Text: "继续完成剩余步骤，跑完测试后汇报结果。"},
		{ID: "tests", Label: "运行测试", Text: "请运行当前项目测试并汇报失败详情。"},
		{ID: "summary", Label: "总结改动", Text: "用 3 条要点总结你刚才的所有改动与风险。"},
	}
}

// App owns the multi-instance CorePool, secrets, TOFU store, preferences, LRU frame cache,
// snippets, and reactive NSGo signals read by all 8 screens.
type App struct {
	cx      *core.Scope
	secrets SecretBackend
	tofu    *TOFUStore
	pool    *CorePool
	cache   *FrameCache

	// Primary v1.0 multi-instance signals
	instances            *core.Signal[[]InstanceSnapshot]
	aggTerms             *core.Signal[[]AggregatedTerminal]
	activeInstanceFilter *core.Signal[string] // "" = all
	searchQuery          *core.Signal[string]
	inboxFilter          *core.Signal[string]
	themeMode            *core.Signal[ThemeMode]
	vtThemeMode          *core.Signal[VTThemeMode]
	vtFontSize           *core.Signal[float32]
	observeDefault       *core.Signal[bool]
	frameCacheOn         *core.Signal[bool]
	notifyAlerts         *core.Signal[bool]
	snippets             *core.Signal[[]Snippet]
	tofuRecords          *core.Signal[[]TOFURecord]
	localPubKey          *core.Signal[string]
	toastMsg             *core.Signal[string]

	// Compatibility signals for legacy tests / v0.1.0 screens until Task 6 removes screens.go
	conn     *core.Signal[string]
	problem  *core.Signal[string]
	coreName *core.Signal[string]
	terms    *core.Signal[[]Terminal]
	machines *core.Signal[[]MachineView]
	texts    *core.Signal[map[string]string]
	composes *core.Signal[map[string]string]
	filter   *core.Signal[string]

	mu                 sync.Mutex
	hosts              []Host
	cachedSnaps        []InstanceSnapshot
	cachedAgg          []AggregatedTerminal
	alerts             *alertTracker
	onNotify           func(TermKey)
	onPoolSync         func()
	transcriptResolver *TranscriptResolver
	transcriptBackend  TranscriptBackend
}

// NewApp wires state, secrets, TOFU store, preferences, snippets, and starts CorePool.
func NewApp(cx *core.Scope) *App {
	prefs := loadPrefs(cx)
	a := &App{
		cx:                   cx,
		secrets:              NewSecretStore(cx),
		tofu:                 NewStorageTOFUStore(cx),
		cache:                NewFrameCache(24),
		alerts:               newAlertTracker(),
		transcriptResolver:   NewTranscriptResolver(),
		instances:            core.NewSignalFunc(cx, []InstanceSnapshot(nil), nil),
		aggTerms:             core.NewSignalFunc(cx, []AggregatedTerminal(nil), nil),
		activeInstanceFilter: core.NewSignal(cx, ""),
		searchQuery:          core.NewSignal(cx, ""),
		inboxFilter:          core.NewSignal(cx, ""),
		themeMode:            core.NewSignal(cx, prefs.ThemeMode),
		vtThemeMode:          core.NewSignal(cx, prefs.VTThemeMode),
		vtFontSize:           core.NewSignal(cx, prefs.VTFontSize),
		observeDefault:       core.NewSignal(cx, prefs.ObserveDefault),
		frameCacheOn:         core.NewSignal(cx, prefs.FrameCacheOn),
		notifyAlerts:         core.NewSignal(cx, prefs.NotifyAlerts),
		snippets:             core.NewSignalFunc(cx, loadSnippets(cx), nil),
		tofuRecords:          core.NewSignalFunc(cx, []TOFURecord(nil), nil),
		localPubKey:          core.NewSignal(cx, ""),
		toastMsg:             core.NewSignal(cx, ""),

		conn:     core.NewSignal(cx, stateOff),
		problem:  core.NewSignal(cx, ""),
		coreName: core.NewSignal(cx, ""),
		terms:    core.NewSignalFunc(cx, []Terminal(nil), nil),
		machines: core.NewSignalFunc(cx, []MachineView(nil), nil),
		texts:    core.NewSignalFunc(cx, map[string]string{}, nil),
		composes: core.NewSignalFunc(cx, map[string]string{}, nil),
		filter:   core.NewSignal(cx, ""),
	}

	if cx != nil {
		cx.SetTheme(AppTheme())
		ApplyAppearance(cx, prefs.ThemeMode)
	}

	a.tofuRecords.Set(a.tofu.List())
	a.tofu.OnChange(func() {
		a.post(func() { a.tofuRecords.Set(a.tofu.List()) })
	})
	a.ensureLocalKey()

	a.pool = NewCorePool(func(h Host) (io.ReadWriteCloser, error) {
		resolved := a.resolveCredentials(h)
		return DialWithTOFU(resolved, a.tofu)
	}, func() {
		a.syncPoolToSignals()
	})
	a.pool.OnNeedsYou(func(at AggregatedTerminal) {
		a.dispatchAlertNotification(at)
	})

	if cx != nil {
		cx.Cleanup(func() { a.pool.Close() })
		cx.OnNotificationPress(func(id string) {
			if inst, addr, ok := strings.Cut(id, "::"); ok {
				a.mu.Lock()
				fn := a.onNotify
				a.mu.Unlock()
				if fn != nil {
					fn(TermKey{InstanceID: inst, Addr: addr})
				}
			}
		})
	}

	hosts := a.loadHosts()
	a.hosts = hosts
	if len(hosts) > 0 {
		a.pool.SetHosts(hosts)
	}
	return a
}

// OnNotificationOpen registers a callback invoked when a system notification is tapped.
func (a *App) OnNotificationOpen(fn func(TermKey)) {
	a.mu.Lock()
	a.onNotify = fn
	a.mu.Unlock()
}

// OnPoolSync registers a UI-thread listener invoked whenever CorePool snapshots/events update.
func (a *App) OnPoolSync(fn func()) {
	a.mu.Lock()
	a.onPoolSync = fn
	a.mu.Unlock()
}

// SetTranscriptBackend overrides the default pooled Client transcript backend (used in tests).
func (a *App) SetTranscriptBackend(b TranscriptBackend) {
	a.mu.Lock()
	a.transcriptBackend = b
	a.mu.Unlock()
}

// TranscriptBackend returns the active TranscriptBackend for Chat View.
func (a *App) TranscriptBackend() TranscriptBackend {
	a.mu.Lock()
	b := a.transcriptBackend
	a.mu.Unlock()
	if b != nil {
		return b
	}
	return NewAppTranscriptBackend(a)
}

func (a *App) post(fn func()) {
	if a.cx != nil {
		a.cx.Post(fn)
		return
	}
	fn()
}

func (a *App) syncPoolToSignals() {
	a.post(func() {
		snaps := a.pool.Snapshots()
		agg := AggregateTerminals(snaps)

		var flatTerms []Terminal
		var flatMachines []MachineView
		connState := stateOff
		problem := ""
		coreName := ""
		for _, s := range snaps {
			flatMachines = append(flatMachines, s.Machines...)
			if s.Online {
				connState = stateOnline
				if coreName == "" {
					coreName = s.InstanceName
				}
			} else if connState != stateOnline {
				if s.State == stateDialing {
					connState = stateDialing
				} else if s.State == stateFailed {
					connState = stateFailed
					if problem == "" {
						problem = s.Problem
					}
				}
			}
		}
		for _, item := range agg {
			flatTerms = append(flatTerms, item.Terminal)
		}

		a.mu.Lock()
		a.cachedSnaps = snaps
		a.cachedAgg = agg
		a.mu.Unlock()

		a.instances.Set(snaps)
		a.aggTerms.Set(agg)
		a.terms.Set(flatTerms)
		a.machines.Set(flatMachines)
		a.conn.Set(connState)
		a.problem.Set(problem)
		a.coreName.Set(coreName)

		// Compute deduplicated notifications strictly on the UI thread.
		for _, alert := range a.alerts.diff(snaps, agg) {
			a.dispatchAlertNotification(alert)
		}

		a.mu.Lock()
		syncFn := a.onPoolSync
		a.mu.Unlock()
		if syncFn != nil {
			syncFn()
		}
	})
}

func (a *App) dispatchAlertNotification(at AggregatedTerminal) {
	if a.cx == nil || !a.notifyAlerts.Get() {
		return
	}
	title := fmt.Sprintf("%s · %s", at.Terminal.Label(), attentionWhy(at.Terminal))
	body := firstLine(at.Terminal.Status, at.Terminal)
	_ = a.cx.ShowNotification(core.Notification{
		ID:    at.Key.String(),
		Title: title,
		Body:  fmt.Sprintf("[%s] %s", at.InstanceName, body),
	}, nil)
}

// ---------------------------------------------------------------------------
// Local SSH ed25519 key & SecretStore credentials

func (a *App) ensureLocalKey() {
	if a.cx != nil {
		if pub, ok := a.cx.Storage().GetItem(localPubKeyKey); ok && pub != "" {
			if _, okPriv, err := a.secrets.Get(localPrivKeyID); err == nil && okPriv {
				a.localPubKey.Set(pub)
				return
			}
		}
	}
	priv, pub, err := GenerateED25519Key()
	if err != nil {
		return
	}
	_ = a.secrets.Set(localPrivKeyID, priv)
	if a.cx != nil {
		a.cx.Storage().SetItem(localPubKeyKey, pub)
	}
	a.localPubKey.Set(pub)
}

// RegenerateLocalKey generates a fresh ed25519 keypair in SecretStore.
func (a *App) RegenerateLocalKey() error {
	priv, pub, err := GenerateED25519Key()
	if err != nil {
		return err
	}
	if err := a.secrets.Set(localPrivKeyID, priv); err != nil {
		return err
	}
	if a.cx != nil {
		a.cx.Storage().SetItem(localPubKeyKey, pub)
	}
	a.post(func() { a.localPubKey.Set(pub) })
	return nil
}

func secretPassAccount(id string) string { return "herdr-nsgo.host." + id + ".pass" }
func secretKeyAccount(id string) string  { return "herdr-nsgo.host." + id + ".key" }

func (a *App) resolveCredentials(h Host) Host {
	out := h
	id := h.EffectiveID()
	if out.Key == "" {
		if k, ok, err := a.secrets.Get(secretKeyAccount(id)); err == nil && ok && k != "" {
			out.Key = k
		}
	}
	if out.Key == "" && out.UseLocalKey {
		if k, ok, err := a.secrets.Get(localPrivKeyID); err == nil && ok && k != "" {
			out.Key = k
		}
	}
	if out.Password == "" {
		if p, ok, err := a.secrets.Get(secretPassAccount(id)); err == nil && ok && p != "" {
			out.Password = p
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Hosts (multi-instance)

func (a *App) loadHosts() []Host {
	if a.cx == nil {
		return nil
	}
	raw, ok := a.cx.Storage().GetItem(hostsKey)
	if !ok || raw == "" {
		// Migrate v0.1.0 hosts once: move plaintext secrets into SecretStore.
		raw, ok = a.cx.Storage().GetItem(legacyHostsKey)
		if !ok || raw == "" {
			return nil
		}
		var legacy []Host
		if json.Unmarshal([]byte(raw), &legacy) != nil {
			return nil
		}
		migratedOK := true
		for i := range legacy {
			if legacy[i].ID == "" {
				legacy[i].ID = legacy[i].EffectiveID()
			}
			if legacy[i].Password != "" {
				if err := a.secrets.Set(secretPassAccount(legacy[i].ID), legacy[i].Password); err != nil {
					migratedOK = false
				} else {
					legacy[i].Password = ""
				}
			}
			if legacy[i].Key != "" {
				if err := a.secrets.Set(secretKeyAccount(legacy[i].ID), legacy[i].Key); err != nil {
					migratedOK = false
				} else {
					legacy[i].Key = ""
				}
			}
		}
		a.persistHosts(legacy)
		if migratedOK {
			a.cx.Storage().RemoveItem(legacyHostsKey)
		}
		return legacy
	}
	var out []Host
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

func (a *App) persistHosts(hosts []Host) {
	if a.cx == nil {
		return
	}
	sanitized := make([]Host, len(hosts))
	for i, h := range hosts {
		h.Password = ""
		h.Key = ""
		sanitized[i] = h
	}
	if raw, err := json.Marshal(sanitized); err == nil {
		a.cx.Storage().SetItem(hostsKey, string(raw))
	}
}

// Hosts returns the current saved instance configurations (without secrets).
func (a *App) Hosts() []Host {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Host(nil), a.hosts...)
}

// SaveHost stores or updates an instance configuration, saving secrets in SecretStore.
func (a *App) SaveHost(h Host) {
	if h.ID == "" {
		h.ID = h.EffectiveID()
	}
	if h.Label == "" {
		h.Label = h.Target
	}
	if h.Password != "" {
		_ = a.secrets.Set(secretPassAccount(h.ID), h.Password)
	}
	if h.Key != "" {
		_ = a.secrets.Set(secretKeyAccount(h.ID), h.Key)
	}
	meta := h
	meta.Password = ""
	meta.Key = ""
	meta.CredRev = time.Now().UnixNano()

	a.mu.Lock()
	kept := make([]Host, 0, len(a.hosts)+1)
	replaced := false
	for _, old := range a.hosts {
		if old.EffectiveID() == meta.ID {
			kept = append(kept, meta)
			replaced = true
		} else {
			kept = append(kept, old)
		}
	}
	if !replaced {
		kept = append(kept, meta)
	}
	a.hosts = kept
	a.mu.Unlock()

	a.persistHosts(kept)
	a.pool.SetHosts(kept)
}

// RemoveHost removes an instance and deletes its stored credentials.
func (a *App) RemoveHost(id string) {
	_ = a.secrets.Delete(secretPassAccount(id))
	_ = a.secrets.Delete(secretKeyAccount(id))

	a.mu.Lock()
	kept := make([]Host, 0, len(a.hosts))
	for _, old := range a.hosts {
		if old.EffectiveID() != id {
			kept = append(kept, old)
		}
	}
	a.hosts = kept
	a.mu.Unlock()

	a.persistHosts(kept)
	a.pool.SetHosts(kept)
	a.syncPoolToSignals()
}

// ForgetHost drops all saved hosts (legacy helper).
func (a *App) ForgetHost() {
	for _, h := range a.Hosts() {
		a.RemoveHost(h.EffectiveID())
	}
}

// Host returns the first saved host (legacy helper).
func (a *App) Host() Host {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.hosts) == 0 {
		return Host{}
	}
	return a.hosts[0]
}

// TestConnection dials h once, verifies hello + TOFU (without mutating persistent TOFU store), and returns core info + latency.
func (a *App) TestConnection(ctx context.Context, h Host) (HelloResult, int64, error) {
	start := time.Now()
	resolved := a.resolveCredentials(h)
	rw, err := DialWithTOFU(resolved, a.tofu.CloneTemporary())
	if err != nil {
		return HelloResult{}, 0, err
	}
	c := NewClient(rw)
	defer c.Close()
	hello, err := c.HelloFramed(ctx)
	if err != nil {
		return HelloResult{}, 0, err
	}
	return hello, time.Since(start).Milliseconds(), nil
}

// Reconnect wakes all pool instances (or a specific one via ReconnectInstance).
func (a *App) Reconnect() { a.pool.WakeAll() }

// ReconnectInstance reconnects a specific instance ID.
func (a *App) ReconnectInstance(id string) { a.pool.ReconnectInstance(id) }

// ---------------------------------------------------------------------------
// Filtering & Attention

// FilteredTerminals returns aggregated terminals matching activeInstanceFilter and searchQuery.
func (a *App) FilteredTerminals() []AggregatedTerminal {
	inst := a.activeInstanceFilter.Get()
	q := strings.ToLower(strings.TrimSpace(a.searchQuery.Get()))
	all := a.aggTerms.Get()
	out := make([]AggregatedTerminal, 0, len(all))
	for _, item := range all {
		if inst != "" && item.Key.InstanceID != inst {
			continue
		}
		if q != "" {
			hay := strings.ToLower(item.Terminal.Label() + " " + item.Terminal.CurrentDir() + " " + item.InstanceName + " " + item.Key.Addr)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// AggregatedAttention returns all waiting (blocked/error) terminals matching inboxFilter.
func (a *App) AggregatedAttention() []AggregatedTerminal {
	q := strings.ToLower(strings.TrimSpace(a.inboxFilter.Get()))
	var out []AggregatedTerminal
	for _, item := range a.aggTerms.Get() {
		if !item.Terminal.NeedsYou() {
			continue
		}
		if q != "" {
			hay := strings.ToLower(item.Terminal.Label() + " " + item.Terminal.CurrentDir() + " " + item.InstanceName + " " + item.Key.Addr)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// Attention keeps v0.1.0 compatibility for screens.go until Task 6.
func (a *App) Attention() []Terminal {
	q := strings.ToLower(strings.TrimSpace(a.filter.Get()))
	var out []Terminal
	for _, t := range a.terms.Get() {
		if !t.NeedsYou() {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(t.Label()+" "+t.Cwd+" "+t.Address), q) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// SeedSnapshots sets both reactive signals and thread-safe cached snapshots (used in headless UI tests).
func (a *App) SeedSnapshots(snaps []InstanceSnapshot) {
	agg := AggregateTerminals(snaps)
	a.mu.Lock()
	a.cachedSnaps = snaps
	a.cachedAgg = agg
	a.mu.Unlock()
	a.instances.Set(snaps)
	a.aggTerms.Set(agg)
}

// FindTerminalSafe locates an AggregatedTerminal without touching NSGo reactive signals (safe on any goroutine).
func (a *App) FindTerminalSafe(key TermKey) (AggregatedTerminal, bool) {
	a.mu.Lock()
	agg := a.cachedAgg
	a.mu.Unlock()
	for _, item := range agg {
		if item.Key == key {
			return item, true
		}
	}
	for _, item := range a.pool.Terminals() {
		if item.Key == key {
			return item, true
		}
	}
	return AggregatedTerminal{Key: key, Terminal: Terminal{Address: key.Addr}}, false
}

// FindTerminal locates an AggregatedTerminal by TermKey (reactive on UI thread).
func (a *App) FindTerminal(key TermKey) (AggregatedTerminal, bool) {
	for _, item := range a.aggTerms.Get() {
		if item.Key == key {
			return item, true
		}
	}
	return AggregatedTerminal{Key: key, Terminal: Terminal{Address: key.Addr}}, false
}

// Terminal returns the first matching terminal by address (legacy helper).
func (a *App) Terminal(addr string) (Terminal, bool) {
	for _, t := range a.terms.Get() {
		if t.Address == addr {
			return t, true
		}
	}
	return Terminal{Address: addr}, false
}

// ---------------------------------------------------------------------------
// Actions (multi-instance aware)

func (a *App) snapshotsSafe() ([]InstanceSnapshot, []AggregatedTerminal) {
	a.mu.Lock()
	snaps := a.cachedSnaps
	agg := a.cachedAgg
	a.mu.Unlock()
	if len(snaps) == 0 && a.pool != nil {
		snaps = a.pool.Snapshots()
		agg = AggregateTerminals(snaps)
	}
	return snaps, agg
}

func (a *App) clientForKey(key TermKey) *Client {
	snaps, agg := a.snapshotsSafe()
	if key.InstanceID != "" {
		if c := a.pool.ClientFor(key.InstanceID); c != nil {
			return c
		}
		// Direct core offline: try falling back to an active online relay carrying this terminal.
		for _, it := range agg {
			if it.Relayed && strings.HasSuffix(it.Key.Addr, "/"+key.Addr) {
				if c := a.pool.ClientFor(it.Key.InstanceID); c != nil {
					return c
				}
			}
		}
	}
	for _, s := range snaps {
		if s.Online {
			return a.pool.ClientFor(s.InstanceID)
		}
	}
	return nil
}

// SendTo sends text + Enter to the terminal identified by key.
func (a *App) SendTo(key TermKey, text string) {
	a.SendToWithCallback(key, text, func(err error) {
		if err == nil {
			a.post(func() {
				if strings.TrimSpace(a.Compose(key.String())) == strings.TrimSpace(text) {
					a.setCompose(key.String(), "")
				}
			})
		}
	})
}

// SendToWithCallback sends text + Enter via terminal.send and invokes onDone (if non-nil) with any error.
func (a *App) SendToWithCallback(key TermKey, text string, onDone func(error)) {
	if strings.TrimSpace(text) == "" {
		return
	}
	c := a.clientForKey(key)
	if c == nil {
		err := errors.New("该实例尚未连上，先检查实例连接。")
		a.notifyToast(err.Error())
		if onDone != nil {
			onDone(err)
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := c.Send(ctx, key.Addr, text)
		if err != nil {
			a.notifyToast(explain(err))
		}
		if onDone != nil {
			onDone(err)
		}
	}()
}

// PressTo sends logical keys to key.
func (a *App) PressTo(key TermKey, keys ...string) {
	c := a.clientForKey(key)
	if c == nil {
		a.notifyToast("该实例尚未连上。")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := c.Keys(ctx, key.Addr, keys...); err != nil {
			a.notifyToast(explain(err))
		}
	}()
}

// InlineApprove executes a verified whitelisted Approve/Deny key sequence after
// re-checking that the terminal is still in blocked+permission state.
func (a *App) InlineApprove(key TermKey, allow bool) {
	cur, ok := a.FindTerminal(key)
	if !ok {
		a.notifyToast("该终端已不在列表中。")
		return
	}
	keys, allowed := ApproveKeys(cur.Terminal, allow)
	if !allowed {
		a.notifyToast("该程序不在直操白名单中，请进入终端处理。")
		return
	}
	c := a.clientForKey(key)
	if c == nil {
		a.notifyToast("该实例已断连。")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		// Re-verify live state before sending keys to guard against race conditions.
		latest, err := c.Get(ctx, key.Addr)
		if err != nil {
			a.notifyToast(explain(err))
			return
		}
		if !CanInlineApprove(latest) {
			a.notifyToast("状态已变化，已取消直操，请进终端确认。")
			return
		}
		if _, err := c.Keys(ctx, key.Addr, keys...); err != nil {
			a.notifyToast(explain(err))
			return
		}
		if allow {
			a.notifyToast("已发送允许")
		} else {
			a.notifyToast("已发送拒绝")
		}
	}()
}

// InterruptTo stops the current turn on key.
func (a *App) InterruptTo(key TermKey) {
	c := a.clientForKey(key)
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := c.Interrupt(ctx, key.Addr); err != nil {
			a.notifyToast(explain(err))
		}
	}()
}

// KillTerminal terminates a live terminal.
func (a *App) KillTerminal(key TermKey) {
	c := a.clientForKey(key)
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if _, err := c.Kill(ctx, key.Addr); err != nil {
			a.notifyToast(explain(err))
			return
		}
		a.notifyToast("已终止终端")
	}()
}

// RemoveTerminal cleans up a finished terminal.
func (a *App) RemoveTerminal(key TermKey) {
	c := a.clientForKey(key)
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := c.Rm(ctx, key.Addr); err != nil {
			a.notifyToast(explain(err))
			return
		}
		a.notifyToast("已清理终端")
	}()
}

// CreateTerminal runs a template or shell on instanceID (optionally on a remote machine via p.On).
func (a *App) CreateTerminal(ctx context.Context, instanceID string, p RunParams) (TermKey, error) {
	c := a.pool.ClientFor(instanceID)
	if c == nil {
		return TermKey{}, errors.New("目标实例未在线")
	}
	res, err := c.Run(ctx, p)
	if err != nil {
		return TermKey{}, err
	}
	return TermKey{InstanceID: instanceID, Addr: res.Address}, nil
}

// UploadFileTo uploads data beside key via chunked terminal.put and appends its remote path to the composer.
func (a *App) UploadFileTo(key TermKey, name string, data []byte) {
	c := a.clientForKey(key)
	if c == nil {
		a.notifyToast("实例未连上，无法上传文件。")
		return
	}
	a.notifyToast("正在上传 " + name + "…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := c.PutFile(ctx, key.Addr, name, data)
		if err != nil {
			a.notifyToast(explain(err))
			return
		}
		a.post(func() {
			prev := a.Compose(key.String())
			if prev != "" && !strings.HasSuffix(prev, " ") {
				prev += " "
			}
			a.setCompose(key.String(), prev+res.Path)
			a.toastMsg.Set("已上传：" + res.Path)
		})
	}()
}

// Legacy single-address helpers retained for screens.go compatibility until Task 6.
func (a *App) Watch(addr string)                      {}
func (a *App) Unwatch(addr string)                    {}
func (a *App) Send(addr, text string)                 { a.SendTo(TermKey{Addr: addr}, text) }
func (a *App) Press(addr string, keys ...string)      { a.PressTo(TermKey{Addr: addr}, keys...) }
func (a *App) Interrupt(addr string)                  { a.InterruptTo(TermKey{Addr: addr}) }
func (a *App) Text(addr string) string                { return a.texts.Get()[addr] }
func (a *App) Compose(key string) string              { return a.composes.Get()[key] }
func (a *App) SetCompose(key, text string)            { a.setCompose(key, text) }

func (a *App) setCompose(key, text string) {
	next := make(map[string]string, len(a.composes.Get())+1)
	for k, v := range a.composes.Get() {
		next[k] = v
	}
	next[key] = text
	a.composes.Set(next)
}

func (a *App) notifyToast(msg string) {
	a.post(func() { a.toastMsg.Set(msg) })
}

// ---------------------------------------------------------------------------
// Preferences & Snippets persistence

func loadPrefs(cx *core.Scope) AppPrefs {
	p := defaultPrefs()
	if cx == nil {
		return p
	}
	if raw, ok := cx.Storage().GetItem(prefsKey); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	if p.VTFontSize < 10 || p.VTFontSize > 15 {
		p.VTFontSize = 12
	}
	if p.ThemeMode == "" {
		p.ThemeMode = ThemeLight
	}
	if p.VTThemeMode == "" {
		p.VTThemeMode = VTFollowTheme
	}
	return p
}

func (a *App) savePrefs() {
	if a.cx == nil {
		return
	}
	p := AppPrefs{
		ThemeMode:      a.themeMode.Get(),
		VTThemeMode:    a.vtThemeMode.Get(),
		VTFontSize:     a.vtFontSize.Get(),
		ObserveDefault: a.observeDefault.Get(),
		FrameCacheOn:   a.frameCacheOn.Get(),
		NotifyAlerts:   a.notifyAlerts.Get(),
	}
	if raw, err := json.Marshal(p); err == nil {
		a.cx.Storage().SetItem(prefsKey, string(raw))
	}
}

func (a *App) SetThemeMode(m ThemeMode) {
	a.themeMode.Set(m)
	ApplyAppearance(a.cx, m)
	a.savePrefs()
}

func (a *App) SetVTThemeMode(m VTThemeMode) {
	a.vtThemeMode.Set(m)
	a.savePrefs()
}

func (a *App) SetVTFontSize(sz float32) {
	if sz < 10 {
		sz = 10
	}
	if sz > 15 {
		sz = 15
	}
	a.vtFontSize.Set(sz)
	a.savePrefs()
}

func (a *App) SetObserveDefault(v bool) {
	a.observeDefault.Set(v)
	a.savePrefs()
}

func (a *App) SetFrameCacheOn(v bool) {
	a.frameCacheOn.Set(v)
	a.savePrefs()
}

func (a *App) SetNotifyAlerts(v bool) {
	a.notifyAlerts.Set(v)
	a.savePrefs()
}

func loadSnippets(cx *core.Scope) []Snippet {
	if cx != nil {
		if raw, ok := cx.Storage().GetItem(snippetsKey); ok && raw != "" {
			var out []Snippet
			if json.Unmarshal([]byte(raw), &out) == nil && len(out) > 0 {
				return out
			}
		}
	}
	return defaultSnippets()
}

// SaveSnippet adds or updates a snippet and persists it.
func (a *App) SaveSnippet(sn Snippet) {
	if strings.TrimSpace(sn.Text) == "" {
		return
	}
	if sn.ID == "" {
		sn.ID = fmt.Sprintf("sn-%d", time.Now().UnixNano())
	}
	if sn.Label == "" {
		sn.Label = sn.Text
	}
	cur := a.snippets.Get()
	next := make([]Snippet, 0, len(cur)+1)
	replaced := false
	for _, old := range cur {
		if old.ID == sn.ID {
			next = append(next, sn)
			replaced = true
		} else {
			next = append(next, old)
		}
	}
	if !replaced {
		next = append(next, sn)
	}
	a.snippets.Set(next)
	if a.cx != nil {
		if raw, err := json.Marshal(next); err == nil {
			a.cx.Storage().SetItem(snippetsKey, string(raw))
		}
	}
}

// DeleteSnippet removes a snippet by ID.
func (a *App) DeleteSnippet(id string) {
	cur := a.snippets.Get()
	next := make([]Snippet, 0, len(cur))
	for _, old := range cur {
		if old.ID != id {
			next = append(next, old)
		}
	}
	a.snippets.Set(next)
	if a.cx != nil {
		if raw, err := json.Marshal(next); err == nil {
			a.cx.Storage().SetItem(snippetsKey, string(raw))
		}
	}
}

// ---------------------------------------------------------------------------
// problems

// explain turns a failure into the line the UI shows: what happened, then
// what to do about it. Never just "error".
func explain(err error) string {
	var re *RPCError
	if errors.As(err, &re) {
		return re.Remediation()
	}
	var mismatch *HostKeyMismatchError
	if errors.As(err, &mismatch) {
		return mismatch.Error()
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"):
		return "域名解析失败。检查机器地址和 Tailscale/网络后重连。"
	case strings.Contains(msg, "connection refused"):
		return "对方拒绝了连接。确认 SSH 在跑、端口对（默认 22）后重连。"
	case strings.Contains(msg, "unable to authenticate"):
		return "SSH 认证失败。检查用户名、本机公钥是否已加入远端 authorized_keys，或改用密码。"
	case strings.Contains(msg, "missing port"):
		return "地址要写「主机」或「主机:端口」。"
	case strings.Contains(msg, "hc is not on"):
		return "本机没有 hc。桌面端要先装 herdr-core；手机端请用 SSH 连一台装了它的机器。"
	case strings.Contains(msg, "hc connect"):
		return "SSH 通了，但远端跑不起 `hc connect`：确认远端装了 herdr-core，且在登录 PATH 里。"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "连接超时。检查 Tailscale/网络或远端负载后重连。"
	}
	return fmt.Sprintf("%s — 点重连；仍失败就检查远端 hc server 状态。", msg)
}
