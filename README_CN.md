<p align="center">
  <img src="web/assets/logo.svg" width="180" alt="cyber logo">
  <h1 align="center">cyber-harness</h1>
  <p align="center">面向网络安全的「一切皆扩展」agent harness</p>
</p>

<p align="center">
  <a href="https://github.com/chainreactors/cyber-harness/releases"><img src="https://img.shields.io/github/v/release/chainreactors/cyber-harness?style=flat-square&color=00E59B" alt="Release"></a>
  <a href="https://github.com/chainreactors/cyber-harness/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/chainreactors/cyber-harness/ci.yml?branch=master&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/chainreactors/cyber-harness/releases"><img src="https://img.shields.io/github/downloads/chainreactors/cyber-harness/total?style=flat-square&color=00B4D8" alt="Downloads"></a>
  <a href="https://github.com/chainreactors/cyber-harness/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue?style=flat-square" alt="AGPL-3.0"></a>
  <a href="https://github.com/chainreactors/cyber-harness/stargazers"><img src="https://img.shields.io/github/stars/chainreactors/cyber-harness?style=flat-square&color=yellow" alt="Stars"></a>
</p>

<p align="center">
  <a href="README.md">English</a>
</p>

---

cyber-harness 是一个「一切皆扩展」的 agent harness。

它的核心只是一个极小的 model-tool loop：请求模型 → 执行它要调用的工具 → 把结果追加回去 →
循环，直到任务完成。扫描器、浏览器、终端、代理、Web UI、多 Agent 协作——全都以同一种方式
构建，静态链接进同一个二进制。扩展这个 harness 的方式，是往现有组件旁边加一个新组件，
而不是去 patch 一个特权内核。

`aiscan` 是它的参考发行版——把 Agent、安全工具集、Skill、确定性扫描和 IOA 协作打包成一个
单文件可执行程序。

> **请只在明确授权的目标上使用，未经授权的使用属于违法行为。**

## 核心特性

- **一切皆扩展** —— 没有特权内核。扫描器、浏览器、终端、代理、Web UI、多 Agent 协作都按同一套模型组装，静态链接进同一个二进制；同一套框架也能嵌入你自己的应用。
- **极简的 agent 架构** —— loop 刻意做小；安全工具、评估、协作、工作流都放在核心之外。
- **单二进制发行** —— `aiscan` 打包核心安全工具集与 IOA 协作；`aiscan-full` 再叠加 Web UI、浏览器自动化、被动测绘与深度爬取。
- **多 Agent 协作** —— 通过 IOA 的共享消息空间与 worker 模式，把任务分发给分布式 agent，内置带 token 认证的 server。

## 运行

```bash
# 一次性 Agent 任务（带 LLM）
aiscan agent --base-url "https://api.deepseek.com" --api-key "sk-..." --model deepseek-chat \
  -p "扫描目标并检查高风险漏洞" -i 192.168.1.0/24

# 绕过 LLM，直接驱动扫描引擎
aiscan scan -i 192.168.1.0/24

# Web 控制台（完整版）
aiscan-full web
```

同一套扫描引擎也以确定性命令的形式暴露，用于自动化流程和没有 LLM 的环境；也可以作为 IOA
worker 参与多 Agent 协作：

```bash
aiscan scan -i http://target.example --mode full --deep --report
aiscan agent -p "扫描分配到的目标并上报发现" \
  --ioa-url http://127.0.0.1:8765 --space pentest-project
```

## 安装

