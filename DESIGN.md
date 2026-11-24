# herdr-nsgo Design System (`Crisp Slate`)

Rules for building and extending UI in `herdr-nsgo`. Every screen and component reads tokens exclusively from [`theme.go`](theme.go); raw `core.RGB(...)` / `core.RGBA(...)` literals outside `theme.go` are blocked by `TestNoRawColorsInScreens`.

## 1. Visual Direction

- **Aesthetic**: Crisp Slate — light-first, hairline-framed, high-density developer utility inspired by Moshi's information architecture and native mobile polish, paired with an Obsidian dark mode.
- **Hierarchy**: Flat surfaces separated by 1px subtle borders and 3px left semantic status rails; no heavy drop shadows, no neon gradients, no marketing bloat.
- **Density**: Compact mobile ergonomics (~58 monospace VT columns in portrait at 12px) with 40px minimum touch targets and 34px single-row keycaps.
- **Color Discipline**: One brand accent (Teal `#0F766E` light / `#2DD4BF` dark) for active tabs, running sessions, and primary actions; Amber (`warn`) solely for attention/blocking; Crimson (`danger`) solely for errors and destructive actions.
- **Terminal Truth**: Real VT terminal output (`terminal.attach`) stays visually distinct on a dedicated VT canvas (`#FAFBFC` light / `#070A0D` dark, with an "Always Dark VT" override in Settings).

## 2. Design Tokens (`theme.go`)

Source of truth: [`theme.go`](theme.go) (`LightPalette()`, `DarkPalette()`, `LightVTPalette()`, `DarkVTPalette()`, `Tokens`, `TokenCatalog()`). All foreground/background pairs are verified WCAG AA (`>= 4.5:1`) by `TestPaletteWCAGContrast`.

| Group | Token | Light | Dark | Role |
|---|---|---|---|---|
| Surface | `--color-bg` (`pal.Bg`) | `#F7F8FA` | `#0C0F12` | Page background |
| Surface | `--color-surface` (`pal.Surface`) | `#FFFFFF` | `#14191F` | Cards, bars, sheets |
| Surface | `--color-surface-raised` (`pal.SurfaceRaised`) | `#EEF1F5` | `#1E2630` | Inputs, secondary buttons, pills, keycaps |
| Surface | `--color-fill` (`pal.Fill`) | `#E3E7EE` | `#27313D` | Sheet handle, finished badge fill |
| Surface | `--color-separator` (`pal.Separator`) | `rgba(17,24,39,0.09)` | `rgba(238,242,246,0.10)` | Header/tab dividers |
| Surface | `--color-border` (`pal.Border`) | `rgba(17,24,39,0.08)` | `rgba(238,242,246,0.09)` | 1px card & control borders |
| Text | `--color-text` (`pal.Text`) | `#111827` | `#EEF2F6` | Titles & session names |
| Text | `--color-text-secondary` (`pal.TextSecondary`) | `#4B5563` | `#9AA7B8` | Status summaries & instance labels |
| Text | `--color-text-muted` (`pal.TextMuted`) | `#5B6575` | `#8291A5` | Paths, addresses, section headers |
| Accent | `--color-accent` (`pal.Accent`) | `#0F766E` | `#2DD4BF` | Primary CTA, active tab, working state |
| Accent | `--color-accent-fg` (`pal.AccentFg`) | `#FFFFFF` | `#04211D` | Foreground on primary CTA |
| Accent | `--color-accent-soft` (`pal.AccentSoft`) | `rgba(15,118,110,0.11)` | `rgba(45,212,191,0.14)` | Selected pill & highlight fill |
| Status | `--color-warn` / `warn-soft` | `#B45309` / 10% | `#F5A623` / 14% | Blocked (`permission`, `question`, `auth`) |
| Status | `--color-danger` / `danger-soft` | `#C81E1E` / 10% | `#F05252` / 14% | Errors, disconnect alerts, Kill/Deny |
| VT | `--color-vt-bg` / `--color-vt-fg` | `#FAFBFC` / `#111827` | `#070A0D` / `#EEF2F6` | Terminal canvas default bg/fg |
| Scale | Spacing (`Tokens.Space1..6`) | `4 / 8 / 12 / 16 / 20 / 24px` | same | 4px spacing grid (`PagePadX: 14`, `RowGap: 7`) |
| Scale | Radii (`Tokens.Radius*`) | `6 / 8 / 10 / 16px` | same | Keycap / Control / Card / Sheet |
| Scale | Sizes (`Tokens.Size*`) | `40 / 34 / 18 / 44 / 54px` | same | Touch / Keycap / Icon / Topbar / TabBar |
| Type | Font scale (`Tokens.Font*`) | `20 / 14 / 14 / 12 / 12 / 11.5 / 12px` | same | Nav / Row / Body / Caption / Section / Mono / VT |

