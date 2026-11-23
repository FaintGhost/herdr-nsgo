# herdr-nsgo

herdr-core 的移动客户端，用 [NSGo](https://github.com/nsgo-dev/nsgo)（Go 原生 UI）写。只做一件事：**把 herdr 的终端当成聊天来看**——列表、一屏文字、一个输入框、一排按键，外加一个只装「等你的人」的收件箱。

设计思路来自分析第三方客户端 Moshi（见 `DESIGN.md`），但只抄信息架构，不抄它的体量：APK **6.3 MB**，Moshi 是 122 MB。

## 现在能做什么

| 能力 | 状态 | 证据 |
|---|---|---|
| 说 hc 的 JSON-RPC 协议（hello / list / read / send / keys / interrupt / wait / subscribe） | 通了 | `client_test.go` 对本机真实核心跑：42 终端 / 4 机器 / 一次读出 2063 字节 |
| SSH 到远端跑 `hc connect`（不用开端口） | 通了 | `transport.go` |
| 首页 / 会话 / 收件箱 / 添加机器 四屏 | 通了 | `ui_test.go` headless 逐屏断言 |
| 构建 Android APK | 出了 | `dist/releases/herdr-nsgo-0.1.0-android-arm64.apk` (6.4 MB) |
| 在真机上连真机核 | **通了** | PLG110 上装好后经 Tailscale+SSH 连到 `kr`（arm-chuncheon）：24 个终端、2 台机器、2 条「需要你」 |
| 真机发文本 | **通了** | 手机上发 `echo hello-from-nsgo`，kr 的终端执行并回显，App 同步显示（`docs/screens/05-sent.png`）|

## 快速开始

```sh
go test ./...                    # 协议层打真实 hc，UI 层 headless
go tool nsgo dev                 # 本机（Windows/macOS/Linux）跑起来
go tool nsgo build -target android_arm64
adb install -r dist/releases/herdr-nsgo-0.1.0-android-arm64.apk
```

第一次进去是空态：点「连接一台机器」→ 填 `主机` / `用户名` / `密码` → 它会 SSH 过去跑 `hc connect`。
桌面端也可以选「用本机」，直接连这台机器上的核心（测试就是这么跑的）。

## 结构

| 文件 | 作用 |
|---|---|
| `main.go` | 导航栈，四条路由 |
| `proto.go` | hc 的数据形状（Terminal / Status / ReadResult …） |
| `client.go` | JSON-RPC 2.0 客户端；失败带 `kind` 与 `Remediation()` |
| `transport.go` | SSH 与本地两种传输 |
| `app.go` | 状态、连接生命周期、轮询、动作、错误解释 |
| `screens.go` | 四个界面 |
| `client_test.go` / `ui_test.go` | 协议对真实核心、界面 headless |

## 明确的非目标

- 不做完整 VT 终端（`terminal.attach` 的二进制流），v1 只读文本屏幕。对 agent 会话够用，对 vim 不够。
- 不做扫码配对、推送、多设备、Pro——Moshi 那些先不抄。
- 不做终端管理（新建 / kill / 改标签）：那是 hc 已经擅长的，先用 CLI。

## 真机验证发现的五个缺陷（已修）

| 现象 | 根因 | 修法 |
|---|---|---|
| `dial tcp: missing port` | 目标没写端口就直接丢给 ssh | `withPort()` 补 `:22` |
| 连上了但列表一直空 | 各 section 一次性构建静态节点，不跟随信号重建 | 换成 `core.Each` |
| 失败后仍显示「正在连接…」 | `fail()` 没同步改连接状态 | 失败即置 `stateFailed` |
| 列表滚到标题栏下面 | 系统标题栏是透明的 | 每个页面给 `HeaderStyle` 实底 |
| 无状态上报的终端显示英文 `live` | 把生命周期状态当成程序状态 | 映射为「运行中」 |

## 真机测试用的凭据（临时）

真机验证时生成了一对一次性密钥，公钥以 `herdr-nsgo-test` 为注释加进了 kr 的
`/root/.ssh/authorized_keys`，私钥在 `../oppo/nsgo_testkey`，也存进了手机上的 App。

不想留了就撤销：

```sh
hc exec --on kr "sed -i '/herdr-nsgo-test/d' ~/.ssh/authorized_keys"
rm -f ../oppo/nsgo_testkey ../oppo/nsgo_testkey.pub
```

App 会随之连不上，届时在「添加机器」里换成密码或你自己的私钥。

## 已知限制

- SSH 只支持密码；主机密钥不校验（`InsecureIgnoreHostKey`），和 Moshi 首连行为一致，但要在设置里明说。
- 会话文字会换行，宽表格会折行；这是「聊天视图」的取舍。
- 密码与私钥存在设备本地存储（`core.Storage`），未加密。
- Powerline / Nerd Font 提示符里的私用区字形显示成 □（系统等宽字体没有这些字形）。接 `base/terminal` 并配 Nerd Font 可解决。
- 文本模式会按宽度折行，TUI 的框线会被折断——这是「聊天视图」的代价，真终端要等 `terminal.attach`。
