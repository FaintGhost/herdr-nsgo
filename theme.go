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

// AppTheme builds the NSGo core.Theme matching our settled Quiet Studio tokens so built-in ui controls stay consistent.
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
		AccentHover:    lp.AccentHover,
		AccentPressed:  lp.AccentPressed,
		AccentText:     lp.AccentFg,
		Danger:         lp.Danger,
		Warning:        lp.Warn,
		Success:        lp.Running,
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
		AccentHover:    dp.AccentHover,
		AccentPressed:  dp.AccentPressed,
		AccentText:     dp.AccentFg,
		Danger:         dp.Danger,
		Warning:        dp.Warn,
		Success:        dp.Running,
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
