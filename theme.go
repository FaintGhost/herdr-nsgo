package main

import (
	"math"

	"github.com/nsgo-dev/nsgo/base/terminal"
	"github.com/nsgo-dev/nsgo/core"
)

// ThemeMode selects the application appearance.
type ThemeMode string

const (
	ThemeLight  ThemeMode = "light"
	ThemeDark   ThemeMode = "dark"
	ThemeSystem ThemeMode = "system"
)

// ThemePreset selects one of the 4 complete Light/Dark workspace palettes.
type ThemePreset string

const (
	PresetStudio  ThemePreset = "studio"
	PresetPaper   ThemePreset = "paper"
	PresetNordic  ThemePreset = "nordic"
	PresetEmerald ThemePreset = "emerald"
)

func AllThemePresets() []ThemePreset {
	return []ThemePreset{PresetStudio, PresetPaper, PresetNordic, PresetEmerald}
}

func (p ThemePreset) Label() string {
	switch p {
	case PresetPaper:
		return "Warm Paper"
	case PresetNordic:
		return "Nordic Slate"
	case PresetEmerald:
		return "Emerald Lab"
	default:
		return "Quiet Studio"
	}
}

func (p ThemePreset) Desc() string {
	switch p {
	case PresetPaper:
		return "纸墨暖调护眼"
	case PresetNordic:
		return "北欧蓝灰冷静"
	case PresetEmerald:
		return "翠绿极客工坊"
	default:
		return "纯石墨极简"
	}
}

// VTThemeMode selects whether the VT terminal canvas follows the app theme or stays dark.
type VTThemeMode string

const (
	VTFollowTheme VTThemeMode = "follow"
	VTAlwaysDark  VTThemeMode = "dark"
)

// VTColorScheme selects the terminal ANSI & background palette.
type VTColorScheme string

const (
	VTSchemeGraphite   VTColorScheme = "graphite"
	VTSchemeTokyo      VTColorScheme = "tokyo"
	VTSchemeCatppuccin VTColorScheme = "catppuccin"
	VTSchemeGruvbox    VTColorScheme = "gruvbox"
)

func AllVTColorSchemes() []VTColorScheme {
	return []VTColorScheme{VTSchemeGraphite, VTSchemeTokyo, VTSchemeCatppuccin, VTSchemeGruvbox}
}

func (s VTColorScheme) Label() string {
	switch s {
	case VTSchemeTokyo:
		return "Tokyo Night"
	case VTSchemeCatppuccin:
		return "Catppuccin"
	case VTSchemeGruvbox:
		return "Gruvbox"
	default:
		return "Graphite"
	}
}

// Palette holds every semantic color role from the Quiet Studio design system.
// No UI file may construct raw core.RGB/core.RGBA colors directly; all colors come from here.
type Palette struct {
	Dark bool

	Bg            core.Color
	Surface       core.Color
	SurfaceRaised core.Color
	Fill          core.Color
	Separator     core.Color
	Border        core.Color

	Text          core.Color
	TextSecondary core.Color
	TextMuted     core.Color

	Accent        core.Color
	AccentHover   core.Color
	AccentPressed core.Color
	AccentFg      core.Color
	AccentSoft    core.Color

	Running     core.Color
	RunningSoft core.Color
	Warn        core.Color
	WarnSoft    core.Color
	Danger      core.Color
	DangerSoft  core.Color

	GlassSurface core.Color
	GlassBorder  core.Color
	Overlay      core.Color
}

// VTPalette holds the terminal canvas background, foreground, cursor, and 16-color ANSI table.
type VTPalette struct {
	Bg     core.Color
	Fg     core.Color
	Muted  core.Color
	Accent core.Color
	Blue   core.Color
	Amber  core.Color
	Red    core.Color
	Cursor core.Color
	ANSI   [16]core.Color
}

// Tokens holds layout, spacing, radius, control size, and typography constants on the 4px grid.
var Tokens = struct {
	Space1, Space2, Space3, Space4, Space5, Space6 core.Points
	PagePadX                                       core.Points
	SectionGap                                     core.Points
	RowGap                                         core.Points

	RadiusKey, RadiusCtrl, RadiusCard, RadiusSheet, RadiusPill core.Points

	SizeTouch, SizeKey, SizeIcon, SizeTopbar, SizeTabBar core.Points
	WideBreakpoint                                       core.Points
	RailWidth                                            core.Points
	SidebarWidth                                         core.Points
	ChatMaxWidth                                         core.Points

	FontNav, FontSession, FontRowTitle, FontBody, FontCaption, FontSection, FontMonoMeta, FontVTDefault core.Points
}{
	Space1: 4, Space2: 8, Space3: 12, Space4: 16, Space5: 20, Space6: 24,
	PagePadX:   16,
	SectionGap: 18,
	RowGap:     8,

	RadiusKey: 6, RadiusCtrl: 10, RadiusCard: 14, RadiusSheet: 20, RadiusPill: 999,

	SizeTouch: 40, SizeKey: 34, SizeIcon: 19, SizeTopbar: 48, SizeTabBar: 56,
	WideBreakpoint: 680,
	RailWidth:      56,
	SidebarWidth:   280,
	ChatMaxWidth:   720,

	FontNav: 22, FontSession: 16, FontRowTitle: 15, FontBody: 15, FontCaption: 13, FontSection: 12, FontMonoMeta: 12, FontVTDefault: 12,
}

