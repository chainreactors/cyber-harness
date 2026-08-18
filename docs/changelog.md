# Changelog

## v1.0.0-rc1 — 原生录屏 + 浏览器自动化扩展 + 稳定接口候选

v1.0.0-rc1 是 AIScan 首个 v1 发布候选版本。它在 v0.4.0 Web 工作台、Agent 会话和 SCO 资产模型之上补齐原生桌面录制、可复用浏览器自动化、scanner-native Artifact/Loot 传输和跨平台 shell 命令组合，同时把 CLI、配置、AOP/Connect 协议、包边界与 standard/full 发布矩阵收敛为 v1 稳定基线。

### New Features

**record — 原生桌面与窗口捕获**

Full 版新增原生 `record` Agent Tool，用于截取桌面或可见应用窗口，并生成 PNG 截图或 H.264/MP4 视频。它不依赖外部 ffmpeg 命令；官方 Windows amd64 与 Linux amd64/arm64 full 产物静态链接裁剪后的 FFmpeg/libx264 SDK。

- 支持 `screenshot`、固定时长 `record`，以及异步 `start` / `stop` / `status`
- 支持桌面、Windows HWND、X11 Window ID，或通过 PID 自动选择最大的可见窗口
- 默认捕获鼠标，视频使用 H.264/libx264 编码并封装为 MP4；最多可并行运行四个录制会话
- 截图通过 AOP media 返回有界预览；视频通过 task-relative `Resource.uri` 与分段 `aop.file` 请求传输
- `make full` 自动下载、校验并缓存固定版本的 recorder SDK；维护者也可从固定源码重建 SDK

Wayland、macOS、Windows arm64、无图形会话的 headless 主机和 Windows session 0 暂不支持原生录制。完整限制与构建说明见 [record 文档](record.md)。

**浏览器自动化与 Katana headless 复用**

Playwright、nuclei headless 和 Katana 现在共享同一套 Chromium 发现逻辑。可以通过 `AISCAN_BROWSER_PATH` 显式指定浏览器，也可以自动复用系统 Chrome/Chromium/Edge，减少不同浏览器工具各自下载或选择不同运行时的问题。

- headless action 新增双击、hover、focus/blur、check/uncheck、drag、scroll、viewport 和自定义 DOM event
- 新增 URL、request、response、可见性与断言等待，以及 cookie、localStorage/sessionStorage 操作
- 支持 reload、前进/后退、替换页面内容和更完整的文件输入、网络请求与页面状态自动化
- Playwright recorder 与 nuclei-compatible headless 模板保持命令和参数一致
- CI 新增真实 Chrome 的登录、重定向、认证状态和 Katana SPA 渲染 E2E

**Scanner-native Artifact 与关联 Loot**

扫描节点不再先把所有工具结果压平成统一文本。gogo、spray、zombie、neutron 等 scanner 会通过 AOP 发出各自的结构化 `Artifact`；服务端保留原始字段，再按需要转换为 SCO 资产和漏洞文档。

- `Artifact` 保存 tool、kind、target、时间戳和 scanner-native 数据
- 稳定 `result_id` 将 `Loot` 高价值标记关联回原始 Artifact，避免复制或丢失证据
- 弱口令、漏洞、Web 资产、服务和指纹可以携带统一来源关系进入 Web、报告和 Agent 上下文
- scan、agent 和独立工具事件继续写入同一份 AOP ProtoJSONL，可恢复、格式化和外部消费

**Shell 内存命令组合**

AIScan 注册的 scan、spray、proton 等进程内命令现在可以像普通可执行文件一样参与 shell 管道和重定向。适配层按需创建，不启动额外常驻服务，并完整传递工作目录、stdin/stdout/stderr、退出码、调用上下文与取消信号。

```bash
scan -i target -j | proton
proton -i . | grep critical
scan -i target -j > scan.jsonl
```

Unix 使用本地 socket，Windows 使用 named pipe；进程退出或异常中断后会回收遗留 runtime，避免无效桥接进程和临时目录累积。

### Improvements

**发布与原生构建链路**

- standard 由 Linux runner 交叉编译 Linux、macOS、Windows 的 amd64/arm64；full 的 macOS amd64/arm64 也通过 Linux 上的 Zig 和固定 SDK 交叉编译
- CI、定时回归和正式 release 共用同一套构建标签与发布约束，版本注入、压缩和平台矩阵不再漂移
- recorder SDK 使用固定源码、组件 allowlist、SHA-256 和静态库体积预算；缺少预构建 SDK 时 CI 可回退到源码构建
- full profile 恢复静态 RE2，并验证 Windows recorder/RE2 原生库没有变成运行时 DLL 依赖
- Windows 发布包经 UPX 压缩后会在干净 runner 中解压并真实执行 `--version`，避免“能打包但无法启动”
- 本地 standard/full release profile 默认使用 `-s -w`；Windows full 从约 200 MiB 恢复到约 124 MiB，且架构测试阻止调试段再次进入发布构建

**v1 包边界与历史清理**

- 终端路由从 `core/terminal` 移至 `pkg/terminal`；`core` 只保留 AIScan 领域基础设施
- 删除 pre-v1 的重复 CLI 别名、配置字段、Playwright 命令和临时文件协议入口
- 移除不可用 recorder backend 的占位实现；不支持原生录制的平台不会注册伪 record 工具
- 架构测试覆盖包方向、legacy 标识、发布 profile、protobuf 字段和子模块 pin，历史债务重新出现会直接阻断 CI
- Web 控制台和 cyber-ui viewer 纳入生成一致性、前端构建和 E2E 门禁

### Bug Fixes

- 修复 zombie Runner 在取消任务时同时关闭和写入 `OutputCh` 的数据竞争，避免 race detector 报错及潜在 send-on-closed-channel
- 修复 runner 在返回前未等待清理完成，以及 Node 上报 panic operation 后遗留运行状态的问题
- 修复 Windows shell bridge 退出后遗留 runtime、PTY/tmux 并发测试互相污染和 offset 读取依赖无关时序的问题
- 修复 Web terminal 重连 teardown 与事件订阅并发时的状态竞争
- 修复 Windows recorder SDK 链接环境未跨 step 保留、MSYS 主机识别错误和 x264 下载源不稳定的问题
- 修复 cyber-ui record 卡片的 focus 状态，使录制结果在 Web 时间线中保持正确交互

