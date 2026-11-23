package main

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"sync"

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

	mu        sync.Mutex
	stream    *AttachStream
	screen    *VTScreen
	ghostty   *terminal.Terminal
	ghosttyIO *sessionGhosttyBridge
	observe   bool
	closed    bool
	rawRing   bytes.Buffer

	// UI signals (updated via cx.Post when cx != nil, or directly in tests).
	cx          *core.Scope
	rev         *core.Signal[uint64]
	observeSig  *core.Signal[bool]
	scrolledUp  *core.Signal[bool]
	searchQuery *core.Signal[string]
	searchIdx   *core.Signal[int]
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
		observe:    observeDefault,
		cx:         cx,
	}
	if cx != nil {
		st.rev = core.NewSignal[uint64](cx, 0)
		st.observeSig = core.NewSignal[bool](cx, observeDefault)
		st.scrolledUp = core.NewSignal[bool](cx, false)
		st.searchQuery = core.NewSignal[string](cx, "")
		st.searchIdx = core.NewSignal[int](cx, 0)
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

	st.bumpRev()
	return st
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
	if cols > 0 && rows > 0 {
		st.screen.Resize(cols, rows)
		if st.ghostty != nil {
			st.ghostty.Resize(cols, rows)
		}
	}
	st.mu.Unlock()

	if prev != nil {
		_ = prev.Close()
	}
	if stream == nil {
		return
	}

	stream.OnResize(func(c, r int) {
		st.mu.Lock()
		st.screen.Resize(c, r)
		if st.ghostty != nil {
			st.ghostty.Resize(c, r)
		}
		st.mu.Unlock()
		st.bumpRev()
	})

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
		}
		if err != nil {
			return
		}
	}
}

// Feed writes VT bytes into both VTScreen and (if active) Ghostty, updating the LRU cache ring.
func (st *SessionTerminal) Feed(data []byte) {
	if len(data) == 0 {
		return
	}
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	st.screen.Write(data)
	st.appendRawLocked(data)
	gt := st.ghostty
	cache := st.frameCache
	var snapshot []byte
	if cache != nil {
		snapshot = append([]byte(nil), st.rawRing.Bytes()...)
	}
	st.mu.Unlock()

	if gt != nil {
		gt.Feed(data)
	}
	if cache != nil && len(snapshot) > 0 {
		cache.Put(st.key, snapshot)
	}
	st.bumpRev()
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

// Observe reports whether the session is currently in non-intrusive observe mode.
func (st *SessionTerminal) Observe() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.observe
}

// SetObserve switches between observe mode (no PTY resize) and active fit-width mode.
// When switching to active (observe=false), sends FrameResize to fit the current viewport.
func (st *SessionTerminal) SetObserve(observe bool, fontSize float32) {
	st.mu.Lock()
	st.observe = observe
	stream := st.stream
	vpW, vpH := st.viewportW, st.viewportH
	st.mu.Unlock()

	st.post(func() {
		if st.observeSig != nil {
			st.observeSig.Set(observe)
		}
	})

	if !observe {
		cols, rows := fitGridForViewport(vpW, vpH, fontSize)
		st.resizeRemote(stream, cols, rows)
	}
}

// SendInput sends bytes to the remote PTY. If currently in observe mode, it first
// promotes the session to active mode and resizes to the mobile viewport as specified.
func (st *SessionTerminal) SendInput(data []byte, fontSize float32) error {
	if len(data) == 0 {
		return nil
	}
	st.mu.Lock()
	wasObserve := st.observe
	if wasObserve {
		st.observe = false
	}
	stream := st.stream
	vpW, vpH := st.viewportW, st.viewportH
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
	st.mu.Lock()
	st.screen.Resize(cols, rows)
	if st.ghostty != nil {
		st.ghostty.Resize(cols, rows)
	}
	st.mu.Unlock()
	st.bumpRev()
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
	st.bumpRev()
}

// SearchMatches returns current search matches across scrollback + screen.
func (st *SessionTerminal) SearchMatches() []VTMatch {
	if st.searchQuery == nil {
		return nil
	}
	return st.screen.Search(st.searchQuery.Get())
}

