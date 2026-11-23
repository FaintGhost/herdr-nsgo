package main

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo/base/terminal"
	"github.com/nsgo-dev/nsgo/core"
)

const maxRawCacheBytes = 128 * 1024

// SessionTerminal unifies NSGo's native Ghostty terminal (on iOS/macOS/Linux/Windows)
// with the pure-Go VTScreen + Canvas renderer (on Android and headless tests).
// It enforces the observe-by-default PTY resize policy: entering a session never
// alters the remote desktop PTY size until the user explicitly fits width or sends input.
type SessionTerminal struct {
	key        TermKey
	frameCache *FrameCache

	observe atomic.Bool

	mu           sync.Mutex
	stream       *AttachStream
	screen       *VTScreen
	ghostty      *terminal.Terminal
	ghosttyIO    *sessionGhosttyBridge
	reattachFn   func(observe bool, cols, rows int)
	loadMoreFn   func()
	pendingInput [][]byte
	closed       bool
	rawRing      bytes.Buffer
	dirtyCache   bool
	revScheduled atomic.Bool

	// UI signals (updated via cx.Post when cx != nil, or directly in tests).
	cx          *core.Scope
	rev         *core.Signal[uint64]
	observeSig  *core.Signal[bool]
	scrolledUp  *core.Signal[bool]
	searchQuery *core.Signal[string]
	searchIdx   *core.Signal[int]
	diag        *core.Signal[string]
	historyHint *core.Signal[string]
	scrollRef   core.Ref
	viewportW   float32
	viewportH   float32
}

// NewSessionTerminal creates a SessionTerminal for key, pre-seeding it from cache if available.
// If enableGhostty is true and the runtime OS supports libghostty-vt (not android),
// it also attaches a native base/terminal.Terminal.
func NewSessionTerminal(cx *core.Scope, key TermKey, initialCols, initialRows int, observeDefault bool, cache *FrameCache, enableGhostty bool) *SessionTerminal {
	if initialCols <= 0 {
		initialCols = 80
	}
	if initialRows <= 0 {
		initialRows = 24
	}
	st := &SessionTerminal{
		key:        key,
		frameCache: cache,
		screen:     NewVTScreen(initialCols, initialRows),
		cx:         cx,
	}
	st.observe.Store(observeDefault)
	if cx != nil {
		st.rev = core.NewSignal[uint64](cx, 0)
		st.observeSig = core.NewSignal[bool](cx, observeDefault)
		st.scrolledUp = core.NewSignal[bool](cx, false)
		st.searchQuery = core.NewSignal[string](cx, "")
		st.searchIdx = core.NewSignal[int](cx, 0)
		st.diag = core.NewSignal[string](cx, "正在接入终端流…")
		st.historyHint = core.NewSignal[string](cx, "↑ 加载更早历史")
	}

	// Pre-seed immediately from LRU FrameCache for 0ms visual feedback before live attach arrives.
	if cache != nil {
		if cached, ok := cache.Get(key); ok && len(cached) > 0 {
			st.screen.Write(cached)
			st.appendRawLocked(cached)
		}
	}

	// On iOS/macOS/Linux/Windows (never Android where libghostty-vt is unavailable),
	// attempt to create a native Ghostty terminal backed by our observe-aware bridge.
	if enableGhostty && runtime.GOOS != "android" {
		bridge := newSessionGhosttyBridge(st)
		gt, err := terminal.New(terminal.Options{
			Conn:         bridge,
			ResizeByConn: true,
		})
		if err == nil {
			gt.Resize(initialCols, initialRows)
			if st.rawRing.Len() > 0 {
				gt.Feed(st.rawRing.Bytes())
			}
			st.ghostty = gt
			st.ghosttyIO = bridge
		} else {
			bridge.Close()
		}
	}

	st.bumpRevImmediate()
	return st
}

// SetDiag updates the subtle loading/diagnostic status shown while the VT screen has no bytes yet.
func (st *SessionTerminal) SetDiag(msg string) {
	if st.diag == nil {
		return
	}
	st.post(func() { st.diag.Set(msg) })
}