### Breaking Changes

- FOFA 仅接受 `fofa_key` / `FOFA_KEY` / `--fofa-key`；Hunter 仅接受 `hunter_api_key` / `HUNTER_API_KEY` / `--hunter-api-key`
- Playwright 仅保留规范命令名，删除 `navigate`、`eval`、`netcap`、`text`、`text-content`、`html`、`inner-html`、`seval`、`sshot`、`select`、`wait`、`cookies` 等重复入口
- Agent Web/AOP 连接仅使用 `--server-url`；IOA 仅使用 `--ioa-url`
- AOP 文件分段读取直接使用 `ReadRequest.offset/limit` 与 `Result.offset/eof`，不再接受编码到 path 中的 range 请求
- evaluator 调用必须显式提供 `InitialInput`
- AOP tool protocol 增加规范 `Artifact` / `Loot` 消息；依赖旧临时 loot/file-range 编码的客户端需要重新生成 protobuf 并迁移

### Release Matrix

| 产物 | Linux | macOS | Windows |
| --- | --- | --- | --- |
| `aiscan` | amd64、arm64 | amd64、arm64 | amd64、arm64 |
| `aiscan-full` | amd64、arm64 | amd64、arm64（Linux 交叉编译） | amd64 |
| 原生 `record` | X11 amd64/arm64 | 不支持 | amd64 |

迁移细节、兼容承诺和发布门禁见 [v1.0.0 发布与迁移](v1.0.0.md)。

## v0.4.0 — Web 控制台升级 + Agent 上下文管理 + SCO 标准化输出 + 统一接入 API

### New Features

**Web 工作台（首次正式发布）**

v0.4.0 是 Web 工作台的首个正式版本。它不是单独的扫描结果页面，而是 aiscan 的浏览器入口：用户可以在同一个界面中选择 Agent、发起自然语言任务、观察工具执行、查看扫描资产与漏洞证据，并继续围绕已有结果追问。界面支持中英文、明暗主题和移动端访问，会话、扫描、配置和资产统一持久化到 SQLite，刷新页面或重启服务后仍可继续工作。

- 集成 Agent 对话、会话管理、扫描结果、资产中心、发现列表和配置中心
- 支持中英文切换、明暗主题和移动端布局
- 会话、扫描、配置和资产通过 SQLite 持久化
- 默认启动内嵌本地 Agent，并自动生成 access key

Full 版默认同时启动 Web 服务和一个本地 Agent，并自动生成 access key。最小启动命令只有一条：

```bash
aiscan-full web
```

**远程 Node 接入**

当扫描需要在其他主机、网络区域或专用执行环境中运行时，Web 可以作为统一 Hub 接收远程 Node。Node 上线后会向 Web 注册自己的 scanner、runtime command 和 skill，用户可在页面中选择执行节点；工具输出、PTY 终端、上传文件和扫描结果仍回到当前会话。下面是一个最小的远程接入示例：

- 自动发现远程 Node，并展示名称、版本、在线状态和忙闲状态
- 动态同步 Node 可用的 scanner、runtime command 和 skill
- 支持在页面中选择任务执行节点
- 自动挂载远程 Runtime REPL 和 PTY 终端
- QuickConnect 可生成不同系统、架构和下载线路的安装接入命令
- `--no-agent` 可让 Web 只作为 Hub 运行，不启动本地 Agent

```bash
# Web 所在主机
aiscan-full web --addr 0.0.0.0:8080 --token demo

# Node 所在主机
aiscan-full agent --server-url http://demo@server.example:8080 --node-name worker-01
```

**Agent 会话与执行过程**

Web 会把 Agent 的回答、thinking、工具参数、工具结果、Goal Evaluation、上下文压缩和 token 用量组织成一条可恢复的时间线。用户可以创建和切换会话、停止正在运行的任务、上传任务文件，并直接执行 `/status`、`/compact`、`/eval`、`/loop` 等 Runtime 命令。远程 Node 被发现后，其 Runtime REPL 和 PTY 终端会自动挂载到页面，不需要额外建立终端连接。

- 创建、切换、重置和删除 Agent 会话
- 流式展示回答、thinking、工具调用、工具结果和 token 用量
- 展示 Goal Evaluation、上下文压缩、子 Agent 和扫描进度等专用事件
- 支持停止运行中的任务，以及断线后的历史恢复和事件续传
- 支持上传文件并交给 Agent 使用
- 支持在 Web 中执行 Runtime slash command 和终端命令

**扫描、资产与报告**

扫描任务在对话中直接显示进度和结果，不再跳转到独立扫描页面。每次扫描提供资产、发现和报告三个视图：资产视图按主机、端口、应用和 URL 展示攻击面；发现视图集中展示漏洞、弱口令和敏感信息；报告视图根据结构化结果生成中文或英文侦察报告。gogo、spray、neutron、katana、proton 等独立工具调用也会转换为同一套 SCO 数据，因此可以继续通过分类 `@` 选择器把某个资产或漏洞引用到后续对话中。

- 在聊天时间线中展示扫描进度、完成状态和结构化结果
- 按主机、端口、服务、应用、URL 和漏洞展示 SCO 资产关系
- 分离同一主机的 HTTP/HTTPS 资产，并显示内容类型和重定向地址
- 独立 scanner 工具调用同样生成结构化资产视图
- 支持导入外部 SCO 数据，以及按类型浏览和统计资产
- 支持通过分类 `@` 选择器在对话中引用资产或漏洞
- 支持按当前界面语言生成中文或英文侦察报告

**配置与协作**

Web 配置中心用于管理多个 LLM profile，并显式选择当前模型。Provider、Base URL、API key、模型、代理、上下文窗口和最大输出可以在页面中修改和探活，更新后热重载到已连接的 Agent。Web 同时提供 IOA Console，用于查看协作空间、在线节点、消息和线程；如果只需要轻量 IOA 服务，也可以使用顶层 `aiscan serve` 启动。

- 创建和管理多个 LLM profile，并显式切换当前 profile
- 提供常用 Provider 预设及自定义 OpenAI/Anthropic 兼容端点
- 配置模型、代理、上下文窗口和最大输出，并执行真实连通性检查
- 配置变更事务化保存并热重载到在线 Agent
- IOA Console 支持查看空间、节点、消息和上下文线程
- Web 与 IOA 共用 access key，也可为 Agent 指定独立 IOA 地址