// LightPalette returns the Quiet Studio Pure Graphite light palette (tok-color-mono).
func LightPalette() Palette {
	return Palette{
		Dark:          false,
		Bg:            core.RGB(0xf5f5f7),
		Surface:       core.RGB(0xffffff),
		SurfaceRaised: core.RGB(0xebebf0),
		Fill:          core.RGB(0xdcdce3),
		Separator:     core.RGBA(0x141416, 0.075),
		Border:        core.RGBA(0x141416, 0.085),
		Text:          core.RGB(0x141416),
		TextSecondary: core.RGB(0x52525b),
		TextMuted:     core.RGB(0x686873),
		Accent:        core.RGB(0x18181b),
		AccentHover:   core.RGB(0x27272a),
		AccentPressed: core.RGB(0x09090b),
		AccentFg:      core.RGB(0xffffff),
		AccentSoft:    core.RGBA(0x18181b, 0.08),
		Running:       core.RGB(0x0d7a5f),
		RunningSoft:   core.RGBA(0x0d7a5f, 0.11),
		Warn:          core.RGB(0xa64b00),
		WarnSoft:      core.RGBA(0xa64b00, 0.10),
		Danger:        core.RGB(0xc81e1e),
		DangerSoft:    core.RGBA(0xc81e1e, 0.10),
		GlassSurface:  core.RGBA(0xffffff, 0.84),
		GlassBorder:   core.RGBA(0x141416, 0.09),
		Overlay:       core.RGBA(0x141416, 0.34),
	}
}

// DarkPalette returns the paired Quiet Studio True Graphite dark palette (tok-color-mono dark).
func DarkPalette() Palette {
	return Palette{
		Dark:          true,
		Bg:            core.RGB(0x0d0d0f),
		Surface:       core.RGB(0x161619),
		SurfaceRaised: core.RGB(0x222226),
		Fill:          core.RGB(0x2e2e34),
		Separator:     core.RGBA(0xf4f4f5, 0.08),
		Border:        core.RGBA(0xf4f4f5, 0.095),
		Text:          core.RGB(0xf4f4f5),
		TextSecondary: core.RGB(0xa8a8b3),
		TextMuted:     core.RGB(0x8b8b96),
		Accent:        core.RGB(0xf4f4f5),
		AccentHover:   core.RGB(0xe4e4e7),
		AccentPressed: core.RGB(0xd4d4d8),
		AccentFg:      core.RGB(0x111113),
		AccentSoft:    core.RGBA(0xf4f4f5, 0.11),
		Running:       core.RGB(0x34d399),
		RunningSoft:   core.RGBA(0x34d399, 0.14),
		Warn:          core.RGB(0xfbbf24),
		WarnSoft:      core.RGBA(0xfbbf24, 0.14),
		Danger:        core.RGB(0xf87171),
		DangerSoft:    core.RGBA(0xf87171, 0.14),
		GlassSurface:  core.RGBA(0x1a1a1e, 0.86),
		GlassBorder:   core.RGBA(0xf4f4f5, 0.11),
		Overlay:       core.RGBA(0x000000, 0.56),
	}
}

// LightVTPalette returns a high-contrast light terminal palette matching #FAFAFA.
func LightVTPalette() VTPalette {
	return VTPalette{
		Bg:     core.RGB(0xfafafa),
		Fg:     core.RGB(0x141416),
		Muted:  core.RGB(0x686873),
		Accent: core.RGB(0x0d7a5f),
		Blue:   core.RGB(0x1d4ed8),
		Amber:  core.RGB(0xa64b00),
		Red:    core.RGB(0xc81e1e),
		Cursor: core.RGB(0x0d7a5f),
		ANSI: [16]core.Color{
			core.RGB(0x141416), // 0 black
			core.RGB(0xc81e1e), // 1 red
			core.RGB(0x0d7a5f), // 2 green/emerald
			core.RGB(0xa64b00), // 3 yellow/amber
			core.RGB(0x1d4ed8), // 4 blue
			core.RGB(0x7e22ce), // 5 magenta
			core.RGB(0x0e7490), // 6 cyan
			core.RGB(0x52525b), // 7 white (readable on light bg)
			core.RGB(0x686873), // 8 bright black
			core.RGB(0xb91c1c), // 9 bright red
			core.RGB(0x0a664f), // 10 bright green
			core.RGB(0x9a3412), // 11 bright yellow
			core.RGB(0x1e40af), // 12 bright blue
			core.RGB(0x6b21a8), // 13 bright magenta
			core.RGB(0x155e75), // 14 bright cyan
			core.RGB(0x141416), // 15 bright white
		},
	}
}

