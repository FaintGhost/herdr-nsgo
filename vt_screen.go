package main

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/nsgo-dev/nsgo/core"
)

// VTColorKind identifies how a terminal cell color is encoded.
type VTColorKind uint8

const (
	VTColorDefault VTColorKind = iota
	VTColorANSI                // 0..15 index into VTPalette.ANSI
	VTColor256                 // 0..255 xterm palette
	VTColorRGB                 // 24-bit truecolor
)

// VTColor represents a terminal foreground or background color.
type VTColor struct {
	Kind  VTColorKind
	Index uint8
	R, G, B uint8
}

// Resolve converts VTColor into a concrete core.Color using VTPalette.
func (c VTColor) Resolve(pal VTPalette, isFG bool) core.Color {
	switch c.Kind {
	case VTColorANSI:
		if int(c.Index) < len(pal.ANSI) {
			return pal.ANSI[c.Index]
		}
	case VTColor256:
		return resolveXterm256(c.Index, pal)
	case VTColorRGB:
		return core.RGB(uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B))
	}
	if isFG {
		return pal.Fg
	}
	return pal.Bg
}

func resolveXterm256(idx uint8, pal VTPalette) core.Color {
	if idx < 16 {
		return pal.ANSI[idx]
	}
	if idx < 232 {
		n := int(idx - 16)
		r := xtermCubeLevel(n / 36)
		g := xtermCubeLevel((n / 6) % 6)
		b := xtermCubeLevel(n % 6)
		return core.RGB(uint32(r)<<16 | uint32(g)<<8 | uint32(b))
	}
	gray := uint32(8 + int(idx-232)*10)
	return core.RGB(gray<<16 | gray<<8 | gray)
}

func xtermCubeLevel(v int) uint8 {
	if v <= 0 {
		return 0
	}
	return uint8(55 + v*40)
}

// VTStyle describes text attributes for a single terminal cell.
type VTStyle struct {
	FG, BG        VTColor
	Bold          bool
	Dim           bool
	Italic        bool
	Underline     bool
	Inverse       bool
	Strikethrough bool
}

// VTCell is one grid cell on a VTScreen.
type VTCell struct {
	Rune  rune
	Width uint8 // 1 normal, 2 wide char head, 0 wide char continuation
	Style VTStyle
}

// VTMatch identifies a search match inside a VTScreen.
type VTMatch struct {
	Line   int // 0-based index across scrollback + active screen
	Col    int // 0-based rune column
	Length int
	Text   string
}

type vtParseState uint8

const (
	vtStateGround vtParseState = iota
	vtStateEscape
	vtStateEscapeCharset
	vtStateCSI
	vtStateOSC
	vtStateOSCEsc
	vtStateSkipST
	vtStateSkipSTEsc
)

const (
	defaultMaxScrollback = 2000
	maxVTSeqBytes        = 4096
)

type vtSavedCursor struct {
	col, row int
	style    VTStyle
	pending  bool
}

// VTScreen is a thread-safe pure-Go VT100/xterm-256color/truecolor screen buffer.
// It is used on Android (where libghostty-vt is not bundled in NSGo v0.3.0), in
// headless tests, and as the shadow screen for search & link extraction everywhere.
type VTScreen struct {
	mu sync.RWMutex

	cols, rows    int
	maxScrollback int

	grid       [][]VTCell
	scrollback [][]VTCell

	// Alternate screen buffer state.
	inAlt      bool
	mainGrid   [][]VTCell
	mainCurCol int
	mainCurRow int
	mainCurSt  VTStyle
	mainPend   bool

	curCol, curRow int
	pendingWrap    bool
	cursorVisible  bool
	autoWrap       bool
	style          VTStyle
	saved          vtSavedCursor

	scrollTop    int
	scrollBottom int

	title       string
	oscLinks    []string
	oscLinkSeen map[string]struct{}

	// Incremental parser state.
	state   vtParseState
	csiBuf  []byte
	oscBuf  []byte
	utf8Buf []byte

	seq uint64
}

// NewVTScreen creates a new VTScreen with the given dimensions.
func NewVTScreen(cols, rows int) *VTScreen {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	s := &VTScreen{
		cols:          cols,
		rows:          rows,
		maxScrollback: defaultMaxScrollback,
		cursorVisible: true,
		autoWrap:      true,
		scrollTop:     0,
		scrollBottom:  rows - 1,
		oscLinkSeen:   make(map[string]struct{}),
	}
	s.grid = makeVTGrid(cols, rows, VTStyle{})
	return s
}

