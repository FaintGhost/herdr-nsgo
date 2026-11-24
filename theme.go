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

// VTThemeMode selects whether the VT terminal canvas follows the app theme or stays dark.
type VTThemeMode string

const (
	VTFollowTheme VTThemeMode = "follow"
	VTAlwaysDark  VTThemeMode = "dark"
)

// Palette holds every semantic color role from the settled Crisp Slate / Soft Light design system.
// No screen file may construct raw core.RGB/core.RGBA colors directly; all colors come from here.
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

	Accent     core.Color
	AccentFg   core.Color
	AccentSoft core.Color

	Warn       core.Color
	WarnSoft   core.Color
	Danger     core.Color
	DangerSoft core.Color

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
	RowGap                                         core.Points

	RadiusKey, RadiusCtrl, RadiusCard, RadiusSheet core.Points

	SizeTouch, SizeKey, SizeIcon, SizeTopbar, SizeTabBar core.Points
	WideBreakpoint                                       core.Points
	SidebarWidth                                         core.Points

	FontNav, FontRowTitle, FontBody, FontCaption, FontSection, FontMonoMeta, FontVTDefault core.Points
}{
	Space1: 4, Space2: 8, Space3: 12, Space4: 16, Space5: 20, Space6: 24,
	PagePadX: 14,
	RowGap:   7,

	RadiusKey: 6, RadiusCtrl: 8, RadiusCard: 10, RadiusSheet: 16,

	SizeTouch: 40, SizeKey: 34, SizeIcon: 18, SizeTopbar: 44, SizeTabBar: 54,
	WideBreakpoint: 680,
	SidebarWidth:   280,

	FontNav: 20, FontRowTitle: 14, FontBody: 14, FontCaption: 12, FontSection: 12, FontMonoMeta: 11.5, FontVTDefault: 12,
}

// LightPalette returns the primary Soft Slate light palette (tok-color-soft).
func LightPalette() Palette {
	return Palette{
		Dark:          false,
		Bg:            core.RGB(0xf7f8fa),
		Surface:       core.RGB(0xffffff),
		SurfaceRaised: core.RGB(0xeef1f5),
		Fill:          core.RGB(0xe3e7ee),
		Separator:     core.RGBA(0x111827, 0.09),
		Border:        core.RGBA(0x111827, 0.08),
		Text:          core.RGB(0x111827),
		TextSecondary: core.RGB(0x4b5563),
		TextMuted:     core.RGB(0x5b6575),
		Accent:        core.RGB(0x0f766e),
		AccentFg:      core.RGB(0xffffff),
		AccentSoft:    core.RGBA(0x0f766e, 0.11),
		Warn:          core.RGB(0x9a4106),
		WarnSoft:      core.RGBA(0x9a4106, 0.10),
		Danger:        core.RGB(0xc81e1e),
		DangerSoft:    core.RGBA(0xc81e1e, 0.10),
		GlassSurface:  core.RGBA(0xffffff, 0.78),
		GlassBorder:   core.RGBA(0x111827, 0.11),
		Overlay:       core.RGBA(0x111827, 0.36),
	}
}

// DarkPalette returns the paired Obsidian dark palette (tok-color-soft dark).
func DarkPalette() Palette {
	return Palette{
		Dark:          true,
		Bg:            core.RGB(0x0c0f12),
		Surface:       core.RGB(0x14191f),
		SurfaceRaised: core.RGB(0x1e2630),
		Fill:          core.RGB(0x27313d),
		Separator:     core.RGBA(0xeef2f6, 0.10),
		Border:        core.RGBA(0xeef2f6, 0.09),
		Text:          core.RGB(0xeef2f6),
		TextSecondary: core.RGB(0xa8b5c6),
		TextMuted:     core.RGB(0x91a0b4),
		Accent:        core.RGB(0x2dd4bf),
		AccentFg:      core.RGB(0x04211d),
		AccentSoft:    core.RGBA(0x2dd4bf, 0.14),
		Warn:          core.RGB(0xf5a623),
		WarnSoft:      core.RGBA(0xf5a623, 0.14),
		Danger:        core.RGB(0xf87171),
		DangerSoft:    core.RGBA(0xf87171, 0.14),
		GlassSurface:  core.RGBA(0x14191f, 0.78),
		GlassBorder:   core.RGBA(0xeef2f6, 0.13),
		Overlay:       core.RGBA(0x000000, 0.56),
	}
}

