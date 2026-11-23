package main

import (
	_ "embed"

	"github.com/nsgo-dev/nsgo/core"
)

//go:embed assets/fonts/JetBrainsMonoNerdFontMono-Regular.ttf
var jetBrainsMonoNerdFontTTF []byte

// vtFontFamily selects our embedded JetBrainsMono Nerd Font Mono first (covering both
// typographic family and legacy family names across Apple & Android), with system monospace
// and sans-serif (Noto Symbols / CJK) fallback for Unicode symbols like U+23F5.
const vtFontFamily = "JetBrainsMono Nerd Font Mono, JetBrainsMono NFM, monospace, sans-serif"

func registerAppFonts(cx *core.Scope) {
	if cx == nil || len(jetBrainsMonoNerdFontTTF) == 0 {
		return
	}
	_ = cx.RegisterFont(jetBrainsMonoNerdFontTTF)
}