## 3. Layout & Responsive Rules

- **Cold-start Gate**: When zero instances are configured, render `screen_onboarding.go` directly (QR pairing + Manual SSH + 1-tap `authorized_keys` command + troubleshooting checklist).
- **Phone (`width < 680px`)**: Persistent 54px bottom 3-Tab bar (`会话` / `待办` / `实例`) + 44px top header. Opening a session pushes a full-screen VT Session view (`screen_session.go`); Settings & Tokens push over the stack.
- **Tablet & Desktop (`width >= 680px`, `Tokens.WideBreakpoint`)**: Split-pane layout (`id:split-pane-layout`) with a fixed `280px` (`Tokens.SidebarWidth`) left 3-Tab sidebar and live VT Session view on the right.
- **Session Viewport & Chat View Discipline**: Entering a session attaches `SessionTerminal` in `observe: true` (never resizing the remote desktop PTY) and initializes `ChatSessionController` on the root scope. Agent harnesses (Claude Code, OMP/Pi, Codex, Antigravity) default to **Chat View** (`screen_chat.go`, reading native remote JSONL via `file.read` with VT fallback and routing sends via `terminal.send` without exiting `observe: true`); plain shells default to **Terminal View** (`vt_view.go`). Tapping `聊天 | 终端` in the header switches 0ms while preserving state. Closing releases the attach channel, stops background tailing, and updates the 24-session LRU cache.

## 4. Core Components & Don'ts (`components.go`)

1. **`SessionRow`**: 3px left semantic rail + session title + monospace instance pill (tagged `中继` when relayed) + `StatusBadge` + single-line semantic status + monospace `addr · app · cwd`.
2. **`InlineApprovalCard`**: Used exclusively for `NeedsYou()` items (`blocked` / `error`). Renders one-tap `允许` / `拒绝` buttons **only** when `CanInlineApprove(term)` passes (whitelisted `Status.App` + `state == blocked` + `kind == permission`); all other items (`question`, `auth`, `error`, unknown apps) render `进入终端处理`.
3. **`KeycapBar`**: Single-row horizontally scrollable 34px keycaps (`Esc`, `Tab`, `Ctrl`, arrows, `↵`, `^C`, `^D`, `^L`) docked immediately above the collapsible Composer.
4. **`SheetModal` / `EmptyStateCard` / `FilterPill`**: 16px top-radius modal sheets with 36x4px handle; empty states always state what will appear + a single clear next action.

**Don'ts**:
- Never hardcode `core.RGB(...)` / `core.RGBA(...)` in any `screen_*.go` or `components.go`.
- Never fire inline approval keys without re-verifying live `terminal.get` state (`App.InlineApprove`).
- Never send a `RESIZE` frame when opening a session in `observe` mode.
- Never show bare `"error"` strings; every RPC or transport error passes through `RPCError.Remediation()` or `explain(err)`.

## 5. Platform & Degradation Rules

- **Pure Go Packaging**: Built via `go tool nsgo build` for Android (`android_arm64`) and iOS (`ios_arm64`) without custom Java/Kotlin/Gradle ejection.
- **VT Engine**: Uses NSGo `base/terminal` (`libghostty-vt`) on iOS/macOS/Linux/Windows; automatically falls back to pure-Go `VTScreen` + `core.Canvas` on Android (`android_arm64`, where NSGo v0.3.0 has no bundled Ghostty shared library) and headless tests.
- **Secrets & Camera**: Uses NSGo `cx.Secrets()` (Android Keystore / iOS Keychain) with encrypted AES-256-GCM fallback; QR pairing supports live `cx.ScanCode` with manual URI/JSON paste fallback.

## 6. Pre-Ship Verification Checklist

- [ ] `go test -race ./...` passes with zero race warnings (includes `TestPaletteWCAGContrast` and `TestNoRawColorsInScreens`).
- [ ] All 8 screens verified via headless `nsgotest` (`ui_test.go`) in both portrait (`390x844`) and split-pane (`960x720`).
- [ ] `go tool nsgo build -target android_arm64` produces a clean APK in `dist/`.
- [ ] Runtime Design Tokens gallery (`设置 → 查看设计系统 Tokens 图鉴`) reflects current `theme.go` values.