func makeVTRow(cols int, st VTStyle) []VTCell {
	row := make([]VTCell, cols)
	for i := range row {
		row[i] = VTCell{Rune: ' ', Width: 1, Style: st}
	}
	return row
}

func makeVTGrid(cols, rows int, st VTStyle) [][]VTCell {
	g := make([][]VTCell, rows)
	for r := 0; r < rows; r++ {
		g[r] = makeVTRow(cols, st)
	}
	return g
}

func cloneVTRow(src []VTCell) []VTCell {
	dst := make([]VTCell, len(src))
	copy(dst, src)
	return dst
}

// Seq returns a monotonically increasing revision counter incremented on writes/resizes.
func (s *VTScreen) Seq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// Size returns current (cols, rows).
func (s *VTScreen) Size() (cols, rows int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cols, s.rows
}

// Cursor returns current (col, row, visible).
func (s *VTScreen) Cursor() (col, row int, visible bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.curCol, s.curRow, s.cursorVisible
}

// Title returns the latest OSC 0/2 window title.
func (s *VTScreen) Title() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.title
}

// InAltScreen reports whether the alternate screen buffer is active.
func (s *VTScreen) InAltScreen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inAlt
}

// ScrollbackLen returns the number of lines stored in scrollback.
func (s *VTScreen) ScrollbackLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.scrollback)
}

// Rows returns a deep copy of the currently visible rows.
func (s *VTScreen) Rows() [][]VTCell {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([][]VTCell, len(s.grid))
	for i := range s.grid {
		out[i] = cloneVTRow(s.grid[i])
	}
	return out
}

// AllLines returns scrollback followed by visible rows (in alt screen, only visible rows).
func (s *VTScreen) AllLines() [][]VTCell {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.inAlt {
		out := make([][]VTCell, len(s.grid))
		for i := range s.grid {
			out[i] = cloneVTRow(s.grid[i])
		}
		return out
	}
	out := make([][]VTCell, 0, len(s.scrollback)+len(s.grid))
	for i := range s.scrollback {
		out = append(out, cloneVTRow(s.scrollback[i]))
	}
	for i := range s.grid {
		out = append(out, cloneVTRow(s.grid[i]))
	}
	return out
}

// Resize updates screen dimensions while preserving existing visible content.
func (s *VTScreen) Resize(cols, rows int) {
	if cols <= 0 {
		cols = 1
	}
	if rows <= 0 {
		rows = 1
	}
	if cols > 512 {
		cols = 512
	}
	if rows > 256 {
		rows = 256
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cols == s.cols && rows == s.rows {
		return
	}
	s.grid = resizeVTGrid(s.grid, cols, rows)
	if s.inAlt && s.mainGrid != nil {
		s.mainGrid = resizeVTGrid(s.mainGrid, cols, rows)
	}
	s.cols = cols
	s.rows = rows
	if s.curCol >= cols {
		s.curCol = cols - 1
	}
	if s.curRow >= rows {
		s.curRow = rows - 1
	}
	s.pendingWrap = false
	s.scrollTop = 0
	s.scrollBottom = rows - 1
	s.seq++
}

func resizeVTGrid(old [][]VTCell, cols, rows int) [][]VTCell {
	ng := makeVTGrid(cols, rows, VTStyle{})
	copyRows := len(old)
	if copyRows > rows {
		copyRows = rows
	}
	for r := 0; r < copyRows; r++ {
		copyCols := len(old[r])
		if copyCols > cols {
			copyCols = cols
		}
		copy(ng[r][:copyCols], old[r][:copyCols])
		// Do not leave a dangling wide-char head on the last column.
		if cols > 0 && ng[r][cols-1].Width == 2 {
			ng[r][cols-1] = VTCell{Rune: ' ', Width: 1, Style: ng[r][cols-1].Style}
		}
	}
	return ng
}

// Write feeds raw VT byte output into the screen emulator.
func (s *VTScreen) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, b := range p {
		s.feedByte(b)
	}
	s.seq++
}

