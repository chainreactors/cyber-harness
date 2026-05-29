<p align="center">
  <h1 align="center">aiscan</h1>
  <p align="center">Agentic Security Scanner — AI-driven reconnaissance meets deterministic scanning</p>
</p>

<p align="center">
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/v/release/chainreactors/aiscan?style=flat-square" alt="Release"></a>
  <a href="https://github.com/chainreactors/aiscan/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/chainreactors/aiscan/ci.yml?branch=master&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/downloads/chainreactors/aiscan/total?style=flat-square" alt="Downloads"></a>
  <a href="https://github.com/chainreactors/aiscan/blob/master/LICENSE"><img src="https://img.shields.io/github/license/chainreactors/aiscan?style=flat-square" alt="License"></a>
</p>

---

**aiscan** 是一个融合 LLM agent 与传统安全扫描引擎的自动化安全扫描器。它既可以像普通 CLI 一样直接运行扫描，也可以让 AI agent 根据自然语言目标自主选择工具、执行扫描、分析证据并输出结论。

> **请只在明确授权的目标上使用。**

## Features

- **多阶段扫描流水线** — `scan` 命令自动串联端口发现 → Web 探测 → 弱口令检测 → POC 检测，无需 LLM 也能运行
- **AI Agent 模式** — 自然语言描述任务，agent 自主选择扫描路径、分析结果、生成结论
- **内置扫描引擎** — 集成 [gogo](https://github.com/chainreactors/gogo)（端口/服务发现）、[spray](https://github.com/chainreactors/spray)（Web 探测/指纹）、[zombie](https://github.com/chainreactors/zombie)（弱口令）、[neutron](https://github.com/chainreactors/neutron)（POC 检测）
- **多 LLM 支持** — OpenAI、DeepSeek、Anthropic、OpenRouter、Groq、Moonshot、Ollama 等
- **AI 验证** — 对扫描发现进行 LLM 驱动的自动验证，减少误报
- **分布式协作** — [IOA 架构](docs/ioa.md)支持多 worker 节点通过消息空间协同扫描
- **多种输出** — 终端实时流式、JSON Lines、Markdown 报告

## Architecture

```
┌─────────────────────────────────────────────────┐
│                    aiscan CLI                    │
├──────────┬──────────┬──────────┬────────────────┤
│   scan   │  agent   │ scanner  │      ioa       │
│ pipeline │ LLM agent│  direct  │ collaboration  │
├──────────┴──────────┴──────────┴────────────────┤
│              Command Registry & Skills           │
├────────┬────────┬─────────┬─────────┬───────────┤
│  gogo  │ spray  │ zombie  │ neutron │  browser  │
│  port  │  web   │  weak   │   poc   │ headless  │
│ scan   │ probe  │  pass   │  check  │  render   │
├────────┴────────┴─────────┴─────────┴───────────┤
│         LLM Providers  │  Cyberhub Resources     │
└─────────────────────────┴────────────────────────┘
```

## Quick Start

### 安装

从 [GitHub Releases](https://github.com/chainreactors/aiscan/releases/latest) 下载对应平台的二进制文件：

```bash
# Linux
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_linux_amd64
chmod +x aiscan
sudo mv aiscan /usr/local/bin/

# macOS (Apple Silicon)
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_darwin_arm64
chmod +x aiscan && xattr -d com.apple.quarantine aiscan 2>/dev/null || true
sudo mv aiscan /usr/local/bin/
```

```powershell
# Windows
Invoke-WebRequest "https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_windows_amd64.exe" -OutFile aiscan.exe
```

### 扫描（无需 LLM）

```bash
# 快速扫描：端口发现 → Web 探测 → 弱口令 → POC
aiscan scan -i 192.168.1.0/24

# 完整扫描：增加路径爆破和更深爬取
aiscan scan -i 192.168.1.0/24 --mode full

# JSON 输出
aiscan scan -i 192.168.1.0/24 -j
```

### Agent 模式（需要 LLM）

```bash
export OPENAI_API_KEY="sk-..."

# 自然语言任务
aiscan agent -p "发现 Web 服务并检查高风险漏洞" -i 192.168.1.0/24

# 交互式 REPL
aiscan agent
```

## Documentation

| 文档 | 说明 |
| --- | --- |
| [Quick Start](docs/quickstart.md) | 安装、环境准备、第一次扫描 |
| [配置指南](docs/configuration.md) | LLM Provider、配置文件、环境变量、Cyberhub 资源 |
| [使用指南](docs/usage.md) | 命令详解、扫描模式、Agent、输出格式 |
| [IOA 协作](docs/ioa.md) | 多 Agent 协作架构、Space/Node/Message 模型、Loop Worker、Heartbeat |

## Supported Platforms

| 系统 | 架构 | 文件 |
| --- | --- | --- |
| Linux | amd64 / arm64 | `aiscan_linux_amd64` / `aiscan_linux_arm64` |
| macOS | Intel / Apple Silicon | `aiscan_darwin_amd64` / `aiscan_darwin_arm64` |
| Windows | amd64 | `aiscan_windows_amd64.exe` |

## License

See [LICENSE](LICENSE) for details.

## Links

- [chainreactors](https://github.com/chainreactors) — Organization
- [gogo](https://github.com/chainreactors/gogo) — Port & service discovery
- [spray](https://github.com/chainreactors/spray) — Web probing & fingerprinting
- [zombie](https://github.com/chainreactors/zombie) — Credential testing
- [neutron](https://github.com/chainreactors/neutron) — Template-based POC engine