// SetLoadMore registers a callback to pull deeper scrollback history when user taps top loader.
func (st *SessionTerminal) SetLoadMore(fn func()) {
	st.mu.Lock()
	st.loadMoreFn = fn
	st.mu.Unlock()
}

// SetHistoryHint updates the top history loader button text ("" hides it).
func (st *SessionTerminal) SetHistoryHint(msg string) {
	if st.historyHint == nil {
		return
	}
	st.post(func() { st.historyHint.Set(msg) })
}

// LoadMoreHistory triggers the registered deeper-history fetch callback.
func (st *SessionTerminal) LoadMoreHistory() {
	st.mu.Lock()
	fn := st.loadMoreFn
	st.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// SetReattach registers a callback invoked when observe/active mode transitions require
// re-attaching with the updated observe flag.
func (st *SessionTerminal) SetReattach(fn func(observe bool, cols, rows int)) {
	st.mu.Lock()
	st.reattachFn = fn
	st.mu.Unlock()
}

// Attached reports whether a live framed AttachStream is currently bound.
func (st *SessionTerminal) Attached() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.stream != nil
}

// Attach binds a live framed *AttachStream to this SessionTerminal and starts reading VT bytes.
func (st *SessionTerminal) Attach(stream *AttachStream, cols, rows int) {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		if stream != nil {
			_ = stream.Close()
		}
		return
	}
	prev := st.stream
	st.stream = stream
	gt := st.ghostty
	if cols > 0 && rows > 0 {
		st.screen.Resize(cols, rows)
	}
	queued := st.pendingInput
	st.pendingInput = nil
	st.mu.Unlock()

	// Call Ghostty Resize outside st.mu to prevent ABBA/self-deadlock with bridge.Resize!
	if gt != nil && cols > 0 && rows > 0 {
		gt.Resize(cols, rows)
	}

	if prev != nil {
		_ = prev.Close()
	}
	if stream == nil {
		return
	}

	// Flush any input typed while transitioning from observe -> active attach.
	for _, buf := range queued {
		_, _ = stream.Write(buf)
	}

	stream.OnResize(func(c, r int) {
		st.mu.Lock()
		st.screen.Resize(c, r)
		g := st.ghostty
		st.mu.Unlock()
		if g != nil {
			g.Resize(c, r)
		}
		st.bumpRev()
	})

	// Grant initial flow-control credit so credit-gated cores begin streaming immediately.
	_ = stream.SendCredit(65536)

	go st.readLoop(stream)
}

func (st *SessionTerminal) readLoop(stream *AttachStream) {
	buf := make([]byte, 16*1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			st.Feed(chunk)
			_ = stream.SendCredit(uint32(n))
		}
		if err != nil {
			return
		}
	}
}

// Feed writes VT bytes into both VTScreen and (if active) Ghostty, coalescing UI redraws & cache writes.
func (st *SessionTerminal) Feed(data []byte) {
	if len(data) == 0 {
		return
	}
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	firstFrame := st.rawRing.Len() == 0
	st.screen.Write(data)
	st.appendRawLocked(data)
	st.dirtyCache = true
	gt := st.ghostty
	st.mu.Unlock()

	if gt != nil {
		gt.Feed(data)
	}
	if firstFrame {
		st.SetDiag("")
		st.bumpRevImmediate()
	} else {
		st.bumpRev()
	}
}

func (st *SessionTerminal) appendRawLocked(data []byte) {
	if len(data) >= maxRawCacheBytes {
		st.rawRing.Reset()
		st.rawRing.Write(data[len(data)-maxRawCacheBytes:])
		return
	}
	if st.rawRing.Len()+len(data) > maxRawCacheBytes {
		keep := maxRawCacheBytes - len(data)
		existing := st.rawRing.Bytes()
		tail := append([]byte(nil), existing[len(existing)-keep:]...)
		st.rawRing.Reset()
		st.rawRing.Write(tail)
	}
	st.rawRing.Write(data)
}