func (s *VTScreen) feedByte(b byte) {
	switch s.state {
	case vtStateGround:
		if len(s.utf8Buf) > 0 {
			if b >= 0x80 && b <= 0xbf {
				s.utf8Buf = append(s.utf8Buf, b)
				if utf8.FullRune(s.utf8Buf) {
					r, _ := utf8.DecodeRune(s.utf8Buf)
					s.utf8Buf = s.utf8Buf[:0]
					s.putRune(r)
				}
				return
			}
			// Invalid continuation byte: flush replacement and handle b normally.
			s.utf8Buf = s.utf8Buf[:0]
		}
		switch {
		case b == 0x1b: // ESC
			s.state = vtStateEscape
		case b == '\r':
			s.curCol = 0
			s.pendingWrap = false
		case b == '\n', b == 0x0b, b == 0x0c:
			s.lineFeed()
		case b == '\b':
			s.pendingWrap = false
			if s.curCol > 0 {
				s.curCol--
			}
		case b == '\t':
			s.pendingWrap = false
			next := ((s.curCol / 8) + 1) * 8
			if next >= s.cols {
				next = s.cols - 1
			}
			s.curCol = next
		case b == 0x07, b == 0x00, b == 0x0e, b == 0x0f:
			// BEL / NUL / SO / SI ignored cleanly.
		case b < 0x20:
			// Other C0 controls ignored.
		case b < 0x80:
			s.putRune(rune(b))
		case b >= 0xc2 && b <= 0xf4:
			s.utf8Buf = append(s.utf8Buf[:0], b)
		default:
			// Stray high byte or C1 8-bit CSI (0x9b).
			if b == 0x9b {
				s.csiBuf = s.csiBuf[:0]
				s.state = vtStateCSI
			}
		}

	case vtStateEscape:
		switch b {
		case '[':
			s.csiBuf = s.csiBuf[:0]
			s.state = vtStateCSI
		case ']':
			s.oscBuf = s.oscBuf[:0]
			s.state = vtStateOSC
		case '(', ')', '*', '+':
			s.state = vtStateEscapeCharset
		case 'P', 'X', '^', '_': // DCS, SOS, PM, APC
			s.state = vtStateSkipST
		case '7': // DECSC
			s.saved = vtSavedCursor{col: s.curCol, row: s.curRow, style: s.style, pending: s.pendingWrap}
			s.state = vtStateGround
		case '8': // DECRC
			s.curCol = clampInt(s.saved.col, 0, s.cols-1)
			s.curRow = clampInt(s.saved.row, 0, s.rows-1)
			s.style = s.saved.style
			s.pendingWrap = s.saved.pending
			s.state = vtStateGround
		case 'D': // IND
			s.lineFeed()
			s.state = vtStateGround
		case 'E': // NEL
			s.curCol = 0
			s.lineFeed()
			s.state = vtStateGround
		case 'M': // RI (reverse index)
			s.reverseIndex()
			s.state = vtStateGround
		case 'c': // RIS (full reset)
			s.reset()
			s.state = vtStateGround
		default:
			s.state = vtStateGround
		}

	case vtStateEscapeCharset:
		s.state = vtStateGround

	case vtStateCSI:
		if b == 0x1b {
			s.state = vtStateEscape
			return
		}
		if b >= 0x40 && b <= 0x7e {
			s.handleCSI(b)
			s.csiBuf = s.csiBuf[:0]
			s.state = vtStateGround
			return
		}
		if len(s.csiBuf) < maxVTSeqBytes {
			s.csiBuf = append(s.csiBuf, b)
		} else {
			// Guard against runaway malformed CSI.
			s.csiBuf = s.csiBuf[:0]
			s.state = vtStateGround
		}

	case vtStateOSC:
		switch b {
		case 0x07: // BEL terminates OSC
			s.handleOSC()
			s.oscBuf = s.oscBuf[:0]
			s.state = vtStateGround
		case 0x1b:
			s.state = vtStateOSCEsc
		default:
			if len(s.oscBuf) < maxVTSeqBytes {
				s.oscBuf = append(s.oscBuf, b)
			}
		}

	case vtStateOSCEsc:
		if b == '\\' {
			s.handleOSC()
			s.oscBuf = s.oscBuf[:0]
			s.state = vtStateGround
		} else {
			s.oscBuf = s.oscBuf[:0]
			s.state = vtStateGround
			s.feedByte(b)
		}

	case vtStateSkipST:
		if b == 0x07 {
			s.state = vtStateGround
		} else if b == 0x1b {
			s.state = vtStateSkipSTEsc
		}

	case vtStateSkipSTEsc:
		if b == '\\' {
			s.state = vtStateGround
		} else {
			s.state = vtStateSkipST
		}
	}
}

