# Changelog

## v0.2.5 — Remote PTY + Web Terminal + Arsenal 工具管理 + 架构精简

本版本核心引入远程 PTY 机制和浏览器终端，实现 agent ↔ web 双向交互式 shell；新增 Arsenal（crtm）安全工具包管理器；大幅精简代码架构——合并三份静态资源、替换手写 YAML 解析器、迁移到 Go 1.22+ ServeMux 路由。

### New Features

**Remote PTY + Web Terminal — 浏览器内操控远程 agent**

- `pkg/webproto`：Message ↔ Frame 序列化层，支持 data/data_b64 双通道编码，14 种帧类型（open/attach/input/output/resize/detach/kill/list 等）
- `pkg/webagent`：agent 侧 WebSocket 连接，PTY 路由器集成，provider-optional 模式——无 LLM 配置时仍可提供远程 REPL 和 PTY
- `pkg/web`：浏览器 ↔ agent 透明终端中继，零解析转发，背压处理，per-terminal stream 隔离
- `pkg/tui/remote_console`：Writer 注入式 console，同时支持本地 TTY 和远程字节流传输
- 前端 `AgentTerminal`：singleton REPL 自动重连、task PTY 面板、resize 事件转发、session 列表导航

```bash
# 1. 启动 web 控制台（内置 IOA server + 前端 + API）
aiscan-web --addr 0.0.0.0:8080 --config config.yaml --debug

# 2. 在目标机器上启动 agent，连回 web 控制台
aiscan agent --web-url http://10.0.0.1:8080

# 3. 打开浏览器 http://10.0.0.1:8080
#    → 点击顶部 "1 agent connected" pill
#    → 进入 xterm.js 终端，直接操作远程 agent 的 REPL

# agent 也可以同时带任务和 IOA 协作
aiscan agent --web-url http://10.0.0.1:8080 \
  --ioa-url http://token@10.0.0.1:8080/ioa --space case-1 \
  -p "扫描内网 192.168.1.0/24 的 Web 服务"
```

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

**TUI 改进**

- verbose tool 渲染重设计：9 项 UX 改进，包含更好的 tool call 格式化、计时和进度展示
- `pkg/tui/remote_console`：远程 agent console 支持，通过 `reeflective/readline/terminal` Stream 抽象桥接

### Architecture — 代码精简

**静态资源合一**

- 三个目录（`cmd/web/static/`、`pkg/web/e2e_static/`、`web/frontend/dist/`）合并为单一 `web/static/`
- `web/embed.go` 导出共享 `embed.FS`，生产和测试共用同一份构建产物
- vite 构建输出直接到 `web/static/`，无需手动同步

**Go 1.22+ ServeMux 路由**

- `pkg/web/handler.go`：85 行 `if segments[0] == "api"` 链替换为 `mux.HandleFunc("GET /api/scans/{id}", ...)`
- 路由参数通过 `r.PathValue("id")` 获取，删除 `pathSegments` 辅助函数和 `serveScans`/`serveConfig` 分发器
- `ServeHTTP` 简化为纯 CORS 中间件

**YAML 解析器替换**

- `cmd/web/main.go`：120 行手写解析器（`parseSimpleYAML`、`splitLines`、`trimString`、`countLeadingSpaces`、`splitKV`、`unquote`）替换为 `gopkg.in/yaml.v3`（已是直接依赖）
- `SaveLLMConfig` 改为直接修改 struct + `yaml.Marshal`，`spaFileServer` 从 40 行 struct 简化为 15 行闭包

**命令输出统一**

- `pkg/commands/output.go`：全局 `OutputWriter` + exec hooks，pseudo-command 输出自动重定向到 session writer
- 替代之前 `io.Discard` 默认行为，消除 pseudo-command 输出丢失问题

**PTY 平台整合**

- 4 个平台特定 PTY 文件（`pty_darwin.go`/`pty_linux.go`/`pty_unix.go`/`pty_other.go`）替换为单一 `go-pty` wrapper
- `tmux.Manager` 提取 `finishSession()` 去重 supervise 逻辑
- IOA 函数从 8 个导出简化为 4 个（统一 writer 参数）

**其他精简**

- `stripANSI` 重复实现委托到 `output.StripANSI`
- 双 task map (`activeTasks` + `taskCancels`) 合并为单一 `tasks map`
- PTY 输出 debounce 从复杂 timer 管理改为 ticker + dirty flag
- `frameTypeFromMessage` 14-case switch 改为 map 查表
- `remoteTerminalWriter` 改用 `bytes.Buffer` 复用避免 per-Write 分配
- 删除死代码 `CommandNames()` stub、`captureStdoutForTest`、`canHyperlink`/`hyperlinkSummary`/`hyperlink`/`pathHyperlink`

### Security & Robustness

- **WebSocket origin check**：`NewAgentPool(hub, allowedOrigins...)` 可配置，默认同源检查，debug 模式 `"*"`
- **streamWriter 缓冲上限**：64KB cap，超限自动 flush，防止无界内存增长
- **指数退避重连**：agent WebSocket 从固定 3s 改为 `RetryDelay()` 1s→2s→4s→...→10s，成功后 reset
- **PTY channel 扩容**：64 → 256 buffer，新增 `atomic.Int64` 丢帧计数
- **agent retry 扩展**：HTTP 406 等瞬态错误纳入可重试范围

### Bug Fixes

- 修复 4 个 pre-existing 测试失败：pseudo-command 缺少 `SetExecHooks` 导致输出到 `io.Discard`；remote REPL 测试 `\r` vs `\n` 行终止符不匹配
- 修复 `go.mod` 本地 replace 路径（`../malice-network/external/readline`）导致 CI 构建失败
- 解决全部 golangci-lint 错误：bodyclose、nilerr、errcheck、gosec G705、staticcheck QF1008、unused
- 修复 DirectScanner 测试数据竞争
- `go.sum` tidy 清理

### Breaking Changes

- 前端静态资源路径 `cmd/web/static/` → `web/static/`，自定义构建脚本需更新
- `NewAgentPool` 签名变更：`NewAgentPool(hub *Hub, allowedOrigins ...string)`
- `agent.RetryDelay` 从 unexported 改为 exported（`retryDelay` → `RetryDelay`）

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
