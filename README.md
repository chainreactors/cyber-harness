<p align="center">
  <img src="assets/logo.svg" width="180" alt="aiscan logo">
  <h1 align="center">aiscan</h1>
  <p align="center">Agentic Security Scanner — AI-driven reconnaissance meets deterministic scanning</p>
  <p align="center"><strong>Preview — APIs and features may change between releases</strong></p>
</p>

<p align="center">
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/v/release/chainreactors/aiscan?style=flat-square&color=00E59B" alt="Release"></a>
  <a href="https://github.com/chainreactors/aiscan/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/chainreactors/aiscan/ci.yml?branch=master&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/downloads/chainreactors/aiscan/total?style=flat-square&color=00B4D8" alt="Downloads"></a>
  <a href="https://github.com/chainreactors/aiscan/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue?style=flat-square" alt="AGPL-3.0"></a>
  <a href="https://github.com/chainreactors/aiscan/stargazers"><img src="https://img.shields.io/github/stars/chainreactors/aiscan?style=flat-square&color=yellow" alt="Stars"></a>
</p>

<p align="center">
  <a href="README_CN.md">中文文档</a>
</p>

---

**aiscan** combines LLM agents with traditional security scanning engines. Three modes: **Scan** (deterministic pipeline, optional AI assist), **Agent** (natural-language autonomous assessment), **IOA** (multi-agent distributed collaboration).

> **Use only on explicitly authorized targets.**

## Quick Start

```bash
# No LLM needed — one-line scan
aiscan scan -i 192.168.1.0/24

# With LLM — one-line agent
aiscan agent --base-url "https://api.deepseek.com" --api-key "sk-..." --model deepseek-chat \
  -p "scan targets and check for high-risk vulnerabilities" -i 192.168.1.0/24
```

## Install

### Download Binary