// DarkVTPalette returns the paired dark terminal palette matching #09090B.
func DarkVTPalette() VTPalette {
	return VTPalette{
		Bg:     core.RGB(0x09090b),
		Fg:     core.RGB(0xf4f4f5),
		Muted:  core.RGB(0x8b8b96),
		Accent: core.RGB(0x34d399),
		Blue:   core.RGB(0x60a5fa),
		Amber:  core.RGB(0xfbbf24),
		Red:    core.RGB(0xf87171),
		Cursor: core.RGB(0x34d399),
		ANSI: [16]core.Color{
			core.RGB(0x222226), // 0 black
			core.RGB(0xf87171), // 1 red
			core.RGB(0x34d399), // 2 green/emerald
			core.RGB(0xfbbf24), // 3 yellow
			core.RGB(0x60a5fa), // 4 blue
			core.RGB(0xc084fc), // 5 magenta
			core.RGB(0x38bdf8), // 6 cyan
			core.RGB(0xd4d4d8), // 7 white
			core.RGB(0x8b8b96), // 8 bright black
			core.RGB(0xfca5a5), // 9 bright red
			core.RGB(0x6ee7b7), // 10 bright green
			core.RGB(0xfcd34d), // 11 bright yellow
			core.RGB(0x93c5fd), // 12 bright blue
			core.RGB(0xd8b4fe), // 13 bright magenta
			core.RGB(0x7dd3fc), // 14 bright cyan
			core.RGB(0xfafafa), // 15 bright white
		},
	}
}

// GhosttyThemeFor converts any VTPalette into a base/terminal.Theme (keeping color math in theme.go).
func GhosttyThemeFor(v VTPalette) *terminal.Theme {
	th := &terminal.Theme{
		Background: v.Bg,
		Foreground: v.Fg,
		Cursor:     v.Cursor,
		CursorText: v.Bg,
		Selection:  core.RGBA(uint32(v.Accent)>>8, 0.22),
	}
	th.Palette = v.ANSI
	return th
}

// GhosttyTerminalThemes converts our Light & Dark VT palettes into base/terminal.Theme values.
func GhosttyTerminalThemes() (light, dark *terminal.Theme) {
	return GhosttyThemeFor(LightVTPalette()), GhosttyThemeFor(DarkVTPalette())
}

// AppTheme builds the NSGo core.Theme matching our default Quiet Studio tokens.
func AppTheme() *core.Theme {
	return AppThemeFor(PresetStudio)
}