从 [GitHub Releases](https://github.com/chainreactors/cyber-harness/releases/latest) 下载：

| 版本 | 说明 |
| --- | --- |
| **aiscan** | 开箱即用的 Agent，包含核心安全工具集与 IOA 协作能力 |
| **aiscan-full** | 额外包含 Web UI、浏览器自动化、被动测绘和深度爬取 |

| 系统 | 架构 | 标准版 | 完整版 |
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

### Web 控制台（完整版）

`aiscan-full web` 会启动浏览器界面和一个内嵌本地 Agent。访问
`http://127.0.0.1:8080`，输入终端中打印的 access key 即可。

```bash
aiscan-full web                                              # 本机，临时 access key
aiscan-full web --addr 0.0.0.0:8080 --token change-me        # 局域网，固定 access key
aiscan-full web --addr 0.0.0.0:8080 --token change-me --no-agent   # 仅作 Hub
```

会话、扫描、资产、发现和配置默认保存在 `cyber-web.db`；可用 `--db <path>` 指定其他 SQLite
数据库。

### 从源码构建

```bash
git clone https://github.com/chainreactors/cyber-harness.git && cd cyber-harness
make              # 标准版（aiscan）
make agent        # 最小本地 Agent（不含 scanner/search/proxy/web）
make full         # 前端 + 完整版（aiscan-full）
```

`make full` 需要 Node.js/npm 和可用的 CGO 工具链，会先构建前端，再把最新的 `web/static`
嵌入完整版二进制。原生 `record` 工具需单独用 `make record` 构建，详见
[record 文档](docs/record.md)。

## 内置能力

**Agent 能力**

- [`terminal`](docs/agent.md) —— 在真实伪终端里执行命令，支持交互输入与后台任务
- [`goal`](docs/agent.md#goal-evaluation) —— 目标评估：agent 在结束前对照目标检查自己的产出
- [`subagent`](docs/agent.md) —— 把复杂任务拆解给子 agent
- [`skills`](docs/agent.md) —— 可挂载的提示词、知识与可复用技能
- [`search`](docs/scan.md) —— 指纹、CVE 与网络情报检索
- [web_search / fetch](docs/agent.md) —— CVE 搜索和 URL 抓取
- [`tmux`](docs/agent.md) —— 后台任务会话，增量输出自动推送
- [`ioa`](docs/ioa.md) —— 通过共享消息空间与 worker 模式进行多 Agent 协作
- [`proxy`](docs/reference.md) —— 多协议代理链（trojan/vless/anytls/hy2/ss）
- [`arsenal`](docs/reference.md) —— 安全工具包管理器

**扫描器**
- gogo — 端口、服务、banner 发现
- spray — Web 探测、指纹识别、路径 fuzz
- zombie — 弱口令检测
- neutron — 模板化 POC 执行
- proton — 敏感信息扫描（API 密钥、令牌、凭证、密码）
- cyberhub — 指纹和 POC 关联查询

**浏览器 & 侦察**（完整版）
- playwright — headless Chromium 会话、截图、网络捕获
- katana — Web 爬虫（standard/headless/hybrid 引擎）
- passive — 网络空间搜索（FOFA、Hunter、Shodan）

可选 SDK 工具：`record` — 原生桌面/窗口截图和 H.264/MP4 录屏（Windows 与 Linux X11）。

## 配置

```bash
export OPENAI_API_KEY="sk-..."      # 环境变量
aiscan agent --provider openai --base-url https://api.deepseek.com/v1 --api-key sk-... --model deepseek-chat
```

配置文件 `cyber.yaml`：

```yaml
llm:
  provider: openai
  api_key: sk-...
  model: gpt-4o
  context_window: 128000   # 真实 Token 数，不要写 128K
  max_tokens: 16384
```

完整的 flag、Provider 与扫描器参数见 [docs/reference.md](docs/reference.md)。

## 嵌入

自定义发行版与官方二进制使用同一条公开组合路径：

```go
entries, err := base.New(config)
entries = append(entries, myExtensions...)
set, err := extension.New(entries...)
```

调用方负责 `set.Load` 和 `set.Close`。需要完整参考发行版时直接使用 `pkg/aiscan.New`，无需自行
选择能力包。可运行的最小发行版位于 [`examples/custom`](examples/custom)，生命周期和顺序规则见
[扩展架构](docs/architecture.md)。

## 文档

| 文档 | 说明 |
| --- | --- |
| [Agent Runtime](docs/agent.md) | 工具、Goal Evaluation、REPL、Session 与 Subagent |
| [Extension 架构](docs/architecture.md) | 类型化组合、生命周期、所有权 |
| [开发手册](docs/development.md) | 如何扩展这个 harness |
| [组合架构 RFC](docs/rfc-composition.md) | 公开组合边界与 Go API 迁移 |
| [安全扫描流水线](docs/scan.md) | 直接扫描、AI 增强与输出格式 |
| [IOA 协作](docs/ioa.md) | 多 Agent 消息空间、Worker 模式 |
| [AOP 集成](docs/integration.md) | Agent Transport 与 Host 集成 |
| [协议与传输架构](docs/protocol-architecture.md) | AOP WebSocket、Connect 管理平面 |
| [Record 工具](docs/record.md) | 桌面/窗口捕获、原生构建 |
| [参考手册](docs/reference.md) | 配置、Provider、全局参数、扫描器用法、FAQ |
| [v1.0.0 发布与迁移](docs/v1.0.0.md) | 稳定接口基线 |
| [Changelog](docs/changelog.md) | 版本变更记录 |

## 贡献

1. Fork 本仓库
2. 创建功能分支 (`git checkout -b feature/xxx`)
3. 提交更改 (`git commit -m 'feat: add xxx'`)
4. 推送分支 (`git push origin feature/xxx`)
5. 创建 Pull Request

## 免责声明

1. 本工具仅面向**合法授权**的企业安全建设行为及个人学习用途，如您需要测试本工具的可用性，请自行搭建靶机环境。
2. 在使用本工具进行检测时，您应确保该行为符合当地的法律法规，并且已经取得了足够的授权。**请勿对非授权目标进行扫描。**
3. 如您在使用本工具的过程中存在任何非法行为，您需自行承担相应后果，我们将不承担任何法律及连带责任。
4. 在安装并使用本工具前，请您**务必审慎阅读、充分理解各条款内容**，限制、免责条款或者其他涉及您重大权益的条款可能会以加粗、加下划线等形式提示您重点注意。
5. 除非您已充分阅读、完全理解并接受本协议所有条款，否则，请您不要安装并使用本工具。您的使用行为或者您以其他任何明示或者默示方式表示接受本协议的，即视为您已阅读并同意本协议的约束。

## 许可证

本项目使用 [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE) 许可。

## 链接

- [chainreactors](https://github.com/chainreactors) — 组织
- [IOA](https://github.com/chainreactors/ioa) — Internet of Agents 多 agent 协作协议
- [sdk](https://github.com/chainreactors/sdk) — 扫描器 SDK
- [proxyclient](https://github.com/chainreactors/proxyclient) — 多协议代理客户端
- [crtm](https://github.com/chainreactors/crtm) — 安全工具包注册中心
- [utils](https://github.com/chainreactors/utils) — 共享工具库 & PTY 管理器
- [parsers](https://github.com/chainreactors/parsers) — 协议和数据解析器

---

<p align="center">
  <a href="https://star-history.com/#chainreactors/cyber-harness&Date">
    <img src="https://api.star-history.com/svg?repos=chainreactors/cyber-harness&type=Date" alt="Star History" width="600">
  </a>
</p>
