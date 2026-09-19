<p align="center">
  <img src="web/assets/logo.svg" width="180" alt="cyber logo">
  <h1 align="center">cyber-harness</h1>
  <p align="center">An everything-is-an-extension agent harness for cybersecurity</p>
</p>

<p align="center">
  <a href="https://github.com/chainreactors/cyber-harness/releases"><img src="https://img.shields.io/github/v/release/chainreactors/cyber-harness?style=flat-square&color=00E59B" alt="Release"></a>
  <a href="https://github.com/chainreactors/cyber-harness/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/chainreactors/cyber-harness/ci.yml?branch=master&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/chainreactors/cyber-harness/releases"><img src="https://img.shields.io/github/downloads/chainreactors/cyber-harness/total?style=flat-square&color=00B4D8" alt="Downloads"></a>
  <a href="https://github.com/chainreactors/cyber-harness/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue?style=flat-square" alt="AGPL-3.0"></a>
  <a href="https://github.com/chainreactors/cyber-harness/stargazers"><img src="https://img.shields.io/github/stars/chainreactors/cyber-harness?style=flat-square&color=yellow" alt="Stars"></a>
</p>

<p align="center">
  <a href="README_CN.md">中文文档</a>
</p>

---

cyber-harness is an agent harness built on one idea: **everything is an extension**.

At its core is a small model-tool loop — ask the model, run the tool calls it requests, feed the
results back, and repeat until the job is done. Scanners, browsers, terminals, proxies, the Web UI,
and multi-agent collaboration are all built the same way and statically linked into one binary.
Extending the harness means adding a new component next to the existing ones, not patching a
privileged kernel.

`aiscan` is the reference distribution — the agent, a security toolset, skills, deterministic
scanning, and IOA collaboration packaged as a single executable.

> **Use only on explicitly authorized targets. Unauthorized use is illegal.**

## Key features

- **Everything is an extension** — no privileged kernel. Scanners, browsers, terminals, proxies,
  the Web UI, and multi-agent collaboration are all assembled the same way and statically linked
  into one binary; the same framework can be embedded in your own application.
- **Minimal agent core** — the loop is deliberately small; security tooling, evaluation,
  collaboration, and workflows all live outside it.
- **Single-binary distribution** — `aiscan` bundles the core security toolset and IOA collaboration;
  `aiscan-full` adds the Web UI, browser automation, passive recon, and deep crawling.
- **Multi-agent collaboration** — IOA provides shared message spaces and worker mode for
  distributed agents, with a built-in token-authenticated server.

## Run

```bash
# One-shot agent task (with an LLM)
aiscan agent --base-url "https://api.deepseek.com" --api-key "sk-..." --model deepseek-chat \
  -p "scan targets and check for high-risk vulnerabilities" -i 192.168.1.0/24

# Skip the LLM and drive the scanner engines directly
aiscan scan -i 192.168.1.0/24

# Web console (full edition)
aiscan-full web
```

The scanner engines are also exposed as deterministic commands — for automation and environments
without an LLM — and as IOA workers for multi-agent collaboration:

```bash
aiscan scan -i http://target.example --mode full --deep --report
aiscan agent -p "scan assigned targets and report findings" \
  --ioa-url http://127.0.0.1:8765 --space pentest-project
```

## Install