// Observe reports whether the session is currently in non-intrusive observe mode (lock-free).
func (st *SessionTerminal) Observe() bool {
	return st.observe.Load()
}

// SetObserve switches between observe mode (no PTY resize) and active fit-width mode.
// When a reattach handler is wired, re-attaches cleanly with the updated observe flag;
// otherwise falls back to sending RESIZE on the current stream.
func (st *SessionTerminal) SetObserve(observe bool, fontSize float32) {
	st.observe.Store(observe)
	st.mu.Lock()
	stream := st.stream
	vpW, vpH := st.viewportW, st.viewportH
	reattach := st.reattachFn
	st.mu.Unlock()

	st.post(func() {
		if st.observeSig != nil {
			st.observeSig.Set(observe)
		}
	})

	cols, rows := fitGridForViewport(vpW, vpH, fontSize)
	if reattach != nil {
		if !observe {
			st.resizeLocal(cols, rows)
		}
		reattach(observe, cols, rows)
		return
	}
	if !observe {
		st.resizeRemote(stream, cols, rows)
	}
}

// SendInput sends bytes to the remote PTY. If currently in observe mode, promotes to active mode
// (re-attaching when wired and queueing the input so no keystroke is lost).
func (st *SessionTerminal) SendInput(data []byte, fontSize float32) error {
	if len(data) == 0 {
		return nil
	}
	wasObserve := st.observe.Swap(false)

	st.mu.Lock()
	stream := st.stream
	vpW, vpH := st.viewportW, st.viewportH
	reattach := st.reattachFn
	if wasObserve && reattach != nil {
		cp := append([]byte(nil), data...)
		st.pendingInput = append(st.pendingInput, cp)
		st.mu.Unlock()
		st.post(func() {
			if st.observeSig != nil {
				st.observeSig.Set(false)
			}
		})
		cols, rows := fitGridForViewport(vpW, vpH, fontSize)
		st.resizeLocal(cols, rows)
		reattach(false, cols, rows)
		return nil
	}
	st.mu.Unlock()

	if wasObserve {
		st.post(func() {
			if st.observeSig != nil {
				st.observeSig.Set(false)
			}
		})
		cols, rows := fitGridForViewport(vpW, vpH, fontSize)
		st.resizeRemote(stream, cols, rows)
	}

	if stream == nil {
		return io.ErrClosedPipe
	}
	_, err := stream.Write(data)
	return err
}

func (st *SessionTerminal) resizeLocal(cols, rows int) {
	if cols < 20 {
		cols = 58
	}
	if rows < 8 {
		rows = 24
	}
	st.mu.Lock()
	st.screen.Resize(cols, rows)
	gt := st.ghostty
	st.mu.Unlock()
	if gt != nil {
		gt.Resize(cols, rows)
	}
	st.bumpRevImmediate()
}

func (st *SessionTerminal) resizeRemote(stream *AttachStream, cols, rows int) {
	if cols < 20 {
		cols = 58
	}
	if rows < 8 {
		rows = 24
	}
	if stream != nil {
		_ = stream.Resize(cols, rows)
	}
	st.resizeLocal(cols, rows)
}

func fitGridForViewport(vpW, vpH, fontSize float32) (cols, rows int) {
	if fontSize <= 0 {
		fontSize = float32(Tokens.FontVTDefault)
	}
	if vpW <= 0 {
		return 58, 24
	}
	cellW := fontSize * 0.62
	cellH := fontSize * 1.38
	cols = int((vpW - 16) / cellW)
	rows = int((vpH - 12) / cellH)
	cols = clampInt(cols, 32, 200)
	rows = clampInt(rows, 10, 80)
	return cols, rows
}

// Screen returns the underlying pure-Go VTScreen (always non-nil).
func (st *SessionTerminal) Screen() *VTScreen {
	return st.screen
}