// LightVTPalette returns a high-contrast light terminal palette matching #FAFBFC.
func LightVTPalette() VTPalette {
	return VTPalette{
		Bg:     core.RGB(0xfafbfc),
		Fg:     core.RGB(0x111827),
		Muted:  core.RGB(0x5b6575),
		Accent: core.RGB(0x0f766e),
		Blue:   core.RGB(0x1d4ed8),
		Amber:  core.RGB(0xb45309),
		Red:    core.RGB(0xc81e1e),
		Cursor: core.RGB(0x0f766e),
		ANSI: [16]core.Color{
			core.RGB(0x111827), // 0 black
			core.RGB(0xc81e1e), // 1 red
			core.RGB(0x0f766e), // 2 green/teal
			core.RGB(0xb45309), // 3 yellow/amber
			core.RGB(0x1d4ed8), // 4 blue
			core.RGB(0x7e22ce), // 5 magenta
			core.RGB(0x0e7490), // 6 cyan
			core.RGB(0x4b5563), // 7 white (readable on light bg)
			core.RGB(0x5b6575), // 8 bright black
			core.RGB(0xb91c1c), // 9 bright red
			core.RGB(0x0d6b63), // 10 bright green
			core.RGB(0x9a3412), // 11 bright yellow
			core.RGB(0x1e40af), // 12 bright blue
			core.RGB(0x6b21a8), // 13 bright magenta
			core.RGB(0x155e75), // 14 bright cyan
			core.RGB(0x111827), // 15 bright white
		},
	}
}

// DarkVTPalette returns the paired dark terminal palette matching #070A0D.
func DarkVTPalette() VTPalette {
	return VTPalette{
		Bg:     core.RGB(0x070a0d),
		Fg:     core.RGB(0xeef2f6),
		Muted:  core.RGB(0x8291a5),
		Accent: core.RGB(0x2dd4bf),
		Blue:   core.RGB(0x60a5fa),
		Amber:  core.RGB(0xf5a623),
		Red:    core.RGB(0xf87171),
		Cursor: core.RGB(0x2dd4bf),
		ANSI: [16]core.Color{
			core.RGB(0x1e2630), // 0 black
			core.RGB(0xf87171), // 1 red
			core.RGB(0x2dd4bf), // 2 green/teal
			core.RGB(0xf5a623), // 3 yellow
			core.RGB(0x60a5fa), // 4 blue
			core.RGB(0xc084fc), // 5 magenta
			core.RGB(0x38bdf8), // 6 cyan
			core.RGB(0xdbe2ec), // 7 white
			core.RGB(0x8291a5), // 8 bright black
			core.RGB(0xfca5a5), // 9 bright red
			core.RGB(0x5eead4), // 10 bright green
			core.RGB(0xfbbf24), // 11 bright yellow
			core.RGB(0x93c5fd), // 12 bright blue
			core.RGB(0xd8b4fe), // 13 bright magenta
			core.RGB(0x7dd3fc), // 14 bright cyan
			core.RGB(0xf8fafc), // 15 bright white
		},
	}
}

