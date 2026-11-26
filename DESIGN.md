# herdr-nsgo Design System (`Quiet Studio`)

Rules for building and extending UI in `herdr-nsgo`. Every screen and component reads tokens exclusively from [`theme.go`](theme.go) and icons from [`icons.go`](icons.go); raw `core.RGB(...)` / `core.RGBA(...)` literals outside `theme.go` are blocked by `TestNoRawColorsInScreens`, and token/catalog drift is blocked by `TestTokenCatalogComplete` and `TestThemePaletteColorsMatchSpec`.

## 1. Visual Direction

- **Aesthetic**: Quiet Studio — light-first Pure Graphite (`#F5F5F7` canvas + `#FFFFFF` inset grouped cards) paired with True Graphite Dark (`#0D0D0F` canvas + `#161619` hairline-framed cards). Surface contrast and generous whitespace replace heavy boxes, shadows, and decorative stripes.
- **Monochrome Accent + Decoupled Semantic Status**: Monochrome Ink (`#18181B` light / `#F4F4F5` dark) is used exclusively for primary CTAs, active filter pills, and circular send/stop controls. Semantic states stay decoupled: **Running / Online** uses Emerald (`#0D7A5F` / `#34D399`), **Needs Attention / Approval** uses Amber (`#A64B00` / `#FBBF24`), and **Error / Destructive** uses Crimson (`#C81E1E` / `#F87171`).
- **Unified Iconography**: All icons come from `github.com/nsgo-dev/nsgo/icons/lucide` via [`icons.go`](icons.go) (1.75px stroke). Hand-written inline SVG strings and bare ASCII chevrons/crosses are disallowed in UI chrome.
- **Reading Comfort**: User bubbles and AI assistant prose share a unified `15px` body scale (`LineHeight: 22/23px`) on `pal.Bg`, centered at `720px` max width on tablet/desktop.
- **Terminal Truth**: Real VT terminal output (`terminal.attach`) renders on a dedicated high-contrast VT canvas (`#FAFAFA` light / `#09090B` dark, with an "Always Dark VT" override in Settings) and spans 100% width on wide screens.

## 2. Design Tokens (`theme.go`)

Source of truth: [`theme.go`](theme.go) (`LightPalette()`, `DarkPalette()`, `LightVTPalette()`, `DarkVTPalette()`, `Tokens`, `AppTheme()`, `TokenCatalog()`). Every foreground/background pair is verified WCAG AA (`>= 4.5:1`) by `TestPaletteWCAGContrast`.

