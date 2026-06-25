# Changelog

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

新增 Arsenal（crtm）安全工具包管理器；Playwright 新增 `-s` 全局 session flag；TUI verbose 渲染全面重设计；命令接口统一为全局 OutputWriter；4 平台 PTY 文件整合为单一 go-pty wrapper。

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

- `Command.Execute` 签名简化：移除 `io.Writer` 参数，统一通过 `fmt.Fprint(commands.Output, ...)` 输出
- `pkg/commands/output.go`：全局 `OutputWriter` + exec hooks，Registry 在每次执行前自动配置 Output（`Reset`/`Captured`）
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

- **`Command.Execute` 签名变更**：`Execute(ctx, args []string) error`（移除 `io.Writer` 参数），所有 pseudo-command 改用 `commands.Output` 全局 writer
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
