# 参考手册

本文档是 aiscan 的完整参考，涵盖命令结构、配置、LLM Provider、各扫描器用法、资源查询和常见问题。

---

## 命令结构

```text
aiscan [全局参数] <subcommand> [子命令参数]
```

| 命令 | 类型 | 功能 |
| --- | --- | --- |
| `agent` | agentic | LLM agent；无任务输入时进入交互式 REPL，`--ioa-url` 时作为 IOA worker |
| `scan` | pipeline | 自动流水线：gogo → spray → zombie → neutron，可选 AI 验证/sniper/deep |
| `gogo` | scanner | 主机存活、端口、服务、banner 和指纹发现 |
| `spray` | scanner | Web 探测、HTTP 指纹、常见文件、爬取和路径检查 |
| `zombie` | scanner | 授权弱口令检测 |
| `neutron` | scanner | 模板化 POC 检测 |
| `proton` | scanner | 敏感信息扫描（API 密钥、令牌、凭证、密码），支持管道输入 |
| `katana` | scanner | Web 爬虫（仅 full 版） |
| `passive` | scanner | 网络空间搜索 FOFA/Hunter（仅 full 版） |
| `arsenal` | tool mgr | 安全工具包管理（install/update/remove） |
| `cyberhub` | query | 查询已加载的指纹和 POC 模板 |
| `ioa serve` | service | 启动 IOA HTTP server |
| `ioa spaces/messages/context/nodes` | query | IOA 查询 |

查看帮助：`aiscan -h`、`aiscan scan -h`、`aiscan neutron -h`

---

## 配置

### 配置优先级

```
CLI 参数 > AIScan/集成环境变量 > 配置文件 > Provider 兼容环境变量 > 编译时默认值
```

`AISCAN_*`、Cyberhub、FOFA、Hunter、Tavily 等明确属于 AIScan 的环境变量会覆盖配置文件。`OPENAI_*`、`ANTHROPIC_*` 等可能由其他工具注入的 Provider 兼容变量只用于填补配置文件中的空值。

### 配置文件

```bash
aiscan --init          # 生成默认 aiscan.yaml 到当前目录
aiscan -c /path/to/aiscan.yaml scan -i 192.168.1.0/24   # 指定配置文件
```

自动搜索路径：`./aiscan.yaml` → `<二进制所在目录>/aiscan.yaml`

### 配置文件结构

```yaml
# LLM Provider
llm:
  provider: ""        # 协议类型：openai（默认，兼容所有 OpenAI API）或 anthropic
  base_url: ""        # API base URL（留空使用 provider 默认值）
  api_key: ""         # API key（建议使用环境变量）
  model: ""           # 模型名称
  context_window: 0    # 真实 Token 数；0 表示按模型推断，未知模型默认 128000
  max_tokens: 0        # 单次最大输出；0 使用默认值 16384
  proxy: ""           # 访问 LLM API 的 HTTP proxy

  # 多 LLM profile 配置（可选；只手动切换，不自动 fallback）
  active_profile: deepseek
  providers:
    - id: deepseek
      name: DeepSeek
      provider: openai
      base_url: https://api.deepseek.com/v1
      api_key: "sk-..."
      model: deepseek-chat
      context_window: 128000
      max_tokens: 16384
    - id: openai
      name: OpenAI
      provider: openai
      api_key: "sk-..."
      model: gpt-4o

# Cyberhub 资源服务
cyberhub:
  url: ""
  key: ""
  mode: ""            # merge（默认）或 override

# IOA 协作
ioa:
  url: ""
  node_name: ""
  space: ""

# Agent 交互输出
output:
  preset: "default"       # default、verbose 或 full
  # reasoning: "hidden"   # hidden 或 full
  # tool_calls: "compact" # hidden 或 compact
  # tool_arguments: "hidden" # hidden、preview 或 full
  # tool_results: "hidden"   # hidden、preview 或 full
  # live_status: true      # thinking/tooling/talking 瞬时状态
  # usage: true            # 瞬时状态中的 token/上下文用量

# 扫描默认值
scan:
  verify: ""          # auto, off, low, medium, high, critical
  verify_timeout: 0

# 通用选项
misc:
  debug: false
  quiet: false
  no_color: false
```