| Group | Token | Light | Dark | Role |
|---|---|---|---|---|
| Surface | `--color-bg` (`pal.Bg`) | `#F5F5F7` | `#0D0D0F` | Page & Chat canvas background |
| Surface | `--color-surface` (`pal.Surface`) | `#FFFFFF` | `#161619` | `GroupCard`, topbars, tool disclosure cards, primary inputs |
| Surface | `--color-surface-raised` (`pal.SurfaceRaised`) | `#EBEBF0` | `#222226` | Secondary buttons, user chat bubbles, segment tracks, keycaps |
| Surface | `--color-fill` (`pal.Fill`) | `#DCDCE3` | `#2E2E34` | Idle status dots, disabled fills, sheet drag handles |
| Border | `--color-separator` (`pal.Separator`) | `rgba(20,20,22,0.075)` | `rgba(244,244,245,0.08)` | `InsetDivider` hairlines & session header bottom divider |
| Border | `--color-border` (`pal.Border`) | `rgba(20,20,22,0.085)` | `rgba(244,244,245,0.095)` | Inputs, user bubbles, tool cards, dark-mode `GroupCard` borders |
| Glass | `--color-glass` (`pal.GlassSurface`) | `rgba(255,255,255,0.84)` | `rgba(26,26,30,0.86)` | `FloatingTabBar` & `chat-glass-composer` frosted surface |
| Glass | `--color-glass-border` (`pal.GlassBorder`) | `rgba(20,20,22,0.09)` | `rgba(244,244,245,0.11)` | Floating capsule & composer outer hairline |
| Glass | `--color-overlay` (`pal.Overlay`) | `rgba(20,20,22,0.34)` | `rgba(0,0,0,0.56)` | Bottom sheet backdrop |
| Text | `--color-text` (`pal.Text`) | `#141416` | `#F4F4F5` | Page titles, session names, body prose |
| Text | `--color-text-secondary` (`pal.TextSecondary`) | `#52525B` | `#A8A8B3` | Status summaries, descriptions, tool targets |
| Text | `--color-text-muted` (`pal.TextMuted`) | `#686873` | `#8B8B96` | Section headers, hosts, paths, timestamps |
| Accent | `--color-accent` (`pal.Accent`) | `#18181B` | `#F4F4F5` | Primary CTA, active filter pill, circular send/stop button |
| Accent | `--color-accent-fg` (`pal.AccentFg`) | `#FFFFFF` | `#111113` | Foreground on monochrome Accent |
| Accent | `--color-accent-soft` (`pal.AccentSoft`) | `rgba(24,24,27,0.08)` | `rgba(244,244,245,0.11)` | Active tab/rail indicator & selected row highlight |
| Semantic | `--color-running` / `running-soft` | `#0D7A5F` / 11% | `#34D399` / 14% | Working agents, online hosts, live dots, DIFF tags |
| Semantic | `--color-warn` / `warn-soft` | `#A64B00` / 10% | `#FBBF24` / 14% | Blocked (`permission`, `question`, `auth`), attention cards |
| Semantic | `--color-danger` / `danger-soft` | `#C81E1E` / 10% | `#F87171` / 14% | Errors, disconnect alerts, Kill/Rm/Deny |
| VT | `--color-vt-bg` / `--color-vt-fg` | `#FAFAFA` / `#141416` | `#09090B` / `#F4F4F5` | Terminal canvas default background & text |
| VT | `--color-vt-accent` | `#0D7A5F` | `#34D399` | Terminal green & cursor accent |
| Scale | Spacing (`Tokens.Space1..6`) | `4 / 8 / 12 / 16 / 20 / 24px` | same | Strict 4px grid (`PagePadX: 16`, `SectionGap: 18`, `RowGap: 8`) |
| Scale | Radii (`Tokens.Radius*`) | `6 / 10 / 14 / 20 / 999px` | same | Keycap / Control / Card / Sheet / Pill |
| Scale | Sizes (`Tokens.Size*`, widths) | `40 / 34 / 19 / 48 / 56 / 280 / 720px` | same | Touch / Keycap / Icon / Topbar / TabBar&Rail / Sidebar / ChatMaxWidth |
| Type | Font scale (`Tokens.Font*`) | `22 / 16 / 15 / 15 / 13 / 12 / 12px` | same | Nav / Session / RowTitle / Body / Caption / Section / MonoMeta & VT |

## 3. Layout & Responsive Rules

- **Cold-start Gate**: When zero instances exist, render `screen_onboarding.go` centered (`MaxWidth: 520px`) with QR pairing, manual SSH setup, grouped numbered steps, and a copyable `authorized_keys` command card.
- **Phone (`width < 680px`)**: Borderless `48px` top header + scrollable feed (`Bottom: 96px` clearance) + floating `56px` glass capsule `FloatingTabBar` (`id:bottom-tab-bar`) across `会话` / `待办` / `实例`. Opening a session pushes `screen_session.go` full-screen; Settings & Tokens push over the stack with a `40×40` `lucide.ChevronLeft` back `IconTap`.
- **Tablet & Desktop (`width >= 680px`, `Tokens.WideBreakpoint`)**: 3-pane layout (`id:split-pane-layout`) consisting of `56px` `WideNavRail` (`id:wide-nav-rail` with `"h"` monogram, 3 primary tabs, bottom Settings button) + `280px` left sidebar (`Tokens.SidebarWidth`, hiding duplicate topbar Settings button) + flexible right live Session pane. Settings pushes full-screen from the Rail with a back button.
- **Inbox Urgency Bucketing**: `screen_inbox.go` groups `AggregatedAttention()` into 3 ordered sections while hiding empty buckets: `权限审批` (`CanInlineApprove`), `等待回答与认证` (other blocked), and `异常与限流` (`state == "error"`).
- **Session Viewport & 0ms Chat/VT Swap**: Single flex-row `48px` header (`[Back?] [8px dot + session-switcher-btn] [聊天|终端 pill] [… tools]`; Search lives inside `sheet-session-tools` with `id:session-search-btn`). Both Chat (`chat-surface` on `pal.Bg`, centered `MaxWidth: 720px`) and VT (`vt-surface`, 100% width) remain mounted simultaneously for `<1ms` visibility swap without layout shift. Entering a session attaches `SessionTerminal` in `observe: true` and routes Chat sends via `terminal.send`.