**Agent 上下文与输出控制**

这组功能面向长时间、跨多轮的安全评估任务。AIScan 会根据模型的真实上下文窗口管理输入与输出预算，在接近上限时压缩历史，避免任务因为 context overflow 中断；同时允许用户控制终端中展示多少 thinking 和工具细节，在交互可见性与输出噪声之间取得平衡。

- 新增 `context_window` 和 `max_tokens` 配置；请求会根据剩余上下文动态收紧输出上限，避免无效请求和上下文溢出
- 新增 `/compact [focus]` 手动压缩会话；上下文接近上限时自动压缩，使用率超过 80% 时提示用户
- `-p/--prompt` 现在可直接传入已有文件路径并读取文件内容
- 新增 `output` 配置，可分别控制 reasoning、工具参数、工具结果、实时状态和 token 用量的展示；继续兼容 `-q`、`-v`、`-vv` 与 `Ctrl+O`

```bash
# 从文件读取任务描述
aiscan agent -p ./assessment.md -i https://target.example

# 交互模式中压缩上下文，并指定摘要重点
/compact 保留已确认漏洞、凭据和待验证目标
```

```yaml
llm:
  context_window: 128000
  max_tokens: 16384

output:
  preset: verbose       # default、verbose、full
  tool_results: preview # hidden、preview、full
```

**标准化扫描结果**

SCO 标准化输出用于解决不同 scanner 各自返回独立格式、结果难以关联和复用的问题。无论结果来自完整 scan 流水线还是单独执行某个 scanner，AIScan 都会把主机、端口、应用、URL 和漏洞转换为统一资产节点，供 Web 展示、报告生成、外部查询和后续 Agent 分析共同使用。

- 扫描流水线及 gogo、spray、neutron、katana、proton 等独立工具输出接入 SCO 标准化资产模型
- Web 端可按主机、端口、应用、URL 和漏洞关联展示结果，独立 scanner 工具调用不再只显示原始文本
- 新增 SCO 数据导入、查询和统计能力，便于复用外部扫描结果

**OKF 知识文档与报告结构**

OKF 风格文档用于组织 Agent 的工具知识和最终交付物。工具说明不再作为大量独立 skill 一次性注入上下文，而是形成带索引和元数据的知识包，在真正调用某个工具时按需加载；扫描报告也使用相同思路，把总览、单个 Finding 和证据来源组织成可以追踪和继续处理的文档集合。

- 原有分散的 scanner/runtime skill 收敛为单一 `aiscan` skill，工具文档按 OKF 风格拆分为可按需加载的 concept 文件
- 知识包分为 `easm` 和 `runtime` 两个 domain，每个目录包含 `index.md` 和带 YAML frontmatter 的工具 playbook
- Agent 调用工具时可通过 `aiscan://skills/aiscan/okf/...` 按需读取对应文档，避免启动时加载全部工具说明
- 安全报告改为 OKF 风格目录：`index.md` 提供摘要，每个确认漏洞或重要线索写入独立的 `findings/<id>.md`
- Finding frontmatter 记录 `status`、`severity`、`verified`、`sources` 和 `tags`，确认漏洞优先引用 MITM 请求/响应与实际执行过的 nuclei/neutron PoC

```text
skills/aiscan/
├── SKILL.md
├── okf/
│   ├── index.md
│   ├── easm/
│   │   ├── index.md
│   │   ├── scan.md
│   │   ├── gogo.md
│   │   ├── spray.md
│   │   └── neutron.md
│   └── runtime/
│       ├── index.md
│       ├── tmux.md
│       ├── proxy.md
│       ├── mitm.md
│       └── search.md
└── reference/
    └── report.md
```

生成的报告目录示例：

```text
report/
├── index.md
└── findings/
    ├── shiro-rce.md
    └── exposed-credential.md
```

**外部接入 API**

外部接入 API 面向需要把 aiscan 嵌入其他平台、桌面客户端或自动化系统的开发者。实时对话和工具事件使用长连接 Application WebSocket，管理查询使用 ConnectRPC，两者共享 protobuf 类型和 access key，避免第三方系统依赖 Web 页面或解析终端文本。

- 实时 Agent 会话统一提供基于 protobuf 的 Application WebSocket，支持 Session/Turn、流式消息、工具调用、文件、PTY、取消与断线续传
- 会话历史、扫描、配置、Agent、系统状态和 SCO 管理统一提供 ConnectRPC API
- 新增 protobuf 字段文档、跨语言代码生成说明，以及 ACP client/server、ConnectRPC 和 RMCP 工具节点示例

```bash
# 启动带内嵌 Agent 的服务
aiscan-full web --addr 127.0.0.1:8080 --token demo

# Application WebSocket：创建会话、发送消息并消费流式事件
go run ./examples/acp/client --server http://127.0.0.1:8080 --token demo --node local -p "检查当前可用工具"

# ConnectRPC：查询会话与持久化事件
go run ./examples/acp/connectrpc --server http://127.0.0.1:8080 --token demo
```

### Improvements

**LLM 配置与可靠性**

- Web 配置页支持多个 LLM profile、显式选择当前 profile、Provider 预设、上下文窗口和最大输出设置
- 配置优先级与 Provider 协议推断更加明确，环境变量不会再意外覆盖已保存的 Base URL 或模型
- LLM 重试、退避和上下文溢出恢复更加稳定；无剩余输出空间时返回包含窗口和用量信息的明确错误

**扫描与 Agent 体验**

- Agent 默认加载人工安全评估规则，减少只给出扫描结论而缺少证据验证的情况
- Proton 文本输出新增 `[match:<name>]` / `[extract]` 分级标签，并分别统计 match 与 extract 数量
- `bash` 工具支持为单次调用指定 timeout
- CLI 各子命令独立展示所属参数，减少 scanner 参数与全局参数混淆
- Web 服务启动更快，远程 Agent 上线后会自动挂载 Runtime REPL；终端重连、会话恢复、扫描取消和事件续传更加稳定

### Bug Fixes

