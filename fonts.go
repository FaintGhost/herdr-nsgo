package main

import (
	_ "embed"

	"github.com/nsgo-dev/nsgo/core"
)

//go:embed assets/fonts/JetBrainsMonoNerdFontMono-Regular.ttf
var jetBrainsMonoNerdFontTTF []byte

//go:embed assets/fonts/FiraCode-Regular.ttf
var firaCodeTTF []byte

//go:embed assets/fonts/IBMPlexMono-Regular.ttf
var ibmPlexMonoTTF []byte

// BodyFontPreset selects the UI / Chat reading typeface.
type BodyFontPreset string

const (
	BodyFontSans  BodyFontPreset = "sans"
	BodyFontSerif BodyFontPreset = "serif"
	BodyFontMono  BodyFontPreset = "mono"
)

func AllBodyFontPresets() []BodyFontPreset {
	return []BodyFontPreset{BodyFontSans, BodyFontSerif, BodyFontMono}
}

func (f BodyFontPreset) Label() string {
	switch f {
	case BodyFontSerif:
		return "衬线书卷 · Serif"
	case BodyFontMono:
		return "全站等宽 · Mono"
	default:
		return "系统默认 · Sans"
	}
}

func (f BodyFontPreset) ShortLabel() string {
	switch f {
	case BodyFontSerif:
		return "Serif"
	case BodyFontMono:
		return "Mono"
	default:
		return "Sans"
	}
}

func (f BodyFontPreset) Desc() string {
	switch f {
	case BodyFontSerif:
		return "长篇 Agent 汇报与方案阅读更舒缓护眼"
	case BodyFontMono:
		return "标题与正文全等宽对齐的极客工作台"
	default:
		return "清晰锐利，贴合系统原生阅读质感"
	}
}

// MonoFontPreset selects the code & VT terminal monospace typeface.
type MonoFontPreset string

const (
	MonoFontJetBrains MonoFontPreset = "jetbrains"
	MonoFontFiraCode  MonoFontPreset = "firacode"
	MonoFontIBMPlex   MonoFontPreset = "ibmplex"
	MonoFontSystem    MonoFontPreset = "system"
)

func AllMonoFontPresets() []MonoFontPreset {
	return []MonoFontPreset{MonoFontJetBrains, MonoFontFiraCode, MonoFontIBMPlex, MonoFontSystem}
}

func (f MonoFontPreset) Label() string {
	switch f {
	case MonoFontFiraCode:
		return "Fira Code"
	case MonoFontIBMPlex:
		return "IBM Plex Mono"
	case MonoFontSystem:
		return "系统等宽"
	default:
		return "JetBrains Mono Nerd"
	}
}

func (f MonoFontPreset) ShortLabel() string {
	switch f {
	case MonoFontFiraCode:
		return "Fira Code"
	case MonoFontIBMPlex:
		return "IBM Plex"
	case MonoFontSystem:
		return "System Mono"
	default:
		return "JetBrains"
	}
}

func (f MonoFontPreset) Desc() string {
	switch f {
	case MonoFontFiraCode:
		return "内置 · 经典编程等宽，操作符与符号辨识度高"
	case MonoFontIBMPlex:
		return "内置 · 工业几何感，紧凑行距下极清晰"
	case MonoFontSystem:
		return "设备原生等宽字体，零额外开销"
	default:
		return "内置 · 含 Nerd 终端图标与高辨识字符"
	}
}

// vtFontFamily is the default JetBrainsMono Nerd Font stack (kept for compatibility).
const vtFontFamily = "JetBrainsMono Nerd Font Mono, JetBrainsMono NFM, monospace, sans-serif"

// ResolveMonoFontFamily returns the CSS/NSGo FontFamily stack for code blocks and VT terminals.
func ResolveMonoFontFamily(m MonoFontPreset) string {
	switch m {
	case MonoFontFiraCode:
		return "Fira Code, FiraCode-Regular, monospace, sans-serif"
	case MonoFontIBMPlex:
		return "IBM Plex Mono, IBMPlexMono-Regular, monospace, sans-serif"
	case MonoFontSystem:
		return core.FontMonospace
	default:
		return vtFontFamily
	}
}

// ResolveBodyFontFamily returns the FontFamily for reading prose (Chat bubbles, assistant replies, descriptions).
func ResolveBodyFontFamily(b BodyFontPreset, m MonoFontPreset) string {
	switch b {
	case BodyFontSerif:
		return "Georgia, Noto Serif CJK SC, Songti SC, serif"
	case BodyFontMono:
		return ResolveMonoFontFamily(m)
	default:
		return ""
	}
}

func registerAppFonts(cx *core.Scope) {
	if cx == nil {
		return
	}
	for _, data := range [][]byte{jetBrainsMonoNerdFontTTF, firaCodeTTF, ibmPlexMonoTTF} {
		if len(data) > 0 {
			_ = cx.RegisterFont(data)
		}
	}
}