// NextSearchMatch cycles forward through search matches.
func (st *SessionTerminal) NextSearchMatch() {
	m := st.SearchMatches()
	if len(m) == 0 || st.searchIdx == nil {
		return
	}
	st.searchIdx.Set((st.searchIdx.Get() + 1) % len(m))
	if gt := st.Ghostty(); gt != nil {
		gt.SearchNext()
	}
	st.bumpRev()
}

// PrevSearchMatch cycles backward through search matches.
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
	st.bumpRev()
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
// while strictly blocking resize callbacks in observe mode.
type sessionGhosttyBridge struct {
	st   *SessionTerminal
	done chan struct{}
	once sync.Once
}

func newSessionGhosttyBridge(st *SessionTerminal) *sessionGhosttyBridge {
	return &sessionGhosttyBridge{st: st, done: make(chan struct{})}
}

func (b *sessionGhosttyBridge) Read(p []byte) (int, error) {
	// Output is pushed via ghostty.Feed(); block until closed.
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
	// Strictly respect observe mode: never resize remote PTY while observing.
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

// RenderSessionTerminal renders the VT terminal surface (native Ghostty on iOS/macOS/desktop,
// high-speed pure-Go VTScreen + core.Canvas on Android and headless tests).
func RenderSessionTerminal(cx *core.Scope, st *SessionTerminal, pal Palette, vtPal VTPalette, fontSize float32) core.Node {
	if fontSize <= 0 {
		fontSize = float32(Tokens.FontVTDefault)
	}
	if st == nil {
		return core.View{
			Style: core.Style{
				FlexGrow:   1,
				Background: vtPal.Bg,
				Padding:    core.All(Tokens.Space3),
			},
			Children: []core.Node{
				core.Text{
					Content: "正在连接终端会话…",
					Style: core.Style{
						Color:    vtPal.Muted,
						FontSize: core.Points(fontSize),
					},
				},
			},
		}
	}

	// Native Ghostty surface on platforms that support it (iOS/macOS/Linux/Windows)
	// when not overridden to canvas.
	if gt := st.Ghostty(); gt != nil {
		lightTh, darkTh := GhosttyTerminalThemes()
		chosenLight, chosenDark := lightTh, darkTh
		if vtPal.Bg == DarkVTPalette().Bg {
			chosenLight = darkTh
		}
		return core.View{
			TestID: "vt-surface",
			Style: core.Style{
				FlexGrow:   1,
				Background: vtPal.Bg,
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
						Family: "Menlo",
						Size:   core.Points(fontSize),
					},
					Style: core.Style{FlexGrow: 1},
					TestID: "vt-ghostty",
				},
			},
		}
	}

	// Pure-Go VTScreen Canvas surface (Android arm64 + headless tests).
	cellW := fontSize * 0.62
	cellH := fontSize * 1.40
	padX := float32(8)
	padY := float32(8)

	return core.Scroll{
		Ref:           &st.scrollRef,
		TestID:        "vt-surface",
		MinZoomScale:  0.7,
		MaxZoomScale:  2.2,
		HideIndicator: false,
		Style: core.Style{
			FlexGrow:   1,
			Background: vtPal.Bg,
		},
		OnLayout: func(b core.Bounds) {
			st.mu.Lock()
			st.viewportW = float32(b.Size.Width)
			st.viewportH = float32(b.Size.Height)
			st.mu.Unlock()
		},
		OnScroll: func(ev core.ScrollEvent) {
			if ev.Stale || st.scrolledUp == nil {
				return
			}
			distFromBottom := float32(ev.ContentSize.Height) - (float32(ev.Offset.Y) + float32(ev.Size.Height))
			st.scrolledUp.Set(distFromBottom > 60)
		},
		Children: []core.Node{
			core.Scroll{
				Horizontal: true,
				TestID:     "vt-surface-hscroll",
				Style:      core.Style{Background: vtPal.Bg},
				Children: []core.Node{
					core.Canvas{
						TestID: "vt-canvas",
						StyleFunc: func() core.Style {
							if st.rev != nil {
								_ = st.rev.Get()
							}
							cols, _ := st.screen.Size()
							lines := st.screen.AllLines()
							w := core.Points(padX*2 + float32(cols)*cellW)
							h := core.Points(padY*2 + float32(len(lines))*cellH)
							if h < 220 {
								h = 220
							}
							return core.Style{
								Width:      core.Pt(w),
								Height:     core.Pt(h),
								Background: vtPal.Bg,
							}
						},
						Draw: func(d *core.Drawing, size core.Size) {
							if st.rev != nil {
								_ = st.rev.Get()
							}
							var query string
							var activeMatch int
							if st.searchQuery != nil {
								query = st.searchQuery.Get()
							}
							if st.searchIdx != nil {
								activeMatch = st.searchIdx.Get()
							}
							drawVTScreen(d, size, st.screen, pal, vtPal, fontSize, cellW, cellH, padX, padY, query, activeMatch)
						},
					},
				},
			},
		},
	}
}