func (s *VTScreen) reset() {
	s.inAlt = false
	s.mainGrid = nil
	s.grid = makeVTGrid(s.cols, s.rows, VTStyle{})
	s.curCol, s.curRow = 0, 0
	s.pendingWrap = false
	s.cursorVisible = true
	s.autoWrap = true
	s.style = VTStyle{}
	s.scrollTop = 0
	s.scrollBottom = s.rows - 1
}

func (s *VTScreen) putRune(r rune) {
	w := runeDisplayWidth(r)
	if w == 0 {
		// Combining mark: ignore for cell grid (do not advance cursor).
		return
	}
	if s.pendingWrap {
		if s.autoWrap {
			s.curCol = 0
			s.lineFeed()
		}
		s.pendingWrap = false
	}
	if w == 2 && s.curCol == s.cols-1 {
		if s.autoWrap {
			s.grid[s.curRow][s.curCol] = VTCell{Rune: ' ', Width: 1, Style: s.style}
			s.curCol = 0
			s.lineFeed()
		} else {
			return
		}
	}

	row := s.grid[s.curRow]
	// Clean broken wide glyph if overwriting its continuation or head.
	if row[s.curCol].Width == 0 && s.curCol > 0 {
		row[s.curCol-1] = VTCell{Rune: ' ', Width: 1, Style: row[s.curCol-1].Style}
	}
	if row[s.curCol].Width == 2 && s.curCol+1 < s.cols {
		row[s.curCol+1] = VTCell{Rune: ' ', Width: 1, Style: row[s.curCol+1].Style}
	}

	row[s.curCol] = VTCell{Rune: r, Width: w, Style: s.style}
	if w == 2 && s.curCol+1 < s.cols {
		if row[s.curCol+1].Width == 2 && s.curCol+2 < s.cols {
			row[s.curCol+2] = VTCell{Rune: ' ', Width: 1, Style: row[s.curCol+2].Style}
		}
		row[s.curCol+1] = VTCell{Rune: 0, Width: 0, Style: s.style}
	}

	if s.curCol+int(w) >= s.cols {
		s.curCol = s.cols - 1
		s.pendingWrap = true
	} else {
		s.curCol += int(w)
	}
}

func (s *VTScreen) lineFeed() {
	s.pendingWrap = false
	if s.curRow == s.scrollBottom {
		s.scrollUp(1)
	} else if s.curRow < s.rows-1 {
		s.curRow++
	}
}

func (s *VTScreen) reverseIndex() {
	s.pendingWrap = false
	if s.curRow == s.scrollTop {
		s.scrollDown(1)
	} else if s.curRow > 0 {
		s.curRow--
	}
}

func (s *VTScreen) scrollUp(n int) {
	if n <= 0 {
		return
	}
	top, bot := s.scrollTop, s.scrollBottom
	if top < 0 || bot >= s.rows || top > bot {
		top, bot = 0, s.rows-1
	}
	height := bot - top + 1
	if n > height {
		n = height
	}
	// Save evicted top lines into scrollback only for full-top main screen scrolls.
	if !s.inAlt && top == 0 {
		for i := 0; i < n; i++ {
			s.appendScrollback(s.grid[i])
		}
	}
	for r := top; r <= bot-n; r++ {
		s.grid[r] = s.grid[r+n]
	}
	blankStyle := VTStyle{BG: s.style.BG}
	for r := bot - n + 1; r <= bot; r++ {
		s.grid[r] = makeVTRow(s.cols, blankStyle)
	}
}

func (s *VTScreen) scrollDown(n int) {
	if n <= 0 {
		return
	}
	top, bot := s.scrollTop, s.scrollBottom
	if top < 0 || bot >= s.rows || top > bot {
		top, bot = 0, s.rows-1
	}
	height := bot - top + 1
	if n > height {
		n = height
	}
	for r := bot; r >= top+n; r-- {
		s.grid[r] = s.grid[r-n]
	}
	blankStyle := VTStyle{BG: s.style.BG}
	for r := top; r < top+n; r++ {
		s.grid[r] = makeVTRow(s.cols, blankStyle)
	}
}

