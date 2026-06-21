<p align="center">
  <img src="assets/logo.svg" width="180" alt="aiscan logo">
  <h1 align="center">aiscan</h1>
  <p align="center">Agentic Security Scanner — AI-driven reconnaissance meets deterministic scanning</p>
  <p align="center"><strong>Preview — 本项目处于早期预览阶段，API 和功能可能随版本变更</strong></p>
</p>

<p align="center">
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/v/release/chainreactors/aiscan?style=flat-square&color=00E59B" alt="Release"></a>
  <a href="https://github.com/chainreactors/aiscan/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/chainreactors/aiscan/ci.yml?branch=master&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/downloads/chainreactors/aiscan/total?style=flat-square&color=00B4D8" alt="Downloads"></a>
  <a href="https://github.com/chainreactors/aiscan/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue?style=flat-square" alt="AGPL-3.0"></a>
  <a href="https://github.com/chainreactors/aiscan/stargazers"><img src="https://img.shields.io/github/stars/chainreactors/aiscan?style=flat-square&color=yellow" alt="Stars"></a>
</p>

---

**aiscan** 是一个融合 LLM agent 与传统安全扫描引擎的自动化安全扫描器。提供三种使用模式：**Scan**（确定性流水线扫描，AI 可选辅助）、**Agent**（自然语言驱动的自主安全评估）、**IOA**（多 agent 分布式协作）。

> **请只在明确授权的目标上使用。**

## Quick Start

```bash
# 无需 LLM，一行启动扫描
aiscan scan -i 192.168.1.0/24

# 有 LLM，一行启动 agent
aiscan agent --base-url "https://api.deepseek.com" --api-key "sk-..." --model deepseek-chat \
  -p "扫描目标并检查高风险漏洞" -i 192.168.1.0/24
```

## 安装

### 下载二进制

