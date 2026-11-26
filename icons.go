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

	// Official Anthropic Claude terracotta starburst brand mark (#D97757).
	iconClaudeBrand = core.MustParseSVG([]byte(
		`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">` +
			`<path fill="#D97757" d="M4.709 15.955l4.72-2.647.08-.23-.08-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.073-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a2.97 2.97 0 01-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312-.006.006z"/>` +
			`</svg>`,
	))

	// Official OpenAI / Codex blossom brand mark (inherits currentColor so it adapts to Light & Dark themes).
	iconCodexBrand = core.MustParseSVG([]byte(
		`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">` +
			`<path fill="currentColor" d="M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z"/>` +
			`</svg>`,
	))

	// Official Google Gemini 4-point sparkle brand mark (#4285F4 -> #9B72CB -> #D96570).
	iconGeminiBrand = core.MustParseSVG([]byte(
		`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">` +
			`<defs>` +
			`<linearGradient id="gemini-g" x1="0%" y1="0%" x2="100%" y2="100%">` +
			`<stop offset="0%" stop-color="#4285F4"/>` +
			`<stop offset="52%" stop-color="#9B72CB"/>` +
			`<stop offset="100%" stop-color="#D96570"/>` +
			`</linearGradient>` +
			`</defs>` +
			`<path fill="url(#gemini-g)" d="M12 0C12 6.627 6.627 12 0 12c6.627 0 12 5.373 12 12 0-6.627 5.373-12 12-12-6.627 0-12-5.373-12-12Z"/>` +
			`</svg>`,
	))
)