func (s *VTScreen) appendScrollback(row []VTCell) {
	if s.maxScrollback <= 0 {
		return
	}
	s.scrollback = append(s.scrollback, cloneVTRow(row))
	if len(s.scrollback) > s.maxScrollback {
		drop := len(s.scrollback) - s.maxScrollback
		s.scrollback = s.scrollback[drop:]
	}
}

func (s *VTScreen) enterAltScreen(saveCur bool) {
	if s.inAlt {
		return
	}
	s.mainGrid = s.grid
	if saveCur {
		s.mainCurCol = s.curCol
		s.mainCurRow = s.curRow
		s.mainCurSt = s.style
		s.mainPend = s.pendingWrap
	}
	s.inAlt = true
	s.grid = makeVTGrid(s.cols, s.rows, VTStyle{})
	s.curCol, s.curRow = 0, 0
	s.pendingWrap = false
	s.scrollTop = 0
	s.scrollBottom = s.rows - 1
}

func (s *VTScreen) exitAltScreen(restoreCur bool) {
	if !s.inAlt {
		return
	}
	s.inAlt = false
	if s.mainGrid != nil {
		s.grid = resizeVTGrid(s.mainGrid, s.cols, s.rows)
		s.mainGrid = nil
	}
	if restoreCur {
		s.curCol = clampInt(s.mainCurCol, 0, s.cols-1)
		s.curRow = clampInt(s.mainCurRow, 0, s.rows-1)
		s.style = s.mainCurSt
		s.pendingWrap = s.mainPend
	}
	s.scrollTop = 0
	s.scrollBottom = s.rows - 1
}

func (s *VTScreen) handleOSC() {
	raw := string(s.oscBuf)
	codeStr, rest, ok := strings.Cut(raw, ";")
	if !ok {
		return
	}
	switch codeStr {
	case "0", "2":
		s.title = strings.TrimSpace(rest)
	case "8":
		// OSC 8 ; params ; URI
		_, uri, hasURI := strings.Cut(rest, ";")
		if hasURI {
			uri = strings.TrimSpace(uri)
			if uri != "" {
				if _, exists := s.oscLinkSeen[uri]; !exists {
					s.oscLinkSeen[uri] = struct{}{}
					s.oscLinks = append(s.oscLinks, uri)
					if len(s.oscLinks) > 200 {
						s.oscLinks = s.oscLinks[len(s.oscLinks)-200:]
					}
				}
			}
		}
	}
}

