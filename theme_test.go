package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
)

func TestPaletteWCAGContrast(t *testing.T) {
	palettes := []struct {
		name string
		p    Palette
		vt   VTPalette
	}{
		{"light", LightPalette(), LightVTPalette()},
		{"dark", DarkPalette(), DarkVTPalette()},
	}
	for _, tc := range palettes {
		t.Run(tc.name, func(t *testing.T) {
			pairs := []struct {
				label string
				fg    core.Color
				bg    core.Color
				min   float64
			}{
				{"text on bg", tc.p.Text, tc.p.Bg, 4.5},
				{"text on surface", tc.p.Text, tc.p.Surface, 4.5},
				{"text-secondary on surface", tc.p.TextSecondary, tc.p.Surface, 4.5},
				{"text-muted on surface", tc.p.TextMuted, tc.p.Surface, 4.5},
				{"text-muted on bg", tc.p.TextMuted, tc.p.Bg, 4.5},
				{"accent on surface", tc.p.Accent, tc.p.Surface, 4.5},
				{"accent-fg on accent", tc.p.AccentFg, tc.p.Accent, 4.5},
				{"warn on surface", tc.p.Warn, tc.p.Surface, 4.5},
				{"danger on surface", tc.p.Danger, tc.p.Surface, 4.5},
				{"vt fg on vt bg", tc.vt.Fg, tc.vt.Bg, 7.0},
				{"vt muted on vt bg", tc.vt.Muted, tc.vt.Bg, 4.5},
				{"vt accent on vt bg", tc.vt.Accent, tc.vt.Bg, 4.5},
				{"vt blue on vt bg", tc.vt.Blue, tc.vt.Bg, 4.5},
				{"vt amber on vt bg", tc.vt.Amber, tc.vt.Bg, 4.5},
				{"vt red on vt bg", tc.vt.Red, tc.vt.Bg, 4.5},
			}
			for _, pair := range pairs {
				ratio := ContrastRatio(pair.fg, pair.bg)
				if ratio < pair.min {
					t.Errorf("%s: contrast %.2f < %.1f (fg=%#08x bg=%#08x)", pair.label, ratio, pair.min, uint32(pair.fg), uint32(pair.bg))
				}
			}
		})
	}
}

func TestThemePaletteColorsMatchSpec(t *testing.T) {
	lp := LightPalette()
	if lp.Bg != core.RGB(0xf7f8fa) || lp.Accent != core.RGB(0x0f766e) {
		t.Errorf("light palette mismatch: bg=%#08x accent=%#08x", uint32(lp.Bg), uint32(lp.Accent))
	}
	dp := DarkPalette()
	if dp.Bg != core.RGB(0x0c0f12) || dp.Accent != core.RGB(0x2dd4bf) {
		t.Errorf("dark palette mismatch: bg=%#08x accent=%#08x", uint32(dp.Bg), uint32(dp.Accent))
	}
	lvt := LightVTPalette()
	if lvt.Bg != core.RGB(0xfafbfc) || len(lvt.ANSI) != 16 {
		t.Errorf("light VT palette invalid: bg=%#08x ansi=%d", uint32(lvt.Bg), len(lvt.ANSI))
	}
	dvt := DarkVTPalette()
	if dvt.Bg != core.RGB(0x070a0d) || len(dvt.ANSI) != 16 {
		t.Errorf("dark VT palette invalid: bg=%#08x ansi=%d", uint32(dvt.Bg), len(dvt.ANSI))
	}
	// Verify every token is listed for the runtime tokens gallery.
	entries := TokenCatalog()
	if len(entries) < 20 {
		t.Errorf("TokenCatalog() has %d entries, want >= 20", len(entries))
	}
	for _, e := range entries {
		if e.Name == "" || e.Use == "" || e.Light == "" || e.Dark == "" {
			t.Errorf("incomplete token catalog entry: %+v", e)
		}
	}
}

func TestNoRawColorsInScreens(t *testing.T) {
	re := regexp.MustCompile(`\bcore\.RGB(A)?\s*\(`)
	patterns := []string{"screen_*.go", "components.go"}
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil {
			t.Fatalf("glob %s: %v", pat, err)
		}
		for _, f := range matches {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			if loc := re.FindString(string(data)); loc != "" {
				t.Errorf("%s contains raw color call %q; use ThemeOf(cx) tokens from theme.go", f, loc)
			}
		}
	}
}
