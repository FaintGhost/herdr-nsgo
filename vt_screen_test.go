package main

import (
	"strings"
	"testing"
)

func TestVTScreenANSIColorsAndBoxDrawing(t *testing.T) {
	s := NewVTScreen(40, 10)
	// Standard 16-color, 256-color, 24-bit truecolor, bold/underline/inverse, and UTF-8 box drawing + CJK.
	input := "\x1b[1;32mOK\x1b[0m \x1b[38;5;208mOrange\x1b[0m \x1b[38;2;12;34;56;48;2;200;210;220mRGB\x1b[0m\r\n" +
		"┌───┬───┐ 中文\r\n" +
		"\x1b[7mINV\x1b[27m"
	s.Write([]byte(input))

	rows := s.Rows()
	if len(rows) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(rows))
	}

	// Row 0: "OK" is bold + ANSI green (index 2).
	if rows[0][0].Rune != 'O' || !rows[0][0].Style.Bold || rows[0][0].Style.FG.Kind != VTColorANSI || rows[0][0].Style.FG.Index != 2 {
		t.Errorf("unexpected style for 'O': %+v", rows[0][0])
	}
	// "Orange" starts at col 3, 256-color index 208.
	if rows[0][3].Rune != 'O' || rows[0][3].Style.FG.Kind != VTColor256 || rows[0][3].Style.FG.Index != 208 {
		t.Errorf("unexpected style for 256-color 'O': %+v", rows[0][3])
	}
	// "RGB" starts at col 10, truecolor FG(12,34,56) BG(200,210,220).
	rgbCell := rows[0][10]
	if rgbCell.Rune != 'R' || rgbCell.Style.FG.Kind != VTColorRGB || rgbCell.Style.FG.R != 12 || rgbCell.Style.FG.G != 34 || rgbCell.Style.FG.B != 56 {
		t.Errorf("unexpected truecolor FG: %+v", rgbCell)
	}
	if rgbCell.Style.BG.Kind != VTColorRGB || rgbCell.Style.BG.R != 200 || rgbCell.Style.BG.G != 210 || rgbCell.Style.BG.B != 220 {
		t.Errorf("unexpected truecolor BG: %+v", rgbCell)
	}

	// Row 1: Box drawing width 1 and CJK wide width 2 with continuation cell.
	if rows[1][0].Rune != '┌' || rows[1][0].Width != 1 {
		t.Errorf("expected box-drawing '┌' width 1, got %+v", rows[1][0])
	}
	// "┌───┬───┐ " is 10 columns, then '中' at col 10 (width 2) and continuation at col 11 (width 0).
	if rows[1][10].Rune != '中' || rows[1][10].Width != 2 || rows[1][11].Width != 0 {
		t.Errorf("expected wide CJK '中' at col 10..11, got col10=%+v col11=%+v", rows[1][10], rows[1][11])
	}

	// Row 2: Inverse video on "INV".
	if rows[2][0].Rune != 'I' || !rows[2][0].Style.Inverse {
		t.Errorf("expected inverse 'I', got %+v", rows[2][0])
	}
}

func TestVTScreenAltScreenAndScrollback(t *testing.T) {
	s := NewVTScreen(30, 4)
	for i := 1; i <= 8; i++ {
		s.Write([]byte("line-" + string(rune('0'+i)) + "\r\n"))
	}
	if s.ScrollbackLen() < 4 {
		t.Fatalf("expected scrollback >= 4 lines, got %d", s.ScrollbackLen())
	}
	sbBefore := s.ScrollbackLen()

	// Enter alternate screen (?1049h), draw TUI content, then exit (?1049l).
	s.Write([]byte("\x1b[?1049h\x1b[2J\x1b[1;1HClaude TUI Menu\r\nOption 1\r\nOption 2\r\nOption 3\r\nOption 4"))
	if !s.InAltScreen() {
		t.Fatal("expected InAltScreen() == true")
	}
	if !strings.Contains(s.VisibleText(), "Option 4") {
		t.Errorf("expected alt screen text, got %q", s.VisibleText())
	}
	// Alt screen must NOT pollute main scrollback.
	if s.ScrollbackLen() != sbBefore {
		t.Errorf("alt screen mutated scrollback: before=%d after=%d", sbBefore, s.ScrollbackLen())
	}

	// Exit alternate screen restores main buffer.
	s.Write([]byte("\x1b[?1049l"))
	if s.InAltScreen() {
		t.Fatal("expected InAltScreen() == false after ?1049l")
	}
	if !strings.Contains(s.PlainText(), "line-1") || !strings.Contains(s.PlainText(), "line-8") {
		t.Errorf("main buffer/scrollback not restored: %q", s.PlainText())
	}
}