func (s *VTScreen) handleCSI(final byte) {
	raw := string(s.csiBuf)
	private := false
	if strings.HasPrefix(raw, "?") {
		private = true
		raw = raw[1:]
	} else if strings.HasPrefix(raw, ">") || strings.HasPrefix(raw, "!") || strings.HasPrefix(raw, "=") {
		// Secondary DA / soft reset / etc.: safely ignored.
		return
	}
	// Strip trailing intermediate bytes (0x20..0x2f) such as DECSCUSR `CSI 2 SP q`.
	raw = strings.TrimRightFunc(raw, func(r rune) bool { return r >= 0x20 && r <= 0x2f })

	if private {
		s.handlePrivateCSI(final, parseCSIParams(raw))
		return
	}

	params := parseCSIParams(raw)
	param := func(idx, def int) int {
		if idx < len(params) && params[idx] > 0 {
			return params[idx]
		}
		return def
	}
	paramZero := func(idx, def int) int {
		if idx < len(params) {
			return params[idx]
		}
		return def
	}

	switch final {
	case 'A': // CUU
		s.pendingWrap = false
		s.curRow = clampInt(s.curRow-param(0, 1), 0, s.rows-1)
	case 'B', 'e': // CUD
		s.pendingWrap = false
		s.curRow = clampInt(s.curRow+param(0, 1), 0, s.rows-1)
	case 'C', 'a': // CUF
		s.pendingWrap = false
		s.curCol = clampInt(s.curCol+param(0, 1), 0, s.cols-1)
	case 'D': // CUB
		s.pendingWrap = false
		s.curCol = clampInt(s.curCol-param(0, 1), 0, s.cols-1)
	case 'E': // CNL
		s.pendingWrap = false
		s.curCol = 0
		s.curRow = clampInt(s.curRow+param(0, 1), 0, s.rows-1)
	case 'F': // CPL
		s.pendingWrap = false
		s.curCol = 0
		s.curRow = clampInt(s.curRow-param(0, 1), 0, s.rows-1)
	case 'G', '`': // CHA
		s.pendingWrap = false
		s.curCol = clampInt(param(0, 1)-1, 0, s.cols-1)
	case 'd': // VPA
		s.pendingWrap = false
		s.curRow = clampInt(param(0, 1)-1, 0, s.rows-1)
	case 'H', 'f': // CUP
		s.pendingWrap = false
		s.curRow = clampInt(param(0, 1)-1, 0, s.rows-1)
		s.curCol = clampInt(param(1, 1)-1, 0, s.cols-1)
	case 'J': // ED
		s.eraseDisplay(paramZero(0, 0))
	case 'K': // EL
		s.eraseLine(paramZero(0, 0))
	case 'L': // IL
		s.insertLines(param(0, 1))
	case 'M': // DL
		s.deleteLines(param(0, 1))
	case '@': // ICH
		s.insertChars(param(0, 1))
	case 'P': // DCH
		s.deleteChars(param(0, 1))
	case 'X': // ECH
		s.eraseChars(param(0, 1))
	case 'S': // SU
		s.scrollUp(param(0, 1))
	case 'T': // SD
		s.scrollDown(param(0, 1))
	case 'r': // DECSTBM
		top := clampInt(param(0, 1)-1, 0, s.rows-1)
		bot := clampInt(param(1, s.rows)-1, 0, s.rows-1)
		if top < bot {
			s.scrollTop = top
			s.scrollBottom = bot
		} else {
			s.scrollTop = 0
			s.scrollBottom = s.rows - 1
		}
		s.curCol, s.curRow = 0, 0
		s.pendingWrap = false
	case 's':
		s.saved = vtSavedCursor{col: s.curCol, row: s.curRow, style: s.style, pending: s.pendingWrap}
	case 'u':
		s.curCol = clampInt(s.saved.col, 0, s.cols-1)
		s.curRow = clampInt(s.saved.row, 0, s.rows-1)
		s.style = s.saved.style
		s.pendingWrap = s.saved.pending
	case 'm': // SGR
		s.applySGR(raw)
	}
}

func (s *VTScreen) handlePrivateCSI(final byte, params []int) {
	if final != 'h' && final != 'l' {
		return
	}
	set := final == 'h'
	for _, p := range params {
		switch p {
		case 7:
			s.autoWrap = set
		case 25:
			s.cursorVisible = set
		case 47, 1047:
			if set {
				s.enterAltScreen(false)
			} else {
				s.exitAltScreen(false)
			}
		case 1049:
			if set {
				s.enterAltScreen(true)
			} else {
				s.exitAltScreen(true)
			}
		}
	}
}

func (s *VTScreen) eraseDisplay(mode int) {
	blank := VTCell{Rune: ' ', Width: 1, Style: VTStyle{BG: s.style.BG}}
	switch mode {
	case 0: // cursor to end
		for c := s.curCol; c < s.cols; c++ {
			s.grid[s.curRow][c] = blank
		}
		for r := s.curRow + 1; r < s.rows; r++ {
			s.grid[r] = makeVTRow(s.cols, blank.Style)
		}
	case 1: // start to cursor
		for r := 0; r < s.curRow; r++ {
			s.grid[r] = makeVTRow(s.cols, blank.Style)
		}
		for c := 0; c <= s.curCol && c < s.cols; c++ {
			s.grid[s.curRow][c] = blank
		}
	case 2, 3: // entire screen (3 also clears scrollback)
		s.grid = makeVTGrid(s.cols, s.rows, blank.Style)
		if mode == 3 && !s.inAlt {
			s.scrollback = nil
		}
	}
}

func (s *VTScreen) eraseLine(mode int) {
	blank := VTCell{Rune: ' ', Width: 1, Style: VTStyle{BG: s.style.BG}}
	row := s.grid[s.curRow]
	switch mode {
	case 0:
		for c := s.curCol; c < s.cols; c++ {
			row[c] = blank
		}
	case 1:
		for c := 0; c <= s.curCol && c < s.cols; c++ {
			row[c] = blank
		}
	case 2:
		for c := 0; c < s.cols; c++ {
			row[c] = blank
		}
	}
}