### Agent 输出

`output.preset` 提供三组基线；未注释的细粒度字段会覆盖所选 preset：

| 输出项 | `default` | `verbose` / `-v` | `full` / `-vv` |
| --- | --- | --- | --- |
| reasoning | hidden | full | full |
| tool_calls | compact | compact | compact |
| tool_arguments | hidden | preview | preview |
| tool_results | hidden | preview | full |
| live_status | true | true | true |
| usage | true | true | true |

默认输出只保留紧凑的工具调用摘要，不显示 reasoning、结构化参数或工具结果。`tool_calls: hidden` 是总开关，同时隐藏工具参数和结果。`live_status: false` 只关闭动态状态，仍按策略输出静态工具摘要；`usage: false` 只隐藏动态状态中的 token 和上下文用量，不产生或删除永久统计行。

输出优先级为 `-q > -vv > -v > output 配置 > default`。`-q` 只显示最终回答；`-v` 和 `-vv` 会完整覆盖 `output` 中的 preset 和细粒度字段。此配置只影响 Agent 交互输出，不改变 scanner 输出或 `--debug` 日志，也不改变最终回答的 stdout 输出。

交互模式下 `Ctrl+O` 按 `default → thinking → full → default` 循环固定 preset。当前为自定义细粒度配置时，第一次按键先切换到 `default`，之后再继续循环。

---

## 全局参数

全局参数建议放在子命令之前。只有 `scan` 支持在命令之后继续写全局参数并自动提取；其他 scanner 后面的参数原样传给对应引擎，避免短参数冲突。

### LLM 参数

| 参数 | 说明 |
| --- | --- |
| `--provider` | LLM 协议类型：`openai`（OpenAI-compatible）或 `anthropic` |
| `--base-url` | LLM API base URL |
| `--api-key` | LLM API key（也可用环境变量） |
| `--model` | 模型名称（默认 `gpt-4o`） |
| `--context-window` | 模型上下文窗口；自定义模型 ID 建议显式设置 |
| `--max-tokens` | 单次 LLM 响应的最大输出 token 数 |
| `--llm-proxy` | 访问 LLM API 的 HTTP 代理 |
| `--ai` | 对 scanner 输出启用 LLM 分析 |

### Agent 参数

| 参数 | 说明 |
| --- | --- |
| `-p, --prompt` | 自然语言任务描述，或已存在的 prompt 文件路径 |
| `-i, --input` | 目标输入（IP、URL、IP:port、CIDR），可重复 |
| `-s, --skill` | 指定 skill 名称或文件路径，可重复 |
| `--task-file` | 从文件读取任务描述 |
| `--heartbeat <分钟>` | heartbeat 间隔（0 表示关闭，默认 0） |
| `--timeout <秒>` | 整体超时（默认 3600） |
| `-e, --eval` | 目标评估标准 — 独立 LLM 判断任务是否达成 |

`context_window` 使用真实整数，例如 128K 窗口填写 `128000`，不是 `128K`。所有正整数都可保存；Web 设置页会对小于 8192 的值显示非阻塞风险提示。

`max_tokens` 并非无条件发送：AIScan 会预估消息和工具 schema 的 token 数，并按 `context_window - 当前上下文 - 4096` 自动收紧。若安全预留后没有输出空间，请求会在发送前返回包含窗口、预估输入和预留量的明确错误。上下文接近窗口时会按 Pi 的默认策略自动压缩；服务端返回上下文溢出时会压缩并自动重试一次。

### Scanner 参数

| 参数 | 说明 |
| --- | --- |
| `--proxy` | Scanner 代理，支持 `socks5://`、`trojan://`、`vless://`、`clash://`（订阅自动负载均衡） |
| `--cyberhub-url` | Cyberhub 资源服务 URL |
| `--cyberhub-key` | Cyberhub API key |
| `--cyberhub-mode` | 资源模式：`merge`（默认）或 `override` |