From [GitHub Releases](https://github.com/chainreactors/aiscan/releases/latest):

| Edition | Description |
| --- | --- |
| **aiscan** | Standard — scan/agent/gogo/spray/zombie/neutron/arsenal |
| **aiscan-full** | Full — adds playwright browser, passive recon, katana crawler |
| **aiscan-agent** | Lightweight agent runtime, ideal for remote worker deployment |

| OS | Arch | Standard | Full | Agent |
| --- | --- | --- | --- | --- |
| Linux | amd64 / arm64 | `aiscan_linux_amd64` | `aiscan-full_linux_amd64` | `aiscan-agent_linux_amd64` |
| macOS | Intel / Apple Silicon | `aiscan_darwin_amd64` | `aiscan-full_darwin_arm64` | `aiscan-agent_darwin_arm64` |
| Windows | amd64 | `aiscan_windows_amd64.exe` | `aiscan-full_windows_amd64.exe` | `aiscan-agent_windows_amd64.exe` |

```bash
# Linux
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_linux_amd64
chmod +x aiscan && sudo mv aiscan /usr/local/bin/

# macOS
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_darwin_arm64
chmod +x aiscan && sudo mv aiscan /usr/local/bin/

# Windows (PowerShell)
Invoke-WebRequest "https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_windows_amd64.exe" -OutFile aiscan.exe
```

### Build from Source

```bash
git clone https://github.com/chainreactors/aiscan.git && cd aiscan

go build -o aiscan ./cmd/aiscan                          # standard
go build -tags full -o aiscan-full ./cmd/aiscan           # full (playwright/katana/passive)
```

---

## Features

### Design

- **Single binary** — one statically-linked executable, zero runtime dependencies; `aiscan-agent` is under 25 MB
- **Minimal agent core** — the agent loop is ~160 lines; tool calls, retries, evaluation, and streaming are composed around it rather than baked in
- **Plugin architecture** — tools register via `init()` side effects; adding a scanner is one file with `RegisterFactory`. Build tags (`full`) gate heavy dependencies (playwright, katana) at compile time
- **Embedded skills** — each tool ships a `SKILL.md` that the agent loads automatically on invocation, providing usage docs and tactical guidance without hardcoded prompts
- **Scan + Agent unified** — the same scanner engines power both the deterministic `scan` pipeline and the autonomous `agent` mode; no separate codebases

### Scan — Deterministic Pipeline

- Multi-stage auto-chaining: port discovery → web probing → weak credentials → POC detection — no LLM required
- AI-enhanced options: `--verify` to reduce false positives, `--sniper` to search public CVEs, `--deep` for AI-driven dynamic testing
- Two modes: `quick` (fast exposure mapping) and `full` (deep crawl + directory brute + extended ports)

### Agent — AI-Autonomous Security Assessment

- Natural language task description — the agent autonomously plans scan paths, invokes tools, analyzes results, and produces conclusions
- Goal Evaluation: `-e` sets evaluation criteria; an independent evaluator LLM judges completion and injects feedback for automatic retry
- Interactive REPL with multi-turn conversation; `!` prefix to execute commands directly (bypass LLM)
- Multi-provider fallback chain for resilience
- TUI verbosity levels: `-v` for tool call details, `-vv` for thinking + full output

### [IOA](https://github.com/chainreactors/ioa) — Distributed Multi-Agent Collaboration

- Multiple agents collaborate through shared message spaces
- IOA worker mode for persistent task listening
- Built-in IOA server with token authentication

### Built-in Toolset

**Scanners**
- [gogo](https://github.com/chainreactors/gogo) — port, service, and banner discovery
- [spray](https://github.com/chainreactors/spray) — web probing, HTTP fingerprinting, path fuzzing
- [zombie](https://github.com/chainreactors/zombie) — credential testing for common services
- [neutron](https://github.com/chainreactors/neutron) — template-based POC execution
- [cyberhub](https://github.com/chainreactors/fingers) — fingerprint and POC association query

**Browser & Recon** (full edition)
- playwright — interactive headless Chromium sessions, screenshots, network capture
- katana — in-process web crawler with standard/headless/hybrid engines
- passive — cyberspace search via FOFA, Hunter, Shodan

**Utilities**
- tmux — PTY session manager for long-running background tasks with incremental output delivery
- arsenal — security tool package manager ([crtm](https://github.com/chainreactors/crtm)), one-command install with auto PATH injection
- proxy — Clash subscription parser + multi-protocol (trojan/vless/anytls/hy2/ss) proxy chain
- web_search / fetch — web search for CVEs and advisories, URL fetching

---

## Usage

### Scan Mode

```bash
aiscan scan -i 192.168.1.0/24                                    # quick scan
aiscan scan -i 192.168.1.0/24 --mode full                        # full scan
aiscan scan -i http://target.example --verify=high --sniper       # AI-enhanced
aiscan scan -i http://target.example --mode full --deep --report  # full + deep + report
```

### Agent Mode

```bash
# One-shot task
aiscan agent -p "scan and find web vulnerabilities" -i 192.168.1.0/24

# With goal evaluation
aiscan agent -p "full scan" -i http://target.example -e "find all open ports with service fingerprints"

# Interactive REPL
aiscan agent
```

### IOA Mode

```bash
# Start IOA server
aiscan ioa serve --ioa-url http://0.0.0.0:8765

# Start IOA worker
aiscan agent --ioa-url http://127.0.0.1:8765 --space pentest-project \
  -p "scan assigned targets and report findings"
```

### LLM Configuration

```bash
# Environment variable
export OPENAI_API_KEY="sk-..."

# CLI arguments
aiscan agent --provider deepseek --base-url https://api.deepseek.com --api-key sk-... --model deepseek-chat
```

Config file `~/.config/aiscan/config.yaml`:

```yaml
llm:
  provider: openai
  api_key: sk-...
  model: gpt-4o
```

---

## Documentation

| Doc | Description |
| --- | --- |
| [Scan Mode](docs/scan.md) | Pipeline, AI enhancements, output formats |
| [Agent Mode](docs/agent.md) | Toolset, Goal Evaluation, REPL |
| [IOA](docs/ioa.md) | Multi-agent architecture, Space/Node/Message model |
| [Reference](docs/reference.md) | Configuration, providers, flags, scanner usage, FAQ |
| [Changelog](docs/changelog.md) | Version history |

## Contributing

1. Fork this repository
2. Create a feature branch (`git checkout -b feature/xxx`)
3. Commit your changes (`git commit -m 'feat: add xxx'`)
4. Push to the branch (`git push origin feature/xxx`)
5. Create a Pull Request

## License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE).

## Links

- [chainreactors](https://github.com/chainreactors) — Organization
- [IOA](https://github.com/chainreactors/ioa) — Internet of Agents
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
