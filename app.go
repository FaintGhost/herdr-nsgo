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
	localPrivKeyID    = "herdr-nsgo.local.ed25519.priv"
	localPubKeyKey    = "herdr-nsgo.local.ed25519.pub"
	doneAckStorageKey = "herdr-nsgo.done.ack.v1"
)

// Snippet is a reusable quick-send command or prompt template.
type Snippet struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Binding bool   `json:"binding,omitzero"`
	Text    string `json:"text"`
}

// AppPrefs persists user appearance, font, and terminal behavior settings.
type AppPrefs struct {
	ThemeMode       ThemeMode      `json:"theme_mode"`
	ThemePreset     ThemePreset    `json:"theme_preset,omitempty"`
	VTThemeMode     VTThemeMode    `json:"vt_theme_mode"`
	VTColorScheme   VTColorScheme  `json:"vt_color_scheme,omitempty"`
	VTFontSize      float32        `json:"vt_font_size"`
	BodyFont        BodyFontPreset `json:"body_font,omitempty"`
	MonoFont        MonoFontPreset `json:"mono_font,omitempty"`
	BodyFontSize    float32        `json:"body_font_size,omitempty"`
	ObserveDefault     bool            `json:"observe_default"`
	FrameCacheOn       bool            `json:"frame_cache_on"`
	NotifyAlerts       bool            `json:"notify_alerts"`
	DefaultSendMode    chatSubmitMode  `json:"default_send_mode,omitempty"`
	SessionSort        SessionSortMode `json:"session_sort,omitempty"`
	ManualSessionOrder []string        `json:"manual_session_order,omitempty"`
}