func (s *VTScreen) insertLines(n int) {
	if s.curRow < s.scrollTop || s.curRow > s.scrollBottom {
		return
	}
	oldTop := s.scrollTop
	s.scrollTop = s.curRow
	s.scrollDown(n)
	s.scrollTop = oldTop
}

func (s *VTScreen) deleteLines(n int) {
	if s.curRow < s.scrollTop || s.curRow > s.scrollBottom {
		return
	}
	oldTop := s.scrollTop
	s.scrollTop = s.curRow
	s.scrollUp(n)
	s.scrollTop = oldTop
}

func (s *VTScreen) insertChars(n int) {
	if n <= 0 || s.curCol >= s.cols {
		return
	}
	row := s.grid[s.curRow]
	if n > s.cols-s.curCol {
		n = s.cols - s.curCol
	}
	copy(row[s.curCol+n:], row[s.curCol:s.cols-n])
	blank := VTCell{Rune: ' ', Width: 1, Style: VTStyle{BG: s.style.BG}}
	for c := s.curCol; c < s.curCol+n; c++ {
		row[c] = blank
	}
}

func (s *VTScreen) deleteChars(n int) {
	if n <= 0 || s.curCol >= s.cols {
		return
	}
	row := s.grid[s.curRow]
	if n > s.cols-s.curCol {
		n = s.cols - s.curCol
	}
	copy(row[s.curCol:], row[s.curCol+n:])
	blank := VTCell{Rune: ' ', Width: 1, Style: VTStyle{BG: s.style.BG}}
	for c := s.cols - n; c < s.cols; c++ {
		row[c] = blank
	}
}

func (s *VTScreen) eraseChars(n int) {
	if n <= 0 || s.curCol >= s.cols {
		return
	}
	row := s.grid[s.curRow]
	blank := VTCell{Rune: ' ', Width: 1, Style: VTStyle{BG: s.style.BG}}
	end := s.curCol + n
	if end > s.cols {
		end = s.cols
	}
	for c := s.curCol; c < end; c++ {
		row[c] = blank
	}
}

func (s *VTScreen) applySGR(raw string) {
	if raw == "" {
		s.style = VTStyle{}
		return
	}
	// Normalize sub-parameter colons (e.g. 38:2::R:G:B or 38:5:N) into tokens.
	normalized := strings.ReplaceAll(raw, ":", ";")
	params := parseCSIParams(normalized)
	if len(params) == 0 {
		s.style = VTStyle{}
		return
	}

	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			s.style = VTStyle{}
		case p == 1:
			s.style.Bold = true
		case p == 2:
			s.style.Dim = true
		case p == 3:
			s.style.Italic = true
		case p == 4:
			s.style.Underline = true
		case p == 7:
			s.style.Inverse = true
		case p == 9:
			s.style.Strikethrough = true
		case p == 22:
			s.style.Bold = false
			s.style.Dim = false
		case p == 23:
			s.style.Italic = false
		case p == 24:
			s.style.Underline = false
		case p == 27:
			s.style.Inverse = false
		case p == 29:
			s.style.Strikethrough = false
		case p >= 30 && p <= 37:
			s.style.FG = VTColor{Kind: VTColorANSI, Index: uint8(p - 30)}
		case p == 39:
			s.style.FG = VTColor{Kind: VTColorDefault}
		case p >= 40 && p <= 47:
			s.style.BG = VTColor{Kind: VTColorANSI, Index: uint8(p - 40)}
		case p == 49:
			s.style.BG = VTColor{Kind: VTColorDefault}
		case p >= 90 && p <= 97:
			s.style.FG = VTColor{Kind: VTColorANSI, Index: uint8(p - 90 + 8)}
		case p >= 100 && p <= 107:
			s.style.BG = VTColor{Kind: VTColorANSI, Index: uint8(p - 100 + 8)}
		case p == 38 || p == 48:
			isFG := p == 38
			if i+1 >= len(params) {
				continue
			}
			mode := params[i+1]
			if mode == 5 && i+2 < len(params) {
				idx := clampInt(params[i+2], 0, 255)
				col := VTColor{Kind: VTColor256, Index: uint8(idx)}
				if isFG {
					s.style.FG = col
				} else {
					s.style.BG = col
				}
				i += 2
			} else if mode == 2 && i+4 < len(params) {
				r := uint8(clampInt(params[i+2], 0, 255))
				g := uint8(clampInt(params[i+3], 0, 255))
				b := uint8(clampInt(params[i+4], 0, 255))
				col := VTColor{Kind: VTColorRGB, R: r, G: g, B: b}
				if isFG {
					s.style.FG = col
				} else {
					s.style.BG = col
				}
				i += 4
			}
		}
	}
}