### IOA 参数

| 参数 | 说明 |
| --- | --- |
| `--ioa-url` | IOA server URL |
| `--ioa-node-id` | 已有 IOA 节点 ID |
| `--ioa-node-name` | 注册时使用的节点名（默认自动生成） |
| `--space` | IOA 空间名（默认 `default`） |
| `--json` | IOA 查询结果以 JSON 输出 |

### 通用参数

| 参数 | 说明 |
| --- | --- |
| `--debug` | 输出调试日志 |
| `-v, --verbose` | 显示完整 reasoning 和预览后的工具参数/结果；重复为 `-vv`，显示完整工具结果 |
| `-q, --quiet` | 只显示最终回答（优先于 `-v/-vv`） |
| `--no-color` | 禁用 ANSI 颜色 |
| `--version` | 输出版本号并退出 |

> **参数名冲突说明**：顶层参数和 scanner 子命令参数可能同名。例如 `aiscan agent -p` 中 `-p` 是自然语言 prompt，`aiscan gogo -p` 中 `-p` 是端口参数，`aiscan zombie -p` 中 `-p` 是密码参数。aiscan 会根据子命令自动区分。

---

## LLM 协议与 Profile

### 支持的协议

| 协议 | 用途 | 默认 Base URL | 环境变量 |
| --- | --- | --- | --- |
| `openai` | OpenAI 及 DeepSeek、OpenRouter、Groq、Moonshot、Ollama 等 OpenAI-compatible API | `https://api.openai.com/v1` | `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL` |
| `anthropic` | Anthropic Messages API 及兼容网关 | `https://api.anthropic.com/v1` | `ANTHROPIC_API_KEY` / `ANTHROPIC_BASE_URL` / `ANTHROPIC_MODEL` |

除 Anthropic 协议外，其余模型服务统一使用 `openai`，通过 `base_url`、`model` 和 `api_key` 指定实际服务。旧配置中的 `deepseek`、`openrouter`、`ollama` 等 provider 名称会自动归一化为 `openai`。

### 多 LLM Profile 配置

配置文件可通过 `llm.providers` 保存多个 LLM profile，并用 `llm.active_profile` 明确选择当前项；未指定时使用列表第一项。每个 entry 支持 `id`、`name`、`provider`、`base_url`、`api_key`、`model`、`proxy`、`timeout`、`max_tokens` 和 `context_window`。`model` 必填，保存配置或激活 Profile 时都会拒绝空模型。Web 设置页可以选择当前 profile，REPL 可通过 `/provider` 查看配置，并用 `/provider set` 显式应用新配置。

Web 设置页拉取模型列表时使用当前编辑 Profile 的已保存密钥。若端点不提供 `GET /models`（返回 404），页面会保留手动模型输入，不把它显示为连接故障。

Agent 只会重试当前 provider。重试耗尽后直接返回错误，不会自动切换到其他 profile，也不会把同一 turn 发给另一模型。

### Provider 配置示例

```bash
# 环境变量
export OPENAI_API_KEY="sk-..."
aiscan agent -p "检查目标" -i http://target.example

# DeepSeek（OpenAI-compatible）
aiscan agent --provider openai --base-url https://api.deepseek.com/v1 --api-key "sk-..." --model deepseek-chat

# Ollama（OpenAI-compatible；部分部署可使用任意非空 API key）
aiscan agent --provider openai --model llama3 --base-url http://localhost:11434/v1 --api-key local

# 任意 OpenAI 兼容 API
aiscan agent --base-url https://my-proxy.example/v1 --api-key "$MY_KEY" --model my-model

# 通过代理访问 LLM API
aiscan agent --llm-proxy http://127.0.0.1:7890
```

---

## 代理（Proxy）

### Scanner 代理

`--proxy` 参数为扫描器设置代理：

```bash
aiscan scan -i http://target.example --proxy socks5://127.0.0.1:1080
aiscan scan -i http://target.example --proxy trojan://password@server:443
aiscan scan -i http://target.example --proxy vless://uuid@server:443?security=tls
aiscan scan -i http://target.example --proxy clash://https://subscribe.example/link
```