// AppThemeFor builds the NSGo core.Theme matching preset's Light & Dark palettes.
// Crucially, it preserves and updates core.DefaultTheme().Light.Colors / Dark.Colors so ui.Switch (track/knob/accentFill),
// ui.TextView (inset/code/line/raised), and ui.Diff render with crisp high-contrast tokens on every platform.
func AppThemeFor(preset ThemePreset) *core.Theme {
	lp, dp := PaletteFor(preset, false), PaletteFor(preset, true)
	t := core.DefaultTheme().Clone()
	t.Radius = Tokens.RadiusCtrl

	lightRoles := t.Light.Colors
	t.Light = core.Palette{
		Background:     lp.Bg,
		Surface:        lp.Surface,
		SurfaceHover:   lp.SurfaceRaised,
		SurfacePressed: lp.Fill,
		Border:         lp.Border,
		Text:           lp.Text,
		TextMuted:      lp.TextMuted,
		Accent:         lp.Accent,
		AccentHover:    lp.AccentHover,
		AccentPressed:  lp.AccentPressed,
		AccentText:     lp.AccentFg,
		Danger:         lp.Danger,
		Warning:        lp.Warn,
		Success:        lp.Running,
		Selection:      lp.AccentSoft,
		Focus:          lp.Accent,
		Scrollbar:      lp.Fill,
		Colors:         lightRoles,
	}
	for name, c := range map[string]core.Color{
		"inset":       lp.SurfaceRaised,
		"raised":      lp.Surface,
		"field":       lp.Surface,
		"fieldBorder": lp.Border,
		"line":        lp.Separator,
		"faint":       lp.TextMuted,
		"accentFill":  lp.Running, // Emerald/Semantic ON track gives crisp contrast with white knob
		"light":       lp.Accent,
		"onLight":     lp.AccentFg,
		"track":       lp.Fill,
		"knob":        lp.Surface,
		"code":        lp.Text,
		"info":        LightVTPalette().Blue,
		"down":        lp.Danger,
	} {
		t.Light.SetColor(name, c)
	}

	darkRoles := t.Dark.Colors
	t.Dark = core.Palette{
		Background:     dp.Bg,
		Surface:        dp.Surface,
		SurfaceHover:   dp.SurfaceRaised,
		SurfacePressed: dp.Fill,
		Border:         dp.Border,
		Text:           dp.Text,
		TextMuted:      dp.TextMuted,
		Accent:         dp.Accent,
		AccentHover:    dp.AccentHover,
		AccentPressed:  dp.AccentPressed,
		AccentText:     dp.AccentFg,
		Danger:         dp.Danger,
		Warning:        dp.Warn,
		Success:        dp.Running,
		Selection:      dp.AccentSoft,
		Focus:          dp.Accent,
		Scrollbar:      dp.Fill,
		Colors:         darkRoles,
	}
	for name, c := range map[string]core.Color{
		"inset":       dp.SurfaceRaised,
		"raised":      dp.SurfaceRaised,
		"field":       dp.Surface,
		"fieldBorder": dp.Border,
		"line":        dp.Separator,
		"faint":       dp.TextMuted,
		"accentFill":  dp.Running, // High-contrast ON track distinct from dark track & light knob
		"light":       dp.Accent,
		"onLight":     dp.AccentFg,
		"track":       dp.Fill,
		"knob":        dp.Text,
		"code":        dp.Text,
		"info":        DarkVTPalette().Blue,
		"down":        dp.Danger,
	} {
		t.Dark.SetColor(name, c)
	}
	return t
}