// Ghostty returns the native Ghostty terminal when active on supported platforms, or nil.
func (st *SessionTerminal) Ghostty() *terminal.Terminal {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.ghostty
}

// SetSearch updates the active in-session search query.
func (st *SessionTerminal) SetSearch(q string) {
	if st.searchQuery != nil {
		st.searchQuery.Set(q)
	}
	if st.searchIdx != nil {
		st.searchIdx.Set(0)
	}
	if gt := st.Ghostty(); gt != nil {
		if strings.TrimSpace(q) == "" {
			gt.EndSearch()
		} else {
			gt.Search(q)
		}
	}
	st.scrollToActiveMatch()
	st.bumpRevImmediate()
}

// SearchMatches returns current search matches across scrollback + screen.
func (st *SessionTerminal) SearchMatches() []VTMatch {
	if st.searchQuery == nil {
		return nil
	}
	return st.screen.Search(st.searchQuery.Get())
}

// NextSearchMatch cycles forward through search matches and scrolls Canvas to the match.
func (st *SessionTerminal) NextSearchMatch() {
	m := st.SearchMatches()
	if len(m) == 0 || st.searchIdx == nil {
		return
	}
	st.searchIdx.Set((st.searchIdx.Get() + 1) % len(m))
	if gt := st.Ghostty(); gt != nil {
		gt.SearchNext()
	}
	st.scrollToActiveMatch()
	st.bumpRevImmediate()
}

// PrevSearchMatch cycles backward through search matches and scrolls Canvas to the match.
func (st *SessionTerminal) PrevSearchMatch() {
	m := st.SearchMatches()
	if len(m) == 0 || st.searchIdx == nil {
		return
	}
	idx := st.searchIdx.Get() - 1
	if idx < 0 {
		idx = len(m) - 1
	}
	st.searchIdx.Set(idx)
	if gt := st.Ghostty(); gt != nil {
		gt.SearchPrevious()
	}
	st.scrollToActiveMatch()
	st.bumpRevImmediate()
}

func (st *SessionTerminal) scrollToActiveMatch() {
	m := st.SearchMatches()
	if len(m) == 0 || st.searchIdx == nil {
		return
	}
	idx := st.searchIdx.Get()
	if idx < 0 || idx >= len(m) {
		return
	}
	y := float32(m[idx].Line)*float32(Tokens.FontVTDefault)*1.40 - 40
	if y < 0 {
		y = 0
	}
	st.scrollRef.ScrollTo(core.Point{X: 0, Y: core.Points(y)}, true)
}

// ScrollToBottom scrolls the VT scroll view back to the newest line.
func (st *SessionTerminal) ScrollToBottom() {
	st.scrollRef.ScrollToEnd(true)
	if st.scrolledUp != nil {
		st.scrolledUp.Set(false)
	}
}

// Close releases the framed attach channel and native terminal, saving the latest frame to cache.
func (st *SessionTerminal) Close() {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	st.closed = true
	stream := st.stream
	st.stream = nil
	gt := st.ghostty
	st.ghostty = nil
	bridge := st.ghosttyIO
	st.ghosttyIO = nil
	cache := st.frameCache
	var snapshot []byte
	if cache != nil && st.rawRing.Len() > 0 {
		snapshot = append([]byte(nil), st.rawRing.Bytes()...)
	}
	st.mu.Unlock()

	if cache != nil && len(snapshot) > 0 {
		cache.Put(st.key, snapshot)
	}
	if stream != nil {
		_ = stream.Close()
	}
	if bridge != nil {
		_ = bridge.Close()
	}
	if gt != nil {
		_ = gt.Close()
	}
}

func (st *SessionTerminal) bumpRev() {
	if !st.revScheduled.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(20*time.Millisecond, func() {
		st.revScheduled.Store(false)
		st.flushCacheSnapshot()
		st.bumpRevImmediate()
	})
}

