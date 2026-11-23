# herdr-nsgo

跨平台（Android / iOS / macOS / Linux / Windows）第三方 [`herdr-core`](https://github.com/herdr-dev/herdr) 客户端，使用纯 Go + [NSGo](https://github.com/nsgo-dev/nsgo) 原生 UI 构建。

参考 Moshi 的高效信息架构与交互打磨，围绕 `herdr-core v0.1.22` 协议原生能力打造：支持**多实例并发直连、真实 VT 终端流、Agent 语义审批卡片、TOFU 主机指纹校验与扫码配对**。

## 核心能力（v1.0）

- **多实例并发连接池 (`CorePool`)**：同时管理多台独立的 `herdr-core` SSH 连接，使用 9 字节小端 `framed` 二进制多路复用与 `events.subscribe` 实时推送（告别轮询）。按 `CoreInfo.ID` / `MachineView.ID` 自动跨核去重，直连优先，直连断线时自动回退至其他在线核的联邦中继。
- **真实 VT 终端 + `observe` 无扰观察模式**：通过 `terminal.attach` 承载完整 ANSI 16/256/Truecolor、框线、宽字符与 Alt-Screen。iOS / 桌面端使用 `base/terminal`（Ghostty VT），Android 端无缝切换至纯 Go `VTScreen` + `core.Canvas` 引擎；默认 `observe: true` 查看不改动桌面端 PTY 尺寸，点击「适配列宽」或发送输入时才发 `RESIZE`。内置 24 会话内存 LRU 帧缓存实现切回瞬开。
- **Agent 语义收件箱 & 安全直操**：顶部「需要你」卡片与独立「待办」Tab 聚合跨实例阻塞/报错会话（支持实时搜索筛选）；对白名单程序（Claude / Codex）且处于 `blocked + permission` 状态的终端提供一键「允许 / 拒绝」（执行前二次校验 `terminal.get` 防误触）。
- **移动端终端增强**：单行可滚动 34px 键帽栏（`Esc / Tab / Ctrl / ↑↓←→ / ↵ / ^C / ^D / ^L`）、可折叠输入栏、常用短语（Snippets）、会内输出搜索、URL/文件路径提取、回到底部 FAB，以及 64 KiB 分块 `terminal.put_begin/chunk/finish` 图片与文件上传。
- **安全基线与 1 分钟冷启动**：应用内自动生成 `ed25519` 密钥对并提供一键复制 `authorized_keys` 命令；凭据存入系统 `core.Secrets`（Android Keystore / iOS Keychain，带 AES-256-GCM 降级）；TOFU 主机指纹首次信任、扫码 `fp` 预置强校验、变更强阻断；支持 `herdr://pair?v=1&...` 与 JSON 扫码/粘贴配对。
- **Crisp Slate 设计系统**：以亮色为主、完整支持暗色与跟随系统（含「终端画布始终暗色」开关），`>= 680px` 自动切换平板/桌面双栏布局；详见 [`DESIGN.md`](DESIGN.md)。

## 快速开始

```sh
# 运行全部单元测试（含 -race 竞态检测、WCAG 对比度、真实本机 hc connect 与 8 屏 headless UI 测试）
go test -v -race ./...

# 本机桌面端启动预览
go tool nsgo dev

# 构建 Android arm64 APK（纯 Go 打包，无需 Java/Gradle）
go tool nsgo build -target android_arm64

# 安装到 USB/无线调试的 Android 手机（例如 OPPO Find X9 Pro）
adb install -r dist/releases/herdr-nsgo-1.0.0-android-arm64.apk

# 构建 iOS（需配置 Apple 签名证书）
go tool nsgo build -target ios_arm64
```

## 项目结构

| 文件 | 职责 |
|---|---|
| [`theme.go`](theme.go) / [`theme_test.go`](theme_test.go) | Crisp Slate 亮/暗/VT Tokens、WCAG `>= 4.5:1` 校验与禁止裸色静态测试 |
| [`proto.go`](proto.go) / [`framed.go`](framed.go) / [`client.go`](client.go) | `hc v0.1.22` 协议类型、9 字节 LE `framed` 多路复用、`AttachStream`、分块 `PutFile` 与可操作修复提示 |
| [`secrets.go`](secrets.go) / [`pairing.go`](pairing.go) / [`transport.go`](transport.go) | `SecretStore`、TOFU 主机指纹校验、`ed25519` 密钥生成、`herdr://pair` 解析与 SSH 拨号 |
| [`pool.go`](pool.go) / [`approvals.go`](approvals.go) / [`app.go`](app.go) | 多实例 `CorePool`、跨核去重、白名单直操审批、24 会话 LRU 帧缓存与偏好/短语持久化 |
| [`vt_screen.go`](vt_screen.go) / [`vt_view.go`](vt_view.go) | 纯 Go VT100/xterm-256color/truecolor 仿真器、`SessionTerminal`（Ghostty / Canvas 双后端）与链接/搜索提取 |
| [`components.go`](components.go) / `screen_*.go` / [`main.go`](main.go) | 4 个核心组件、8 个业务界面、`<680px` 三 Tab / `>=680px` 双栏自适应布局与生命周期唤醒 |
| [`ui_test.go`](ui_test.go) | 覆盖全部 8 屏与双栏布局的 `nsgotest` headless UI 测试套件 |