## 4. Core Components & Don'ts (`components.go`)

1. **`GroupCard` & `InsetDivider`**: `14px`-radius `pal.Surface` containers (hairline-free in Light, `1px pal.Border` in Dark) separating consecutive rows with `1px` `pal.Separator` inset hairlines.
2. **`SessionRow`**: `8px` semantic status dot (`Running` / `Warn` / `Danger` / `Fill`), `15px` medium title, quiet right-aligned harness/state label, optional 1-line summary, and monospace `host · cwd` metadata. No 3px left stripe, no per-row outer box, no gray instance badge.
3. **`InlineApprovalCard`**: Used exclusively for `NeedsYou()` items. Renders soft semantic border (`WarnSoft` / `DangerSoft`), `pal.Bg` monospace command block, and one-tap `允许` / `拒绝` buttons **only** when `CanInlineApprove(term)` passes; all other blocked/error items render `进入终端处理`.
4. **`FloatingTabBar` & `WideNavRail`**: Mobile floating glass capsule (`id:bottom-tab-bar`) and desktop 56px vertical icon rail (`id:wide-nav-rail`).
5. **`SheetModal` & `SheetListRow`**: Unified bottom sheets (`20px` top radius, `36×4px` drag handle, `32×32` circular `lucide.X` close button) with left-aligned `46px` `SheetListRow`s (leading Lucide icon + label + optional trailing mono value or chevron) grouped in `GroupCard`s. All 7 sheets (`newSessionSheet`, `sheet-session-tools`, `sheet-session-switcher`, `projectSheet`, `muxPicker`, `mobileAgentPicker`, `browserPreviewSheet`) use `SheetModal`.
6. **Floating Chat Composer (`chat-glass-composer`)**: Handle-free `22px`-radius frosted card (`GlassSurface` + `GlassBorder`, `MaxWidth: 720px`), `32×32` circular `+` button, compact selector chips (`chat-model-selector`, `chat-effort-selector`, `chat-mode-toggle`), and a fixed `34×34` circular right button (`session-interrupt-btn` `Square` while working+empty ↔ `session-send-btn` `ArrowUp`).

**Don'ts**:
- Never hardcode `core.RGB(...)` / `core.RGBA(...)` in any `screen_*.go`, `components.go`, or sheet file.
- Never use monochrome `pal.Accent` for running/online semantic status; always use `pal.Running`.
- Never use centered `ActionButton` stacks for selectable lists or sheet tool menus; use `GroupCard` + `SheetListRow`.
- Never fire inline approval keys without re-verifying live `terminal.get` state (`App.InlineApprove`).
- Never send a `RESIZE` frame when opening a session in `observe` mode.
- Never show bare `"error"` strings; every RPC or transport error passes through `RPCError.Remediation()` or `explain(err)`.

## 5. Platform & Degradation Rules

- **Pure Go Packaging**: Built via `go tool nsgo build -target android_arm64` / `ios_arm64` without native Java/Kotlin/Swift ejection.
- **VT Engine**: Uses NSGo `base/terminal` (`libghostty-vt`) on iOS/macOS/Linux/Windows; automatically falls back to pure-Go `VTScreen` + `core.Canvas` on Android (`android_arm64`) and headless tests.
- **Secrets, Camera & Speech**: Uses NSGo `cx.Secrets()` (Android Keystore / iOS Keychain) with encrypted AES-256-GCM fallback; QR pairing supports `cx.ScanCode` with manual URI/JSON paste fallback; speech input runs via `cx.StartSpeech` inside `dictationSheet`.

## 6. Pre-Ship Verification Checklist

- [ ] `go test -race ./...` passes with zero race warnings (including `TestPaletteWCAGContrast`, `TestThemePaletteColorsMatchSpec`, `TestTokenCatalogComplete`, `TestNoRawColorsInScreens`, and all `TestQuietStudio*` suites).
- [ ] All primary tabs, wide 3-pane shell (`960×720`), compact phone shell (`390×844` & `360×720`), Session Chat/VT swap, and all 7 unified sheets verified via `nsgotest`.
- [ ] `go tool nsgo build -target android_arm64` produces a clean APK in `dist/`.
- [ ] Runtime Design Tokens & Live Component Gallery (`设置 → 设计系统 Tokens 图鉴`) renders live slices synced with `theme.go`.
