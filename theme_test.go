package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
)

func colorHex(c core.Color) string {
	u := uint32(c)
	return fmt.Sprintf("#%06x", (u>>8)&0xffffff)
}

func TestPaletteWCAGContrast(t *testing.T) {
	type palCase struct {
		name string
		p    Palette
		vt   VTPalette
	}
	var palettes []palCase
	for _, preset := range AllThemePresets() {
		palettes = append(palettes,
			palCase{string(preset) + "-light", PaletteFor(preset, false), LightVTPalette()},
			palCase{string(preset) + "-dark", PaletteFor(preset, true), DarkVTPalette()},
		)
	}
	for _, scheme := range AllVTColorSchemes() {
		if scheme == VTSchemeGraphite {
			continue
		}
		palettes = append(palettes, palCase{"vt-" + string(scheme), DarkPalette(), VTPaletteFor(DarkPalette(), VTAlwaysDark, scheme)})
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
				{"running on surface", tc.p.Running, tc.p.Surface, 4.5},
				{"warn on surface", tc.p.Warn, tc.p.Surface, 4.5},
				{"danger on surface", tc.p.Danger, tc.p.Surface, 4.5},
				{"vt fg on vt bg", tc.vt.Fg, tc.vt.Bg, 7.0},
				{"vt muted on vt bg", tc.vt.Muted, tc.vt.Bg, 4.5},
				{"vt accent on vt bg", tc.vt.Accent, tc.vt.Bg, 4.5},
				{"vt blue on vt bg", tc.vt.Blue, tc.vt.Bg, 4.5},
				{"vt amber on vt bg", tc.vt.Amber, tc.vt.Bg, 4.5},
				{"vt red on vt bg", tc.vt.Red, tc.vt.Bg, 4.5},
				{"vt fg on warn-soft search highlight", tc.vt.Fg, Composite(tc.p.WarnSoft, tc.vt.Bg), 4.5},
				{"vt fg on running-soft search highlight", tc.vt.Fg, Composite(tc.p.RunningSoft, tc.vt.Bg), 4.5},
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
	if lp.Bg != core.RGB(0xf5f5f7) || lp.Surface != core.RGB(0xffffff) || lp.Accent != core.RGB(0x18181b) || lp.Running != core.RGB(0x0d7a5f) {
		t.Errorf("light Quiet Studio palette mismatch: bg=%s surface=%s accent=%s running=%s",
			colorHex(lp.Bg), colorHex(lp.Surface), colorHex(lp.Accent), colorHex(lp.Running))
	}
	dp := DarkPalette()
	if dp.Bg != core.RGB(0x0d0d0f) || dp.Surface != core.RGB(0x161619) || dp.Accent != core.RGB(0xf4f4f5) || dp.Running != core.RGB(0x34d399) {
		t.Errorf("dark Quiet Studio palette mismatch: bg=%s surface=%s accent=%s running=%s",
			colorHex(dp.Bg), colorHex(dp.Surface), colorHex(dp.Accent), colorHex(dp.Running))
	}
	lvt := LightVTPalette()
	if lvt.Bg != core.RGB(0xfafafa) || lvt.Accent != core.RGB(0x0d7a5f) || len(lvt.ANSI) != 16 {
		t.Errorf("light VT palette mismatch: bg=%s accent=%s ansi=%d", colorHex(lvt.Bg), colorHex(lvt.Accent), len(lvt.ANSI))
	}
	dvt := DarkVTPalette()
	if dvt.Bg != core.RGB(0x09090b) || dvt.Accent != core.RGB(0x34d399) || len(dvt.ANSI) != 16 {
		t.Errorf("dark VT palette mismatch: bg=%s accent=%s ansi=%d", colorHex(dvt.Bg), colorHex(dvt.Accent), len(dvt.ANSI))
	}

	// Verify strict 4px scale & Quiet Studio sizing tokens.
	if Tokens.PagePadX != 16 || Tokens.SectionGap != 18 || Tokens.RadiusCtrl != 10 || Tokens.RadiusCard != 14 || Tokens.RadiusSheet != 20 {
		t.Errorf("Quiet Studio layout tokens mismatch: %+v", Tokens)
	}
	if Tokens.FontNav != 22 || Tokens.FontSession != 16 || Tokens.FontRowTitle != 15 || Tokens.FontBody != 15 || Tokens.FontCaption != 13 || Tokens.FontMonoMeta != 12 {
		t.Errorf("Quiet Studio font scale mismatch: %+v", Tokens)
	}
	if Tokens.SizeTopbar != 48 || Tokens.SizeTabBar != 56 || Tokens.RailWidth != 56 || Tokens.ChatMaxWidth != 720 {
		t.Errorf("Quiet Studio responsive size tokens mismatch: %+v", Tokens)
	}

	// Verify AppTheme differentiates Accent / AccentHover / AccentPressed / Success.
	at := AppTheme()
	if at.Light.Success != lp.Running || at.Dark.Success != dp.Running {
		t.Errorf("AppTheme Success must map to Running semantic emerald")
	}
	if at.Light.AccentHover == at.Light.Accent || at.Light.AccentPressed == at.Light.Accent {
		t.Errorf("AppTheme Light AccentHover/AccentPressed must differentiate from Accent")
	}
	if at.Dark.AccentHover == at.Dark.Accent || at.Dark.AccentPressed == at.Dark.Accent {
		t.Errorf("AppTheme Dark AccentHover/AccentPressed must differentiate from Accent")
	}
	for _, role := range []string{"track", "knob", "accentFill", "inset", "raised", "code", "line", "field", "fieldBorder"} {
		if at.Light.Colors[role] == 0 {
			t.Errorf("AppTheme Light.Colors[%q] must be populated for ui.Switch / ui.TextView", role)
		}
		if at.Dark.Colors[role] == 0 {
			t.Errorf("AppTheme Dark.Colors[%q] must be populated for ui.Switch / ui.TextView", role)
		}
	}
	if at.Light.Colors["track"] == at.Light.Colors["accentFill"] || at.Dark.Colors["track"] == at.Dark.Colors["accentFill"] {
		t.Errorf("AppTheme switch track and accentFill must contrast clearly")
	}
}

func TestTokenCatalogComplete(t *testing.T) {
	lp, dp := LightPalette(), DarkPalette()
	lvt, dvt := LightVTPalette(), DarkVTPalette()
	entries := TokenCatalog()
	byName := make(map[string]TokenEntry, len(entries))
	for _, e := range entries {
		if e.Name == "" || e.Use == "" || e.Light == "" || e.Dark == "" {
			t.Errorf("incomplete token catalog entry: %+v", e)
		}
		byName[e.Name] = e
	}

	// Guard against catalog drift: every solid Palette & VT token must exist and match exact hexes.
	solidChecks := []struct {
		name  string
		light core.Color
		dark  core.Color
	}{
		{"--color-bg", lp.Bg, dp.Bg},
		{"--color-surface", lp.Surface, dp.Surface},
		{"--color-surface-raised", lp.SurfaceRaised, dp.SurfaceRaised},
		{"--color-fill", lp.Fill, dp.Fill},
		{"--color-text", lp.Text, dp.Text},
		{"--color-text-secondary", lp.TextSecondary, dp.TextSecondary},
		{"--color-text-muted", lp.TextMuted, dp.TextMuted},
		{"--color-accent", lp.Accent, dp.Accent},
		{"--color-accent-fg", lp.AccentFg, dp.AccentFg},
		{"--color-running", lp.Running, dp.Running},
		{"--color-warn", lp.Warn, dp.Warn},
		{"--color-danger", lp.Danger, dp.Danger},
		{"--color-vt-bg", lvt.Bg, dvt.Bg},
		{"--color-vt-fg", lvt.Fg, dvt.Fg},
		{"--color-vt-accent", lvt.Accent, dvt.Accent},
	}
	for _, chk := range solidChecks {
		e, ok := byName[chk.name]
		if !ok {
			t.Errorf("TokenCatalog missing required token %q", chk.name)
			continue
		}
		if strings.ToLower(e.Light) != colorHex(chk.light) {
			t.Errorf("TokenCatalog %s light drift: catalog=%s palette=%s", chk.name, e.Light, colorHex(chk.light))
		}
		if strings.ToLower(e.Dark) != colorHex(chk.dark) {
			t.Errorf("TokenCatalog %s dark drift: catalog=%s palette=%s", chk.name, e.Dark, colorHex(chk.dark))
		}
	}

	// Ensure glass, separator, border, type, space, radius, size, motion tokens are documented too.
	for _, req := range []string{
		"--color-separator", "--color-border", "--color-glass", "--color-glass-border", "--color-overlay",
		"--type-scale", "--space-grid", "--radius-scale", "--size-scale", "--motion-curve",
	} {
		if _, ok := byName[req]; !ok {
			t.Errorf("TokenCatalog missing required entry %q", req)
		}
	}
}

func TestNoRawColorsInScreens(t *testing.T) {
	re := regexp.MustCompile(`\bcore\.RGB(A)?\s*\(`)
	patterns := []string{
		"screen_*.go", "components.go", "icons.go", "main.go", "vt_view.go",
		"mobile_picker.go", "project_browser.go", "browser_preview.go",
		"slash_commands.go", "dictation.go", "prompt_card.go",
	}
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