- 修复 Proton 预过滤导致 private key、JWT、Stripe、数据库连接串等大量规则漏报的问题
- 修复 Proton JSON 输出字段不一致，统一为 `template-id`、`template-name` 等 nuclei 风格字段
- 修复 Neutron JSON 结果缺少实际请求和响应，漏洞复现现在可展示完整证据
- 修复同一主机的 HTTP/HTTPS 资产被错误合并，并补充 `content_type`、`redirect_url` 信息
- 修复 gogo 向 neutron 注入模板后缺少 ChainExec，导致开放主机上的 exploit 扫描 panic 并提前终止的问题
- 为 scanner 和工具执行增加统一 panic recovery，单个工具异常不再直接中断整个 Agent 或扫描流程
- 修复代理处理 HTTP CONNECT 时可能丢失隧道首批数据的问题
- 修复 Web 配置热重载部分生效、无效 LLM profile 可保存、扫描取消状态不完整等问题
- 修复 Web 会话事件订阅间隙、历史回放覆盖实时消息、终端重连状态竞争和多行命令输出折叠问题
- 统一会话持久化为 AOP JSONL，并在 `/clear`、`/compact` 和恢复会话时创建可追踪的 continuation，避免覆盖已有记录
- 修复 Web Runtime 命令和 Turn 健康状态不同步，并扩展 `/status` 的 LLM、工具、扫描器和 Skill 健康信息
- 修复 TUI 补全、滚动区域、运行日志和双击 Ctrl+C 退出不稳定的问题

### Breaking Changes

- `llm.providers` 现在是可手动切换的 profile 列表，不再在请求失败后自动切换 Provider；使用 `llm.active_profile`、Web 设置页或 `/provider set` 显式选择
- Agent 连接 AIScan Web/AOP 统一使用 `--server-url`；IOA 通过独立的 `--ioa-url` 配置，未指定时默认使用 `<server-url>/ioa`
- 官方 Release 不再单独发布 `aiscan-agent`，请统一使用 `aiscan agent`
- 独立的 gogo、spray、neutron、proton 等顶层 tool skill 已收敛到 `aiscan` skill；工具细节改为调用时加载 `okf/easm` 或 `okf/runtime` concept
- 报告输出由单一 Markdown 内容调整为 `index.md` + `findings/<id>.md` 的 OKF 风格 bundle
- 外部 Web/Agent 接入迁移到 protobuf Application WebSocket 与 ConnectRPC；依赖旧 JSON WebSocket、管理 REST 或旧 endpoint 的客户端需要迁移
- Proton JSON 字段从下划线命名迁移为连字符命名，例如 `template_id` → `template-id`

---

## v0.2.8 — 外部 API 工具错误治理 + 文件上传 + Agent 提示优化

### New Features

**文件上传**

- 支持通过 agent 通道直接上传文件

**`--tavily-key` flag**

- Tavily web search 现在可通过 CLI flag 配置，不再仅限 config 文件和环境变量

```bash
aiscan agent --tavily-key tvly-xxx -p "search CVE-2024-1234"
```

### Improvements

**外部 API 工具错误治理**

未配置 API key 时，`passive`、`web_search`、`search cyberhub` 等工具之前要么不注册（agent 看不到），要么返回模糊错误导致 agent 反复重试。现在统一为：始终注册，缺 key 时返回一次性明确错误，列出所有配置方式（flag / env / config），agent 不会再重复调用。

```
passive: no recon credentials configured.
  Set via flags (--fofa-key, --hunter-api-key),
  env (FOFA_KEY, HUNTER_API_KEY),
  or config file (recon.fofa_key, recon.hunter_api_key).
  Do not retry until credentials are provided
```

**Agent 工具调用准确率提升**

- 将 quick-reference 文档嵌入 system prompt，减少 agent 对扫描工具的试错调用
- read tool 输出移除冗余行号前缀，降低 token 消耗

### Bug Fixes

- 修复 CI 构建失败：移除 go.mod 中 `tui/console` 和 `tui/readline` 的本地 `replace` 指令，改用远程发布版本

### Dependencies

