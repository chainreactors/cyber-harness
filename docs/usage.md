# aiscan 使用文档

本文档基于 `v0.1.0` 源码编写，面向 GitHub Release 发布的二进制版本。

aiscan 是一个 agentic security scanner：它既可以像普通 CLI 一样直接运行扫描器，也可以让 LLM agent 根据自然语言目标选择工具、执行扫描、读取证据并输出结论。请只在明确授权的目标上使用。

---

## 目录

- [安装](#安装)
- [命令结构](#命令结构)
- [全局参数](#全局参数)
- [LLM Provider 配置](#llm-provider-配置)
- [scan：自动扫描流水线](#scan自动扫描流水线)
- [agent：自然语言扫描代理](#agent自然语言扫描代理)
- [scanner 的 --ai 模式](#scanner-的---ai-模式)
- [gogo：服务发现](#gogo服务发现)
- [spray：Web 探测和指纹](#sprayweb-探测和指纹)
- [zombie：弱口令检测](#zombie弱口令检测)
- [neutron：POC 检测](#neutronpoc-检测)
- [cyberhub：资源查询](#cyberhub资源查询)
- [IOA：协作模式](#ioa协作模式)
- [Cyberhub 资源服务](#cyberhub-资源服务)
- [输出与格式](#输出与格式)
- [场景选择建议](#场景选择建议)
- [常见问题](#常见问题)

---

## 安装

下载最新正式版本：

```text
https://github.com/chainreactors/aiscan/releases/latest
```

| 系统 | 架构 | 文件 |
| --- | --- | --- |
| Linux | amd64 | `aiscan_linux_amd64` |
| Linux | arm64 | `aiscan_linux_arm64` |
| macOS | Intel | `aiscan_darwin_amd64` |
| macOS | Apple Silicon | `aiscan_darwin_arm64` |
| Windows | amd64 | `aiscan_windows_amd64.exe` |

Linux amd64：

```bash
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_linux_amd64
chmod +x aiscan
sudo mv aiscan /usr/local/bin/aiscan
aiscan --version
```

macOS Apple Silicon：

```bash
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_darwin_arm64
chmod +x aiscan
xattr -d com.apple.quarantine aiscan 2>/dev/null || true
sudo mv aiscan /usr/local/bin/aiscan
aiscan --version
```

Windows PowerShell：

```powershell
Invoke-WebRequest "https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_windows_amd64.exe" -OutFile aiscan.exe
.\aiscan.exe --version
```

---

## 命令结构

```text
aiscan [全局参数] <subcommand> [子命令参数]
```

子命令一览：

| 命令 | 类型 | 功能 |
| --- | --- | --- |
| `agent` | agentic | LLM agent；无任务输入时进入交互式 CLI，`--loop` 时作为 IOA worker |
| `scan` | pipeline | 自动扫描流水线，串联 gogo → spray → zombie → neutron → 可选 AI 验证 |
| `gogo` | scanner | 主机存活、端口、服务、banner 和指纹发现 |
| `spray` | scanner | Web 探测、HTTP 指纹、常见文件、爬取和路径检查 |
| `zombie` | scanner | 授权弱口令检测 |
| `neutron` | scanner | 模板化 POC 检测 |
| `cyberhub` | query | 查询已加载的指纹和 POC 模板 |
| `ioa serve` | service | 启动 IOA HTTP server |
| `ioa spaces` | query | 列出 IOA 空间 |
| `ioa messages` | query | 列出空间中的起始消息 |
| `ioa context` | query | 查看消息上下文/线程 |
| `ioa nodes` | query | 列出节点 |

查看帮助：

```bash
aiscan -h
aiscan scan -h
aiscan neutron -h
```

---

## 全局参数

全局参数放在子命令之前，也可以放在 scanner 子命令之后（aiscan 会自动提取）。

### LLM 参数

| 参数 | 别名 | 说明 |
| --- | --- | --- |
| `--llm-provider` | `--provider` | LLM provider 名称 |
| `--llm-base-url` | `--base-url` | LLM API base URL |
| `--llm-api-key` | `--api-key` | LLM API key |
| `--llm-model` | `--model` | 模型名称（默认 `gpt-4o`） |
| `--llm-proxy` | `--proxy` | 访问 LLM API 的 HTTP proxy |
| `--ai` | | 对 scanner 输出使用 LLM 分析 |

### Agent 参数

| 参数 | 说明 |
| --- | --- |
| `-p, --prompt` | 自然语言任务描述 |
| `-i, --input` | 目标输入（IP、URL、IP:port、CIDR），可重复 |
| `-s, --skill` | 指定 skill 名称，可重复 |
| `--task-file` | 从文件读取任务描述 |
| `--loop` | 作为 IOA loop worker 运行 |
| `--heartbeat <分钟>` | loop 模式下 heartbeat 间隔（0 表示关闭，默认 0） |
| `--timeout <秒>` | 整体超时（默认 3600） |

### Scanner 参数

| 参数 | 说明 |
| --- | --- |
| `--cyberhub-url` | Cyberhub 资源服务 URL |
| `--cyberhub-key` | Cyberhub API key |
| `--cyberhub-mode` | 资源模式：`merge`（默认）或 `override` |

### IOA 参数

| 参数 | 说明 |
| --- | --- |
| `--ioa-url` | IOA server URL |
| `--ioa-node-id` | 已有 IOA 节点 ID |
| `--ioa-node-name` | 注册时使用的节点名（默认自动生成） |
| `--ioa-db` | IOA SQLite 数据库路径（默认 `./ioa.db`） |
| `--space` | IOA 空间名（默认 `default`） |
| `--json` | IOA 查询结果以 JSON 输出 |

### 通用参数

| 参数 | 说明 |
| --- | --- |
| `--debug` | 输出调试日志 |
| `-q, --quiet` | 减少日志输出 |
| `--no-color` | 禁用 ANSI 颜色 |
| `--version` | 输出版本号并退出 |

> 注意：顶层参数和 scanner 子命令参数可能同名。例如 `aiscan agent -p` 是自然语言 prompt，`aiscan gogo -p` 是端口参数。aiscan 会根据子命令自动区分。

---

## LLM Provider 配置

`agent`、`agent --loop`、`scan --verify` 和 scanner 的 `--ai` 模式需要 LLM provider。

默认 provider 是 `openai`，默认模型是 `gpt-4o`。aiscan 可以从 `--llm-base-url` 自动推断 provider（如 URL 包含 `deepseek.com` 则推断为 `deepseek`）。

### 支持的 Provider

| Provider | 默认 Base URL | API Key 环境变量 |
| --- | --- | --- |
| `openai` | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| `openrouter` | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` |
| `deepseek` | `https://api.deepseek.com/v1` | `DEEPSEEK_API_KEY` |
| `groq` | `https://api.groq.com/openai/v1` | `GROQ_API_KEY` |
| `moonshot` | `https://api.moonshot.cn/v1` | `MOONSHOT_API_KEY` |
| `anthropic` | `https://api.anthropic.com/v1` | `ANTHROPIC_API_KEY` |
| `ollama` | `http://localhost:11434/v1` | 不需要 |

统一 fallback 环境变量：

```bash
export AISCAN_API_KEY="..."
```

API key 解析优先级：`--llm-api-key` > Provider 对应环境变量 > `AISCAN_API_KEY`。

### 示例

OpenAI：

```bash
export OPENAI_API_KEY="sk-..."
aiscan agent --llm-model gpt-4o -p "发现 Web 服务并检查高风险漏洞" -i 192.168.1.0/24
```

DeepSeek：

```bash
export DEEPSEEK_API_KEY="..."
aiscan agent --llm-provider deepseek --llm-model deepseek-chat -p "枚举服务并输出风险摘要" -i 10.0.0.0/24
```

Ollama 本地模型：

```bash
ollama run llama3
aiscan agent --llm-provider ollama --llm-model llama3 --llm-base-url http://localhost:11434/v1 -p "检查这个站点" -i http://target.example
```

通过代理访问 API：

```bash
aiscan agent --llm-proxy http://127.0.0.1:7890 -p "检查目标暴露面" -i http://target.example
```

任意 OpenAI 兼容 API：

```bash
aiscan agent --llm-base-url https://my-proxy.example/v1 --llm-api-key "$MY_KEY" --llm-model my-model -p "扫描目标" -i 10.0.0.0/24
```

---

## scan：自动扫描流水线

`scan` 是最常用的自动扫描入口。它按 capability 驱动的事件流水线自动串联所有扫描器，不依赖 LLM 也能运行。

### 基本用法

```bash
aiscan scan -i <target> [options]
```

输入可以是 URL、IP、IP:port、CIDR，也可以用文件：

```bash
aiscan scan -i 127.0.0.1
aiscan scan -i http://target.example --mode quick
aiscan scan -i 192.168.1.0/24 --mode full
aiscan scan -l targets.txt --mode full
```

### 扫描流程

```text
输入目标 → gogo 端口发现 → spray Web 探测/指纹/常见文件/爬取 → zombie 弱口令 → neutron POC → 可选 agent_verify
```

各阶段通过事件队列串联。例如 gogo 发现 HTTP 服务后，Web 目标自动进入 spray；spray 识别到指纹后，指纹用于 neutron 选择 POC；发现可以进入 AI 验证。

### quick 和 full 模式

| 模式 | 说明 |
| --- | --- |
| `quick` | **默认模式**。gogo 端口扫描（ports=all）、spray check/finger/common/backup/active/crawl（depth 1）、弱口令、基于指纹的 POC |
| `full` | 在 quick 基础上增加 spray brute（默认字典探测）和更深的 crawl（depth 2） |

quick 模式的 capability：

| Capability | 说明 |
| --- | --- |
| `gogo_portscan` | 端口扫描，默认 ports=all |
| `spray_check` | Web 基础探测 |
| `spray_finger` | HTTP 指纹识别 |
| `core_web` | Web 结果关联分析 |
| `spray_common` | 常见路径/文件探测 |
| `spray_backup` | 备份文件检测 |
| `spray_active` | 主动 Web 检查 |
| `spray_crawl` | 网页爬取（depth 1） |
| `zombie_weakpass` | 弱口令检测 |
| `neutron_poc` | 基于指纹的 POC 检测 |

full 模式额外增加：

| Capability | 说明 |
| --- | --- |
| `spray_brute` | 默认字典路径爆破 |

### scan 参数

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `-i, --input` | 目标，可重复 | |
| `-l, --list` | 目标文件 | |
| `--mode` | 扫描模式：`quick` 或 `full` | `quick` |
| `--thread` | 总并发预算，自动分配给各引擎 | `1000` |
| `--timeout` | 每个探测的超时秒数 | `5` |
| `--ports` | gogo 端口集合（quick 默认 `all`，full 默认 `-`） | |
| `--port` | 端口参数，覆盖 `--ports` | |
| `--dict` | spray 字典文件，可重复 | |
| `--rule` | spray 变形规则文件，可重复 | |
| `--word` | spray 词汇生成 DSL | |
| `--default-dict` | 使用 spray 默认字典 | |
| `--advance` | 启用 spray advance 插件 | |
| `--user` | 弱口令用户名，可重复 | |
| `--pwd` | 弱口令密码，可重复 | |
| `--zombie-top` | 使用 top N 默认弱口令 | |
| `--max-neutron-per-finger` | 每个指纹最大 neutron 模板数 | `20` |
| `--broad-poc` | 无指纹时也运行 POC | |
| `--verify` | AI 验证模式：`off`, `low`, `medium`, `high`, `critical` | `off` |
| `--verify-timeout` | 单次验证超时秒数 | `120` |
| `-j, --json` | JSON Lines 输出 | |
| `--report` | Markdown 报告输出 | |
| `-f, --file` | 输出写入文件（不含 ANSI 颜色） | |
| `--no-color` | 禁用终端颜色 | |
| `--debug` | 打印事件流水线 trace | |

并发分配策略（`--thread` 默认 1000）：

| 引擎 | 分配比例 | 默认并发 |
| --- | --- | --- |
| gogo | 80% | 500/次 |
| spray | 10% | 20/次 |
| zombie | 10% | 100/次 |
| neutron | 10% | - |

### AI 验证

`scan --verify=<priority>` 启用 `agent_verify`，对达到指定优先级的发现进行 LLM 验证。

验证模式：

| 值 | 说明 |
| --- | --- |
| `off` | 关闭验证（CLI 显式传 `--verify` 时的默认值） |
| `low` | 验证所有优先级 |
| `medium` | 验证 medium 及以上 |
| `high` | 验证 high 及以上 |
| `critical` | 仅验证 critical |
| `auto` | 编译时默认值；等效于 `high`，但 LLM 不可用时自动跳过 |

> 当未显式传 `--verify` 时，aiscan 使用编译时 `auto` 默认策略：尝试以 `high` 优先级启用验证。如果 LLM provider 未配置，验证会被跳过，扫描主体仍可运行。

```bash
aiscan scan -i http://target.example --mode quick --verify=high --llm-api-key "$OPENAI_API_KEY" --llm-model gpt-4o
aiscan scan -i http://target.example --mode full --verify=critical --verify-timeout 90
aiscan scan -i http://target.example --verify=off
```

### scan 示例

```bash
# 快速扫描
aiscan scan -i 192.168.1.0/24

# 完整扫描
aiscan scan -i 192.168.1.0/24 --mode full

# 指定端口
aiscan scan -i 192.168.1.0/24 --port top100

# 自定义弱口令
aiscan scan -i 127.0.0.1 --user admin --pwd admin123

# 自定义字典
aiscan scan -i http://target.example --dict paths.txt --rule rules.txt

# 无指纹时也做 POC
aiscan scan -i http://target.example --broad-poc

# JSON Lines 输出
aiscan scan -i 127.0.0.1 -j

# Markdown 报告
aiscan scan -i 127.0.0.1 --report

# 输出到文件
aiscan scan -i 127.0.0.1 -f result.txt
```

---

## agent：自然语言扫描代理

`agent` 是 aiscan 的 agentic 模式。它构造系统提示词，加载内置工具、扫描器文档和 skills，由 LLM 在多轮循环中选择工具、执行命令、读取证据并输出最终报告。

### 运行模式

agent 有三种运行模式，根据输入自动选择：

| 条件 | 模式 |
| --- | --- |
| 提供 `-p`、`--task-file`、`-i` 或 stdin pipe | One-shot：执行任务后退出 |
| 指定 `--loop` | Loop worker：连接 IOA server，监听任务 |
| 无任何输入 | 交互式 CLI（REPL） |

### One-shot 模式

```bash
aiscan agent -p "<任务描述>" -i <target>
```

```bash
# 基本用法
aiscan agent -p "发现 Web 服务并检查高风险漏洞，给出可复现证据" -i 192.168.1.0/24

# 多个目标
aiscan agent -p "枚举服务并输出风险摘要" -i 10.0.0.10 -i http://10.0.0.20

# 从文件读取任务
aiscan agent --task-file task.md -i 192.168.1.0/24

# 仅提供目标（自动生成扫描任务）
aiscan agent -i http://target.example

# 指定 skill
aiscan agent -s scan -s neutron -p "先做快速扫描，再分析高危 POC 命中" -i http://target.example

# 从 stdin 读取任务
echo "检查这个网段的暴露面" | aiscan agent -i 192.168.1.0/24
```

### 交互式 CLI

直接运行 `aiscan agent` 且不提供任何输入时，进入交互式 REPL。支持命令历史和补全，会话上下文保留，适合连续追问。

```bash
aiscan agent --llm-model gpt-4o
```

交互式命令：

| 命令 | 说明 |
| --- | --- |
| `/help` | 显示交互命令 |
| `/reset` | 清空会话上下文 |
| `/continue` | 不追加新 prompt，让 agent 继续当前上下文 |
| `/exit`, `/quit` | 退出 |
| `/<skill-name> [prompt]` | 调用内置 skill |
| `/spaces` | 列出 IOA 空间（需 `--ioa-url`） |
| `/messages <space>` | 列出空间消息（需 `--ioa-url`） |
| `/context <space> <id>` | 查看消息上下文（需 `--ioa-url`） |
| `/nodes [space]` | 列出节点（需 `--ioa-url`） |

内置 skill 自动注册为 REPL 命令。当前包括 `/aiscan`、`/scan`、`/gogo`、`/spray`、`/zombie`、`/neutron`。输入普通文本（非 `/` 开头）会直接作为 prompt 发送给 agent。

```text
aiscan> 扫描 192.168.1.0/24 的 Web 服务
aiscan> /scan 检查这个网段的高危漏洞
aiscan> /neutron 用 critical 级别 POC 检查 http://target.example
aiscan> /continue
```

### Skills

aiscan 内置一组 skill，为 agent 提供特定扫描器的使用指南和工作流程。

| Skill | 说明 |
| --- | --- |
| `aiscan` | 核心机制和工具调用规则 |
| `scan` | 扫描流水线编排 |
| `gogo` | 主机/端口发现 |
| `spray` | Web 探测 |
| `zombie` | 弱口令检测 |
| `neutron` | POC 检测 |

通过 `-s` 参数指定 skill：

```bash
aiscan agent -s aiscan -s scan -p "全面扫描这个网段" -i 10.0.0.0/24
```

### agent 适合

- 任务描述不完全确定，需要 agent 自己选择扫描路径
- 需要把多个扫描器结果串起来解释
- 需要生成面向人的摘要、复现步骤或后续建议
- 需要接入 IOA 进行多 worker 协作

### agent 不适合

- 大范围无约束扫描
- 对时间和输出格式要求严格的批处理
- 没有 LLM provider 的环境

---

## scanner 的 --ai 模式

直接 scanner 命令加 `--ai` 时，aiscan 会先执行 scanner，再让 LLM 解释结果。对 `scan` 以外的 scanner（gogo、spray、zombie、neutron），`--ai` 会启动一个完整的 scanner agent，agent 可以使用工具进一步分析输出。对 `scan` 命令，`--ai` 在扫描完成后进行低成本的 LLM 总结。

```bash
# gogo 结果由 agent 分析（可调用工具）
aiscan --ai -p "只提取高风险暴露面，并给出证据" gogo -i 192.168.1.0/24 -p top100

# spray 结果分析
aiscan --ai -p "判断这些 Web 指纹是否值得进一步验证" spray -u http://target.example --finger

# neutron 结果分析
aiscan --ai -p "解释命中的 POC 影响和复现条件" neutron -u http://target.example -s critical,high

# scan 结果总结
aiscan --ai scan -i http://target.example --mode quick
```

`--ai` 会自动加载对应 scanner 的 skill。也可以额外指定 skill：

```bash
aiscan --ai --skill scan gogo -i 192.168.1.0/24 -p all
```

> `--ai` 更适合对 scanner 输出做总结、解释和筛选；`scan --verify` 更适合对发现进行自动化证据验证。两者可以组合使用。

---

## gogo：服务发现

`gogo` 用于主机、端口、服务、banner 和指纹发现。参数直接传递给底层 gogo 引擎。

```bash
aiscan gogo -i 192.168.1.0/24 -p top100
aiscan gogo -i 10.0.0.10 -p 80,443,8080
aiscan gogo -i targets.txt -p all
```

输出可作为后续 `spray`、`zombie`、`neutron` 的输入线索。对于多数任务，优先使用 `scan` 自动串联。

---

## spray：Web 探测和指纹

`spray` 用于 Web 目标探测、HTTP 指纹、常见文件、路径和 crawl。

```bash
aiscan spray -u http://target.example
aiscan spray -u http://target.example --finger
aiscan spray -l urls.txt --finger
```

aiscan 包装的 spray 默认附加 `--no-bar --no-stat` 参数，避免进度条影响输出。

---

## zombie：弱口令检测

`zombie` 用于授权弱口令检测。

```bash
aiscan zombie -i ssh://127.0.0.1:22 --top 3
aiscan zombie -i ssh://admin@127.0.0.1:22 -p admin123
aiscan zombie -l services.txt --top 10
```

> 注意 `zombie -p` 是密码参数，不是 agent prompt。

---

## neutron：POC 检测

`neutron` 用于模板化 POC 执行，支持按 ID、tag、severity、fingerprint 和模板路径过滤。

| 参数 | 说明 |
| --- | --- |
| `-u, --target` | URL、host 或 ip:port，可重复 |
| `-i, --input` | target 别名 |
| `-l, --list` | 目标文件 |
| `-t, --templates` | 自定义模板文件或目录 |
| `--id` | 按模板 ID 执行 |
| `--finger` | 按指纹过滤模板 |
| `--tags, --tag` | 按 tag 过滤模板 |
| `-s, --severity` | 按严重性过滤 |
| `-c, --concurrency` | 模板并发 |
| `--rate-limit` | 每秒执行上限 |
| `-j, --json` | JSON Lines 输出 |
| `-o, --output` | 写入文件 |
| `--template-list` | 列出匹配模板 |

```bash
aiscan neutron -u http://target.example -s critical,high
aiscan neutron -u http://target.example --finger nginx --max-per-finger 20
aiscan neutron -l targets.txt --tags cve,rce -c 10 --rate-limit 20
aiscan neutron -u http://target.example -t ./pocs --id shiro-detect -j -o findings.jsonl
aiscan neutron -u http://target.example --template-list
```

---

## cyberhub：资源查询

`cyberhub` 子命令用于查询和搜索已加载的指纹和 POC 模板。

```text
cyberhub list [finger|poc|all] [options]
cyberhub search [finger|poc|all] <query> [options]
```

| 参数 | 说明 |
| --- | --- |
| `-t, --type` | 资源类型：`finger`、`poc`、`all` |
| `-q, --query` | 搜索关键词 |
| `--tag` | 按 tag 过滤，可逗号分隔或重复 |
| `--protocol` | 指纹协议过滤：`http` 或 `tcp` |
| `--finger` | 按指纹名过滤 POC，可逗号分隔或重复 |
| `-s, --severity` | 按严重性过滤 POC，可逗号分隔或重复 |
| `--limit` | 最大输出行数（默认 50，0 表示全部） |
| `-j, --json` | JSON Lines 输出 |

```bash
aiscan cyberhub list finger --limit 20
aiscan cyberhub search finger nginx
aiscan cyberhub list poc --severity critical,high
aiscan cyberhub search poc spring --tag rce -j
```

---

## IOA：协作模式

IOA 是 aiscan 的协作层。`ioa serve` 启动本地 HTTP server 和 SQLite store；`agent --loop` 连接 server 注册 worker 并监听任务。

### 启动 IOA server

默认监听 `http://127.0.0.1:8765`，数据库为 `./ioa.db`：

```bash
aiscan ioa serve
```

指定地址和数据库：

```bash
aiscan ioa serve --ioa-url http://127.0.0.1:8765 --ioa-db ./ioa.db
```

### 启动 loop worker

`agent --loop` 连接 IOA server，注册节点，进入指定 space，监听任务并执行 agentic 工作流。

```bash
# 基本 loop worker
aiscan agent --loop --ioa-url http://127.0.0.1:8765 --space case-1 --llm-model gpt-4o

# 带初始 intent 和 skill
aiscan agent --loop --ioa-url http://127.0.0.1:8765 --space case-1 -p "负责内网 Web 资产扫描和漏洞验证" -s aiscan -s scan

# 指定节点名
aiscan agent --loop --ioa-url http://127.0.0.1:8765 --space case-1 --ioa-node-name web-scanner-1

# 启用 heartbeat（每 5 分钟主动运行一次 agent）
aiscan agent --loop --ioa-url http://127.0.0.1:8765 --space case-1 --heartbeat 5 -p "负责持续观察 IOA 上下文并协调下一步扫描"
```

`--heartbeat <分钟>` 让 loop worker 每隔 N 分钟主动运行一次 agent。heartbeat 读取最近 IOA 消息，把 space、node、intent 和上下文交给 agent 决定下一步。agent 可以执行本地工具，也可以通过 IOA 工具发送协调消息或给其他节点分配 task。

不指定 `--ioa-url` 时，loop 模式默认连接 `http://127.0.0.1:8765`。

### agent 接入 IOA 工具

`agent` 传入 `--ioa-url` 后，会向 IOA server 注册节点和工具，让 agent 能使用 IOA 相关工具（`ioa_space`、`ioa_send`、`ioa_read`）。

```bash
aiscan agent --ioa-url http://127.0.0.1:8765 -p "在 case-1 中协调扫描任务" -i http://target.example
```

### IOA 客户端查询

```bash
# 列出所有空间
aiscan ioa spaces --ioa-url http://127.0.0.1:8765

# 列出空间消息
aiscan ioa messages default --ioa-url http://127.0.0.1:8765

# 查看消息上下文
aiscan ioa context default <message-id> --ioa-url http://127.0.0.1:8765

# 列出所有节点
aiscan ioa nodes --ioa-url http://127.0.0.1:8765

# 列出空间内节点
aiscan ioa nodes case-1 --ioa-url http://127.0.0.1:8765

# JSON 输出
aiscan ioa spaces --ioa-url http://127.0.0.1:8765 --json
```

---

## Cyberhub 资源服务

aiscan 可以从 Cyberhub 加载指纹、模板等扫描资源。

```bash
aiscan scan -i http://target.example --cyberhub-url http://127.0.0.1:9000 --cyberhub-key "$CYBERHUB_KEY"
```

| 模式 | 说明 |
| --- | --- |
| `merge` | 默认。合并内置资源和 Cyberhub 资源 |
| `override` | 使用 Cyberhub 资源覆盖内置资源 |

---

## 输出与格式

`scan` 命令支持多种输出格式：

| 参数 | 格式 | 说明 |
| --- | --- | --- |
| （默认） | 终端友好 | 带颜色的结构化文本，实时流式输出 |
| `-j, --json` | JSON Lines | 适合机器处理 |
| `--report` | Markdown | 结构化报告 |
| `-f, --file <路径>` | 文件 | 输出写入文件，自动去除 ANSI 颜色 |
| `--no-color` | | 禁用终端颜色 |

`scan` 默认使用流式输出（边扫描边显示结果）。使用 `-j` 或 `--report` 时关闭流式输出，等待扫描完成后一次性输出。

其他 scanner（gogo、spray、zombie、neutron）的输出格式由各自参数控制。

agent 输出会通过 Markdown 渲染（终端支持时），`--no-color` 可禁用。

---

## 场景选择建议

| 目标 | 推荐命令 |
| --- | --- |
| 快速资产发现和风险初筛 | `aiscan scan -i <target>` |
| 深入扫描和更多 Web 路径 | `aiscan scan -i <target> --mode full` |
| 自动解释结果和生成结论 | `aiscan agent -p "<任务>" -i <target>` |
| 对 scanner 输出做 AI 摘要 | `aiscan --ai -p "<意图>" <scanner> ...` |
| 查询已加载的指纹和 POC | `aiscan cyberhub list poc --severity critical` |
| 机器读取结果 | `aiscan scan -i <target> -j` |
| 人读报告 | `aiscan scan -i <target> --report` |
| 多 worker 协作 | `aiscan ioa serve` + `aiscan agent --loop` |
| 交互式探索 | `aiscan agent` |

---

## 常见问题

### agent 报 provider 未配置

`agent` 必须有可用 LLM provider。设置对应环境变量或显式传入 `--llm-api-key`。

```bash
export OPENAI_API_KEY="sk-..."
aiscan agent --llm-model gpt-4o -p "检查目标" -i http://target.example
```

### scan --verify 没有产生 AI 验证

1. 检查是否配置了 LLM provider
2. 确认发现的风险优先级达到了 `--verify` 指定阈值
3. 未显式传 `--verify` 时默认策略为 `auto`（等效 `high`），如果 provider 不可用会静默跳过

```bash
aiscan scan -i http://target.example --verify=low --llm-api-key "$OPENAI_API_KEY"
```

### 输出太多或包含颜色

使用文件输出或关闭颜色：

```bash
aiscan scan -i 127.0.0.1 -f result.txt --no-color
```

### 扫描太慢

降低并发或范围：

```bash
aiscan scan -i 192.168.1.0/24 --port top100 --thread 500
```

### --ai 需要 LLM 但 scan 不需要

`--ai` 对所有 scanner 都需要 LLM provider。`scan` 的核心流水线不依赖 LLM；`scan --verify` 在 LLM 不可用时会自动跳过验证。

### cyberhub 没有结果

检查 `--cyberhub-url` 和 `--cyberhub-key` 是否正确配置，以及 Cyberhub 服务是否可达：

```bash
aiscan cyberhub list finger --cyberhub-url http://127.0.0.1:9000 --cyberhub-key "$CYBERHUB_KEY"
```

### 信号处理

- 第一次 Ctrl+C：优雅关闭，完成当前工作后退出
- 第二次 Ctrl+C：强制退出