func TestVTScreenSearchAndLinkExtraction(t *testing.T) {
	s := NewVTScreen(80, 10)
	s.Write([]byte(
		"Preview at https://herdr.dev/docs/mobile?ref=1\r\n" +
			"Error in /Volumes/ExternalSSD/workspace/herdr-nsgo/pool.go:142\r\n" +
			"\x1b]8;;https://github.com/nsgo-dev/nsgo\x07nsgo repo\x1b]8;;\x07\r\n" +
			"ERROR: retry failed (error code 500)\r\n",
	))

	// Case-insensitive search.
	matches := s.Search("error")
	if len(matches) != 3 {
		t.Fatalf("expected 3 case-insensitive matches for 'error', got %d: %+v", len(matches), matches)
	}

	links := s.ExtractLinks()
	joined := strings.Join(links, "\n")
	for _, want := range []string{
		"https://herdr.dev/docs/mobile?ref=1",
		"/Volumes/ExternalSSD/workspace/herdr-nsgo/pool.go:142",
		"https://github.com/nsgo-dev/nsgo",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ExtractLinks() missing %q; got %v", want, links)
		}
	}
}

func TestVTScreenResizePreservesContent(t *testing.T) {
	s := NewVTScreen(80, 6)
	s.Write([]byte("header-line\r\nsecond-line\r\nthird-line"))

	s.Resize(58, 12)
	cols, rows := s.Size()
	if cols != 58 || rows != 12 {
		t.Fatalf("Size() = (%d,%d), want (58,12)", cols, rows)
	}
	txt := s.VisibleText()
	for _, want := range []string{"header-line", "second-line", "third-line"} {
		if !strings.Contains(txt, want) {
			t.Errorf("VisibleText() lost %q after resize: %q", want, txt)
		}
	}

	// Malformed / split UTF-8 and truncated CSI sequences must never panic.
	s.Write([]byte("\x1b[999999;999999H\x1b[38;5;999m\x1b]0;unterminated"))
	s.Write([]byte{0xe4, 0xb8}) // split '中' across writes
	s.Write([]byte{0xad})
	s.Write([]byte("\x07")) // terminate OSC
	if !strings.Contains(s.VisibleText(), "header-line") {
		t.Errorf("malformed sequence corrupted screen: %q", s.VisibleText())
	}
}

func TestVTScreenSessionObserveVsActiveResize(t *testing.T) {
	cache := NewFrameCache(24)
	key := TermKey{InstanceID: "kr", Addr: "t1"}
	cache.Put(key, []byte("cached-frame-ready"))

	// Create SessionTerminal in observe=true mode (no Ghostty for headless test).
	st := NewSessionTerminal(nil, key, 140, 40, true, cache, false)
	defer st.Close()

	// Cached frame should appear immediately at 0ms.
	if !strings.Contains(st.Screen().VisibleText(), "cached-frame-ready") {
		t.Fatalf("expected pre-seeded cache frame, got %q", st.Screen().VisibleText())
	}
	if !st.Observe() {
		t.Fatal("expected initial Observe() == true")
	}
	// Remote PTY size stays 140x40 in observe mode.
	cols, rows := st.Screen().Size()
	if cols != 140 || rows != 40 {
		t.Fatalf("expected remote desktop size 140x40 preserved in observe mode, got %dx%d", cols, rows)
	}

	// Switching to active fit-width resizes screen to mobile cols (~58 when viewport unset).
	st.SetObserve(false, 12)
	if st.Observe() {
		t.Fatal("expected Observe() == false after SetObserve(false)")
	}
	cols, _ = st.Screen().Size()
	if cols != 58 {
		t.Fatalf("expected fit cols=58, got %d", cols)
	}
}