func drawVTScreen(
	d *core.Drawing,
	size core.Size,
	screen *VTScreen,
	pal Palette,
	vtPal VTPalette,
	fontSize, cellW, cellH, padX, padY float32,
	searchQuery string,
	activeMatch int,
) {
	// Background fill.
	d.SetFillColor(vtPal.Bg)
	d.BeginPath().Rect(0, 0, float32(size.Width), float32(size.Height)).Fill()

	all := screen.AllLines()
	sbLen := screen.ScrollbackLen()
	if screen.InAltScreen() {
		sbLen = 0
	}
	curCol, curRow, curVis := screen.Cursor()

	// Build match highlight lookup per line if search is active.
	var matches []VTMatch
	if strings.TrimSpace(searchQuery) != "" {
		matches = screen.Search(searchQuery)
	}

	d.SetTextBaseline(core.BaselineTop)
	d.SetTextAlign(core.TextAlignLeft)

	baseFont := core.Font{
		Family: core.FontMonospace,
		Size:   core.Points(fontSize),
	}

	for lineIdx, row := range all {
		y := padY + float32(lineIdx)*cellH

		// Pass 1: cell background rectangles (run-length batched for speed).
		for col := 0; col < len(row); col++ {
			cell := row[col]
			if cell.Width == 0 {
				continue
			}
			fgCol := cell.Style.FG.Resolve(vtPal, true)
			bgCol := cell.Style.BG.Resolve(vtPal, false)
			hasBG := cell.Style.BG.Kind != VTColorDefault
			if cell.Style.Inverse {
				fgCol, bgCol = bgCol, fgCol
				hasBG = true
			}
			_ = fgCol
			if hasBG {
				w := float32(cell.Width) * cellW
				d.SetFillColor(bgCol)
				d.BeginPath().Rect(padX+float32(col)*cellW, y, w, cellH).Fill()
			}
		}

		// Search match highlights on this line.
		for mi, m := range matches {
			if m.Line != lineIdx {
				continue
			}
			hl := pal.WarnSoft
			if mi == activeMatch {
				hl = pal.Warn
			}
			d.SetFillColor(hl)
			d.BeginPath().Rect(padX+float32(m.Col)*cellW, y, float32(m.Length)*cellW, cellH).Fill()
		}

		// Pass 2: run-length batched text runs sharing identical style.
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
			for col < len(row) {
				c := row[col]
				if c.Width == 0 {
					col++
					continue
				}
				if c.Style != runStyle {
					break
				}
				if c.Rune == 0 {
					buf.WriteByte(' ')
				} else {
					buf.WriteRune(c.Rune)
				}
				col++
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

		// Cursor block on the active screen line.
		if curVis && lineIdx == sbLen+curRow {
			d.SetFillColor(vtPal.Cursor)
			d.BeginPath().Rect(padX+float32(curCol)*cellW, y, cellW, cellH).Fill()
		}
	}
}