func parseCSIParams(raw string) []int {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ";")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			out = append(out, 0)
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, v)
	}
	return out
}

func rowToString(row []VTCell) string {
	var b strings.Builder
	b.Grow(len(row))
	for _, c := range row {
		if c.Width == 0 {
			continue
		}
		if c.Rune == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(c.Rune)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// VisibleText returns the visible grid lines joined by '\n'.
func (s *VTScreen) VisibleText() string {
	rows := s.Rows()
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = rowToString(r)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// PlainText returns scrollback + visible rows joined by '\n'.
func (s *VTScreen) PlainText() string {
	all := s.AllLines()
	lines := make([]string, len(all))
	for i, r := range all {
		lines[i] = rowToString(r)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// Search performs a case-insensitive search over scrollback + visible rows.
func (s *VTScreen) Search(query string) []VTMatch {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	qRunes := []rune(q)
	all := s.AllLines()
	var matches []VTMatch
	for lineIdx, row := range all {
		text := rowToString(row)
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		lowerRunes := []rune(lower)
		origRunes := []rune(text)
		if len(lowerRunes) < len(qRunes) {
			continue
		}
		for i := 0; i <= len(lowerRunes)-len(qRunes); i++ {
			ok := true
			for j := 0; j < len(qRunes); j++ {
				if lowerRunes[i+j] != qRunes[j] {
					ok = false
					break
				}
			}
			if ok {
				end := i + len(qRunes)
				if end > len(origRunes) {
					end = len(origRunes)
				}
				matches = append(matches, VTMatch{
					Line:   lineIdx,
					Col:    i,
					Length: len(qRunes),
					Text:   string(origRunes[i:end]),
				})
			}
		}
	}
	return matches
}

var (
	vtURLPattern  = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `\)\]]+|file://[^\s<>"'` + "`" + `\)\]]+`)
	vtPathPattern = regexp.MustCompile(`(?:^|[\s(\['"])((?:/|\./|\.\./|~/)[A-Za-z0-9._\-/]+(?:\.[A-Za-z0-9]{1,12})(?::\d+(?::\d+)?)?)`)
)

// ExtractLinks returns deduplicated HTTP/file URLs, OSC 8 hyperlinks, and file paths found in the buffer.
func (s *VTScreen) ExtractLinks() []string {
	s.mu.RLock()
	oscCopy := make([]string, len(s.oscLinks))
	copy(oscCopy, s.oscLinks)
	s.mu.RUnlock()

	text := s.PlainText()
	seen := make(map[string]struct{})
	var out []string
	add := func(link string) {
		link = strings.TrimRight(strings.TrimSpace(link), ".,;:!?)")
		if link == "" {
			return
		}
		if _, ok := seen[link]; ok {
			return
		}
		seen[link] = struct{}{}
		out = append(out, link)
	}

	for _, u := range oscCopy {
		add(u)
	}
	for _, m := range vtURLPattern.FindAllString(text, -1) {
		add(m)
	}
	for _, sm := range vtPathPattern.FindAllStringSubmatch(text, -1) {
		if len(sm) > 1 {
			add(sm[1])
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// runeDisplayWidth returns terminal column width: 0 combining, 1 narrow/box/powerline, 2 CJK/wide/emoji.
func runeDisplayWidth(r rune) uint8 {
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200d || (r >= 0xfe00 && r <= 0xfe0f) {
		return 0
	}
	// Box drawing (2500-257F), block elements (2580-259F), Powerline (E0A0-E0D7) are single-width.
	if (r >= 0x2500 && r <= 0x259f) || (r >= 0xe0a0 && r <= 0xe0d7) {
		return 1
	}
	// East Asian Wide / Fullwidth + common Emoji blocks.
	switch {
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0x303e,
		r >= 0x3041 && r <= 0x33ff,
		r >= 0x3400 && r <= 0x4dbf,
		r >= 0x4e00 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe10 && r <= 0xfe6f,
		r >= 0xff01 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1faff,
		r >= 0x20000 && r <= 0x2fffd:
		return 2
	}
	return 1
}