// PaletteFor returns the WCAG-AA verified Light or Dark palette for a ThemePreset.
func PaletteFor(preset ThemePreset, dark bool) Palette {
	switch preset {
	case PresetPaper:
		if dark {
			return Palette{
				Dark:          true,
				Bg:            core.RGB(0x141210),
				Surface:       core.RGB(0x1e1b18),
				SurfaceRaised: core.RGB(0x2a2520),
				Fill:          core.RGB(0x3a332c),
				Separator:     core.RGBA(0xf4efe6, 0.085),
				Border:        core.RGBA(0xf4efe6, 0.105),
				Text:          core.RGB(0xf4efe6),
				TextSecondary: core.RGB(0xb8afa3),
				TextMuted:     core.RGB(0x9a9083),
				Accent:        core.RGB(0xe2a678),
				AccentHover:   core.RGB(0xeab58c),
				AccentPressed: core.RGB(0xd19263),
				AccentFg:      core.RGB(0x1a1410),
				AccentSoft:    core.RGBA(0xe2a678, 0.14),
				Running:       core.RGB(0x4ade80),
				RunningSoft:   core.RGBA(0x4ade80, 0.14),
				Warn:          core.RGB(0xfbbf24),
				WarnSoft:      core.RGBA(0xfbbf24, 0.14),
				Danger:        core.RGB(0xf87171),
				DangerSoft:    core.RGBA(0xf87171, 0.14),
				GlassSurface:  core.RGBA(0x1e1b18, 0.88),
				GlassBorder:   core.RGBA(0xf4efe6, 0.12),
				Overlay:       core.RGBA(0x000000, 0.58),
			}
		}
		return Palette{
			Dark:          false,
			Bg:            core.RGB(0xf7f4ee),
			Surface:       core.RGB(0xfffdf9),
			SurfaceRaised: core.RGB(0xece6da),
			Fill:          core.RGB(0xdfd7c8),
			Separator:     core.RGBA(0x231f1b, 0.08),
			Border:        core.RGBA(0x231f1b, 0.095),
			Text:          core.RGB(0x231f1b),
			TextSecondary: core.RGB(0x544b41),
			TextMuted:     core.RGB(0x6a6054),
			Accent:        core.RGB(0x7c4a2d),
			AccentHover:   core.RGB(0x8e5635),
			AccentPressed: core.RGB(0x663c24),
			AccentFg:      core.RGB(0xffffff),
			AccentSoft:    core.RGBA(0x7c4a2d, 0.10),
			Running:       core.RGB(0x146c43),
			RunningSoft:   core.RGBA(0x146c43, 0.11),
			Warn:          core.RGB(0x9a4500),
			WarnSoft:      core.RGBA(0x9a4500, 0.10),
			Danger:        core.RGB(0xb91c1c),
			DangerSoft:    core.RGBA(0xb91c1c, 0.10),
			GlassSurface:  core.RGBA(0xfffdf9, 0.86),
			GlassBorder:   core.RGBA(0x231f1b, 0.095),
			Overlay:       core.RGBA(0x231f1b, 0.34),
		}

	case PresetNordic:
		if dark {
			return Palette{
				Dark:          true,
				Bg:            core.RGB(0x0b1120),
				Surface:       core.RGB(0x131c31),
				SurfaceRaised: core.RGB(0x1e293b),
				Fill:          core.RGB(0x334155),
				Separator:     core.RGBA(0xf1f5f9, 0.085),
				Border:        core.RGBA(0xf1f5f9, 0.105),
				Text:          core.RGB(0xf1f5f9),
				TextSecondary: core.RGB(0x94a3b8),
				TextMuted:     core.RGB(0x8496b0),
				Accent:        core.RGB(0x60a5fa),
				AccentHover:   core.RGB(0x93c5fd),
				AccentPressed: core.RGB(0x3b82f6),
				AccentFg:      core.RGB(0x0b1120),
				AccentSoft:    core.RGBA(0x60a5fa, 0.15),
				Running:       core.RGB(0x34d399),
				RunningSoft:   core.RGBA(0x34d399, 0.14),
				Warn:          core.RGB(0xfbbf24),
				WarnSoft:      core.RGBA(0xfbbf24, 0.14),
				Danger:        core.RGB(0xf87171),
				DangerSoft:    core.RGBA(0xf87171, 0.14),
				GlassSurface:  core.RGBA(0x131c31, 0.88),
				GlassBorder:   core.RGBA(0xf1f5f9, 0.12),
				Overlay:       core.RGBA(0x000000, 0.58),
			}
		}
		return Palette{
			Dark:          false,
			Bg:            core.RGB(0xeef3f8),
			Surface:       core.RGB(0xffffff),
			SurfaceRaised: core.RGB(0xe2e8f0),
			Fill:          core.RGB(0xcbd5e1),
			Separator:     core.RGBA(0x0f172a, 0.075),
			Border:        core.RGBA(0x0f172a, 0.09),
			Text:          core.RGB(0x0f172a),
			TextSecondary: core.RGB(0x475569),
			TextMuted:     core.RGB(0x5b6b80),
			Accent:        core.RGB(0x1d4ed8),
			AccentHover:   core.RGB(0x2563eb),
			AccentPressed: core.RGB(0x1e40af),
			AccentFg:      core.RGB(0xffffff),
			AccentSoft:    core.RGBA(0x1d4ed8, 0.10),
			Running:       core.RGB(0x0d7a5f),
			RunningSoft:   core.RGBA(0x0d7a5f, 0.11),
			Warn:          core.RGB(0xa64b00),
			WarnSoft:      core.RGBA(0xa64b00, 0.10),
			Danger:        core.RGB(0xc81e1e),
			DangerSoft:    core.RGBA(0xc81e1e, 0.10),
			GlassSurface:  core.RGBA(0xffffff, 0.86),
			GlassBorder:   core.RGBA(0x0f172a, 0.09),
			Overlay:       core.RGBA(0x0f172a, 0.34),
		}

	case PresetEmerald:
		if dark {
			return Palette{
				Dark:          true,
				Bg:            core.RGB(0x0b1210),
				Surface:       core.RGB(0x121e1a),
				SurfaceRaised: core.RGB(0x1b2c26),
				Fill:          core.RGB(0x283f37),
				Separator:     core.RGBA(0xecf5f1, 0.085),
				Border:        core.RGBA(0xecf5f1, 0.105),
				Text:          core.RGB(0xecf5f1),
				TextSecondary: core.RGB(0x9cb8ae),
				TextMuted:     core.RGB(0x82a397),
				Accent:        core.RGB(0x34d399),
				AccentHover:   core.RGB(0x6ee7b7),
				AccentPressed: core.RGB(0x10b981),
				AccentFg:      core.RGB(0x081410),
				AccentSoft:    core.RGBA(0x34d399, 0.15),
				Running:       core.RGB(0x34d399),
				RunningSoft:   core.RGBA(0x34d399, 0.14),
				Warn:          core.RGB(0xfbbf24),
				WarnSoft:      core.RGBA(0xfbbf24, 0.14),
				Danger:        core.RGB(0xf87171),
				DangerSoft:    core.RGBA(0xf87171, 0.14),
				GlassSurface:  core.RGBA(0x121e1a, 0.88),
				GlassBorder:   core.RGBA(0xecf5f1, 0.12),
				Overlay:       core.RGBA(0x000000, 0.58),
			}
		}
		return Palette{
			Dark:          false,
			Bg:            core.RGB(0xf2f7f5),
			Surface:       core.RGB(0xffffff),
			SurfaceRaised: core.RGB(0xe3efea),
			Fill:          core.RGB(0xcfe2da),
			Separator:     core.RGBA(0x0e1c17, 0.075),
			Border:        core.RGBA(0x0e1c17, 0.09),
			Text:          core.RGB(0x0e1c17),
			TextSecondary: core.RGB(0x3f574e),
			TextMuted:     core.RGB(0x556d64),
			Accent:        core.RGB(0x0d7a5f),
			AccentHover:   core.RGB(0x0f8f6f),
			AccentPressed: core.RGB(0x0a624c),
			AccentFg:      core.RGB(0xffffff),
			AccentSoft:    core.RGBA(0x0d7a5f, 0.10),
			Running:       core.RGB(0x0f766e),
			RunningSoft:   core.RGBA(0x0f766e, 0.11),
			Warn:          core.RGB(0xa64b00),
			WarnSoft:      core.RGBA(0xa64b00, 0.10),
			Danger:        core.RGB(0xc81e1e),
			DangerSoft:    core.RGBA(0xc81e1e, 0.10),
			GlassSurface:  core.RGBA(0xffffff, 0.86),
			GlassBorder:   core.RGBA(0x0e1c17, 0.09),
			Overlay:       core.RGBA(0x0e1c17, 0.34),
		}

	default:
		if dark {
			return DarkPalette()
		}
		return LightPalette()
	}
}