Agent 模式下还可通过 `proxy` 工具在运行时动态管理代理，详见 [Agent 模式详解](agent.md)。

### LLM API 代理

`--llm-proxy` 单独为 LLM API 请求设置 HTTP 代理：

```bash
aiscan agent --llm-proxy http://127.0.0.1:7890 -p "检查目标" -i http://target.example
```

---

## 直接使用扫描器

### gogo：服务发现

```bash
aiscan gogo -i 192.168.1.0/24 -p top100
aiscan gogo -i 10.0.0.10 -p 80,443,8080
aiscan gogo -i targets.txt -p all
```

### spray：Web 探测和指纹

```bash
aiscan spray -u http://target.example
aiscan spray -u http://target.example --finger
aiscan spray -l urls.txt --finger
```

### zombie：弱口令检测

```bash
aiscan zombie -i ssh://127.0.0.1:22 --top 3
aiscan zombie -i ssh://admin@127.0.0.1:22 -p admin123
```

> 注意：`zombie -p` 是密码参数，不是 agent 的 prompt 参数。

### neutron：POC 检测

| 参数 | 说明 |
| --- | --- |
| `-u, --target` | URL、host 或 ip:port，可重复 |
| `-l, --list` | 目标文件 |
| `-t, --templates` | 自定义模板文件或目录 |
| `--id` | 按模板 ID 执行 |
| `--finger` | 按指纹过滤模板 |
| `--tags` | 按 tag 过滤模板 |
| `-s, --severity` | 按严重性过滤 |
| `-c, --concurrency` | 模板并发数 |
| `--rate-limit` | 每秒执行上限 |
| `-j, --json` | JSON Lines 输出 |
| `--template-list` | 列出匹配模板（不执行） |

```bash
aiscan neutron -u http://target.example -s critical,high
aiscan neutron -u http://target.example --finger nginx
aiscan neutron -l targets.txt --tags cve,rce -c 10 --rate-limit 20
aiscan neutron -u http://target.example -t ./pocs --id shiro-detect -j
```

### proton：敏感信息扫描

| 参数 | 说明 |
| --- | --- |
| `-i, --input` | 目标文件或目录 |
| `-l, --list` | 包含多个目标路径的文件 |
| `-e, --expression` | 自定义正则表达式（可重复） |
| `-t, --templates` | 自定义模板文件或目录 |
| `-c, --category` | 内置模板类别：keys, spray, all（默认 keys） |
| `--id` | 按规则 ID 过滤 |
| `--tags` | 按 tag 过滤 |
| `-s, --severity` | 按严重性过滤 |
| `-j, --json` | JSON Lines 输出 |
| `-o, --output` | 输出结果到文件 |
| `--template-list` | 列出匹配规则（不执行） |

```bash
aiscan proton -i /path/to/project
aiscan proton -i . --tags cloud --severity high
aiscan proton -i . -e "AKIA[0-9A-Z]{16}" -e "password\s*[:=]"
aiscan proton --template-list -c keys
# 管道输入
curl -s http://target/api | aiscan proton
cat .env | aiscan proton -c keys
```

### katana：Web 爬虫（仅 full 版）

```bash
aiscan katana -u https://target.example -d 3 -jc
aiscan katana -u https://target.example -hl -d 3 -jc       # headless
aiscan katana -u https://target.example -hh -d 2            # hybrid
```

| 参数 | 说明 |
| --- | --- |
| `-hl, --headless` | 启用 headless 浏览器爬取 |
| `-hh, --hybrid` | 启用 headless hybrid 爬取 |
| `-cwu, --chrome-ws-url` | 连接已有 Chrome 实例 |

### passive：网络空间搜索（仅 full 版）

```bash
aiscan passive -s fofa 'domain="example.com"'
aiscan passive -s hunter 'domain.suffix="example.com"'
```

| 数据源 | 凭据参数 | 环境变量 |
| --- | --- | --- |
| `fofa` | `--fofa-email`, `--fofa-key` | `FOFA_EMAIL`, `FOFA_KEY` |
| `hunter` | `--hunter-api-key` | `HUNTER_API_KEY` |
| `shodan-idb` | 无需 API key | — |