From [GitHub Releases](https://github.com/chainreactors/cyber-harness/releases/latest):

| Edition | Description |
| --- | --- |
| **aiscan** | The agent with the core security toolset and IOA collaboration |
| **aiscan-full** | Adds the Web UI, browser automation, passive recon, and deep crawling |

| OS | Arch | Standard | Full |
| --- | --- | --- | --- |
| Linux | amd64 / arm64 | `aiscan_linux_<arch>.zip` | `aiscan-full_linux_<arch>.zip` |
| macOS | Intel / Apple Silicon | `aiscan_darwin_<arch>.zip` | `aiscan-full_darwin_<arch>.zip` |
| Windows | amd64 / arm64 | `aiscan_windows_<arch>.zip` | `aiscan-full_windows_amd64.zip` |

```bash
# Linux
curl -LO https://github.com/chainreactors/cyber-harness/releases/latest/download/aiscan_linux_amd64.zip
unzip aiscan_linux_amd64.zip
chmod +x aiscan && sudo mv aiscan /usr/local/bin/

# macOS Apple Silicon
curl -LO https://github.com/chainreactors/cyber-harness/releases/latest/download/aiscan_darwin_arm64.zip
unzip aiscan_darwin_arm64.zip
chmod +x aiscan && sudo mv aiscan /usr/local/bin/

# Windows (PowerShell)
Invoke-WebRequest "https://github.com/chainreactors/cyber-harness/releases/latest/download/aiscan_windows_amd64.zip" -OutFile aiscan.zip
Expand-Archive .\aiscan.zip -DestinationPath .
.\aiscan.exe --version
```

### Web console (full edition)

`aiscan-full web` starts the browser UI and an embedded local agent. Open
`http://127.0.0.1:8080` and enter the access key printed at startup.

```bash
aiscan-full web                                              # local, ephemeral key
aiscan-full web --addr 0.0.0.0:8080 --token change-me        # network, fixed key
aiscan-full web --addr 0.0.0.0:8080 --token change-me --no-agent   # hub only
```

Sessions, scans, assets, findings, and configuration are stored in `cyber-web.db` by default;
override with `--db <path>`.

### Build from source

```bash
git clone https://github.com/chainreactors/cyber-harness.git && cd cyber-harness
make              # standard edition (aiscan)
make agent        # minimal local agent (no scanner/search/proxy/web)
make full         # frontend + full edition (aiscan-full)
```

`make full` needs Node.js/npm and a working CGO toolchain; it builds the frontend first so the
latest `web/static` is embedded. The native `record` tool is built separately with `make record`
(see [docs/record.md](docs/record.md)).

## What's inside

**Agent capabilities**

- [`terminal`](docs/agent.md) — runs commands in a real pseudo-terminal, with interactive input and background tasks
- [`goal`](docs/agent.md#goal-evaluation) — goal evaluation: the agent checks its work against the goal before finishing
- [`subagent`](docs/agent.md) — breaks complex tasks into subagents
- [`skills`](docs/agent.md) — pluggable prompts, knowledge, and reusable capabilities
- [`search`](docs/scan.md) — fingerprint, CVE, and web intelligence lookup
- [web_search / fetch](docs/agent.md) — CVE search and URL fetching
- [`tmux`](docs/agent.md) — background task sessions with incremental output delivery
- [`ioa`](docs/ioa.md) — multi-agent collaboration over shared message spaces and worker mode
- [`proxy`](docs/reference.md) — multi-protocol proxy chain (trojan/vless/anytls/hy2/ss)
- [`arsenal`](docs/reference.md) — security tool package manager

**Scanners**
- gogo — port, service, and banner discovery
- spray — web probing, fingerprinting, path fuzzing
- zombie — credential testing
- neutron — template-based POC execution
- proton — sensitive information scanning (API keys, tokens, credentials, secrets)
- cyberhub — fingerprint and POC association query

**Browser & recon** (full edition)
- playwright — headless Chromium sessions, screenshots, network capture
- katana — web crawler (standard/headless/hybrid engines)
- passive — cyberspace search (FOFA, Hunter, Shodan)

Optional SDK tool: `record` — native desktop/window screenshots and H.264/MP4 recording
(Windows and Linux X11).

## Configuration

```bash
export OPENAI_API_KEY="sk-..."      # environment
aiscan agent --provider openai --base-url https://api.deepseek.com/v1 --api-key sk-... --model deepseek-chat
```

Config file `cyber.yaml`:

```yaml
llm:
  provider: openai
  api_key: sk-...
  model: gpt-4o
  context_window: 128000   # literal token count, not "128K"
  max_tokens: 16384
```

See [docs/reference.md](docs/reference.md) for the full flag, provider, and scanner reference.

## Embed

Custom distributions use the same public composition path as the shipped binaries:

```go
entries, err := base.New(config)
entries = append(entries, myExtensions...)
set, err := extension.New(entries...)
```

The caller owns `set.Load` and `set.Close`. Use `pkg/aiscan.New` when embedding the complete
reference distribution instead of selecting capability packs yourself. A runnable minimal
distribution is in [`examples/custom`](examples/custom); lifecycle and ordering rules are in the
[extension architecture](docs/architecture.md).

## Documentation

| Doc | Description |
| --- | --- |
| [Agent Runtime](docs/agent.md) | Tools, goal evaluation, REPL, sessions, subagents |
| [Extension Architecture](docs/architecture.md) | Typed composition, lifecycle, ownership |
| [Development](docs/development.md) | How to extend the harness |
| [Composition RFC](docs/rfc-composition.md) | Public composition boundary and Go API migration |
| [Security Pipeline](docs/scan.md) | Direct scanning, AI enhancements, output formats |
| [IOA Collaboration](docs/ioa.md) | Multi-agent message spaces, worker mode |
| [AOP Integration](docs/integration.md) | Agent transport and host integration |
| [Protocol & Transport](docs/protocol-architecture.md) | AOP WebSocket, Connect management plane |
| [Record Tool](docs/record.md) | Desktop/window capture, native builds |
| [Reference](docs/reference.md) | Configuration, providers, flags, scanner usage, FAQ |
| [v1.0.0 Guide](docs/v1.0.0.md) | Stable API baseline, pre-v1 cleanup |
| [Changelog](docs/changelog.md) | Version history |

## Contributing

1. Fork this repository
2. Create a feature branch (`git checkout -b feature/xxx`)
3. Commit your changes (`git commit -m 'feat: add xxx'`)
4. Push to the branch (`git push origin feature/xxx`)
5. Create a Pull Request

## Disclaimer

1. This tool is intended for **authorized security testing and research purposes only**. If you need to test its capabilities, please set up your own lab environment.
2. Before using this tool for any scanning, you must ensure compliance with local laws and regulations and obtain **sufficient authorization. Do not scan unauthorized targets.**
3. If you engage in any illegal activity while using this tool, you shall bear all consequences yourself. We assume no legal or joint liability.
4. Before installing and using this tool, please **carefully read and fully understand all terms**. Limitation and disclaimer clauses may be highlighted for your attention.
5. Unless you have fully read, understood, and accepted all terms of this agreement, please do not install or use this tool. Your use or any other express or implied acceptance constitutes your agreement to be bound by these terms.

## License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE).

## Links

- [chainreactors](https://github.com/chainreactors) — Organization
- [IOA](https://github.com/chainreactors/ioa) — Internet of Agents
- [sdk](https://github.com/chainreactors/sdk) — Scanner SDK
- [proxyclient](https://github.com/chainreactors/proxyclient) — Multi-protocol proxy client
- [crtm](https://github.com/chainreactors/crtm) — Security tool package registry
- [utils](https://github.com/chainreactors/utils) — Shared utilities & PTY manager
- [parsers](https://github.com/chainreactors/parsers) — Protocol & data parsers

---

<p align="center">
  <a href="https://star-history.com/#chainreactors/cyber-harness&Date">
    <img src="https://api.star-history.com/svg?repos=chainreactors/cyber-harness&type=Date" alt="Star History" width="600">
  </a>
</p>
