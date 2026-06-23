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
CLI 参数 > 环境变量 > 配置文件 > 编译时默认值
```

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
  provider: ""        # openai, deepseek, openrouter, ollama, groq, moonshot, anthropic
  base_url: ""        # API base URL（留空使用 provider 默认值）
  api_key: ""         # API key（建议使用环境变量）
  model: ""           # 模型名称
  proxy: ""           # 访问 LLM API 的 HTTP proxy

  # 多 provider 降级链（可选）
  providers:
    - provider: deepseek
      base_url: https://api.deepseek.com
      api_key: "sk-..."
      model: deepseek-chat
    - provider: openai
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

---

## 全局参数

全局参数建议放在子命令之前。只有 `scan` 支持在命令之后继续写全局参数并自动提取；其他 scanner 后面的参数原样传给对应引擎，避免短参数冲突。

### LLM 参数

| 参数 | 说明 |
| --- | --- |
| `--provider` | LLM provider 名称（openai、deepseek、openrouter、ollama 等） |
| `--base-url` | LLM API base URL |
| `--api-key` | LLM API key（也可用环境变量） |
| `--model` | 模型名称（默认 `gpt-4o`） |
| `--llm-proxy` | 访问 LLM API 的 HTTP 代理 |
| `--ai` | 对 scanner 输出启用 LLM 分析 |

### Agent 参数

| 参数 | 说明 |
| --- | --- |
| `-p, --prompt` | 自然语言任务描述 |
| `-i, --input` | 目标输入（IP、URL、IP:port、CIDR），可重复 |
| `-s, --skill` | 指定 skill 名称或文件路径，可重复 |
| `--task-file` | 从文件读取任务描述 |
| `--heartbeat <分钟>` | heartbeat 间隔（0 表示关闭，默认 0） |
| `--timeout <秒>` | 整体超时（默认 3600） |
| `-e, --eval` | 目标评估标准 — 独立 LLM 判断任务是否达成 |

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
| `-q, --quiet` | 减少日志输出 |
| `--no-color` | 禁用 ANSI 颜色 |
| `--version` | 输出版本号并退出 |

> **参数名冲突说明**：顶层参数和 scanner 子命令参数可能同名。例如 `aiscan agent -p` 中 `-p` 是自然语言 prompt，`aiscan gogo -p` 中 `-p` 是端口参数，`aiscan zombie -p` 中 `-p` 是密码参数。aiscan 会根据子命令自动区分。

---

## LLM Provider

### 支持的 Provider

| Provider | 默认 Base URL | 默认模型 | API Key 环境变量 |
| --- | --- | --- | --- |
| `openai` | `https://api.openai.com/v1` | `gpt-4o` | `OPENAI_API_KEY` |
| `deepseek` | `https://api.deepseek.com/v1` | `deepseek-chat` | `DEEPSEEK_API_KEY` |
| `anthropic` | `https://api.anthropic.com/v1` | — | `ANTHROPIC_API_KEY` |
| `openrouter` | `https://openrouter.ai/api/v1` | — | `OPENROUTER_API_KEY` |
| `groq` | `https://api.groq.com/openai/v1` | — | `GROQ_API_KEY` |
| `moonshot` | `https://api.moonshot.cn/v1` | — | `MOONSHOT_API_KEY` |
| `ollama` | `http://localhost:11434/v1` | — | 不需要 |

aiscan 可以从 `--base-url` 自动推断 provider（如 URL 包含 `deepseek.com` 自动识别为 `deepseek`）。

### 多 Provider 降级链

当主 provider 重试耗尽后，agent loop 自动切换到降级链中的下一个 provider 并重放当前 turn。配置文件中通过 `llm.providers` 数组定义，每个 entry 支持 `provider`、`base_url`、`api_key`、`model`、`proxy`、`timeout` 字段。启动时并行初始化，失败的跳过。REPL 中可通过 `/provider` 查看链状态。

### Provider 配置示例

```bash
# 环境变量
export OPENAI_API_KEY="sk-..."
aiscan agent -p "检查目标" -i http://target.example

# 指定 provider
aiscan agent --provider deepseek --base-url https://api.deepseek.com --api-key "sk-..." --model deepseek-chat

# Ollama 本地模型
aiscan agent --provider ollama --model llama3 --base-url http://localhost:11434/v1

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
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `ANTHROPIC_BASE_URL` / `ANTHROPIC_BASEURL` | Claude Code 风格 API base URL |
| `ANTHROPIC_MODEL` | Claude Code 风格模型名 |
| `OPENROUTER_API_KEY` | OpenRouter API key |
| `GROQ_API_KEY` | Groq API key |
| `MOONSHOT_API_KEY` | Moonshot API key |
| `AISCAN_API_KEY` | 统一 fallback API key（所有 provider 通用） |
| `AISCAN_BASE_URL` / `AISCAN_LLM_BASE_URL` | 统一 LLM API base URL |
| `AISCAN_MODEL` / `AISCAN_LLM_MODEL` | 统一模型名 |
| `AISCAN_PROVIDER` / `AISCAN_LLM_PROVIDER` | 统一 provider 名称 |
| `AISCAN_LLM_PROXY` | LLM API 请求代理 |
| `TAVILY_API_KEY` | Tavily Web Search API key（agent `web_search` 工具） |
| `FOFA_EMAIL` / `FOFA_KEY` | FOFA 凭据 |
| `HUNTER_API_KEY` | Hunter API key |

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