---

## Cyberhub 资源

Cyberhub 提供外部指纹库和 POC 模板，可以扩充或替换内置资源。

```bash
aiscan scan -i http://target.example --cyberhub-url http://127.0.0.1:9000 --cyberhub-key "$CYBERHUB_KEY"
```

资源模式：`merge`（默认，合并内置和远程）或 `override`（远程覆盖内置）。

### cyberhub 查询命令

```bash
aiscan cyberhub search --finger tomcat
aiscan cyberhub search --cve CVE-2021-44228
aiscan cyberhub search --vendor apache --product tomcat
aiscan cyberhub list poc --severity critical --limit 10
aiscan cyberhub id tomcat
```

结构化查询标志：`--finger`、`--cve`、`--vendor`、`--product`、`--poc`、`--tag`、`-s`、`--limit`、`-j`。

本地缓存位于 `~/.aiscan/cache/`，TTL 24 小时。

---

## 扫描默认值

```yaml
scan:
  verify: "auto"       # auto 等效 high，LLM 不可用时跳过
  verify_timeout: 0
```

| 值 | 说明 |
| --- | --- |
| `auto` | 编译时默认值；等效 `high`，LLM 不可用时自动跳过 |
| `off` | 关闭验证 |
| `low` / `medium` / `high` / `critical` | 验证对应优先级及以上的发现 |

---

## 环境变量汇总

| 变量 | 说明 |
| --- | --- |
| `OPENAI_API_KEY` | OpenAI API key |
| `OPENAI_BASE_URL` / `OPENAI_BASEURL` | OpenAI/Codex 风格 API base URL |
| `OPENAI_MODEL` | OpenAI/Codex 风格模型名 |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `ANTHROPIC_BASE_URL` / `ANTHROPIC_BASEURL` | Claude Code 风格 API base URL |
| `ANTHROPIC_MODEL` | Claude Code 风格模型名 |
| `AISCAN_API_KEY` | 统一 fallback API key（所有 provider 通用） |
| `AISCAN_BASE_URL` / `AISCAN_LLM_BASE_URL` | 统一 LLM API base URL |
| `AISCAN_MODEL` / `AISCAN_LLM_MODEL` | 统一模型名 |
| `AISCAN_PROVIDER` / `AISCAN_LLM_PROVIDER` | 协议类型：`openai` 或 `anthropic` |
| `AISCAN_LLM_PROXY` | LLM API 请求代理 |
| `AISCAN_DATA_DIR` | 数据目录；优先级低于显式 `--data-dir` |
| `AISCAN_PROXY` / `AISCAN_SCANNER_PROXY` | 扫描工具代理 |
| `AISCAN_CYBERHUB_URL` / `CYBERHUB_URL` | Cyberhub URL |
| `AISCAN_CYBERHUB_KEY` / `CYBERHUB_KEY` | Cyberhub API key |
| `AISCAN_CYBERHUB_MODE` / `CYBERHUB_MODE` | Cyberhub 资源模式 |
| `TAVILY_API_KEY` / `TAVILY_API_KEYS` | Tavily Web Search API key，多个 key 可逗号分隔 |
| `FOFA_EMAIL` / `FOFA_KEY` | FOFA 凭据 |
| `HUNTER_API_KEY` / `HUNTER_TOKEN` | Hunter 凭据 |
| `RECON_PROXY` | 被动测绘出站代理 |
| `SHODAN_API_KEY`、`QUAKE_TOKEN`、`ZOOMEYE_API_KEY`、`NETLAS_API_KEY` | Uncover 数据源凭据 |
| `CENSYS_API_TOKEN` / `CENSYS_ORGANIZATION_ID` | Censys 凭据 |
| `CRIMINALIP_API_KEY`、`PUBLICWWW_API_KEY`、`HUNTERHOW_API_KEY` | Uncover 数据源凭据 |
| `BINARYEDGE_API_KEY`、`ONYPHE_API_KEY`、`GREYNOISE_API_KEY` | Uncover 数据源凭据 |
| `DRIFTNET_API_KEY`、`DAYDAYMAP_API_KEY`、`ODIN_API_KEY`、`NERDYDATA_API_KEY` | Uncover 数据源凭据 |
| `GOOGLE_API_KEY` / `GOOGLE_API_CX` | Google Search 凭据 |
| `AISCAN_RENDER` | 终端渲染模式：interactive、static、forwarded |
| `AISCAN_REPL` | REPL 输入模式：readline 或 fast |
| `PLAYWRIGHT_CLI_SESSION` | Playwright 默认 session |