从 [GitHub Releases](https://github.com/chainreactors/aiscan/releases/latest) 下载：

| 版本 | 说明 |
| --- | --- |
| **aiscan** | 标准版，包含 scan/agent/gogo/spray/zombie/neutron/arsenal |
| **aiscan-full** | 完整版，额外包含 playwright 浏览器、passive recon、katana 爬虫 |
| **aiscan-agent** | 轻量 agent 版，仅包含 agent 运行时，适合部署为远程 worker |

```bash
# Linux (standard)
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_linux_amd64
chmod +x aiscan && sudo mv aiscan /usr/local/bin/

# Linux (agent-only)
curl -L -o aiscan-agent https://github.com/chainreactors/aiscan/releases/latest/download/aiscan-agent_linux_amd64
chmod +x aiscan-agent && sudo mv aiscan-agent /usr/local/bin/

# macOS
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_darwin_arm64
chmod +x aiscan && sudo mv aiscan /usr/local/bin/

# Windows (PowerShell)
Invoke-WebRequest "https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_windows_amd64.exe" -OutFile aiscan.exe
```

### 从源码构建

```bash
git clone https://github.com/chainreactors/aiscan.git
cd aiscan

# 基础版
go build -o aiscan ./cmd/aiscan

# 完整版（含 playwright/katana/passive）
go build -tags full -o aiscan-full ./cmd/aiscan
```

---

## Features

- **多阶段扫描流水线** — `scan` 命令自动串联端口发现 → Web 探测 → 弱口令检测 → POC 检测，无需 LLM 也能运行
- **AI Agent 模式** — 自然语言描述任务，agent 自主选择扫描路径、调用工具、分析结果、生成结论
- **Goal Evaluation** — `-e` 指定评估标准，独立 evaluator LLM 判定任务完成度，fail 时自动注入反馈驱动 agent 重试
- **[IOA](https://github.com/chainreactors/ioa) 分布式协作** — 多 agent 通过消息空间协同扫描，支持 IOA worker 持续监听任务
- **内置扫描引擎** — [gogo](https://github.com/chainreactors/gogo)（端口/服务）、[spray](https://github.com/chainreactors/spray)（Web/指纹）、[zombie](https://github.com/chainreactors/zombie)（弱口令）、[neutron](https://github.com/chainreactors/neutron)（POC）
- **多 LLM 支持** — OpenAI、DeepSeek、Anthropic、OpenRouter、Groq、Moonshot、Ollama 等，支持多 provider 容错降级
- **AI 增强扫描** — `--verify` 验证减少误报，`--sniper` 搜索公开漏洞，`--deep` 深度动态测试
- **Katana 爬虫**（full 版）— 进程内 katana，支持 standard/headless/hybrid 引擎
- **Playwright 浏览器**（full 版）— 交互式浏览器会话、headless 引擎
- **Arsenal 工具管理** — 内置安全工具包管理器（crtm），`arsenal install httpx` 一键安装，自动注入 PATH
- **TMux 终端** — agent 可执行长时间后台任务，增量输出自动推送 inbox
- **Proxy 代理** — Clash 订阅 + 多协议（trojan/vless/anytls/hy2/ss）+ proxy-chain 执行
- **TUI 分级详细度** — `-v` 显示 tool call 详情，`-vv` 显示 thinking + 完整输出
- **Passive Recon**（full 版）— FOFA / Hunter 网络空间搜索

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                         aiscan CLI                            │
├──────────┬──────────┬──────────┬──────────┬──────────────────┤
│   scan   │  agent   │ scanner  │   ioa    │      tools       │
│ pipeline │ LLM agent│  direct  │  collab  │ tmux/proxy/search│
│          │ +eval    │          │          │                  │
├──────────┴──────────┴──────────┴──────────┴──────────────────┤
│        Command Registry & Skills & Goal Evaluator             │
├────────┬────────┬────────┬─────────┬─────────┬───────────────┤
│  gogo  │ spray  │ zombie │ neutron │playwright│ passive/katana│
│  port  │  web   │  weak  │   poc   │ browser │ recon / crawl │
│  scan  │ probe  │  pass  │  check  │ headless│   (full)      │
├────────┴────────┴────────┴─────────┴─────────┴───────────────┤
│     LLM Providers (fallback chain)  │  Cyberhub Resources     │
└─────────────────────────────────────┴─────────────────────────┘
```

---

## Mode 1: Scan — 确定性扫描流水线

无需 LLM，`scan` 命令自动串联 gogo → spray → zombie → neutron，完成端口发现、Web 探测、弱口令检测和 POC 检测。可选开启 AI 辅助增强。

```bash
# 快速扫描（默认 quick 模式）
aiscan scan -i 192.168.1.0/24

# 完整扫描：更多端口 + 路径爆破 + 深度爬取
aiscan scan -i 192.168.1.0/24 --mode full

# AI 增强：LLM 验证扫描结果减少误报
aiscan scan -i http://target.example --verify=high

# AI 增强：搜索公开 CVE/漏洞
aiscan scan -i http://target.example --sniper

# AI 增强：深度动态测试
aiscan scan -i http://target.example --mode full --deep

# 组合使用 + 输出报告
aiscan scan -i http://target.example --mode full --verify=high --sniper --report
```

| 模式 | 说明 |
| --- | --- |
| `quick`（默认） | 快速暴露面发现，HTTP 基础弱口令，指纹匹配 POC |
| `full` | 更多端口，crawl depth=2，常见备份/目录探测，默认字典 |

| AI 标志 | 说明 |
| --- | --- |
| `--verify=<level>` | LLM 主动验证扫描发现（auto/low/medium/high/critical） |
| `--sniper` | 对每个指纹搜索公开 CVE/exploit |
| `--deep` | 对发现的 Web 资产进行 AI 驱动的动态测试 |

---

## Mode 2: Agent — AI 自主安全评估

Agent 模式下 LLM 自主规划扫描路径、调用工具（gogo/spray/zombie/neutron/web_search/fetch/tmux 等）、分析结果并输出结论。

### 一次性任务

```bash
# 自然语言任务
aiscan agent -p "扫描目标，发现所有 Web 服务并检查高风险漏洞" -i 192.168.1.0/24

# Goal Evaluation：独立 LLM 评估任务完成度，未达标自动注入反馈驱动重试
aiscan agent -p "全面扫描目标" -i http://target.example -e "发现所有开放端口并输出服务指纹"
```

### 交互式 REPL

```bash
# 进入交互式会话，支持多轮对话
aiscan agent

# REPL 内置命令
# /help          — 查看所有命令
# /provider      — 查看 LLM provider 状态
# /eval <criteria> — 设置 goal evaluation 标准
# ! <command>    — 直接执行 bash 或扫描命令（绕过 LLM）
```

### Agent 工具集

| 工具 | 说明 |
| --- | --- |
| `gogo` / `spray` / `zombie` / `neutron` | 扫描器 |
| `cyberhub` | 指纹和 POC 关联搜索 |
| `web_search` / `fetch` | 搜索安全情报、抓取网页 |
| `bash` / `tmux` | 执行命令、管理后台会话 |
| `arsenal` | 安全工具包管理（install/update/remove） |
| `proxy` | 代理节点管理、proxy-chain 执行 |
| `playwright` | 无头浏览器操作（仅 full 版） |
| `subagent` | 子 agent（sync/async/fork） |
| `read` / `write` / `glob` | 文件操作 |
| `finish` | 显式结束任务 |

---

## Mode 3: IOA — 分布式多 Agent 协作

通过 [IOA（Internet of Agents）](https://github.com/chainreactors/ioa) 架构，多个 aiscan agent 实例通过消息空间协同工作。详见 [IOA 文档](docs/ioa.md)。

```bash
# 启动 IOA Server
aiscan ioa serve --ioa-url http://0.0.0.0:8765

# 启动 IOA worker
aiscan agent \
  --ioa-url http://127.0.0.1:8765 \
  --ioa-node-name worker-1 \
  --space pentest-project \
  -p "scan assigned targets and report findings"

# 查询 IOA 状态
aiscan ioa spaces --ioa-url http://127.0.0.1:8765
aiscan ioa messages <space-name> --ioa-url http://127.0.0.1:8765
aiscan ioa nodes --ioa-url http://127.0.0.1:8765
```

---

## LLM 配置

Agent 和 AI 增强功能需要 LLM。通过 CLI 参数、环境变量或配置文件设置：

```bash
# 环境变量
export OPENAI_API_KEY="sk-..."

# CLI 参数
aiscan agent --provider deepseek --base-url https://api.deepseek.com --api-key sk-... --model deepseek-chat
```

配置文件 `~/.config/aiscan/config.yaml`：

```yaml
llm:
  provider: openai
  api_key: sk-...
  model: gpt-4o

  # 多 provider 降级链（可选）
  providers:
    - provider: deepseek
      base_url: https://api.deepseek.com
      api_key: sk-...
      model: deepseek-chat
```

支持的 Provider：OpenAI、DeepSeek、Anthropic、OpenRouter、Groq、Moonshot、Ollama，以及任意 OpenAI 兼容 API。详见 [参考手册](docs/reference.md)。

---

## Documentation

| 文档 | 说明 |
| --- | --- |
| [Scan 模式详解](docs/scan.md) | 扫描流水线、AI 增强、输出格式 |
| [Agent 模式详解](docs/agent.md) | Agent 工具集、Goal Evaluation、REPL、工具详解 |
| [IOA 协作](docs/ioa.md) | 多 Agent 协作架构、Space/Node/Message 模型 |
| [参考手册](docs/reference.md) | 配置、LLM Provider、全局参数、扫描器用法、FAQ |
| [Changelog](docs/changelog.md) | 版本变更记录 |

## Supported Platforms

| 系统 | 架构 | 标准版 | 完整版 | Agent 版 |
| --- | --- | --- | --- | --- |
| Linux | amd64 / arm64 | `aiscan_linux_amd64` | `aiscan-full_linux_amd64` | `aiscan-agent_linux_amd64` |
| macOS | Intel / Apple Silicon | `aiscan_darwin_amd64` | `aiscan-full_darwin_arm64` | `aiscan-agent_darwin_arm64` |
| Windows | amd64 | `aiscan_windows_amd64.exe` | `aiscan-full_windows_amd64.exe` | `aiscan-agent_windows_amd64.exe` |

## Contributing

欢迎提交 Issue 和 Pull Request。

1. Fork 本仓库
2. 创建功能分支 (`git checkout -b feature/xxx`)
3. 提交更改 (`git commit -m 'feat: add xxx'`)
4. 推送分支 (`git push origin feature/xxx`)
5. 创建 Pull Request

## License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE).

## Links

- [chainreactors](https://github.com/chainreactors) — Organization
- [IOA](https://github.com/chainreactors/ioa) — Internet of Agents, multi-agent collaboration protocol
- [gogo](https://github.com/chainreactors/gogo) — Port & service discovery
- [spray](https://github.com/chainreactors/spray) — Web probing & fingerprinting
- [zombie](https://github.com/chainreactors/zombie) — Credential testing
- [neutron](https://github.com/chainreactors/neutron) — Template-based POC engine

---

<p align="center">
  <a href="https://star-history.com/#chainreactors/aiscan&Date">
    <img src="https://api.star-history.com/svg?repos=chainreactors/aiscan&type=Date" alt="Star History" width="600">
  </a>
</p>
