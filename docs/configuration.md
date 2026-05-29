# 配置指南

aiscan 的配置涵盖 LLM Provider、扫描参数、资源服务和协作选项。配置可通过 CLI 参数、配置文件或环境变量提供。

---

## 目录

- [配置优先级](#配置优先级)
- [配置文件](#配置文件)
- [LLM Provider](#llm-provider)
- [Cyberhub 资源服务](#cyberhub-资源服务)
- [IOA 协作配置](#ioa-协作配置)
- [代理（Proxy）](#代理proxy)
- [扫描默认值](#扫描默认值)
- [环境变量汇总](#环境变量汇总)

---

## 配置优先级

aiscan 按以下顺序加载配置，后者覆盖前者：

```
编译时固化值（build.sh ldflags）
  ↓
默认配置文件（./config.yaml 或 ~/.config/aiscan/config.yaml）
  ↓
CLI 参数
  ↓
-c 自定义配置文件
```

仅填写需要的字段，未填写的字段不会覆盖其他来源的值。

---

## 配置文件

### 生成默认配置

```bash
aiscan --init
```

生成 `config.yaml` 到当前目录。

### 配置文件路径

aiscan 自动搜索：

1. `./config.yaml`（当前目录）
2. `~/.config/aiscan/config.yaml`（用户目录）

通过 `-c` 指定自定义路径：

```bash
aiscan -c /path/to/config.yaml scan -i 192.168.1.0/24
```

### 配置文件结构

```yaml
# LLM Provider
llm:
  provider: ""        # openai, deepseek, openrouter, ollama, groq, moonshot, anthropic
  base_url: ""        # API base URL（留空使用 provider 默认值）
  api_key: ""         # API key（建议使用环境变量）
  model: ""           # 模型名称
  proxy: ""           # 访问 LLM API 的 HTTP proxy

# Vision（可选，独立的视觉模型配置）
vision:
  enabled: false
  provider: ""
  base_url: ""
  api_key: ""
  model: ""
  proxy: ""

# Cyberhub 资源服务
cyberhub:
  url: ""
  key: ""
  mode: ""            # merge 或 override

# IOA 协作
ioa:
  url: ""
  db: ""
  node_name: ""
  space: ""

# 扫描默认值
scan:
  verify: ""          # auto, off, low, medium, high, critical
  verify_timeout: 0   # 单次验证超时秒数

# 通用选项
misc:
  debug: false
  quiet: false
  no_color: false
```

---

## LLM Provider

`agent`、`scan --verify` 和 `--ai` 模式需要 LLM Provider。

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

### API Key 优先级

```
--api-key CLI 参数 > Provider 对应环境变量 > AISCAN_API_KEY 统一变量
```

### Provider 自动推断

aiscan 可以从 `--base-url` 自动推断 provider。例如 URL 包含 `deepseek.com` 时自动识别为 `deepseek`，无需显式指定 `--provider`。

### 配置示例

**OpenAI：**

```bash
export OPENAI_API_KEY="sk-..."
aiscan agent --model gpt-4o -p "检查目标" -i http://target.example
```

**DeepSeek：**

```bash
export DEEPSEEK_API_KEY="..."
aiscan agent --provider deepseek --model deepseek-chat -p "扫描目标" -i 10.0.0.0/24
```

**Ollama（本地模型）：**

```bash
ollama run llama3
aiscan agent --provider ollama --model llama3 --base-url http://localhost:11434/v1 -p "检查站点" -i http://target.example
```

**任意 OpenAI 兼容 API：**

```bash
aiscan agent --base-url https://my-proxy.example/v1 --api-key "$MY_KEY" --model my-model -p "扫描目标" -i 10.0.0.0/24
```

**通过代理访问 LLM API：**

```bash
aiscan agent --llm-proxy http://127.0.0.1:7890 -p "检查暴露面" -i http://target.example
```

**配置文件方式：**

```yaml
# ~/.config/aiscan/config.yaml
llm:
  provider: openai
  api_key: "sk-..."
  model: gpt-4o
```

---

## Cyberhub 资源服务

Cyberhub 提供外部指纹库和 POC 模板，可以扩充或替换 aiscan 内置资源。

### 配置

```bash
# CLI 参数
aiscan scan -i http://target.example --cyberhub-url http://127.0.0.1:9000 --cyberhub-key "$CYBERHUB_KEY"
```

```yaml
# 配置文件
cyberhub:
  url: "http://127.0.0.1:9000"
  key: "your-key"
  mode: "merge"
```

### 资源模式

| 模式 | 说明 |
| --- | --- |
| `merge`（默认） | 合并内置资源和 Cyberhub 资源 |
| `override` | 使用 Cyberhub 资源完全覆盖内置资源 |

### 查询资源

```bash
# 列出指纹
aiscan cyberhub list finger --limit 20

# 搜索指纹
aiscan cyberhub search finger nginx

# 列出高危 POC
aiscan cyberhub list poc --severity critical,high

# 搜索 POC
aiscan cyberhub search poc spring --tag rce -j
```

---

## IOA 协作配置

IOA（Intelligent Operation Architecture）支持多个 aiscan 实例协同工作。

### CLI 参数

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `--ioa-url` | IOA server URL | `http://127.0.0.1:8765` |
| `--ioa-node-id` | 已有节点 ID | — |
| `--ioa-node-name` | 注册时的节点名 | 自动生成 |
| `--ioa-db` | SQLite 数据库路径 | `./ioa.db` |
| `--space` | IOA 空间名 | `default` |

### 配置文件

```yaml
ioa:
  url: "http://127.0.0.1:8765"
  db: "./ioa.db"
  node_name: "web-scanner-1"
  space: "default"
```

---

## 代理（Proxy）

### Scanner 代理

`--proxy` 参数为扫描器设置代理，支持多种协议：

```bash
# SOCKS5
aiscan scan -i http://target.example --proxy socks5://127.0.0.1:1080

# Trojan
aiscan scan -i http://target.example --proxy trojan://password@server:443

# VLESS
aiscan scan -i http://target.example --proxy vless://uuid@server:443?security=tls

# Clash 订阅（自动负载均衡）
aiscan scan -i http://target.example --proxy clash://https://subscribe.example/link
```

### LLM API 代理

`--llm-proxy` 单独为 LLM API 请求设置 HTTP 代理：

```bash
aiscan agent --llm-proxy http://127.0.0.1:7890 -p "检查目标" -i http://target.example
```

---

## 扫描默认值

```yaml
scan:
  # AI 验证模式
  # auto: 等效 high，LLM 不可用时跳过
  # off / low / medium / high / critical
  verify: "auto"
  # 单次验证超时秒数（0 使用默认值）
  verify_timeout: 0
```

### 验证模式说明

| 值 | 说明 |
| --- | --- |
| `auto` | 编译时默认值；等效 `high`，LLM 不可用时自动跳过 |
| `off` | 关闭验证 |
| `low` | 验证所有优先级的发现 |
| `medium` | 验证 medium 及以上 |
| `high` | 验证 high 及以上 |
| `critical` | 仅验证 critical |

---

## 环境变量汇总

| 变量 | 说明 |
| --- | --- |
| `OPENAI_API_KEY` | OpenAI API key |
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `OPENROUTER_API_KEY` | OpenRouter API key |
| `GROQ_API_KEY` | Groq API key |
| `MOONSHOT_API_KEY` | Moonshot API key |
| `AISCAN_API_KEY` | 统一 fallback API key（所有 provider 通用） |