func (st *SessionTerminal) flushCacheSnapshot() {
	st.mu.Lock()
	if st.closed || !st.dirtyCache || st.frameCache == nil {
		st.mu.Unlock()
		return
	}
	st.dirtyCache = false
	cache := st.frameCache
	snapshot := append([]byte(nil), st.rawRing.Bytes()...)
	st.mu.Unlock()
	if len(snapshot) > 0 {
		cache.Put(st.key, snapshot)
	}
}

func (st *SessionTerminal) bumpRevImmediate() {
	if st.rev == nil {
		return
	}
	next := st.screen.Seq()
	st.post(func() {
		st.rev.Set(next)
	})
}

func (st *SessionTerminal) post(fn func()) {
	if st.cx != nil {
		st.cx.Post(fn)
		return
	}
	fn()
}

// sessionGhosttyBridge connects base/terminal.Terminal input back to SessionTerminal.SendInput
// while strictly blocking resize callbacks in observe mode (lock-free via atomic.Bool).
type sessionGhosttyBridge struct {
	st   *SessionTerminal
	done chan struct{}
	once sync.Once
}

func newSessionGhosttyBridge(st *SessionTerminal) *sessionGhosttyBridge {
	return &sessionGhosttyBridge{st: st, done: make(chan struct{})}
}

func (b *sessionGhosttyBridge) Read(p []byte) (int, error) {
	<-b.done
	return 0, io.EOF
}

