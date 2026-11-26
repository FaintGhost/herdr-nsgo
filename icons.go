package main

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
)

// Unified Lucide 1.75px stroke icons across the entire Quiet Studio UI.
var (
	iconTerminal = lucide.Terminal()
	iconInbox    = lucide.Inbox()
	iconServer   = lucide.Server()
	iconSettings = lucide.Settings()
	iconBack     = lucide.ChevronLeft()
	iconPlus     = lucide.Plus()
	iconSearch   = lucide.Search()
	iconMore     = lucide.Ellipsis()
	iconQR       = lucide.QrCode()
	iconImage    = lucide.Image()
	iconSnippets = lucide.Zap()
	iconSend     = lucide.ArrowUp()
	iconRefresh  = lucide.RefreshCw()
	iconQuote    = lucide.CornerUpLeft()
	iconSubagent = lucide.GitBranch()
	iconClose    = lucide.X()
	iconChevronR = lucide.ChevronRight()
	iconChevronU = lucide.ChevronUp()
	iconChevronD = lucide.ChevronDown()
	iconCopy     = lucide.Copy()
	iconTrash    = lucide.Trash()
	iconPencil   = lucide.Pencil()
	iconShield   = lucide.ShieldCheck()
	iconFolder   = lucide.Folder()
	iconFileCode = lucide.FileCode()
	iconFileDiff = lucide.FileDiff()
	iconFolderGt = lucide.FolderGit2()
	iconGlobe    = lucide.Globe()
	iconLink     = lucide.Link()
	iconExtLink  = lucide.ExternalLink()
	iconUpload   = lucide.Upload()
	iconStop     = lucide.Square()
	iconOctagonX = lucide.OctagonX()
	iconPanels   = lucide.PanelsTopLeft()
	iconCheck         = lucide.Check()
	iconCircleAlert   = lucide.CircleAlert()
	iconTriangleAlert = lucide.TriangleAlert()
	iconInfo          = lucide.Info()
	iconMic           = lucide.Mic()
	iconPalette  = lucide.Palette()
	iconType     = lucide.Type()

	// Official Pi Coding Agent (pi.dev) colorful "P" brand mark (Coral #F09082, Blue #4D9ABF, Yellow #F1BE58).
	iconPiBrand = core.MustParseSVG([]byte(
		`<svg viewBox="155 155 490 490" xmlns="http://www.w3.org/2000/svg">` +
			`<path fill="#F09082" d="M165.29 165.29H517.36V400H400V282.65H165.29Z"/>` +
			`<path fill="#4D9ABF" d="M165.29 282.65H282.65V400H400V517.36H282.65V634.72H165.29Z"/>` +
			`<path fill="#F1BE58" d="M517.36 400H634.72V634.72H517.36Z"/>` +
			`</svg>`,
	))

	// Official Claude Code pixel-block robot mascot ("Clawd", terracotta #D97757 with dark eyes).
	iconClaudeBrand = core.MustParseSVG([]byte(
		`<svg viewBox="0 0 18 14" xmlns="http://www.w3.org/2000/svg">` +
			`<rect x="3" y="1" width="12" height="8" rx="0.6" fill="#D97757"/>` +
			`<rect x="1" y="4" width="2.2" height="2.2" rx="0.3" fill="#D97757"/>` +
			`<rect x="14.8" y="4" width="2.2" height="2.2" rx="0.3" fill="#D97757"/>` +
			`<rect x="4" y="8.8" width="1.7" height="3.4" rx="0.3" fill="#D97757"/>` +
			`<rect x="6.6" y="8.8" width="1.7" height="3.4" rx="0.3" fill="#D97757"/>` +
			`<rect x="9.7" y="8.8" width="1.7" height="3.4" rx="0.3" fill="#D97757"/>` +
			`<rect x="12.3" y="8.8" width="1.7" height="3.4" rx="0.3" fill="#D97757"/>` +
			`<rect x="5.4" y="3.4" width="1.8" height="2.2" rx="0.2" fill="#141416"/>` +
			`<rect x="10.8" y="3.4" width="1.8" height="2.2" rx="0.2" fill="#141416"/>` +
			`</svg>`,
	))

	// Official OpenAI / Codex blossom brand mark (inherits currentColor so it adapts to Light & Dark themes).
	iconCodexBrand = core.MustParseSVG([]byte(
		`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">` +
			`<path fill="currentColor" d="M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z"/>` +
			`</svg>`,
	))

	// Official Google Antigravity floating arch brand mark (antigravity.google).
	iconAntigravityBrand = core.MustParseSVG([]byte(
		`<svg viewBox="10 14 92 86" xmlns="http://www.w3.org/2000/svg">` +
			`<defs>` +
			`<linearGradient id="ag-g" x1="15%" y1="15%" x2="85%" y2="95%">` +
			`<stop offset="0%" stop-color="#00B95C"/>` +
			`<stop offset="30%" stop-color="#FBBC04"/>` +
			`<stop offset="58%" stop-color="#FC413D"/>` +
			`<stop offset="94%" stop-color="#3186FF"/>` +
			`</linearGradient>` +
			`</defs>` +
			`<path fill="url(#ag-g)" d="M89.6992 93.695C94.3659 97.195 101.366 94.8617 94.9492 88.445C75.6992 69.7783 79.7825 18.445 55.8659 18.445C31.9492 18.445 36.0325 69.7783 16.7825 88.445C9.78251 95.445 17.3658 97.195 22.0325 93.695C40.1159 81.445 38.9492 59.8617 55.8659 59.8617C72.7825 59.8617 71.6159 81.445 89.6992 93.695Z"/>` +
			`</svg>`,
	))
)