- **zombie** `v1.2.3` → [`v1.3.0`](https://github.com/chainreactors/zombie/releases/tag/v1.3.0)
- **spray** `v1.3.2` → `v1.3.3`
- **proton** → `v0.3.1`
- **ioa** `v0.1.1` → `v0.1.2`
- **tui/console**、**tui/readline** — 迁移到 chainreactors/tui，替代 reeflective fork
- **utils** 全子模块升级到 `20260630`，新增 `utils/parsers` 子模块
- 所有 chainreactors 依赖升级到 master 最新

---

## v0.2.7 — MITM 流量捕获 + Proton 敏感信息扫描 + /loop 循环任务 + TUI 交互增强

MITM 透明流量拦截（`proxy mitm` 子命令族）；Proton 敏感信息扫描器（SDK 引擎 + 197 条内嵌规则 + 双向管道）；`/loop` 循环任务调度；TUI 交互全面增强（verbosity 切换、中断控制、文件补全、实时 token 用量）；多 Provider 列表配置格式；FOFA key-only 认证支持。

### New Features

**Proton — 敏感信息扫描器**

- 内嵌 197 条 YAML 检测规则（API key、token、credential、私钥、数据库连接串等），覆盖 AWS/GitHub/Stripe/GCP 等 156+ 模板
- 基于 SDK `proton.Engine` 构建，从硬编码规则迁移为模板引擎 + `ResourceProvider` 架构
- 对齐 neutron CLI 模式：`-l/--list` 多目标输入、`--stats/--silent` 输出控制、`--template-list` 模板列表
- 支持代理（`WithProxy()`/`SetProxy()`），自动接入 `deps.ScannerProxy`

```bash
# 扫描目录
proton -i /path/to/project

# 管道组合 — shell 输出 → proton
curl http://target/api/config | proton
cat .env.production | proton

# 管道组合 — proton 输出 → shell
proton -i . | grep critical

# 指定模板标签
spray -u http://target | proton --tags spray
```

**双向管道支持**

- Pseudo-command → Shell：伪命令输出通过 buffer 管道到 `sh -c` 执行的 shell pipeline
- Shell → Pseudo-command：shell 输出经临时文件通过 `StdinReceiver` 接口传递给伪命令
- 安全约束：仅支持单管道 `|`，拒绝 `||`、`>`、`&&`、`;` 防止沙箱逃逸

```bash
# 双向管道示例
scan -i target -j | head -20       # pseudo → shell
cat targets.txt | spray -u stdin   # shell → pseudo
```

**/loop — 循环任务调度（cron 表达式）**

- `loop` 作为 bash pseudo-command 注册，agent 通过 `bash(command="loop ...")` 直接调用
- 支持标准 5 字段 cron 表达式（`*/5 * * * *`）和 Go duration 简写（`30s`/`5m`/`1h`）
- `/loop` REPL 快捷命令直接执行，不经 LLM 中转
- 内置 cron 解析器，支持 `*`/`*/step`/`range`/`range/step`/`list` 全部语法
- name 自动生成，无需手动命名

```bash
# cron 表达式
/loop */5 * * * * check scan progress         # 每 5 分钟
/loop 0 */2 * * * review findings             # 每 2 小时
/loop 30 9 * * 1-5 daily standup check        # 工作日 9:30

# duration 简写
/loop 30s check status
/loop 5m monitor targets

# 管理
/loop list
/loop stop loop-a1b2c3d4

# agent 通过 bash 调用
bash(command="loop */5 * * * * check scan progress")
bash(command="loop list")
```

**MITM 流量捕获**

透明 HTTP/HTTPS 流量拦截，集成 utils/mitmproxy 到 proxy 命令组。扫描引擎（gogo/spray/zombie/neutron）自动路由到本地 MITM 代理；若已有外部代理（trojan/vless/clash）则作为上游透传。

- `proxy mitm start [--addr]`：启动本地 MITM 代理，自动切换扫描引擎代理
- `proxy mitm stop`：停止 MITM 并恢复之前的代理设置
- `proxy mitm status`：查看状态和 flow 计数
- `proxy mitm flows [--host/--status/--type/--last]`：按条件查询捕获的 HTTP 流
- `proxy mitm flow <id>`：查看单个 flow 详情
- `proxy mitm clear`：清空 flow 存储
- `proxy mitm analyze [--host/--last]`：结构化输出供 AI 分析

```bash
# 启动 MITM 拦截
proxy mitm start --addr 127.0.0.1:8888

# 正常执行扫描（流量自动经过 MITM）
scan -i target

# 查看捕获的流量
proxy mitm flows --last 20
proxy mitm flow 42

# AI 分析捕获的请求
proxy mitm analyze --host target.com

# 停止并恢复
proxy mitm stop
```

### Improvements

**TUI 交互增强**

- **Ctrl+O 切换 verbosity**：四级循环（quiet → default → tools → thinking），运行中动态调整
- **Ctrl+C / Esc 中断**：Ctrl+C 中断当前任务（双击退出），Esc 中断并区分 escape 序列
- **@ 文件补全**：基于 carapace 的 `@` 前缀文件路径自动补全
- **Spinner 快捷键提示**：agent 执行中展示 `Esc interrupt  Ctrl+O verbosity` 提示
- **Thinking 渲染稳定化**：reasoning/content 流分离，避免混合输出时终端闪烁
- **Agent 实时状态统一**：`LiveStatus` 集中管理 thinking/tooling/talking 状态和并行工具追踪
- **累计 token 用量实时展示**：显示 context window 占用百分比，跨 turn 累计 prompt/completion/total

**并发工具执行 OOM 防护**

- 信号量限流：`MaxParallelTools` 默认 16 并发槽位，防止无限并行导致 OOM
- 移除 ExecParallel/ExecSequential 模式，统一为共享信号量队列

**多 Provider 列表配置**

- 新增 `llm.providers` 列表格式作为主要配置方式，`providers[0]` 为主 provider，其余为降级链
- 向后兼容：单 provider 字段（provider/api_key/model）仍可使用，优先级高于列表
- 两种格式可混用：单字段 + 列表 = 单字段为主，列表为降级备选

```yaml
# 新格式 — 多 provider 列表
llm:
  providers:
    - provider: deepseek
      api_key: sk-...
      model: deepseek-chat
    - provider: openai
      api_key: sk-...
      model: gpt-4o
```

### Refactoring

**配置文件重命名**

- `config.yaml` → `aiscan.yaml`，避免与其他项目的通用 config.yaml 冲突

**Scanner 工具基础设施精简**

- **Resources 统一**：4 套独立 config map（gogo/spray/zombie/proton）合并为单一 `configs map[string]map[string][]byte` + `Config(engine, name)` 方法
- **toolargs 共享工具包**：提取 `ResolveRelativePaths` 和 `NormalizeFlags` 到 `toolargs/`，6 个工具共用（proton/neutron/scan/spray/zombie/katana）

### Bug Fixes

- **FOFA key-only 认证**：FOFA 2023 年简化认证后只需 API key，但 aiscan 仍要求 email+key 双字段才注册 fofa 引擎。修复后仅 `FofaKey` 即可使用 `passive -s fofa`，同时兼容旧版 `email:key` 格式（#41）
- 修复测试中的 Stripe key 触发 GitHub push protection（替换为假 key）
- 修复 cumulative usage 事件发射，确保跨 turn token 统计正确
- 修复 agent live status 渲染不一致
- 修复并发 data race：TUI 测试 stderr buffer、zombie OutputCh、spray/logs concurrent logger
- 清理历史重构遗留的无效引用（`Deps.Model`、已删除的 `SDKRecover` 测试）

### Dependencies

- **spray [v1.3.1](https://github.com/chainreactors/spray/releases/tag/v1.3.1)**：mask 表达式支持所有请求字段、`--keys` 插件内嵌 156 条 proton 模板、extract severity 分级 + 上下文捕获、修复 crawl-only 提前 drain 和 OutputCh panic
- **utils/cert**：集成 utils/cert 原子化证书原语（CA 生成、子证书签发、随机 Subject、PEM 工具函数），移除本地 replace
- bump SDK、zombie、logs、utils/pty 修复上游 data race

### Breaking Changes

- **配置文件名变更**：`config.yaml` → `aiscan.yaml`，需手动重命名现有配置文件
- **`/reset` 重命名为 `/clear`**

---

## v0.2.6 — Session 持久化 + 多模型容错 + 输出格式统一 + 命令架构重组

Session 会话持久化（`--resume`/`--save-session`）；非视觉模型图片容错（三层防御：静态模型注册表 + 请求清洗 + 运行时自动恢复）；统一输出记录格式；命令架构重组为 aiscan/aiscan-agent/web 三入口。

### New Features

**Session 持久化**

- `--save-session`：自动保存 agent 对话到 `.aiscan/sessions/`，每次 run 后持久化
- `--resume`：恢复最近一次保存的 session
- `--resume <path>`：从指定 session 文件恢复
- 反射驱动的 config 生成，自动同步 CLI flag 与配置文件字段

```bash
# 自动保存对话
aiscan agent -p "scan target" --save-session

# 恢复最近 session 继续
aiscan agent --resume -p "now check the results"

# 从指定文件恢复
aiscan agent --resume .aiscan/sessions/2026-06-22_scan.json
```

**Config 路径 Fallback 链**

- 配置文件查找顺序：`-c` 指定 > 当前目录 > 二进制所在目录
- 数据目录（`.aiscan/`）统一跟随二进制路径

### Improvements

**多模型图片容错（三层防御）**

针对 DeepSeek、Qwen、GLM 等不支持图片的模型，解决了图片内容导致 400 错误后 session 无法恢复的问题：

1. **静态预防** — 从 Claude Code 的模型注册表提取 30+ 模型族关键词，自动识别 text-only 模型（deepseek/qwen/glm/mistral/llama/kimi/minimax 等），图片在发送前 strip
2. **请求清洗** — `sanitizeMessages` 过滤历史中的空 assistant 消息，防止旧 session 或失败 turn 的遗留消息污染上下文
3. **运行时自动恢复** — 未知模型遇到图片相关 400 错误时，自动调用 `DisableImages()` 并重试，后续请求持久生效

**输出记录格式统一**

- 所有工具输出统一为 tool-named record 类型
- 新增 loot flag 标记高价值发现
- Agent 输出自动包装为结构化记录

**命令架构重组**

- 拆分为 `aiscan`（全功能）、`aiscan-agent`（最小 agent）、`web`（子命令）三入口
- Arsenal 工具始终加载，无需额外 flag
- 解决 passive scanner 循环导入问题

### Bug Fixes

- `IsRetryable` 从黑名单改为白名单（仅 429/500/502/503/529），防止 400 Bad Request 无限重试
- 错误路径不再向 transcript 追加空 assistant 消息，防止 session 损坏
- 统一 panic recovery 覆盖 tool 执行和 scan pipeline
- 修复 passive scanner 包循环导入
- PTY 兼容 Windows 7 / Server 2008（utils/pty 更新）

---

## v0.2.5 — Arsenal 工具管理 + TUI 重设计 + 命令接口统一 + PTY 平台整合

新增 Arsenal（crtm）安全工具包管理器；Playwright 新增 `-s` 全局 session flag；TUI verbose 渲染全面重设计；命令执行统一为 invocation 级输入输出；4 平台 PTY 文件整合为单一 go-pty wrapper。

### New Features

**Arsenal — crtm 安全工具包管理器**

- `arsenal install/update/remove`：安全工具的安装、更新、卸载，幂等操作
- manifest 机制：`arsenal list` 瞬时版本查询，无需遍历文件系统
- 从 AgentTool 重构为 bash pseudo-command，统一执行模型
- 自动注入 `$PATH`，安装后的工具立即可通过 bash 调用

```bash
# 在 REPL 或 agent 对话中使用（通过 bash pseudo-command）

# 查看所有可用工具及安装状态
!arsenal list

# 搜索关键词
!arsenal search subdomain

# 安装工具（自动下载 + 注入 PATH）
!arsenal install httpx
!arsenal install nuclei --version v3.3.0

# 安装后立即可用
!httpx -l targets.txt -silent

# 更新 / 卸载
!arsenal update httpx
!arsenal remove nuclei

# 添加第三方仓库
!arsenal add projectdiscovery/subfinder
```

**Playwright — `-s` 全局 session flag**

- 所有子命令支持 `-s=<name>` / `-s <name>` 指定目标 session，对齐 playwright-cli 习惯
- 环境变量 `PLAYWRIGHT_CLI_SESSION=<name>` 设置默认 session

```bash
# -s flag 替代位置参数指定 session
playwright -s=mySession click "button"
playwright -s=s1 goto
```

**TUI — verbose 渲染重设计**

- ▸/✓/✗ 标记替代 ⎿/│ 盒线，结构化 key-value 参数展示
- turn 统计新增 cache hit ratio（`cached=85%`）
- 耗时颜色编码（<1s 绿色，1-5s 黄色，>5s 红色）
- 并行 tool 调用标记（`[parallel 3/3]`）
- turn 开始分隔标记
- agent 结束时汇总 tool 调用统计
- eval 渲染增强（verdict + feedback 结构化展示）
- result preview 行数限制优化
- `-vv` 模式禁用输出截断，显示完整 tool result

### Architecture — 代码精简

**命令接口统一**

- Command 执行统一使用 invocation 级输入输出流，避免跨会话共享全局 writer
- 每次伪命令调用持有独立的输入输出流和执行状态，Registry 不再切换进程级输出对象
- `FetchTool` wrapper 移除：`fetch` 从 `RegisterTool` 转为直接 `Register` 的 Command
- `SetExecHooks` 注入 tmux.Manager，打破 commands ↔ output 的循环依赖

**PTY 平台整合**

- 4 个平台特定 PTY 文件（`pty_darwin.go`/`pty_linux.go`/`pty_unix.go`/`pty_other.go`）替换为单一 `go-pty` wrapper
- `tmux.Manager` 提取 `finishSession()` 去重 supervise 逻辑
- IOA 函数从 8 个导出简化为 4 个（统一 writer 参数）

**其他精简**

- 删除死代码 `CommandNames()` stub、`captureStdoutForTest`、`canHyperlink`/`hyperlinkSummary`

### Robustness

- **agent retry 扩展**：HTTP 406 等瞬态错误纳入可重试范围

### Bug Fixes

- 修复测试失败：pseudo-command 缺少 `SetExecHooks` 导致输出到 `io.Discard`
- 修复 `go.mod` 本地 replace 路径导致 CI 构建失败
- 解决全部 golangci-lint 错误
- 修复 DirectScanner 测试数据竞争

### Breaking Changes

- **Command 执行模型变更**：命令通过 invocation 级输入输出流读写，不再共享进程级输出状态
- **`FetchTool` 移除**：`fetch` 不再是独立 `AgentTool`，改为普通 `Command` 通过 `Register` 注册

---

## v0.2.3 — Playwright 全面升级 + Provider 双协议简化 + TUI 流式渲染 + IOA 架构精简

本版本包含 **Breaking Changes**。核心变更：Playwright 浏览器自动化对齐 microsoft/playwright-cli 接口，Provider 层简化为 openai/anthropic 双协议，TUI 流式 Markdown 渲染，移除 `--loop` 和 `checkpoint`/`loop` custom tool。

### Breaking Changes

- **`--loop` 移除**: 设置 `--ioa-url` 即自动启用 IOA worker 模式，不再需要单独的 `--loop` flag。迁移：`aiscan agent --loop --ioa-url http://... --space s1` → `aiscan agent --ioa-url http://... --space s1`
- **`checkpoint`/`loop` tool 移除**: `checkpoint` 已迁移到 IOA protocol（`ioa_send checkpoint`），verify/sniper 子 agent 改用 `finish` tool + 结构化 status header；`loop` 不再作为 LLM custom tool 暴露，LoopScheduler 内部机制（`--heartbeat`）保留
- **Provider 简化为双协议**: 移除 deepseek/groq/moonshot/ollama/openrouter 等独立 provider type，统一为 openai（OpenAI-compatible）和 anthropic 两种协议，通过 `--base-url` 指定实际端点
- **`-q` 静默模式移除**: 被 `-v`/`-vv` 分级详细度替代

### New Features

**Playwright — 对齐 microsoft/playwright-cli 接口**

- 新增 `cookie-list`/`cookie-get`/`cookie-set`/`cookie-delete`/`cookie-clear` 五个独立 cookie 命令
- 新增 `storage-list`/`storage-get`/`storage-set`/`storage-delete`/`storage-clear` 覆盖 localStorage 和 sessionStorage 完整 CRUD
- 新增 `console`：通过 `EvalOnNewDocument` JS 注入，从 session open 开始自动捕获 `console.log/warn/error`
- 新增 `snapshot`：CDP `Accessibility.getFullAXTree` 获取可访问性树，支持 `--depth` 控制层级
- 新增 `requests`/`request <index>`：session open 时自动启动网络捕获，列出全部请求或查看单条详情（headers、post data）
- 新增 `route-list`、`state-save`/`state-load`、`dialog-accept`/`dialog-dismiss`
- `open` 新增 `--headed`（GUI 窗口）和 `--cdp <endpoint>`（连接已有浏览器）
- 移除 session GC/TTL 机制，session 持久存活直到 `close` 或进程退出，LRU 8 上限保留

**图像优化 — LLM 视觉输入管线**

- 截图自动优化：缩放至 2000×2000 以内，PNG vs JPEG 双编码取较小，渐进降质直到 base64 < 4.5MB
- 非视觉模型自动降级：基于 provider type + model 名推断图像支持能力，不支持时替换为文字提示

**TUI — 流式 Markdown 渲染 + 分级详细度**

- 段落缓冲式 Markdown 渲染 + chroma 语法高亮（read tool 结果带行号）
- `-v`/`-vv` 分级详细度：默认流式内容 + turn 统计；`-v` 显示 tool call 详情；`-vv` 显示 thinking content
- 每个 turn 结束显示 `[turn N | tools=X | input=Y (+ Z cached) output=W | Ns]`
- Agent 结束显示 `[agent STATUS | turns=N | input=Y (+ Z cached) output=W | Ns]`

**Evaluator — Context Window 感知 + inherit_context**

- 内置模型 context window 查询表（Claude/DeepSeek/GPT/Gemini/Qwen/Kimi），未匹配 fallback 128k
- verdict 新增 `inherit_context`：evaluator LLM 决定下一轮是否继承对话历史，`false` 时 `agent.Reset()`
- system prompt 明确阈值：>80% 必须 reset，>50% 建议 reset，<=50% 默认继承

**IOA — Token Auth**

- server 端 `--ioa-token` 设置访问密钥，client 端 `http://token@host:port` URL 格式自动认证
- `ensureNode` 通过 `EnsureRegistered` type assertion 实现 auth-aware 节点注册

### Bug Fixes

- **Anthropic 兼容 API**: 第三方端点（如 DeepSeek `/anthropic`）不识别 `type: "custom"` tool 类型返回 400。改为仅在 `anthropic.com` 端点发送该字段，第三方省略
- **环境变量 provider 推断**: 仅设 `OPENAI_API_KEY` 或 `ANTHROPIC_API_KEY` 时未自动推断 provider，导致 env alias 失效。修复：从 API key env var 存在性推断 provider
- **tmux 增量读取**: `capture-pane` poll 循环意外推进增量游标，导致 `--new` 读取为空。修复：poll 改用 `--full`
- **evaluator 历史丢失**: evaluator 仅收到当轮消息，重试时丢失前几轮 context。改为传入完整 transcript
- **非视觉模型图像拒绝**: 不支持 multimodal 的 provider 收到 `image_url` 返回 400。新增 per-provider 图像支持推断 + strip

---

## v0.2.2 (2026-06-16)

新增 goal evaluation 闭环机制——独立 LLM 评估 agent 任务完成度并自动注入反馈驱动重试；内嵌 katana 爬虫引擎支持 headless 浏览器；新增多 provider 容错降级链；重构 TUI/REPL 为统一 pkg/tui 模块；大幅整理包结构，aiscan 专用包从 pkg/ 移入 core/。

### New Features

**goal evaluation — 独立评估 + 反馈重试闭环（核心）**

- 新增 `-e` / `--eval` 指定目标评估标准，`--eval-model` 可选独立评估模型，`--eval-retries` 控制最大评估轮数（默认 3）
- 评估机制：agent 完成一轮执行后，独立 evaluator LLM 接收压缩后的 execution trace（tool call 序列 + assistant 摘要 + final output），通过强制 tool call（verdict tool）返回结构化判定（pass/reason/feedback）
- 闭环重试：verdict.pass=false 时，evaluator 的 feedback 作为新 prompt 注入 agent 继续执行，直到 pass=true 或达到最大评估轮数
- evaluator 调用失败时降级为通用反馈（"请检查你的工作并继续"），不中断主流程
- trace 压缩策略：仅保留 tool call 序列和 assistant 摘要，不传完整 tool result，最大 16KB 防止 context 膨胀
- 全程通过 eventbus 发射 `GoalEvalStart` / `GoalEvalEnd` / `GoalEvalError` 事件，TUI 实时展示评估进度和结果

**katana — 进程内爬虫 + headless 引擎**

- 将 katana 从外部二进制调用重构为进程内 SDK 集成，通过 goflags 解析参数保持完整 CLI 兼容性，OnResult 回调收集结果
- 新增 headless/hybrid 引擎支持，根据 `-hl`/`-hh`/`-cwu` 标志自动选择引擎

**multi-provider — 容错降级链**

- 当主 provider 重试耗尽后，agent loop 自动切换到降级链中的下一个 provider 并重放当前 turn
- 配置文件 `llm.providers` 数组定义降级链，启动时并行初始化（失败跳过）
- 新增 REPL `/provider` 命令展示 provider 链的 active/standby 状态

**agent — finish tool / thinking block / web search**

- 新增 finish tool：通过 `ToolResult.Terminate` 显式终止 agent loop
- 非流式响应支持解析 Anthropic thinking block 为 `ReasoningContent`
- 新增 `WebSearchProvider` 接口，Anthropic 走 `web_search_20250305` server tool，OpenAI 走 Responses API；provider 原生搜索失败时回退 Tavily/DDG

**heartbeat + tmux 增量监控**

- `--heartbeat` 接入 LoopScheduler 作为通用周期唤醒
- tmux 后台命令自动推送增量输出到 agent inbox（每 10s per-session goroutine）
- `capture-pane` 新增 `-n`（末尾 N 行）和 `-c`（末尾 N 字节）参数

**信号处理 — 两阶段 Ctrl+C**

- 第一次 Ctrl+C 停止当前任务，第二次退出 REPL，第三次强制退出

### Bug Fixes

- **scanner CLI**: `aiscan scan` / `aiscan gogo` 等直接命令模式因引擎异步加载导致 "unknown subcommand" 失败。新增 `WaitEngines(ctx)` 同步等待引擎就绪

### Refactoring

- `pkg/app` 合并进 `core/runner`，删除 `pkg/app`
- `eventbus`、`pidlock`、`resources`、`output`、`harness` 从 `pkg/` 移入 `core/`
- TUI/REPL 提取到 `pkg/tui`，合并 `pkg/repl`
- evaluator 使用 tool call 结构化输出替代 JSON text fallback
- cyberhub 基于 SDK association index 重建，新增结构化查询 flag
- provider 层简化：移除中间结构体，提取共享 HTTP 工具

### Dependencies

- SDK `v0.2.4` → `v0.3.2`
- 新增 SDK panic recovery
- 42 个 e2e 测试

---

## v0.2.1 — IOA 集成重构 + AI 驱动监听 (2026-06-09)

适配 IOA v0.1.0 的统一架构。核心变更：多 Agent 协作从自动推送切换为 AI 主动监听。

### Breaking Changes

- `--ai` 标志移除 — 使用 `--verify=high --sniper` 替代
- IOA build tag 移除 — SQLite、MCP、Auth 始终内置

### IOA 协作

- AI 驱动的实时监听替代 push-to-inbox
- ioa_read 新增 `--direction` 参数（upstream/downstream）
- IOA 内置 Server：`--ioa-db` 持久化，MCP endpoint 始终可用
- ioa_send 新增 `--content_type` 参数

### Skill 更新

- ioa/SKILL.md — 新增 Background Monitoring 段落、`--direction` 过滤文档
- ioa/swarm.md — 工作阶段从轮询改为 tmux peek

### 文档

- README、usage.md、quickstart.md、configuration.md 全面更新

---

## v0.2.0 — Playwright 浏览器引擎 + Agent/Skill/Pipeline 全面重构 (2026-06-08)

架构级大版本更新 (148 commits)。核心引入 Playwright 浏览器引擎、TMux 交互式终端、Proxy 代理管理、Passive Recon、Search 搜索等新工具模块，同时对 Agent / Tool / Skill / Scan Pipeline 四大子系统进行全面重构。

### Breaking Changes

- `browser` 和 `recon` build tag 合并为单一 `full` tag
- `ioa` 独立二进制移除，通过 `aiscan ioa` 子命令访问
- 每个平台仅产出 `aiscan`（基础版）和 `aiscan-full`

### Tool 更新

- **Playwright** — 22 个命令，Session Recorder 生成 nuclei headless 模板，完整兼容 nuclei headless 协议
- **TMux** — 统一 bash/tmux 执行层 + task manager，完整 PTY 支持
- **Proxy** — Clash 订阅解析，trojan/vless/anytls/hy2/ss 多协议，代理池管理
- **Passive Recon** — 集成 uncover，支持 FOFA/Hunter
- **Search** — WebSearch (Tavily)、WebFetch、CyberhubSearch、Multimodal vision

### Agent 更新

- 统一 Agent 抽象，SubAgent 三模式，模板化 Prompt
- 统一 EventBus，Per-turn Token 可观测性，LLM Prompt Cache

### Scan Pipeline 更新

- 基于订阅的 DAG Pipeline，统一 AI Skill 插件架构
- Loot 类型统一，`-f` JSONL 输出，Katana crawl 集成

### IOA & Swarm 更新

- `protocols/` 动态协议注册，Checkpoint 同步至 IOA Space
- Swarm 多节点协作调度增强

---

## v0.1.2 (2026-06-08)

- fix cli scanner flag isolation
- feat: add `--proxy` for scanner tools and `--llm-proxy` for LLM API

## v0.1.1 (2026-06-08)

- fix: resolve remaining CI test failures

## v0.1.0 (2026-06-08)

- refactor: unify capability pipeline, remove registry abstraction
- refactor: migrate pkg/acp to standalone github.com/chainreactors/ioa
- feat: agent loop resilience, capacity-driven concurrency, verification enhancement
- feat: add console agent REPL
- feat: add config.yaml system and build script
- feat: ACP CLI query subcommands and enhanced space tool