func (b *sessionGhosttyBridge) Write(p []byte) (int, error) {
	if err := b.st.SendInput(p, float32(Tokens.FontVTDefault)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (b *sessionGhosttyBridge) Resize(cols, rows int) error {
	// Strictly respect observe mode without acquiring st.mu (prevents ABBA deadlock).
	if b.st.Observe() {
		return nil
	}
	b.st.mu.Lock()
	stream := b.st.stream
	b.st.mu.Unlock()
	if stream != nil {
		return stream.Resize(cols, rows)
	}
	return nil
}

func (b *sessionGhosttyBridge) Close() error {
	b.once.Do(func() { close(b.done) })
	return nil
}

// RenderSessionTerminal renders the VT terminal surface reactively (native Ghostty on iOS/macOS/desktop,
// high-speed pure-Go VTScreen + core.Canvas on Android and headless tests).
func RenderSessionTerminal(
	cx *core.Scope,
	st *SessionTerminal,
	palFn func() Palette,
	vtPalFn func() VTPalette,
	fontSizeFn func() float32,
) core.Node {
	getFontSize := func() float32 {
		fs := fontSizeFn()
		if fs <= 0 {
			return float32(Tokens.FontVTDefault)
		}
		return fs
	}

	if st == nil {
		return core.View{
			StyleFunc: func() core.Style {
				return core.Style{
					FlexGrow:   1,
					Background: vtPalFn().Bg,
					Padding:    core.All(Tokens.Space3),
				}
			},
			Children: []core.Node{
				core.Text{
					Content: "正在连接终端会话…",
					StyleFunc: func() core.Style {
						return core.Style{
							Color:    vtPalFn().Muted,
							FontSize: core.Points(getFontSize()),
						}
					},
				},
			},
		}
	}

	// Native Ghostty surface on platforms that support it (iOS/macOS/Linux/Windows).
	if gt := st.Ghostty(); gt != nil {
		lightTh, darkTh := GhosttyTerminalThemes()
		vtPal := vtPalFn()
		chosenLight, chosenDark := lightTh, darkTh
		if vtPal.Bg == DarkVTPalette().Bg {
			chosenLight = darkTh
		}
		return core.View{
			TestID: "vt-surface",
			StyleFunc: func() core.Style {
				return core.Style{
					FlexGrow:   1,
					Background: vtPalFn().Bg,
				}
			},
			OnLayout: func(b core.Bounds) {
				st.mu.Lock()
				st.viewportW = float32(b.Size.Width)
				st.viewportH = float32(b.Size.Height)
				st.mu.Unlock()
			},
			Children: []core.Node{
				terminal.View{
					Terminal:  gt,
					Theme:     chosenLight,
					DarkTheme: chosenDark,
					NoKeyBar:  true,
					Font: core.Font{
						Family: vtFontFamily,
						Size:   core.Points(getFontSize()),
					},
					Style:  core.Style{FlexGrow: 1},
					TestID: "vt-ghostty",
				},
			},
		}
	}

	// Pure-Go VTScreen Canvas surface (Android arm64 + headless tests).
	// Soft-wraps wide desktop lines cleanly to the phone viewport width so nothing is cut off
	// on the right edge, even while staying in non-intrusive Observe mode!
	padX := float32(8)
	padY := float32(8)

	wrapColsForViewport := func(fs float32) int {
		st.mu.Lock()
		vpW := st.viewportW
		st.mu.Unlock()
		if vpW <= 0 {
			return 0
		}
		cellW := fs * 0.62
		cols := int((vpW - padX*2) / cellW)
		if cols < 24 {
			cols = 24
		}
		return cols
	}

	return core.Scroll{
		Ref:           &st.scrollRef,
		TestID:        "vt-surface",
		MinZoomScale:  0.7,
		MaxZoomScale:  2.2,
		HideIndicator: false,
		StyleFunc: func() core.Style {
			return core.Style{
				FlexGrow:   1,
				Background: vtPalFn().Bg,
			}
		},
		OnLayout: func(b core.Bounds) {
			st.mu.Lock()
			changed := st.viewportW != float32(b.Size.Width) || st.viewportH != float32(b.Size.Height)
			st.viewportW = float32(b.Size.Width)
			st.viewportH = float32(b.Size.Height)
			st.mu.Unlock()
			if changed {
				st.bumpRevImmediate()
			}
		},
		OnScroll: func(ev core.ScrollEvent) {
			if ev.Stale || st.scrolledUp == nil {
				return
			}
			distFromBottom := float32(ev.ContentSize.Height) - (float32(ev.Offset.Y) + float32(ev.Size.Height))
			st.scrolledUp.Set(distFromBottom > 60)
		},
		Children: []core.Node{
			core.Show{
				When: func() bool {
					return st.diag != nil && st.diag.Get() != ""
				},
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						Style: core.Style{Padding: core.Symmetric(Tokens.Space2, Tokens.Space3)},
						Children: []core.Node{
							core.Text{
								ContentFunc: st.diag.Get,
								StyleFunc: func() core.Style {
									return core.Style{
										Color:      vtPalFn().Muted,
										FontSize:   Tokens.FontCaption,
										FontFamily: core.FontMonospace,
									}
								},
							},
						},
					}
				},
			},
			core.Show{
				When: func() bool {
					return st.historyHint != nil && st.historyHint.Get() != "" && (st.diag == nil || st.diag.Get() == "")
				},
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						TestID: "vt-load-more-history",
						Role:   core.RoleButton,
						OnPress: func(core.PressEvent) {
							st.LoadMoreHistory()
						},
						StyleFunc: func() core.Style {
							pal := palFn()
							return core.Style{
								AlignItems:  core.AlignCenter,
								Padding:     core.Symmetric(core.Points(6), Tokens.Space3),
								Borders:     core.Edges{Bottom: 1},
								BorderColor: pal.Separator,
							}
						},
						Children: []core.Node{
							core.Text{
								ContentFunc: st.historyHint.Get,
								StyleFunc: func() core.Style {
									pal := palFn()
									return core.Style{
										Color:      pal.Accent,
										FontSize:   Tokens.FontMonoMeta,
										FontWeight: core.FontWeightSemibold,
									}
								},
							},
						},
					}
				},
			},
			core.Canvas{
				TestID: "vt-canvas",
				StyleFunc: func() core.Style {
					if st.rev != nil {
						_ = st.rev.Get()
					}
					fs := getFontSize()
					cellW := fs * 0.62
					cellH := fs * 1.40
					wrapCols := wrapColsForViewport(fs)
					vlines := buildVisualLines(st.screen.AllLines(), wrapCols)
					cols, _ := st.screen.Size()
					if wrapCols > 0 {
						cols = wrapCols
					}
					w := core.Points(padX*2 + float32(cols)*cellW)
					h := core.Points(padY*2 + float32(len(vlines))*cellH)
					if h < 220 {
						h = 220
					}
					return core.Style{
						Width:      core.Pt(w),
						Height:     core.Pt(h),
						Background: vtPalFn().Bg,
					}
				},
				Draw: func(d *core.Drawing, size core.Size) {
					if st.rev != nil {
						_ = st.rev.Get()
					}
					fs := getFontSize()
					cellW := fs * 0.62
					cellH := fs * 1.40
					var query string
					var activeMatch int
					if st.searchQuery != nil {
						query = st.searchQuery.Get()
					}
					if st.searchIdx != nil {
						activeMatch = st.searchIdx.Get()
					}
					drawVTScreen(d, size, st.screen, palFn(), vtPalFn(), fs, cellW, cellH, padX, padY, wrapColsForViewport(fs), query, activeMatch)
				},
			},
		},
	}
}

