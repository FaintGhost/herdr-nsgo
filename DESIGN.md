# 设计说明：从 Moshi 抄了什么，为什么

依据是 `../oppo/moshi_extract/MOSHI-DESIGN.md`（Moshi 4.5.1 的真机 + 反编译分析）。
原则：**抄信息架构，不抄体量**。Moshi 有 3282 条文案、22 个品牌图标墙、17 项功能清单；这里只留必要的那几条。

## 一、抄的五条

### 1. 空状态 = 说明 + 唯一下一步

Moshi：空收件箱先摆图标墙，再一句 "Pair it once, and … sessions show up here."，最后一个绿色主 CTA。

这里：`emptyCard()`（`screens.go`）三件套——标题说清"这里将有什么"，正文说"连上之后你会看到什么"，一个主按钮。

```
还没有活跃会话
连上之后，这里会列出每个终端：状态、目录、最后一句输出。
[ 添加机器 ]
```

测试锁住了这条：`TestHomeEmptyStateIsAnInstruction` 断言空态文案和按钮都在，并且按下之后确实到了添加页。

### 2. 选项标注成本

Moshi：`最快 · 约1分钟` vs `熟悉 SSH · 约3分钟 · 需要主机名 + 密钥`。

这里：`optionCard()`。添加机器页两条路各带成本行：

- `推荐 · 约1分钟` — SSH 连接
- `只用于桌面端 · 需要本机装 herdr-core` — 本机调试

不问"你要哪种"，直接说每种要付多少。

### 3. 状态机优先于输出流

Moshi：agent 有 task/plan/question/等审批/等输入 五种显式状态，推送挂在状态上。

关键发现：**herdr-core 的协议层已经有这个状态机**，不用客户端自己造——
`terminal.list` 的每条记录带 `state`，`terminal.get` 带 `status{state,kind,msg,title,progress}`，
`state` 的取值就是 `idle | working | done | blocked | error`，`kind` 是 `permission | question | auth`。

所以这里只做一层映射（`screens.go` 的 `stateWord` / `stateColor` / `attentionWhy`）：

| herdr | 界面 |
|---|---|
| working | 跑着（绿点） |
| blocked + permission | 等你 · 等审批（黄点） |
| blocked + question | 等你 · 等你回答 |
| blocked + auth | 等你 · 等你登录 |
| idle / done / error | 空闲 / 结束 / 出错 |

### 4. 收件箱是独立一等界面

Moshi：独立入口，只列需要你决策的。

这里：`Attention()`（`app.go`）的整条定义就是 `NeedsYou()`——`blocked` 或 `error`。
首页顶部优先显示"需要你 · N"，收件箱页是同一份数据的完整列表 + 筛选。

不是"通知聚合"，是**筛选视图**：没有状态，就没有收件箱。

### 5. 错误即修复指令

Moshi：`Install mosh and ensure mosh-server resolves in non-interactive shells.`、`Saved. Restart moshi-hook to apply.`

这里两层：

- 协议失败：`RPCError.Remediation()` 按 `data.kind` 给出下一步（`not_found` / `machine_unknown` / `finished` / `blocked` / `unavailable` / `config` / `needs_target` …）。
- 连接失败：`explain()` 把 DNS / 拒绝连接 / 认证失败 / 没有 hc / 远端跑不起 `hc connect` / 超时分开说，不合成为一句"连接失败"。

`TestFailureCarriesRemediation` 断言每种 kind 都给得出修复动作；`TestExplainKeepsObservedFailure` 断言三种连接失败没有被压平成同一句话。

## 二、刻意没抄的

| Moshi 的做法 | 这里为什么不做 |
|---|---|
| 22 个 agent 品牌图标墙 | 需要打包几十个商标资源；换成一句说明同样说清"将来会有什么" |
| 17 项可试用功能清单 | 能力面只有 4 个，做成 4 条静态说明即可，等能力变多再升级成可试用 |
| 手势矩阵 / 可编程工具栏 / D-Pad | 先把"能发一句话、能按 Enter/Esc/Ctrl+C"做对；手势是第二阶段 |
| 推送 / Live Activities / 多设备 / Pro | 与"只支持 herdr-core、不做太复杂"直接冲突 |
| 主题扫码导入、字体连字开关 | 个性化，不是可用性 |

## 三、和官方 hc CLI 的差别

`hc` 把复杂度给调用方：30+ 命令、350 行 `--skill` 手册、`wait` 只能"等到它停止工作"。
这个客户端只暴露四件事：看列表、看一屏、发一句、按一下；其余全部收起。

如果要把这些改动回灌到 herdr-core（见 `MOSHI-DESIGN.md` §5 的 P0），这里是同一套结论的客户端侧验证：
**`wait` 的结构化原因、失败带 remediation、inbox 是个筛选视图** —— 这三条在移动端都能直接落地，说明它们在 CLI 上也成立。

## 四、下一步（按收益排序）

1. **真机验证**：现在只有 headless 测试和 APK，没在手机上跑过。
2. **`terminal.attach`**：接真正的 VT 流（`base/terminal` 组件已具备），把"聊天视图"升级成"真终端"。
3. **扫码配对**：替代手填 SSH 密码，也是 Moshi 最好用的那个入口。
4. **密钥认证 + 主机密钥固定**：现在密码明文存本地、主机密钥不校验。