func defaultPrefs() AppPrefs {
	return AppPrefs{
		ThemeMode:       ThemeLight,
		ThemePreset:     PresetStudio,
		VTThemeMode:     VTFollowTheme,
		VTColorScheme:   VTSchemeGraphite,
		VTFontSize:      12,
		BodyFont:        BodyFontSans,
		MonoFont:        MonoFontJetBrains,
		BodyFontSize:    15,
		ObserveDefault:  true,
		FrameCacheOn:    true,
		NotifyAlerts:    true,
		DefaultSendMode: chatQueue,
		SessionSort:     SortActiveNewest,
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

type toastKind uint8

const (
	toastInfo toastKind = iota
	toastSuccess
	toastWarn
	toastError
)

type toastNotice struct {
	Msg  string
	Kind toastKind
	Seq  uint64
}

func classifyToastKind(msg string) toastKind {
	s := strings.TrimSpace(msg)
	low := strings.ToLower(s)
	for _, kw := range []string{"失败", "错误", "无法", "超过", "尚未连上", "已断连", "不在", "未连接", "尚未接入", "error", "failed", "timeout", "refused", "denied"} {
		if strings.Contains(low, kw) {
			return toastError
		}
	}
	for _, kw := range []string{"已发送拒绝", "状态已变化", "离线", "请先", "剪贴板中没有", "未连上"} {
		if strings.Contains(low, kw) {
			return toastWarn
		}
	}
	if strings.HasPrefix(s, "已") {
		return toastSuccess
	}
	return toastInfo
}

func toastDurationFor(kind toastKind, msg string) time.Duration {
	switch kind {
	case toastError:
		return 4000 * time.Millisecond
	case toastWarn:
		return 2800 * time.Millisecond
	case toastSuccess:
		return 2000 * time.Millisecond
	default:
		if strings.HasPrefix(strings.TrimSpace(msg), "正在") {
			return 5000 * time.Millisecond
		}
		return 2400 * time.Millisecond
	}
}

// App owns the multi-instance CorePool, secrets, TOFU store, preferences, LRU frame cache,
// snippets, and reactive NSGo signals read by all 8 screens.
type App struct {
	cx        *core.Scope
	secrets   SecretBackend
	tofu      *TOFUStore
	pool      *CorePool
	cache     *FrameCache
	snapshots *SnapshotStore

	// Primary v1.0 multi-instance signals
	instances            *core.Signal[[]InstanceSnapshot]
	aggTerms             *core.Signal[[]AggregatedTerminal]
	activeInstanceFilter *core.Signal[string] // "" = all
	searchQuery          *core.Signal[string]
	inboxFilter          *core.Signal[string]
	themeMode            *core.Signal[ThemeMode]
	themePreset          *core.Signal[ThemePreset]
	vtThemeMode          *core.Signal[VTThemeMode]
	vtColorScheme        *core.Signal[VTColorScheme]
	vtFontSize           *core.Signal[float32]
	bodyFont             *core.Signal[BodyFontPreset]
	monoFont             *core.Signal[MonoFontPreset]
	bodyFontSize         *core.Signal[float32]
	observeDefault       *core.Signal[bool]
	frameCacheOn         *core.Signal[bool]
	notifyAlerts         *core.Signal[bool]
	notifPerm            *core.Signal[core.NotificationPermission]
	defaultSendMode      *core.Signal[chatSubmitMode]
	sessionSort          *core.Signal[SessionSortMode]
	manualSessionOrder   *core.Signal[[]string]
	snippets             *core.Signal[[]Snippet]
	tofuRecords          *core.Signal[[]TOFURecord]
	localPubKey          *core.Signal[string]
	toastMsg             *core.Signal[string]
	toast                *core.Signal[toastNotice]
	resuming             *core.Signal[bool]
	showingHomeCache     *core.Signal[bool]
	sessionModels        *core.Signal[map[TermKey]string]
	sessionEfforts       *core.Signal[map[TermKey]string]
	catalogRev           *core.Signal[uint64]
	completedReports     *core.Signal[map[TermKey]bool]
	ackDone              *core.Signal[map[string]string]

	// Compatibility signals for legacy tests / v0.1.0 screens until Task 6 removes screens.go
	conn       *core.Signal[string]
	problem    *core.Signal[string]
	coreName   *core.Signal[string]
	terms      *core.Signal[[]Terminal]
	machines   *core.Signal[[]MachineView]
	texts      *core.Signal[map[string]string]
	composes   *core.Signal[map[string]string]
	chatImages *core.Signal[map[TermKey]chatImageDraft]
	filter     *core.Signal[string]

	mu                 sync.Mutex
	hosts              []Host
	cachedSnaps        []InstanceSnapshot
	cachedAgg          []AggregatedTerminal
	lastHomeSave          time.Time
	resumeGen             uint64
	toastSeq              uint64
	toastTimer            *time.Timer
	toastDurationOverride time.Duration
	notifPrompted         bool
	catalogFetchInFly     map[string]bool
	catalogFetchedAt   map[string]time.Time
	prevTermStates     map[TermKey]string
	prevTermSigs       map[TermKey]string
	lastActiveMS       map[TermKey]uint64
	activeSessionKey   TermKey
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
		snapshots:            NewSnapshotStore(cx),
		alerts:               newAlertTracker(),
		transcriptResolver:   NewTranscriptResolver(),
		instances:            core.NewSignalFunc(cx, []InstanceSnapshot(nil), nil),
		aggTerms:             core.NewSignalFunc(cx, []AggregatedTerminal(nil), nil),
		activeInstanceFilter: core.NewSignal(cx, ""),
		searchQuery:          core.NewSignal(cx, ""),
		inboxFilter:          core.NewSignal(cx, ""),
		themeMode:            core.NewSignal(cx, prefs.ThemeMode),
		themePreset:          core.NewSignal(cx, prefs.ThemePreset),
		vtThemeMode:          core.NewSignal(cx, prefs.VTThemeMode),
		vtColorScheme:        core.NewSignal(cx, prefs.VTColorScheme),
		vtFontSize:           core.NewSignal(cx, prefs.VTFontSize),
		bodyFont:             core.NewSignal(cx, prefs.BodyFont),
		monoFont:             core.NewSignal(cx, prefs.MonoFont),
		bodyFontSize:         core.NewSignal(cx, prefs.BodyFontSize),
		observeDefault:       core.NewSignal(cx, prefs.ObserveDefault),
		frameCacheOn:         core.NewSignal(cx, prefs.FrameCacheOn),
		notifyAlerts:         core.NewSignal(cx, prefs.NotifyAlerts),
		notifPerm:            core.NewSignal(cx, core.NotificationsNotAsked),
		defaultSendMode:      core.NewSignal(cx, prefs.DefaultSendMode),
		sessionSort:          core.NewSignal(cx, prefs.SessionSort),
		manualSessionOrder:   core.NewSignalFunc(cx, append([]string(nil), prefs.ManualSessionOrder...), nil),
		snippets:             core.NewSignalFunc(cx, loadSnippets(cx), nil),
		tofuRecords:          core.NewSignalFunc(cx, []TOFURecord(nil), nil),
		localPubKey:          core.NewSignal(cx, ""),
		toastMsg:             core.NewSignal(cx, ""),
		toast:                core.NewSignal(cx, toastNotice{}),
		resuming:             core.NewSignal(cx, false),
		showingHomeCache:     core.NewSignal(cx, false),
		sessionModels:        core.NewSignalFunc(cx, map[TermKey]string{}, nil),
		sessionEfforts:       core.NewSignalFunc(cx, map[TermKey]string{}, nil),
		catalogRev:           core.NewSignal(cx, uint64(0)),
		completedReports:     core.NewSignalFunc(cx, map[TermKey]bool{}, nil),
		ackDone:              core.NewSignalFunc(cx, loadDoneAcks(cx), nil),
		catalogFetchInFly:    make(map[string]bool),
		catalogFetchedAt:     make(map[string]time.Time),
		prevTermStates:       make(map[TermKey]string),

		conn:       core.NewSignal(cx, stateOff),
		problem:    core.NewSignal(cx, ""),
		coreName:   core.NewSignal(cx, ""),
		terms:      core.NewSignalFunc(cx, []Terminal(nil), nil),
		machines:   core.NewSignalFunc(cx, []MachineView(nil), nil),
		texts:      core.NewSignalFunc(cx, map[string]string{}, nil),
		composes:   core.NewSignalFunc(cx, map[string]string{}, nil),
		chatImages: core.NewSignalFunc(cx, map[TermKey]chatImageDraft{}, nil),
		filter:     core.NewSignal(cx, ""),
	}

	if cx != nil {
		cx.SetTheme(AppThemeFor(prefs.ThemePreset))
		ApplyAppearance(cx, prefs.ThemeMode)
	}
	a.transcriptResolver.SetSnapshotStore(a.snapshots)

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
		cx.Cleanup(func() {
			a.mu.Lock()
			if a.toastTimer != nil {
				a.toastTimer.Stop()
				a.toastTimer = nil
			}
			a.mu.Unlock()
			a.pool.Close()
		})
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
		if !cx.Env().Simulated() {
			a.RefreshNotificationPermission()
			if prefs.NotifyAlerts {
				time.AfterFunc(600*time.Millisecond, func() {
					a.RequestNotificationPermissionIfNeeded(false)
				})
			}
		}
	}

	hosts := a.loadHosts()
	a.hosts = hosts
	if len(hosts) > 0 {
		// 0ms cold-start Frame-0 restore: show last known sessions (marked connecting) immediately while SSH dials.
		if cached, ok := a.snapshots.LoadHome(); ok && len(cached) > 0 {
			agg := AggregateTerminals(cached)
			a.cachedSnaps = cached
			a.cachedAgg = agg
			a.instances.Set(cached)
			a.aggTerms.Set(agg)
			a.conn.Set(stateDialing)
			a.showingHomeCache.Set(true)
		} else {
			// Seed dialing placeholders immediately so the UI shows connecting skeleton instead of an empty flash.
			dialing := make([]InstanceSnapshot, 0, len(hosts))
			for _, h := range hosts {
				if !h.IsEnabled() {
					continue
				}
				name := h.Label
				if name == "" {
					name = h.Target
				}
				dialing = append(dialing, InstanceSnapshot{
					InstanceID:   h.EffectiveID(),
					InstanceName: name,
					Target:       h.Target,
					User:         h.User,
					State:        stateDialing,
					Online:       false,
				})
			}
			a.cachedSnaps = dialing
			a.instances.Set(dialing)
			a.conn.Set(stateDialing)
		}
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
		prevOnline := 0
		for _, old := range a.cachedSnaps {
			if old.Online {
				prevOnline++
			}
		}
		prevTermCount := len(a.cachedAgg)
		nowOnline := 0
		anyDialing := false
		for _, s := range snaps {
			if s.Online {
				nowOnline++
			} else if s.State == stateDialing {
				anyDialing = true
			}
		}
		// Keep cached home sessions visible on cold start until at least one instance comes online or stops dialing.
		useCachedHome := a.showingHomeCache.Get() && nowOnline == 0 && anyDialing && len(agg) == 0 && len(a.cachedAgg) > 0
		if useCachedHome {
			// Merge live dialing/error states into cached instances without wiping their cached terminals yet.
			byID := make(map[string]InstanceSnapshot, len(snaps))
			for _, s := range snaps {
				byID[s.InstanceID] = s
			}
			merged := make([]InstanceSnapshot, 0, len(a.cachedSnaps))
			for _, old := range a.cachedSnaps {
				if live, ok := byID[old.InstanceID]; ok {
					cp := live
					if len(cp.Terminals) == 0 {
						cp.Terminals = old.Terminals
					}
					merged = append(merged, cp)
				}
			}
			if len(merged) > 0 {
				snaps = merged
				agg = AggregateTerminals(merged)
			}
		} else {
			if nowOnline > 0 || !anyDialing {
				a.showingHomeCache.Set(false)
			}
			a.cachedSnaps = snaps
			a.cachedAgg = agg
		}
		shouldPersistHome := nowOnline > 0 && time.Since(a.lastHomeSave) >= 4*time.Second
		if shouldPersistHome {
			a.lastHomeSave = time.Now()
		}
		a.mu.Unlock()

		// Smooth spring layout transition whenever transitioning from skeleton/offline into live populated sessions.
		if (prevOnline == 0 && nowOnline > 0) || (prevTermCount == 0 && len(agg) > 0) {
			animateSpringLayout(a.cx)
		}

		a.instances.Set(snaps)
		a.aggTerms.Set(agg)
		a.trackCompletionTransitions(agg)
		a.terms.Set(flatTerms)
		a.machines.Set(flatMachines)
		a.conn.Set(connState)
		a.problem.Set(problem)
		a.coreName.Set(coreName)
		if nowOnline > 0 && !anyDialing && a.resuming.Get() {
			animateSpringLayout(a.cx)
			a.resuming.Set(false)
		}
		if shouldPersistHome && a.snapshots != nil {
			a.snapshots.SaveHome(snaps)
		}

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

func (a *App) RefreshNotificationPermission() {
	if a.cx == nil || a.cx.Env().Simulated() {
		return
	}
	a.post(func() {
		a.cx.NotificationPermission(func(p core.NotificationPermission) {
			a.post(func() { a.notifPerm.Set(p) })
		})
	})
}

func (a *App) RequestNotificationPermissionIfNeeded(force bool) {
	if a.cx == nil || a.cx.Env().Simulated() {
		return
	}
	a.post(func() {
		a.cx.NotificationPermission(func(p core.NotificationPermission) {
			a.post(func() {
				a.notifPerm.Set(p)
				switch p {
				case core.NotificationsGranted, core.NotificationsUnavailable:
					return
				case core.NotificationsNotAsked:
					a.mu.Lock()
					if !force && (a.notifPrompted || !a.notifyAlerts.GetUntracked()) {
						a.mu.Unlock()
						return
					}
					a.notifPrompted = true
					a.mu.Unlock()
					a.cx.RequestNotificationPermission(func(next core.NotificationPermission) {
						a.post(func() {
							a.notifPerm.Set(next)
							if next == core.NotificationsGranted && force {
								a.notifyToast("已开启系统通知权限")
							}
						})
					})
				case core.NotificationsDenied:
					if force {
						a.cx.OpenPermissionSettings(core.PermissionNotifications, nil)
						a.notifyToast("请在系统设置中开启通知权限")
					}
				}
			})
		})
	})
}

func (a *App) dispatchAlertNotification(at AggregatedTerminal) {
	if a.cx == nil || !a.notifyAlerts.Get() {
		return
	}
	// Do not push a completion notification for the session the user is actively viewing right now.
	a.mu.Lock()
	activeKey := a.activeSessionKey
	a.mu.Unlock()
	if !at.Terminal.NeedsYou() && activeKey == at.Key {
		return
	}
	reason := attentionWhy(at.Terminal)
	if !at.Terminal.NeedsYou() {
		reason = "已完成"
	}
	title := fmt.Sprintf("%s · %s", at.Terminal.Label(), reason)
	body := firstLine(at.Terminal.Status, at.Terminal)
	if strings.TrimSpace(body) == "" {
		body = "点按查看最新结果"
	}
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

// HasOfflineEnabledInstance reports whether any enabled host is currently disconnected or dialing.
func (a *App) HasOfflineEnabledInstance() bool {
	snaps := a.instances.GetUntracked()
	if len(snaps) == 0 {
		return len(a.Hosts()) > 0
	}
	for _, s := range snaps {
		if !s.Online {
			return true
		}
	}
	return false
}

// OnForegroundResume decides whether returning to active focus needs a visible reconnect sync.
// Brief focus drops (e.g. pulling down Android's notification shade) while all SSH instances are
// still online must NOT flash the "正在同步" banner.
func (a *App) OnForegroundResume(wasBackground bool, away time.Duration) bool {
	if len(a.Hosts()) == 0 {
		return false
	}
	needsSync := a.HasOfflineEnabledInstance() ||
		(wasBackground && away >= 8*time.Second) ||
		away >= 20*time.Second
	if !needsSync {
		return false
	}
	a.BeginResumeSync()
	return true
}

// BeginResumeSync marks a foreground-resume / manual-refresh sync cycle with immediate visual feedback
// and a bounded safety timeout so the syncing indicator never gets stuck if an instance is unreachable.
func (a *App) BeginResumeSync() {
	if len(a.Hosts()) == 0 {
		return
	}
	a.mu.Lock()
	a.resumeGen++
	gen := a.resumeGen
	a.mu.Unlock()
	a.post(func() {
		animateSpringLayout(a.cx)
		a.resuming.Set(true)
	})
	a.pool.WakeAll()
	time.AfterFunc(4500*time.Millisecond, func() {
		a.mu.Lock()
		same := a.resumeGen == gen
		a.mu.Unlock()
		if same {
			a.post(func() {
				if a.resuming.Get() {
					animateSpringLayout(a.cx)
					a.resuming.Set(false)
				}
			})
		}
	})
}

// SyncStatus returns (active, label) describing any in-progress cold-start dial or background-resume sync.
func (a *App) SyncStatus() (bool, string) {
	snaps := a.instances.Get()
	var dialing []string
	online := 0
	for _, s := range snaps {
		if s.Online {
			online++
		} else if s.State == stateDialing {
			dialing = append(dialing, s.InstanceName)
		}
	}
	if len(dialing) > 0 {
		if len(dialing) == 1 {
			return true, "正在连接 " + dialing[0] + "…"
		}
		return true, fmt.Sprintf("正在连接 %d 台开发机…", len(dialing))
	}
	if a.resuming.Get() {
		if online > 0 {
			return true, "正在同步最新会话…"
		}
		return true, "正在恢复连接…"
	}
	return false, ""
}

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

// AggregatedCompleted returns unread Agent completion reports matching inboxFilter
// (both explicit "done" status / finished agent sessions and live working -> idle/done transitions).
func (a *App) AggregatedCompleted() []AggregatedTerminal {
	q := strings.ToLower(strings.TrimSpace(a.inboxFilter.Get()))
	liveDone := a.completedReports.Get()
	acks := a.ackDone.Get()
	var out []AggregatedTerminal
	for _, item := range a.aggTerms.Get() {
		if !a.isUnreadCompletion(item, liveDone, acks) {
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

// InboxBadgeCount returns unfiltered counts of urgent (NeedsYou) items and unread completion reports.
func (a *App) InboxBadgeCount() (urgent int, done int) {
	liveDone := a.completedReports.Get()
	acks := a.ackDone.Get()
	for _, item := range a.aggTerms.Get() {
		if item.Terminal.NeedsYou() {
			urgent++
		} else if a.isUnreadCompletion(item, liveDone, acks) {
			done++
		}
	}
	return urgent, done
}

func (a *App) isUnreadCompletion(item AggregatedTerminal, liveDone map[TermKey]bool, acks map[string]string) bool {
	if item.Terminal.NeedsYou() {
		return false
	}
	if !isExplicitCompletionTerminal(item.Terminal) && !liveDone[item.Key] {
		return false
	}
	if acks != nil && acks[item.Key.String()] == completionSignature(item.Terminal) {
		return false
	}
	return true
}

func (a *App) trackCompletionTransitions(agg []AggregatedTerminal) {
	a.mu.Lock()
	activeKey := a.activeSessionKey
	if a.prevTermStates == nil {
		a.prevTermStates = make(map[TermKey]string)
	}
	if a.prevTermSigs == nil {
		a.prevTermSigs = make(map[TermKey]string)
	}
	if a.lastActiveMS == nil {
		a.lastActiveMS = make(map[TermKey]uint64)
	}
	nowMS := uint64(time.Now().UnixMilli())
	var newlyDone []TermKey
	var resumedWorking []TermKey
	seen := make(map[TermKey]bool, len(agg))
	for _, item := range agg {
		seen[item.Key] = true
		cur := item.Terminal.StateWord()
		prev := a.prevTermStates[item.Key]
		if base := item.Terminal.ActiveMS(); base > a.lastActiveMS[item.Key] {
			a.lastActiveMS[item.Key] = base
		}
		sig := fmt.Sprintf("%s|%d|%s|%s", cur, item.Terminal.Turn, item.Terminal.Title, firstLine(item.Terminal.Status, item.Terminal))
		if oldSig, had := a.prevTermSigs[item.Key]; had && oldSig != sig {
			if nowMS > a.lastActiveMS[item.Key] {
				a.lastActiveMS[item.Key] = nowMS
			}
		}
		a.prevTermSigs[item.Key] = sig
		if cur == "working" {
			resumedWorking = append(resumedWorking, item.Key)
		} else if prev == "working" && (cur == "idle" || cur == "done") && item.Key != activeKey {
			newlyDone = append(newlyDone, item.Key)
		}
		a.prevTermStates[item.Key] = cur
	}
	for k := range a.prevTermStates {
		if !seen[k] {
			delete(a.prevTermStates, k)
			delete(a.prevTermSigs, k)
		}
	}
	a.mu.Unlock()

	if len(newlyDone) == 0 && len(resumedWorking) == 0 {
		return
	}
	curLive := a.completedReports.GetUntracked()
	changed := false
	next := make(map[TermKey]bool, len(curLive)+len(newlyDone))
	for k, v := range curLive {
		if seen[k] && v {
			next[k] = true
		} else {
			changed = true
		}
	}
	for _, k := range resumedWorking {
		if next[k] {
			delete(next, k)
			changed = true
		}
	}
	for _, k := range newlyDone {
		if !next[k] {
			next[k] = true
			changed = true
		}
	}
	if changed {
		a.completedReports.Set(next)
	}
}

// SetActiveSession records the currently viewed session and marks its completion report read.
func (a *App) SetActiveSession(key TermKey) {
	a.mu.Lock()
	a.activeSessionKey = key
	a.mu.Unlock()
	if key != (TermKey{}) {
		a.AcknowledgeCompleted(key)
	}
}

// AcknowledgeCompleted marks a completed session report as read so it clears from Inbox.
func (a *App) AcknowledgeCompleted(key TermKey) {
	if key == (TermKey{}) {
		return
	}
	live := a.completedReports.GetUntracked()
	if live[key] {
		nextLive := make(map[TermKey]bool, len(live))
		for k, v := range live {
			if k != key && v {
				nextLive[k] = true
			}
		}
		a.completedReports.Set(nextLive)
	}
	if at, ok := a.FindTerminal(key); ok && !at.Terminal.NeedsYou() {
		sig := completionSignature(at.Terminal)
		acks := a.ackDone.GetUntracked()
		if acks[key.String()] != sig {
			nextAcks := make(map[string]string, len(acks)+1)
			for k, v := range acks {
				nextAcks[k] = v
			}
			nextAcks[key.String()] = sig
			a.ackDone.Set(nextAcks)
			saveDoneAcks(a.cx, nextAcks)
		}
	}
}

// AcknowledgeAllCompleted marks all currently unread completion reports as read.
func (a *App) AcknowledgeAllCompleted() {
	done := a.AggregatedCompleted()
	if len(done) == 0 {
		return
	}
	a.completedReports.Set(map[TermKey]bool{})
	acks := a.ackDone.GetUntracked()
	nextAcks := make(map[string]string, len(acks)+len(done))
	for k, v := range acks {
		nextAcks[k] = v
	}
	for _, it := range done {
		nextAcks[it.Key.String()] = completionSignature(it.Terminal)
	}
	a.ackDone.Set(nextAcks)
	saveDoneAcks(a.cx, nextAcks)
}

func loadDoneAcks(cx *core.Scope) map[string]string {
	out := make(map[string]string)
	if cx == nil {
		return out
	}
	if raw, ok := cx.Storage().GetItem(doneAckStorageKey); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func saveDoneAcks(cx *core.Scope, acks map[string]string) {
	if cx == nil {
		return
	}
	if len(acks) > 100 {
		trimmed := make(map[string]string, 100)
		for k, v := range acks {
			trimmed[k] = v
			if len(trimmed) >= 100 {
				break
			}
		}
		acks = trimmed
	}
	if raw, err := json.Marshal(acks); err == nil {
		cx.Storage().SetItem(doneAckStorageKey, string(raw))
	}
}

// SeedSnapshots sets both reactive signals and thread-safe cached snapshots (used in headless UI tests).
func (a *App) SeedSnapshots(snaps []InstanceSnapshot) {
	agg := AggregateTerminals(snaps)
	a.mu.Lock()
	a.cachedSnaps = snaps
	a.cachedAgg = agg
	a.mu.Unlock()
	a.showingHomeCache.Set(false)
	a.resuming.Set(false)
	a.instances.Set(snaps)
	a.aggTerms.Set(agg)
	a.trackCompletionTransitions(agg)
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
	a.sendChatWithMode(key, text, "", chatNormal, onDone)
}

func (a *App) sendChatWithMode(key TermKey, text, kind string, mode chatSubmitMode, onDone func(error)) {
	if strings.TrimSpace(text) == "" {
		return
	}
	// A command or model selector must only reach the core owning this address.
	c := a.pool.ClientFor(key.InstanceID)
	if key.InstanceID == "" {
		c = a.clientForKey(key)
	}
	if c == nil {
		err := errors.New("该实例尚未连上，先检查实例连接。")
		a.notifyToast(err.Error())
		if onDone != nil {
			onDone(err)
		}
		return
	}
	go func() {
		// hc can hold a send while the terminal has an unfinished draft (60s by default).
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := c.submitChat(ctx, key.Addr, text, kind, mode)
		if err != nil {
			a.notifyToast(chatSendError(err))
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
			a.notifyToast("已上传：" + res.Path)
		})
	}()
}

// Legacy single-address helpers retained for screens.go compatibility until Task 6.
func (a *App) Watch(addr string)                 {}
func (a *App) Unwatch(addr string)               {}
func (a *App) Send(addr, text string)            { a.SendTo(TermKey{Addr: addr}, text) }
func (a *App) Press(addr string, keys ...string) { a.PressTo(TermKey{Addr: addr}, keys...) }
func (a *App) Interrupt(addr string)             { a.InterruptTo(TermKey{Addr: addr}) }
func (a *App) Text(addr string) string           { return a.texts.Get()[addr] }
func (a *App) Compose(key string) string         { return a.composes.Get()[key] }
func (a *App) SetCompose(key, text string)       { a.setCompose(key, text) }

func (a *App) setCompose(key, text string) {
	next := make(map[string]string, len(a.composes.Get())+1)
	for k, v := range a.composes.Get() {
		next[k] = v
	}
	next[key] = text
	a.composes.Set(next)
}

func (a *App) notifyToast(msg string) {
	a.notifyToastWithKind(msg, classifyToastKind(msg))
}

func (a *App) notifyToastWithKind(msg string, kind toastKind) {
	clean := strings.TrimSpace(msg)
	if clean == "" {
		a.dismissToast()
		return
	}
	a.mu.Lock()
	a.toastSeq++
	seq := a.toastSeq
	if a.toastTimer != nil {
		a.toastTimer.Stop()
		a.toastTimer = nil
	}
	dur := a.toastDurationOverride
	if dur <= 0 {
		dur = toastDurationFor(kind, clean)
	}
	a.toastTimer = time.AfterFunc(dur, func() {
		a.mu.Lock()
		if a.toastSeq != seq {
			a.mu.Unlock()
			return
		}
		a.toastTimer = nil
		a.mu.Unlock()
		a.post(func() {
			if cur := a.toast.GetUntracked(); cur.Seq != seq {
				return
			}
			a.toast.Set(toastNotice{})
			a.toastMsg.Set("")
		})
	})
	a.mu.Unlock()

	a.post(func() {
		a.toast.Set(toastNotice{Msg: clean, Kind: kind, Seq: seq})
		a.toastMsg.Set(clean)
		if a.cx != nil && !a.cx.Env().Simulated() {
			switch kind {
			case toastSuccess:
				a.cx.PlayHaptic(core.HapticSuccess)
			case toastWarn:
				a.cx.PlayHaptic(core.HapticWarning)
			case toastError:
				a.cx.PlayHaptic(core.HapticError)
			}
		}
	})
}

func (a *App) dismissToast() {
	a.stopToastTimer()
	a.post(func() {
		a.toast.Set(toastNotice{})
		a.toastMsg.Set("")
	})
}

func (a *App) dismissToastSync() {
	a.stopToastTimer()
	a.toast.Set(toastNotice{})
	a.toastMsg.Set("")
}

func (a *App) stopToastTimer() {
	a.mu.Lock()
	a.toastSeq++
	if a.toastTimer != nil {
		a.toastTimer.Stop()
		a.toastTimer = nil
	}
	a.mu.Unlock()
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
	if p.VTFontSize < 10 || p.VTFontSize > 16 {
		p.VTFontSize = 12
	}
	if p.BodyFontSize < 13 || p.BodyFontSize > 19 {
		p.BodyFontSize = 15
	}
	if p.ThemeMode == "" {
		p.ThemeMode = ThemeLight
	}
	if p.ThemePreset == "" {
		p.ThemePreset = PresetStudio
	}
	if p.VTThemeMode == "" {
		p.VTThemeMode = VTFollowTheme
	}
	if p.VTColorScheme == "" {
		p.VTColorScheme = VTSchemeGraphite
	}
	if p.BodyFont == "" {
		p.BodyFont = BodyFontSans
	}
	if p.MonoFont == "" {
		p.MonoFont = MonoFontJetBrains
	}
	if p.DefaultSendMode != chatQueue && p.DefaultSendMode != chatSteer {
		p.DefaultSendMode = chatQueue
	}
	if p.SessionSort != SortActiveNewest && p.SessionSort != SortActiveOldest && p.SessionSort != SortManual {
		p.SessionSort = SortActiveNewest
	}
	return p
}

func (a *App) savePrefs() {
	if a.cx == nil {
		return
	}
	p := AppPrefs{
		ThemeMode:          a.themeMode.Get(),
		ThemePreset:        a.themePreset.Get(),
		VTThemeMode:        a.vtThemeMode.Get(),
		VTColorScheme:      a.vtColorScheme.Get(),
		VTFontSize:         a.vtFontSize.Get(),
		BodyFont:           a.bodyFont.Get(),
		MonoFont:           a.monoFont.Get(),
		BodyFontSize:       a.bodyFontSize.Get(),
		ObserveDefault:     a.observeDefault.Get(),
		FrameCacheOn:       a.frameCacheOn.Get(),
		NotifyAlerts:       a.notifyAlerts.Get(),
		DefaultSendMode:    a.defaultSendMode.Get(),
		SessionSort:        a.sessionSort.Get(),
		ManualSessionOrder: append([]string(nil), a.manualSessionOrder.Get()...),
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

func (a *App) SetThemePreset(p ThemePreset) {
	if p == "" {
		p = PresetStudio
	}
	a.themePreset.Set(p)
	if a.cx != nil {
		a.cx.SetTheme(AppThemeFor(p))
	}
	a.savePrefs()
}

func (a *App) SetVTThemeMode(m VTThemeMode) {
	a.vtThemeMode.Set(m)
	a.savePrefs()
}

func (a *App) SetVTColorScheme(s VTColorScheme) {
	if s == "" {
		s = VTSchemeGraphite
	}
	a.vtColorScheme.Set(s)
	a.savePrefs()
}

func (a *App) SetVTFontSize(sz float32) {
	if sz < 10 {
		sz = 10
	}
	if sz > 16 {
		sz = 16
	}
	a.vtFontSize.Set(sz)
	a.savePrefs()
}

func (a *App) SetBodyFont(f BodyFontPreset) {
	if f == "" {
		f = BodyFontSans
	}
	a.bodyFont.Set(f)
	a.savePrefs()
}

func (a *App) SetMonoFont(f MonoFontPreset) {
	if f == "" {
		f = MonoFontJetBrains
	}
	a.monoFont.Set(f)
	a.savePrefs()
}

func (a *App) SetBodyFontSize(sz float32) {
	if sz < 13 {
		sz = 13
	}
	if sz > 19 {
		sz = 19
	}
	a.bodyFontSize.Set(sz)
	a.savePrefs()
}

// BodyFontFamily returns the reactive reading typeface stack for Chat & prose.
func (a *App) BodyFontFamily() string {
	return ResolveBodyFontFamily(a.bodyFont.Get(), a.monoFont.Get())
}

// MonoFontFamily returns the reactive monospace typeface stack for code blocks & VT terminals.
func (a *App) MonoFontFamily() string {
	return ResolveMonoFontFamily(a.monoFont.Get())
}

// BodyFontSize returns the user's configured reading body font size in points.
func (a *App) BodyFontSize() core.Points {
	return core.Points(a.bodyFontSize.Get())
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

// DefaultSendMode returns the user's preferred behavior when sending messages to a busy agent (chatQueue or chatSteer).
func (a *App) DefaultSendMode() chatSubmitMode {
	if a == nil || a.defaultSendMode == nil {
		return chatQueue
	}
	m := a.defaultSendMode.Get()
	if m != chatQueue && m != chatSteer {
		return chatQueue
	}
	return m
}

// SessionSort returns the active Home session sort mode.
func (a *App) SessionSort() SessionSortMode {
	if a.sessionSort == nil {
		return SortActiveNewest
	}
	m := a.sessionSort.Get()
	if m != SortActiveNewest && m != SortActiveOldest && m != SortManual {
		return SortActiveNewest
	}
	return m
}

// SetSessionSort updates and persists the Home session sort mode.
func (a *App) SetSessionSort(m SessionSortMode) {
	if m != SortActiveNewest && m != SortActiveOldest && m != SortManual {
		m = SortActiveNewest
	}
	a.sessionSort.Set(m)
	a.savePrefs()
}

// ManualSessionOrder returns the persisted manual session ordering.
func (a *App) ManualSessionOrder() []string {
	if a.manualSessionOrder == nil {
		return nil
	}
	return a.manualSessionOrder.Get()
}

// ReorderSession moves fromKey relative to toKey (seeding from visibleKeys), switches to SortManual, and persists.
func (a *App) ReorderSession(fromKey, toKey string, visibleKeys []string, placeAfter bool) {
	if fromKey == "" || toKey == "" || fromKey == toKey {
		return
	}
	cur := a.manualSessionOrder.GetUntracked()
	base := make([]string, 0, len(visibleKeys)+len(cur))
	seen := make(map[string]bool, len(visibleKeys)+len(cur))
	for _, k := range visibleKeys {
		if k != "" && !seen[k] {
			seen[k] = true
			base = append(base, k)
		}
	}
	for _, k := range cur {
		if k != "" && !seen[k] {
			seen[k] = true
			base = append(base, k)
		}
	}
	next := ReorderKeySlice(base, fromKey, toKey, placeAfter)
	a.manualSessionOrder.Set(next)
	if a.sessionSort.GetUntracked() != SortManual {
		a.sessionSort.Set(SortManual)
	}
	a.savePrefs()
}

// TouchSessionActivity records recent user interaction with a session so SortActiveNewest reflects it immediately.
func (a *App) TouchSessionActivity(key TermKey) {
	if key == (TermKey{}) {
		return
	}
	nowMS := uint64(time.Now().UnixMilli())
	a.mu.Lock()
	if a.lastActiveMS == nil {
		a.lastActiveMS = make(map[TermKey]uint64)
	}
	if nowMS > a.lastActiveMS[key] {
		a.lastActiveMS[key] = nowMS
	}
	a.mu.Unlock()
}

// LiveActiveMap returns a snapshot copy of observed activity timestamps for sorting.
func (a *App) LiveActiveMap() map[TermKey]uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.lastActiveMS) == 0 {
		return nil
	}
	cp := make(map[TermKey]uint64, len(a.lastActiveMS))
	for k, v := range a.lastActiveMS {
		cp[k] = v
	}
	return cp
}

// SetDefaultSendMode updates and persists the default message send behavior (等待 / 插队).
func (a *App) SetDefaultSendMode(m chatSubmitMode) {
	if m != chatQueue && m != chatSteer {
		m = chatQueue
	}
	a.defaultSendMode.Set(m)
	a.savePrefs()
}

// EffectiveSendMode resolves the submit mode for a harness kind using the user's Settings default.
func (a *App) EffectiveSendMode(kind string) chatSubmitMode {
	if defaultSubmitMode(kind) == chatNormal {
		return chatNormal
	}
	return a.DefaultSendMode()
}

// SteerQueuedChat promotes a previously queued message by interrupting the current turn cleanly.
func (a *App) SteerQueuedChat(key TermKey, onDone func(error)) {
	c := a.pool.ClientFor(key.InstanceID)
	if key.InstanceID == "" {
		c = a.clientForKey(key)
	}
	if c == nil {
		if onDone != nil {
			onDone(nil)
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := c.steerQueuedChat(ctx, key.Addr)
		if err != nil {
			a.notifyToast("插队失败：" + explain(err))
		} else {
			a.notifyToast("已插队优先处理")
		}
		if onDone != nil {
			onDone(err)
		}
	}()
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