type vtVisualLine struct {
	origLine int
	colStart int
	cells    []VTCell
}

// buildVisualLines soft-wraps logical terminal rows into viewport-width visual lines
// without splitting wide 2-column CJK glyphs, trimming trailing empty cells on wrapped rows.
func buildVisualLines(all [][]VTCell, wrapCols int) []vtVisualLine {
	if wrapCols <= 0 {
		out := make([]vtVisualLine, len(all))
		for i, r := range all {
			out[i] = vtVisualLine{origLine: i, colStart: 0, cells: r}
		}
		return out
	}
	out := make([]vtVisualLine, 0, len(all))
	for i, row := range all {
		// Find last cell with an actual visible non-space character so trailing background-padded
		// spaces from wide desktop TUIs (e.g. 140-col Claude Code bars) never wrap into empty bars.
		lastText := -1
		for c := len(row) - 1; c >= 0; c-- {
			cell := row[c]
			if cell.Width == 0 || (cell.Rune != 0 && cell.Rune != ' ') {
				lastText = c
				break
			}
		}
		if lastText < 0 || lastText < wrapCols {
			end := len(row)
			if end > wrapCols {
				end = wrapCols
			}
			out = append(out, vtVisualLine{origLine: i, colStart: 0, cells: row[:end]})
			continue
		}
		// Keep pure horizontal divider lines (e.g. 140-col '───' rules) as a single clean viewport-width line.
		if isDividerRow(row[:lastText+1]) {
			out = append(out, vtVisualLine{origLine: i, colStart: 0, cells: row[:wrapCols]})
			continue
		}
		pos := 0
		for pos <= lastText {
			end := pos + wrapCols
			if end > lastText+1 {
				end = lastText + 1
			}
			// Never cut a wide 2-column character in half across wrap boundary.
			if end < len(row) && row[end].Width == 0 && end > pos+1 {
				end--
			}
			out = append(out, vtVisualLine{origLine: i, colStart: pos, cells: row[pos:end]})
			pos = end
		}
	}
	return out
}

func isDividerRow(cells []VTCell) bool {
	seen := 0
	for _, c := range cells {
		if c.Width == 0 || c.Rune == 0 || c.Rune == ' ' {
			continue
		}
		switch c.Rune {
		case '─', '━', '═', '┄', '┈', '-', '_':
			seen++
		default:
			return false
		}
	}
	return seen >= 8
}