// ThemeOf resolves the active default Quiet Studio Palette for the requested ThemeMode.
func ThemeOf(cx *core.Scope, mode ThemeMode) Palette {
	return ThemeFor(cx, PresetStudio, mode)
}

// ThemeFor resolves the active Palette for the requested ThemePreset & ThemeMode.
func ThemeFor(cx *core.Scope, preset ThemePreset, mode ThemeMode) Palette {
	switch mode {
	case ThemeDark:
		return PaletteFor(preset, true)
	case ThemeLight:
		return PaletteFor(preset, false)
	default:
		if cx != nil && cx.Dark() {
			return PaletteFor(preset, true)
		}
		return PaletteFor(preset, false)
	}
}

// VTPaletteOf resolves the default Graphite VT canvas palette given app Palette and VTThemeMode.
func VTPaletteOf(pal Palette, vtMode VTThemeMode) VTPalette {
	return VTPaletteFor(pal, vtMode, VTSchemeGraphite)
}

// VTPaletteFor resolves the active VT canvas palette for the requested scheme and mode.
func VTPaletteFor(pal Palette, vtMode VTThemeMode, scheme VTColorScheme) VTPalette {
	switch scheme {
	case VTSchemeTokyo:
		return VTPalette{
			Bg:     core.RGB(0x1a1b26),
			Fg:     core.RGB(0xc0caf5),
			Muted:  core.RGB(0x8b94b8),
			Accent: core.RGB(0x9ece6a),
			Blue:   core.RGB(0x7aa2f7),
			Amber:  core.RGB(0xe0af68),
			Red:    core.RGB(0xf7768e),
			Cursor: core.RGB(0x7aa2f7),
			ANSI: [16]core.Color{
				core.RGB(0x15161e), core.RGB(0xf7768e), core.RGB(0x9ece6a), core.RGB(0xe0af68),
				core.RGB(0x7aa2f7), core.RGB(0xbb9af7), core.RGB(0x7dcfff), core.RGB(0xa9b1d6),
				core.RGB(0x565f89), core.RGB(0xf7768e), core.RGB(0x9ece6a), core.RGB(0xe0af68),
				core.RGB(0x7aa2f7), core.RGB(0xbb9af7), core.RGB(0x7dcfff), core.RGB(0xc0caf5),
			},
		}
	case VTSchemeCatppuccin:
		return VTPalette{
			Bg:     core.RGB(0x1e1e2e),
			Fg:     core.RGB(0xcdd6f4),
			Muted:  core.RGB(0x9399b2),
			Accent: core.RGB(0xa6e3a1),
			Blue:   core.RGB(0x89b4fa),
			Amber:  core.RGB(0xf9e2af),
			Red:    core.RGB(0xf38ba8),
			Cursor: core.RGB(0xcba6f7),
			ANSI: [16]core.Color{
				core.RGB(0x45475a), core.RGB(0xf38ba8), core.RGB(0xa6e3a1), core.RGB(0xf9e2af),
				core.RGB(0x89b4fa), core.RGB(0xf5c2e7), core.RGB(0x94e2d5), core.RGB(0xbac2de),
				core.RGB(0x585b70), core.RGB(0xf38ba8), core.RGB(0xa6e3a1), core.RGB(0xf9e2af),
				core.RGB(0x89b4fa), core.RGB(0xcba6f7), core.RGB(0x94e2d5), core.RGB(0xcdd6f4),
			},
		}
	case VTSchemeGruvbox:
		return VTPalette{
			Bg:     core.RGB(0x282828),
			Fg:     core.RGB(0xebdbb2),
			Muted:  core.RGB(0xa89984),
			Accent: core.RGB(0xb8bb26),
			Blue:   core.RGB(0x83a598),
			Amber:  core.RGB(0xfabd2f),
			Red:    core.RGB(0xff6b57),
			Cursor: core.RGB(0xfabd2f),
			ANSI: [16]core.Color{
				core.RGB(0x282828), core.RGB(0xcc241d), core.RGB(0x98971a), core.RGB(0xd79921),
				core.RGB(0x458588), core.RGB(0xb16286), core.RGB(0x689d6a), core.RGB(0xa89984),
				core.RGB(0x928374), core.RGB(0xfb4934), core.RGB(0xb8bb26), core.RGB(0xfabd2f),
				core.RGB(0x83a598), core.RGB(0xd3869b), core.RGB(0x8ec07c), core.RGB(0xebdbb2),
			},
		}
	default:
		if vtMode == VTAlwaysDark || pal.Dark {
			return DarkVTPalette()
		}
		return LightVTPalette()
	}
}