运行时业务环境变量只在 `core/config` 解析一次，再通过运行时配置下传。`PATH`、子进程环境继承以及 Go 标准库的 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 属于操作系统级行为，不纳入业务配置优先级。前端开发服务器的 `AISCAN_BACKEND_URL` 是 Vite 构建期配置，也不进入 Go 运行时配置。

---

## 场景选择建议

| 场景 | 推荐命令 |
| --- | --- |
| 快速资产发现和风险初筛 | `aiscan scan -i <target>` |
| 完整扫描（含路径爆破） | `aiscan scan -i <target> --mode full` |
| 搜索已知漏洞情报 | `aiscan scan -i <target> --sniper` |
| 深度动态测试 | `aiscan scan -i <target> --deep` |
| AI 主动验证 + 漏洞搜索 | `aiscan scan -i <target> --verify=high --sniper` |
| 自动解释结果和生成结论 | `aiscan agent -p "<任务>" -i <target>` |
| 目标驱动 + 自动评估 | `aiscan agent -e "<标准>" -p "<任务>" -i <target>` |
| 对 scanner 输出做 AI 摘要 | `aiscan --ai -p "<意图>" <scanner> ...` |
| 查询指纹和 POC | `aiscan cyberhub search --finger <name>` |
| 机器可读输出 | `aiscan scan -i <target> -j` |
| 人可读报告 | `aiscan scan -i <target> --report` |
| 回看历史扫描记录 | `aiscan -F result.jsonl` |
| 多 worker 协作 | `aiscan ioa serve` + `aiscan agent --ioa-url http://127.0.0.1:8765 --space case-1` |
| 交互式探索 | `aiscan agent` |

---

## 常见问题

### agent 报 provider 未配置

设置对应环境变量或通过 `--api-key` 传入：

```bash
export OPENAI_API_KEY="sk-..."
aiscan agent -p "检查目标" -i http://target.example
```

### scan --verify 没有产生 AI 验证

1. 检查是否配置了 LLM provider
2. 确认发现的风险优先级达到了 `--verify` 阈值
3. 未显式传 `--verify` 时默认 `auto`（等效 `high`），LLM 不可用时静默跳过

### 输出太多或包含颜色

```bash
aiscan scan -i 127.0.0.1 -f result.txt          # 文件输出（自动去除 ANSI）
aiscan scan -i 127.0.0.1 --no-color              # 禁用颜色
```

### 扫描太慢

```bash
aiscan scan -i 192.168.1.0/24 --port top100      # 缩小端口范围
aiscan scan -i 192.168.1.0/24 --thread 500        # 降低并发
```

### --ai 需要 LLM 但 scan 不需要

顶层 `--ai` 在 scanner 执行后启动 LLM agent 分析输出，必须配置 LLM。`scan` 核心流水线不依赖 LLM。`scan --verify` 在 LLM 不可用时自动跳过。

### cyberhub 没有结果

检查 `--cyberhub-url`/`--cyberhub-key` 是否正确。本地缓存在 `~/.aiscan/cache/`（TTL 24h），删除缓存可强制刷新。

### 信号处理

| 操作 | 行为 |
| --- | --- |
| 第一次 Ctrl+C | 停止当前任务 |
| 第二次 Ctrl+C | 取消上下文，退出 |
| 第三次 Ctrl+C | 强制退出进程 |

连续按键间隔超过 5 秒时计数器重置。