// GhosttyTerminalThemes converts our Light & Dark VT palettes into base/terminal.Theme values.
func GhosttyTerminalThemes() (light, dark *terminal.Theme) {
	toTermTheme := func(v VTPalette) *terminal.Theme {
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
	return toTermTheme(LightVTPalette()), toTermTheme(DarkVTPalette())
}

// AppTheme builds the NSGo core.Theme matching our settled tokens so built-in ui controls stay consistent.
func AppTheme() *core.Theme {
	lp, dp := LightPalette(), DarkPalette()
	t := core.DefaultTheme().Clone()
	t.Radius = Tokens.RadiusCtrl
	t.Light = core.Palette{
		Background:     lp.Bg,
		Surface:        lp.Surface,
		SurfaceHover:   lp.SurfaceRaised,
		SurfacePressed: lp.Fill,
		Border:         lp.Border,
		Text:           lp.Text,
		TextMuted:      lp.TextMuted,
		Accent:         lp.Accent,
		AccentHover:    lp.Accent,
		AccentPressed:  lp.Accent,
		AccentText:     lp.AccentFg,
		Danger:         lp.Danger,
		Warning:        lp.Warn,
		Success:        lp.Accent,
		Selection:      lp.AccentSoft,
		Focus:          lp.Accent,
		Scrollbar:      lp.Fill,
	}
	t.Dark = core.Palette{
		Background:     dp.Bg,
		Surface:        dp.Surface,
		SurfaceHover:   dp.SurfaceRaised,
		SurfacePressed: dp.Fill,
		Border:         dp.Border,
		Text:           dp.Text,
		TextMuted:      dp.TextMuted,
		Accent:         dp.Accent,
		AccentHover:    dp.Accent,
		AccentPressed:  dp.Accent,
		AccentText:     dp.AccentFg,
		Danger:         dp.Danger,
		Warning:        dp.Warn,
		Success:        dp.Accent,
		Selection:      dp.AccentSoft,
		Focus:          dp.Accent,
		Scrollbar:      dp.Fill,
	}
	return t
}

// ThemeOf resolves the active Palette for the requested ThemeMode.
func ThemeOf(cx *core.Scope, mode ThemeMode) Palette {
	switch mode {
	case ThemeDark:
		return DarkPalette()
	case ThemeLight:
		return LightPalette()
	default:
		if cx != nil && cx.Dark() {
			return DarkPalette()
		}
		return LightPalette()
	}
}

// VTPaletteOf resolves the active VT canvas palette given app Palette and VTThemeMode.
func VTPaletteOf(pal Palette, vtMode VTThemeMode) VTPalette {
	if vtMode == VTAlwaysDark || pal.Dark {
		return DarkVTPalette()
	}
	return LightVTPalette()
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
}

// TokenCatalog returns the complete runtime token catalog matching Spec §6.2.
func TokenCatalog() []TokenEntry {
	return []TokenEntry{
		{"Surface", "--color-bg", "#f7f8fa", "#0c0f12", "页面底色"},
		{"Surface", "--color-surface", "#ffffff", "#14191f", "卡片与列表底色"},
		{"Surface", "--color-surface-raised", "#eef1f5", "#1e2630", "次级按钮、实例标签、键帽底色"},
		{"Surface", "--color-fill", "#e3e7ee", "#27313d", "抽屉拖拽条与已结束状态条"},
		{"Surface", "--color-separator", "rgba(17,24,39,0.09)", "rgba(238,242,246,0.10)", "分组内部分隔线"},
		{"Surface", "--color-border", "rgba(17,24,39,0.08)", "rgba(238,242,246,0.09)", "1px 卡片与控件边框"},
		{"Text", "--color-text", "#111827", "#eef2f6", "主标题与会话名"},
		{"Text", "--color-text-secondary", "#4b5563", "#9aa7b8", "次要说明与实例名"},
		{"Text", "--color-text-muted", "#5b6575", "#8291a5", "目录路径与时间元信息"},
		{"Accent", "--color-accent", "#0f766e", "#2dd4bf", "主操作、运行中状态、激活 Tab"},
		{"Accent", "--color-accent-fg", "#ffffff", "#04211d", "主按钮内文字"},
		{"Accent", "--color-accent-soft", "rgba(15,118,110,0.11)", "rgba(45,212,191,0.14)", "选中芯片与按压底色"},
		{"Status", "--color-warn", "#b45309", "#f5a623", "等你审批 / 提问阻塞"},
		{"Status", "--color-danger", "#c81e1e", "#f05252", "出错 / 断连告警与终止"},
		{"VT", "--color-vt-bg", "#fafbfc", "#070a0d", "VT 终端默认画布底色"},
		{"VT", "--color-vt-fg", "#111827", "#eef2f6", "VT 终端前景文字"},
		{"Type", "--type-nav / row / mono / vt", "20 / 14 / 11.5 / 12px", "同左", "高密度字阶（竖屏 ~58 列）"},
		{"Space", "--space-1..6", "4 / 8 / 12 / 16 / 20 / 24px", "同左", "4px 间距网格"},
		{"Radius", "--radius-key/ctrl/card/sheet", "6 / 8 / 10 / 16px", "同左", "嵌套圆角阶梯"},
		{"Size", "--size-touch/key/icon/tabbar", "40 / 34 / 18 / 54px", "同左", "统一触控与导航尺寸"},
		{"Motion", "--motion-press/nav/sheet", "120 / 220 / 300ms", "同左", "cubic-bezier(0.22,1,0.36,1)"},
	}
}