// ApplyAppearance pushes ThemeMode into NSGo's window appearance.
func ApplyAppearance(cx *core.Scope, mode ThemeMode) {
	if cx == nil {
		return
	}
	switch mode {
	case ThemeLight:
		cx.SetAppearance(core.AppearanceLight)
	case ThemeDark:
		cx.SetAppearance(core.AppearanceDark)
	default:
		cx.SetAppearance(core.AppearanceSystem)
	}
}

// Composite alpha-blends fg over an opaque background color bg.
func Composite(fg, bg core.Color) core.Color {
	fu := uint32(fg)
	bu := uint32(bg)
	a := float64(fu&0xff) / 255.0
	fr, fgC, fb := float64((fu>>24)&0xff), float64((fu>>16)&0xff), float64((fu>>8)&0xff)
	br, bgC, bb := float64((bu>>24)&0xff), float64((bu>>16)&0xff), float64((bu>>8)&0xff)
	r := uint32(math.Round(fr*a + br*(1-a)))
	g := uint32(math.Round(fgC*a + bgC*(1-a)))
	b := uint32(math.Round(fb*a + bb*(1-a)))
	return core.RGB((r << 16) | (g << 8) | b)
}

// ContrastRatio computes WCAG 2.1 contrast ratio between opaque colors fg and bg.
func ContrastRatio(fg, bg core.Color) float64 {
	l1 := relativeLuminance(fg)
	l2 := relativeLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func relativeLuminance(c core.Color) float64 {
	u := uint32(c)
	r := sRGBChannel(float64((u>>24)&0xff) / 255.0)
	g := sRGBChannel(float64((u>>16)&0xff) / 255.0)
	b := sRGBChannel(float64((u>>8)&0xff) / 255.0)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func sRGBChannel(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// TokenEntry describes one design token for runtime inspection on the Tokens gallery screen.
type TokenEntry struct {
	Group string
	Name  string
	Light string
	Dark  string
	Use   string
	Swatch func(Palette, VTPalette) core.Color
}

// TokenCatalog returns the complete runtime Quiet Studio token catalog synced with theme.go & Spec §4.
func TokenCatalog() []TokenEntry {
	return []TokenEntry{
		{"Surface", "--color-bg", "#f5f5f7", "#0d0d0f", "页面底色（纯石墨浅灰 / 深石墨）", func(p Palette, _ VTPalette) core.Color { return p.Bg }},
		{"Surface", "--color-surface", "#ffffff", "#161619", "分组白卡、顶栏、抽屉主表面", func(p Palette, _ VTPalette) core.Color { return p.Surface }},
		{"Surface", "--color-surface-raised", "#ebebf0", "#222226", "次级按钮、输入框、用户气泡、键帽", func(p Palette, _ VTPalette) core.Color { return p.SurfaceRaised }},
		{"Surface", "--color-fill", "#dcdce3", "#2e2e34", "空闲圆点、禁用态、拖拽手柄", func(p Palette, _ VTPalette) core.Color { return p.Fill }},
		{"Border", "--color-separator", "rgba(20,20,22,0.075)", "rgba(244,244,245,0.08)", "分组内 0.5px 内缩线与顶栏细线", func(p Palette, _ VTPalette) core.Color { return p.Separator }},
		{"Border", "--color-border", "rgba(20,20,22,0.085)", "rgba(244,244,245,0.095)", "输入框、气泡与暗色卡片边界线", func(p Palette, _ VTPalette) core.Color { return p.Border }},
		{"Glass", "--color-glass", "rgba(255,255,255,0.84)", "rgba(26,26,30,0.86)", "悬浮底栏与 Chat 悬浮输入器背景", func(p Palette, _ VTPalette) core.Color { return p.GlassSurface }},
		{"Glass", "--color-glass-border", "rgba(20,20,22,0.09)", "rgba(244,244,245,0.11)", "悬浮底栏与输入器外描边", func(p Palette, _ VTPalette) core.Color { return p.GlassBorder }},
		{"Glass", "--color-overlay", "rgba(20,20,22,0.34)", "rgba(0,0,0,0.56)", "底部抽屉遮罩层", func(p Palette, _ VTPalette) core.Color { return p.Overlay }},
		{"Text", "--color-text", "#141416", "#f4f4f5", "主标题、会话名、正文", func(p Palette, _ VTPalette) core.Color { return p.Text }},
		{"Text", "--color-text-secondary", "#52525b", "#a8a8b3", "状态摘要、次要说明、工具参数", func(p Palette, _ VTPalette) core.Color { return p.TextSecondary }},
		{"Text", "--color-text-muted", "#686873", "#8b8b96", "分区标题、主机名、路径与时间", func(p Palette, _ VTPalette) core.Color { return p.TextMuted }},
		{"Accent", "--color-accent", "#18181b", "#f4f4f5", "主操作按钮、选中胶囊与发送/停止圆键", func(p Palette, _ VTPalette) core.Color { return p.Accent }},
		{"Accent", "--color-accent-fg", "#ffffff", "#111113", "主按钮内文字与图标", func(p Palette, _ VTPalette) core.Color { return p.AccentFg }},
		{"Accent", "--color-accent-soft", "rgba(24,24,27,0.08)", "rgba(244,244,245,0.11)", "激活 Tab / 列表选中行背景", func(p Palette, _ VTPalette) core.Color { return p.AccentSoft }},
		{"Semantic", "--color-running", "#0d7a5f", "#34d399", "运行中 / 实例在线唯一翡翠绿", func(p Palette, _ VTPalette) core.Color { return p.Running }},
		{"Semantic", "--color-warn", "#a64b00", "#fbbf24", "需要你 / 权限审批 / 等待回答", func(p Palette, _ VTPalette) core.Color { return p.Warn }},
		{"Semantic", "--color-danger", "#c81e1e", "#f87171", "报错、断连告警、拒绝与终止", func(p Palette, _ VTPalette) core.Color { return p.Danger }},
		{"VT", "--color-vt-bg", "#fafafa", "#09090b", "VT 终端默认画布底色", func(_ Palette, vt VTPalette) core.Color { return vt.Bg }},
		{"VT", "--color-vt-fg", "#141416", "#f4f4f5", "VT 终端前景文字", func(_ Palette, vt VTPalette) core.Color { return vt.Fg }},
		{"VT", "--color-vt-accent", "#0d7a5f", "#34d399", "VT 终端绿色与光标强调", func(_ Palette, vt VTPalette) core.Color { return vt.Accent }},
		{"Type", "--type-scale", "22 / 16 / 15 / 13 / 12px", "同左", "6 级字阶（用户气泡与 AI 回复统一 15px）", nil},
		{"Space", "--space-grid", "4 / 8 / 12 / 16 / 20 / 24px", "同左", "严格 4px 间距网格（页边距 16，组间距 18）", nil},
		{"Radius", "--radius-scale", "6 / 10 / 14 / 20 / 999px", "同左", "嵌套圆角（键帽 / 控件 / 分组卡 / 抽屉 / 胶囊）", nil},
		{"Size", "--size-scale", "40 / 34 / 48 / 56 / 280 / 720px", "同左", "触控 / 键帽 / 顶栏 / 底栏&Rail / 侧栏 / 桌面Chat限宽", nil},
		{"Motion", "--motion-curve", "140 / 240 / 300ms · (0.22,1,0.36,1)", "同左", "按压缩放 0.97 + 120fps Spring 预设", nil},
	}
}