func drawVTScreen(
	d *core.Drawing,
	size core.Size,
	screen *VTScreen,
	pal Palette,
	vtPal VTPalette,
	fontSize, cellW, cellH, padX, padY float32,
	wrapCols int,
	searchQuery string,
	activeMatch int,
) {
	// Background fill.
	d.SetFillColor(vtPal.Bg)
	d.BeginPath().Rect(0, 0, float32(size.Width), float32(size.Height)).Fill()

	all := screen.AllLines()
	vlines := buildVisualLines(all, wrapCols)
	sbLen := screen.ScrollbackLen()
	if screen.InAltScreen() {
		sbLen = 0
	}
	curCol, curRow, curVis := screen.Cursor()
	curLine := sbLen + curRow

	var matches []VTMatch
	if strings.TrimSpace(searchQuery) != "" {
		matches = screen.Search(searchQuery)
	}

	d.SetTextBaseline(core.BaselineTop)
	d.SetTextAlign(core.TextAlignLeft)

	baseFont := core.Font{
		Family: vtFontFamily,
		Size:   core.Points(fontSize),
	}

	for vIdx, vl := range vlines {
		y := padY + float32(vIdx)*cellH
		row := vl.cells

		// Pass 1: cell background rectangles.
		for col := 0; col < len(row); col++ {
			cell := row[col]
			if cell.Width == 0 {
				continue
			}
			bgCol := cell.Style.BG.Resolve(vtPal, false)
			hasBG := cell.Style.BG.Kind != VTColorDefault
			if cell.Style.Inverse {
				bgCol = cell.Style.FG.Resolve(vtPal, true)
				hasBG = true
			}
			if hasBG {
				w := float32(cell.Width) * cellW
				d.SetFillColor(bgCol)
				d.BeginPath().Rect(padX+float32(col)*cellW, y, w, cellH).Fill()
			}
		}

		// Search match highlights overlapping this visual slice.
		for mi, m := range matches {
			if m.Line != vl.origLine {
				continue
			}
			mStart := m.Col - vl.colStart
			mEnd := mStart + m.Length
			if mEnd <= 0 || mStart >= len(row) {
				continue
			}
			if mStart < 0 {
				mStart = 0
			}
			if mEnd > len(row) {
				mEnd = len(row)
			}
			hl := pal.AccentSoft
			if mi == activeMatch {
				hl = pal.Accent
			}
			d.SetFillColor(hl)
			d.BeginPath().Rect(padX+float32(mStart)*cellW, y, float32(mEnd-mStart)*cellW, cellH).Fill()
		}

		// Cursor drawn BEFORE text (as underline bar so character underneath stays readable).
		if curVis && vl.origLine == curLine && curCol >= vl.colStart && curCol < vl.colStart+len(row) {
			d.SetFillColor(vtPal.Cursor)
			d.BeginPath().Rect(padX+float32(curCol-vl.colStart)*cellW, y+cellH-2, cellW, 2).Fill()
		}

		// Pass 2: run-length batched ASCII/narrow runs; wide CJK/emoji drawn individually at exact col*cellW.
		col := 0
		for col < len(row) {
			cell := row[col]
			if cell.Width == 0 || cell.Rune == 0 || cell.Rune == ' ' {
				col++
				continue
			}
			startCol := col
			runStyle := cell.Style
			var buf strings.Builder
			if cell.Width == 2 {
				buf.WriteRune(cell.Rune)
				col += 2
			} else {
				for col < len(row) {
					c := row[col]
					if c.Width == 0 {
						col++
						continue
					}
					if c.Width == 2 || c.Style != runStyle {
						break
					}
					if c.Rune == 0 {
						buf.WriteByte(' ')
					} else {
						buf.WriteRune(c.Rune)
					}
					col++
				}
			}

			fgCol := runStyle.FG.Resolve(vtPal, true)
			if runStyle.Inverse {
				fgCol = runStyle.BG.Resolve(vtPal, false)
			}
			if runStyle.Dim {
				fgCol = vtPal.Muted
			}

			f := baseFont
			if runStyle.Bold {
				f.Weight = core.FontWeightBold
			}
			if runStyle.Italic {
				f.Italic = true
			}
			d.SetFillColor(fgCol)
			d.FillText(buf.String(), padX+float32(startCol)*cellW, y+1, f)

			if runStyle.Underline {
				d.SetFillColor(fgCol)
				d.BeginPath().Rect(padX+float32(startCol)*cellW, y+cellH-2, float32(col-startCol)*cellW, 1).Fill()
			}
		}
	}
}
