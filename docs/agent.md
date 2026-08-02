# Agent 模式详解

本文档基于 `v0.2.2` 源码编写，是 Agent 模式的完整参考。基本用法参见 [README](../README.md)，LLM Provider 配置参见 [参考手册](reference.md)。

标记 ★ 的功能为 v0.2.2 新增。

---

## 目录

- [运行模式](#运行模式)
- [One-shot 模式](#one-shot-模式)
- [Goal Evaluation](#goal-evaluation)
- [交互式 REPL](#交互式-repl)
- [Agent 工具集](#agent-工具集)
- [--ai 模式](#--ai-模式)
- [Skills](#skills)
- [信号处理](#信号处理)
- [多 Provider 配置](#多-provider-配置)
- [适用场景](#适用场景)

---

## 运行模式

`aiscan agent` 根据输入自动选择三种运行模式之一：

| 条件 | 模式 | 行为 |
| --- | --- | --- |
| 提供 `-p`、`--task-file`、`-i` 或 stdin pipe | **One-shot** | 执行任务后退出 |
| 指定 `--ioa-url` | **IOA worker** | 连接 IOA server，注册节点，监听并执行任务 |
| 无任何输入 | **交互式 REPL** | 进入交互命令行，支持会话保持和连续追问 |

判断逻辑：`--ioa-url` 优先级最高；其次检查是否存在任务输入（prompt、目标、文件、stdin）；均不满足时进入 REPL。

---

## One-shot 模式

One-shot 模式接收一次性任务，agent 执行完成后自动退出。

### 输入方式

| 方式 | 参数 | 说明 |
| --- | --- | --- |
| Prompt | `-p, --prompt` | 任务描述；若值是已存在的文件路径，则读取文件内容 |
| 目标 | `-i, --input` | IP、URL、IP:port、CIDR，可重复 |
| 任务文件 | `--task-file` | 从文件读取任务描述（支持 Markdown） |
| 指定 skill | `-s, --skill` | 加载指定 skill，可重复 |
| stdin | 管道输入 | 从标准输入读取任务描述 |

输入可以组合使用。仅提供 `-i` 时，agent 会自动生成扫描任务。

### 示例

```bash
# 基本用法
aiscan agent -p "发现 Web 服务并检查高风险漏洞，给出可复现证据" -i 192.168.1.0/24

# 多个目标
aiscan agent -p "枚举服务并输出风险摘要" -i 10.0.0.10 -i http://10.0.0.20

# 从文件读取任务
aiscan agent --task-file task.md -i 192.168.1.0/24

# -p 也会自动读取已存在的 prompt 文件
aiscan agent -p task.md -i 192.168.1.0/24

# 仅提供目标（自动生成扫描任务）
aiscan agent -i http://target.example

# 指定 skill
aiscan agent -s scan -s neutron -p "先做快速扫描，再分析高危 POC 命中" -i http://target.example

# 从 stdin 读取任务
echo "检查这个网段的暴露面" | aiscan agent -i 192.168.1.0/24
```

---

## Goal Evaluation

★ v0.2.2 新增。

Goal Evaluation 让一个独立的评估 LLM 在 agent 完成任务后判定是否达成目标。如果未通过，评估反馈会被注入 agent 继续执行，直到通过或达到最大重试轮数。

### 启用方式

```bash
# One-shot 模式
aiscan agent -p "检查目标 Web 漏洞" -i http://target.example -e "必须给出至少一个可复现的漏洞证据，包含请求和响应"

# 交互式 REPL
aiscan> /eval 必须包含完整的端口列表和风险等级
aiscan> 扫描 192.168.1.0/24
```

| 参数 | 说明 |
| --- | --- |
| `-e, --eval` | 指定评估标准（自然语言） |
| `/eval <criteria>` | REPL 中设置评估标准 |
| `/eval` | REPL 中查看当前评估标准 |
| `/eval off` | REPL 中关闭评估 |

### 机制

1. Agent 完成一次运行后，评估器将执行轨迹压缩为结构化摘要（工具调用序列 + assistant 摘要 + 最终输出，最大 16KB），连同评估标准一起发送给评估 LLM
2. 评估 LLM 通过强制工具调用（verdict tool）返回结构化判定：

```json
{
  "pass": false,
  "reason": "报告中缺少请求和响应的原始数据",
  "feedback": "请补充漏洞验证的完整 HTTP 请求和响应内容"
}
```

3. 如果 `pass=false`，feedback 被注入为新 prompt，agent 继续执行
4. 循环直到 `pass=true` 或达到最大 3 轮
5. 所有轮次用尽仍未通过时，返回最后一次执行结果（不报错）

### 评估器容错

评估 LLM 调用失败时不会中断主流程。系统降级为通用反馈：

> Goal evaluation could not determine if the task is complete. Original criteria: {criteria}. Please review your work and continue if the goal is not yet fully achieved.

agent 收到此反馈后继续执行，不会因为评估器问题而停止。

### 事件

评估过程通过 eventbus 发布以下事件：

| 事件 | 时机 | 携带数据 |
| --- | --- | --- |
| `GoalEvalStart` | 开始评估 | `EvalRound` |
| `GoalEvalEnd` | 评估完成 | `EvalRound`, `EvalPass`, `EvalReason` |
| `GoalEvalError` | 评估器调用失败 | `EvalRound`, `EvalError` |

### 示例

```bash
# 要求输出格式和内容的评估
aiscan agent -p "扫描目标所有端口并识别服务" -i 10.0.0.0/24 \
  -e "输出必须包含每个开放端口的服务名称和版本号，使用表格格式"

# 要求漏洞验证深度的评估
aiscan agent -p "检查 Web 应用漏洞" -i http://target.example \
  -e "每个发现的漏洞必须附带可复现的 curl 命令"

# REPL 中动态启用/关闭
aiscan> /eval 扫描结果必须覆盖 top100 端口
aiscan> 扫描 192.168.1.1
aiscan> /eval off
```

---

## 交互式 REPL

无任何输入时进入交互式 REPL。支持命令历史、补全，会话上下文在 `/reset` 前保留。

```bash
aiscan agent --model gpt-4o
```

### 命令列表

#### 内置命令

| 命令 | 说明 |
| --- | --- |
| `/help` | 显示命令面板 |
| `/status` | 查看当前模型、渲染模式、IOA 连接和已加载 skill |
| `/reset` | 清空会话上下文 |
| `/continue` | 不追加新 prompt，让 agent 继续当前上下文 |
| `/stop` | 停止当前正在执行的任务 |
| `/followup <prompt>` | 排队消息，等当前任务完成后自动发送 |
| `/eval [criteria\|off]` | 设置/查看/关闭 Goal Evaluation ★ |
| `/exit`, `/quit` | 退出 |

#### Provider 命令 ★

| 命令 | 说明 |
| --- | --- |
| `/provider` | 查看 LLM Provider 配置（active/configured） |
| `/provider list` | 列出当前和其他可用 provider 配置 |
| `/provider set ...` | 显式设置当前 provider 和模型 |

#### IOA 命令（需 `--ioa-url`）

| 命令 | 说明 |
| --- | --- |
| `/spaces` | 列出所有 IOA 空间 |
| `/messages <space>` | 列出空间中的起始消息 |
| `/context <space> <msg-id>` | 查看消息上下文/线程 |
| `/nodes [space]` | 列出节点 |

#### Skill 命令

每个已注册的非 internal skill 自动成为 REPL 命令：

```text
aiscan> /scan 检查这个网段的高危漏洞
aiscan> /neutron 用 critical 级别 POC 检查 http://target.example
aiscan> /report 根据上次扫描结果生成报告
```

#### `!` 直接执行 ★

`!` 前缀直接执行命令，绕过 LLM。所有注册的 scanner 伪命令和 shell 命令均可使用，支持 Ctrl+C / Escape 取消。

```text
aiscan> !gogo -i 192.168.1.0/24 -p top100
aiscan> !scan -i http://target.example
aiscan> !cyberhub list poc --severity critical
aiscan> !neutron -u http://target.example -s high
```

输入普通文本（非 `/` 或 `!` 开头）直接作为 prompt 发送给 agent。

---

## Agent 工具集

Agent 在运行时可使用以下工具，由 LLM 自主选择调用。

### Agent 工具（LLM 直接调用）

| 工具 | 说明 | 备注 |
| --- | --- | --- |
| `bash` | 执行 shell 命令（通过 tmux PTY 运行） | 核心工具 |
| `read` | 读取文件内容 | 核心工具 |
| `write` | 写入文件内容 | 核心工具 |
| `glob` | 文件模式匹配搜索 | 核心工具 |
| `web_search` ★ | Web 搜索，查询 CVE/Exploit/安全情报 | 优先使用 provider 原生搜索，回退 Tavily |
| `fetch` | 抓取 URL 内容为可读文本 | |
| `finish` ★ | 显式终止 agent 循环（`ToolResult.Terminate`） | 终止工具 |
| `subagent` | 创建子 agent（sync 同步 / async 异步 / fork 分支） | |

### Scanner 伪命令（通过 bash 工具调用）

| 命令 | 说明 |
| --- | --- |
| `gogo` | 主机存活、端口、服务、banner 和指纹发现 |
| `spray` | Web 探测、HTTP 指纹、路径检查、爬取 |
| `zombie` | 弱口令检测 |
| `neutron` | 模板化 POC 检测 |
| `scan` | 自动扫描流水线 |
| `cyberhub` ★ | 指纹和 POC 关联查询（基于 SDK association index 重构，支持 `--finger`/`--cve`/`--vendor`/`--product`/`--poc` 结构化查询） |
| `katana` | Web 爬虫（仅 full 版） |
| `passive` | 网络空间搜索（仅 full 版） |

### tmux — 后台会话管理

tmux 是 agent 管理长时间运行命令的核心工具。bash 工具执行的命令如果超时会自动转入 tmux 后台会话，增量输出每 10 秒自动推送到 agent inbox。

| 子命令 | 说明 |
| --- | --- |
| `tmux new-session [-d] [-s name] [--timeout duration] "command"` | 创建会话。`-d` 后台运行，`-s` 指定名称 |
| `tmux ls` | 列出所有会话及状态 |
| `tmux capture-pane -t <id>` | 读取新增输出（默认增量模式，仅返回上次读取后的新内容） |
| `tmux capture-pane -t <id> -n <N>` | 读取末尾 N 行 |
| `tmux capture-pane -t <id> -c <N>` | 读取末尾 N 字节 |
| `tmux capture-pane -t <id> --full` | 读取完整缓冲区 |
| `tmux send-keys -t <id> "text" Enter` | 向会话发送按键（支持 Enter、C-c、C-d、Escape、Tab 等） |
| `tmux kill-session -t <id>` | 终止会话 |
| `tmux wait-for -t <id> [--timeout 60s]` | 阻塞等待会话结束 |

增量输出机制：`capture-pane` 默认只返回上次读取后的新输出，避免重复。同时后台 goroutine 每 10 秒将新输出推送到 agent inbox，agent 不需要手动轮询。

直接传命令也可以隐式创建后台会话：

```bash
tmux nmap -sV 192.168.1.0/24    # 等价于 tmux new-session -d "nmap -sV 192.168.1.0/24"
```

### proxy — 代理节点管理

proxy 工具管理扫描代理，支持 Clash 订阅自动负载均衡和多协议直连。

| 子命令 | 说明 |
| --- | --- |
| `proxy <url> <command> [args...]` | 通过指定代理执行命令（类似 proxychains） |
| `proxy auto <url> [options]` | 推荐模式：订阅 + 自适应负载均衡 |
| `proxy subscribe <url>` | 拉取 Clash 订阅并列出可用节点 |
| `proxy list` | 列出已加载的代理节点 |
| `proxy switch <name\|index>` | 切换活跃代理节点 |
| `proxy test [name\|index]` | 测试代理节点连通性 |
| `proxy current` | 显示当前活跃代理 |
| `proxy clear` | 清除订阅，恢复原始代理 |

支持的协议：`socks5://`、`trojan://`、`vless://`、`anytls://`、`hysteria2://`、`shadowsocks://`、`clash://`（订阅 URL）。

**proxy-chain 执行**（通过代理运行扫描命令）：

```bash
proxy socks5://127.0.0.1:1080 gogo -i 10.0.0.1 -p top2
proxy trojan://pass@host:443 zombie -i 10.0.0.1 -s ssh
proxy 6 gogo -i 10.0.0.1 -p top2       # 使用订阅节点 #6
proxy HK gogo -i 10.0.0.1               # 使用名称匹配 "HK" 的节点
```

**auto 模式**（推荐）：

```bash
proxy auto https://subscribe.example/link --type trojan,vless --country HK,JP --strategy adaptive
```

auto 模式选项：

| 选项 | 说明 |
| --- | --- |
| `--type, -t` | 按协议类型过滤（trojan, vless 等） |
| `--name, -n` | 按节点名关键词过滤 |
| `--country, -c` | 按服务器 IP 国家过滤（ISO 3166-1 alpha-2） |
| `--strategy, -s` | 负载均衡策略：adaptive（自适应）、url-test、round-robin、random |

全局 `--proxy` 参数与 proxy 工具的关系：`--proxy` 设置初始扫描代理（所有 scanner 共享），proxy 工具可在运行时动态切换或通过 proxy-chain 对单次命令使用不同代理。

### playwright — 无头浏览器（仅 full 版）

playwright 提供 Chromium 无头浏览器，用于 JS 渲染页面、截图、网络捕获和交互式漏洞验证。

**无状态命令**（直接传 URL，用完即关）：

| 子命令 | 说明 |
| --- | --- |
| `playwright goto <url> [selector]` | 导航到 URL 并返回文本内容 |
| `playwright content <url> [selector]` | 导航到 URL 并返回 HTML |
| `playwright screenshot <url> [options]` | 截图 |
| `playwright evaluate <url> <script>` | 执行 JavaScript |
| `playwright network <url>` | 捕获页面网络请求 |
| `playwright pdf <url>` | 生成 PDF |

**会话模式**（多步交互工作流）：

```bash
playwright open <url> [--session name] [--record]   # 打开持久会话
playwright discover <session>                         # 发现表单、按钮、事件监听器
playwright autofill <session> [--form N] [--data k=v] # 智能表单填充
playwright click <session> <selector>                 # 点击
playwright fill <session> <selector> <value>          # 输入
playwright screenshot <session>                       # 截图当前状态
playwright network <session> --start/--dump/--stop    # 网络捕获
playwright close <session>                            # 关闭会话
playwright sessions                                   # 列出活跃会话
```

会话交互命令完整列表：`goto`、`content`、`evaluate`、`screenshot`、`network`、`reload`、`go-back`、`go-forward`、`click`、`dblclick`、`fill`、`press`、`hover`、`select-option`、`check`、`uncheck`、`set-input-files`、`focus`、`blur`、`wait-for`、`wait-for-url`、`wait-for-request`、`wait-for-response`、`dispatch-event`。

`--record` 选项开启操作录制，可用于生成自动化测试模板。

### subagent — 子 agent

subagent 工具创建独立子 agent 处理子任务。

| action | 说明 |
| --- | --- |
| `create` | 创建子 agent（默认） |
| `list` | 列出运行中的子 agent |
| `kill` | 按名称取消子 agent |
| `message` | 向运行中的子 agent 发送消息 |

运行模式：

| 模式 | 说明 |
| --- | --- |
| `sync` | 同步阻塞，等待子 agent 完成后返回结果 |
| `async` | 异步后台运行，使用全新上下文 |
| `fork` | 异步后台运行，继承父 agent 对话上下文（cache 友好） |

可通过 `type` 参数指定 agent 类型（对应 `agent: true` 的 skill），子 agent 会使用该 skill 的系统提示词。

### IOA 工具（需 `--ioa-url`）

| 命令 | 说明 |
| --- | --- |
| `ioa send` | 向 Space 发送消息（任务分派、情报共享、结果汇报） |
| `ioa read` | 读取 Space 中的消息 |
| `ioa space` | 获取或创建 Space |

### web_search 详情

`web_search` 支持多后端：

1. 如果当前 LLM provider 实现了 `WebSearchProvider` 接口（如 Anthropic `web_search_20250305`、OpenAI Responses API），优先使用 provider 原生搜索
2. 否则回退到 Tavily Search API
3. 两者均不可用时返回错误

### finish 工具

`finish` 工具让 agent 显式声明任务完成。调用时传入 `summary` 参数，返回 `ToolResult.Terminate=true`，agent 循环以 `StopReasonTerminated` 结束。这比等待 LLM 自然停止更可控。

---

## --ai 模式

`--ai` 模式将 scanner 执行和 LLM 分析结合：先运行 scanner，再由 agent 分析输出。

```bash
aiscan --ai -p "<分析意图>" <scanner> [scanner 参数...]
```

### 工作方式

1. 解析 scanner 命令和参数
2. 自动加载对应 scanner 的 skill（如 `gogo` 命令加载 gogo skill）
3. 创建 agent runtime，将 scanner 参数和用户意图格式化为任务 prompt
4. Agent 拥有完整工具集，可以运行 scanner、分析输出、调用其他工具进一步验证

### 示例

```bash
# gogo 结果由 agent 分析
aiscan --ai -p "只提取高风险暴露面，并给出证据" gogo -i 192.168.1.0/24 -p top100

# spray 结果分析
aiscan --ai -p "判断这些 Web 指纹是否值得进一步验证" spray -u http://target.example --finger

# neutron 结果分析
aiscan --ai -p "解释命中的 POC 影响和复现条件" neutron -u http://target.example -s critical,high

# 额外指定 skill
aiscan --ai --skill scan gogo -i 192.168.1.0/24 -p all
```

> `--ai` 适合对 scanner 输出做总结、解释和筛选。如果需要自动化证据验证，使用 `scan --verify`。

---

## Skills

Skills 是 agent 按需加载的知识文件，提供工具使用指南、最佳实践和领域知识。

### 内置 skill 列表

| Skill | 说明 | 可用性 |
| --- | --- | --- |
| `aiscan` | 核心机制、能力、scanner 伪命令、工具调用规则 | 默认 |
| `scan` | 多阶段扫描流水线知识 | 默认 |
| `search` | Cyberhub 指纹和 POC 模板查询 | 默认 |
| `report` | 安全扫描报告生成 | 默认 |
| `gogo` | 主机/端口/服务/指纹发现 | 默认 |
| `spray` | Web 探测/HTTP 指纹/路径分析 | 默认 |
| `zombie` | 弱口令检测和认证结果分析 | 默认 |
| `neutron` | 模板化 POC 执行和结果分析 | 默认 |
| `ioa` | IOA 多 agent 协作 | 默认 |
| `tmux` | 后台会话管理 | 默认 |
| `playwright` | 无头浏览器操作 | 仅 full 版 |
| `passive` | 网络空间搜索（FOFA/Hunter） | 仅 full 版 |
| `katana` | Web 深度爬取和参数发现 | 仅 full 版 |

### 指定 skill

通过 `-s` 参数指定加载的 skill：

```bash
# 加载多个 skill
aiscan agent -s aiscan -s scan -p "全面扫描这个网段" -i 10.0.0.0/24

# 报告生成
aiscan agent -s report -p "根据扫描结果生成报告" -i http://target.example
```

### 加载优先级

```
内置 embedded skill < .aiscan/skills/ < .agent/skills/ < -s 指定路径
```

后加载的同名 skill 覆盖先加载的。`-s` 除了接受 skill 名称，也可以指定自定义 skill 文件路径。

---

## 信号处理

★ v0.2.2 重构了信号处理机制，支持三阶段 Ctrl+C 和上下文传播。

### 三阶段 Ctrl+C

| 阶段 | 行为 |
| --- | --- |
| 第一次 Ctrl+C | 停止当前正在执行的任务（调用 `controller.Stop()`），回到 REPL 等待新输入。如果没有任务在执行，提示再按一次退出 |
| 第二次 Ctrl+C | 取消根上下文，结束当前 turn 后退出 REPL |
| 第三次 Ctrl+C | 强制退出（`os.Exit(1)`） |

### 5 秒空闲重置

如果两次 Ctrl+C 之间间隔超过 5 秒，信号计数器重置为 0。下一次 Ctrl+C 重新从第一阶段开始，避免误操作导致退出。

### 上下文传播

信号处理的上下文链：

```
根 context → controller runCtx → agent runCtx → tool 执行 context → bash/tmux timeout context
```

`controller.Stop()` 取消 `runCtx`，该取消信号通过 context 链传播到所有正在执行的工具，包括 `tmux.CreateFunc` 创建的后台命令。同时调用 `output.AbortCurrentRun()` 停止 spinner 和流式输出。

### `!` 直接命令取消

REPL 中 `!` 前缀的直接命令拥有独立的 cancel context，支持 Ctrl+C 和 Escape 取消，不影响 agent 会话状态。

---

## 多 Provider 配置

可以保存多个 provider profile，但模型切换始终由用户显式触发。Agent 不会在请求失败后自动换 provider。

### 机制

1. 当前 provider 的 LLM 请求按配置执行重试和退避
2. 可重试的错误：HTTP 429（限流）、500/502/503/529（服务端错误）、超时、连接错误
3. 不可重试的错误：HTTP 401/403/404（认证/权限/不存在）立即失败
4. 当前 provider 重试耗尽后，agent 以 `StopReasonError` 停止
5. 其他 profile 不会收到当前 turn，避免跨模型隐式重放

### 配置

在配置文件中定义 provider profile，并明确指定当前项：

```yaml
llm:
  active_profile: openai
  providers:
    - id: openai
      provider: openai
      model: gpt-4o
      api_key: "sk-..."
    - id: deepseek
      provider: openai
      base_url: "https://api.deepseek.com/v1"
      model: deepseek-chat
      api_key: "..."
    - id: ollama
      provider: openai
      model: llama3
      base_url: "http://localhost:11434/v1"
      api_key: "local"
```

`active_profile` 按 `id` 选择当前项；未设置时使用列表第一项。完整格式参见 [参考手册](reference.md)。

### 查看状态

REPL 中使用 `/provider` 命令查看当前和其他可用配置：

```text
aiscan> /provider
Provider profiles:
  1. openai / gpt-4o          # active
  2. openai / deepseek-chat   # deepseek profile
  3. openai / llama3          # ollama profile
```

切换通过 Web 设置页，或 REPL 的 `/provider set --provider ... --model ...` 显式完成。

---

## 适用场景

### agent 适合

- **任务描述不完全确定** — 需要 agent 自己选择扫描路径和工具组合
- **需要多 scanner 关联** — 把 gogo、spray、neutron 等结果串联起来分析
- **需要人可读输出** — 生成面向人的摘要、复现步骤或后续建议
- **IOA 多 agent 协作** — 多个 worker 分工执行不同扫描任务
- **需要迭代验证** — 通过 Goal Evaluation 确保输出质量
- **交互式探索** — 连续追问，根据上一步结果调整方向

### agent 不适合

- **大范围批量扫描** — 大规模无约束扫描直接用 `scan` 命令更高效
- **严格输出格式** — 对时间和格式有严格要求的批处理任务用 `scan -j`
- **没有 LLM 环境** — agent 必须有可用的 LLM provider；无 LLM 时使用 `scan` 命令
- **低延迟要求** — LLM 调用增加延迟，纯扫描任务不需要 agent 介入
