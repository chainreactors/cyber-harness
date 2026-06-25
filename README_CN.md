<p align="center">
  <img src="assets/logo.svg" width="180" alt="aiscan logo">
  <h1 align="center">aiscan</h1>
  <p align="center">AI 驱动的面向实战的单文件渗透 agent，内置多引擎武器库开箱即用</p>
  <p align="center"><strong>Preview — 本项目处于早期预览阶段，API 和功能可能随版本变更</strong></p>
</p>

<p align="center">
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/v/release/chainreactors/aiscan?style=flat-square&color=00E59B" alt="Release"></a>
  <a href="https://github.com/chainreactors/aiscan/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/chainreactors/aiscan/ci.yml?branch=master&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/chainreactors/aiscan/releases"><img src="https://img.shields.io/github/downloads/chainreactors/aiscan/total?style=flat-square&color=00B4D8" alt="Downloads"></a>
  <a href="https://github.com/chainreactors/aiscan/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue?style=flat-square" alt="AGPL-3.0"></a>
  <a href="https://github.com/chainreactors/aiscan/stargazers"><img src="https://img.shields.io/github/stars/chainreactors/aiscan?style=flat-square&color=yellow" alt="Stars"></a>
</p>

<p align="center">
  <a href="README.md">English</a>
</p>

---

**aiscan** 融合 LLM agent 与传统安全扫描引擎。三种模式：**Scan**（确定性流水线扫描，AI 可选辅助）、**Agent**（自然语言驱动的自主安全评估）、**IOA**（多 agent 分布式协作）。

> **请只在明确授权的目标上使用，未经授权的使用属于违法行为。**

## 快速开始

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
| **aiscan** | 标准版 — scan/agent/gogo/spray/zombie/neutron/proton/arsenal |
| **aiscan-full** | 完整版 — 额外包含 playwright 浏览器、passive recon、katana 爬虫 |
| **aiscan-agent** | 轻量 agent 版 — 仅 agent 运行时，适合部署为远程 worker |

| 系统 | 架构 | 标准版 | 完整版 | Agent 版 |
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

### 从源码构建

```bash
git clone https://github.com/chainreactors/aiscan.git && cd aiscan

go build -o aiscan ./cmd/aiscan                          # 标准版
go build -tags full -o aiscan-full ./cmd/aiscan           # 完整版（含 playwright/katana/passive）
```

---

## Features

### 设计理念

- **单文件、零依赖** — 静态链接，开箱即用
- **极简 agent 内核** — 可组合的 ~160 行循环；工具、重试、评估均为插拔式，非硬编码
- **插件式架构** — 新增工具只需一个文件；重依赖（playwright、katana）编译期可选
- **内嵌 Skill** — 每个工具自带用法文档和战术指导，agent 按需加载
- **Scan + Agent 统一** — 同一套引擎驱动确定性流水线和自主 agent

### Scan — 确定性扫描流水线

- 多阶段自动串联：端口发现 → Web 探测 → 弱口令检测 → POC 检测，无需 LLM
- 可选 AI 驱动的结果验证、公开漏洞关联和动态测试
- quick 模式快速暴露面发现，full 模式深度爬取和扩展覆盖

### Agent — 自主安全评估

- 自然语言描述任务，agent 自主规划、扫描、分析、输出结论
- Goal Evaluation — 独立评估器判定任务完成度，自动驱动重试
- 交互式 REPL，支持直接执行命令
- 多 provider 容错降级

### [IOA](https://github.com/chainreactors/ioa) — 多 Agent 协作

- 共享消息空间实现分布式 agent 协调
- Worker 模式持续监听任务
- 内置 IOA server，支持 token 认证
- 参阅：[设计理念](https://github.com/chainreactors/ioa/blob/main/docs/design_zh.md) | [CLI 文档](https://github.com/chainreactors/ioa/blob/main/docs/cli_zh.md) | [扩展开发](https://github.com/chainreactors/ioa/blob/main/docs/extension_zh.md)

### 内置工具集

**扫描器**
- [gogo](https://github.com/chainreactors/gogo) — 端口、服务、banner 发现
- [spray](https://github.com/chainreactors/spray) — Web 探测、指纹识别、路径 fuzz
- [zombie](https://github.com/chainreactors/zombie) — 弱口令检测
- [neutron](https://github.com/chainreactors/neutron) — 模板化 POC 执行
- [proton](https://github.com/chainreactors/proton) — 敏感信息扫描（API 密钥、令牌、凭证、密码）
- [cyberhub](https://github.com/chainreactors/fingers) — 指纹和 POC 关联查询

**浏览器 & 侦察**（完整版）
- playwright — headless Chromium 会话、截图、网络捕获
- katana — Web 爬虫，支持 standard/headless/hybrid 引擎
- passive — 网络空间搜索（FOFA、Hunter、Shodan）

**辅助工具**
- tmux — 后台任务会话，增量输出自动推送
- arsenal — 安全工具包管理器（[crtm](https://github.com/chainreactors/crtm)），一键安装
- proxy — 多协议代理链（trojan/vless/anytls/hy2/ss）
- web_search / fetch — CVE 搜索和 URL 抓取

---

## 使用示例

### Scan 模式

```bash
aiscan scan -i 192.168.1.0/24                                    # 快速扫描
aiscan scan -i 192.168.1.0/24 --mode full                        # 完整扫描
aiscan scan -i http://target.example --verify=high --sniper       # AI 增强
aiscan scan -i http://target.example --mode full --deep --report  # 完整 + 深度 + 报告
```

### Agent 模式

```bash
# 一次性任务
aiscan agent -p "扫描目标，发现所有 Web 服务并检查高风险漏洞" -i 192.168.1.0/24

# 带 Goal Evaluation
aiscan agent -p "全面扫描目标" -i http://target.example -e "发现所有开放端口并输出服务指纹"

# 交互式 REPL
aiscan agent
```

### IOA 模式

```bash
# 启动 IOA Server
aiscan ioa serve --ioa-url http://0.0.0.0:8765

# 启动 IOA worker
aiscan agent --ioa-url http://127.0.0.1:8765 --space pentest-project \
  -p "scan assigned targets and report findings"
```

### LLM 配置

```bash
# 环境变量
export OPENAI_API_KEY="sk-..."

# CLI 参数
aiscan agent --provider deepseek --base-url https://api.deepseek.com --api-key sk-... --model deepseek-chat
```

配置文件 `aiscan.yaml`：

```yaml
llm:
  provider: openai
  api_key: sk-...
  model: gpt-4o
```

---

## 文档

| 文档 | 说明 |
| --- | --- |
| [Scan 模式详解](docs/scan.md) | 扫描流水线、AI 增强、输出格式 |
| [Agent 模式详解](docs/agent.md) | Agent 工具集、Goal Evaluation、REPL |
| [IOA 协作](docs/ioa.md) | 多 Agent 协作架构、Space/Node/Message 模型 |
| [参考手册](docs/reference.md) | 配置、LLM Provider、全局参数、扫描器用法、FAQ |
| [Changelog](docs/changelog.md) | 版本变更记录 |

## 贡献

欢迎提交 Issue 和 Pull Request。

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
- [gogo](https://github.com/chainreactors/gogo) — 端口和服务发现
- [spray](https://github.com/chainreactors/spray) — Web 探测和指纹识别
- [zombie](https://github.com/chainreactors/zombie) — 弱口令检测
- [neutron](https://github.com/chainreactors/neutron) — 模板化 POC 引擎
- [fingers](https://github.com/chainreactors/fingers) — 指纹规则引擎
- [sdk](https://github.com/chainreactors/sdk) — 扫描器 SDK（gogo/spray/zombie 核心）
- [proxyclient](https://github.com/chainreactors/proxyclient) — 多协议代理客户端
- [crtm](https://github.com/chainreactors/crtm) — 安全工具包注册中心
- [utils](https://github.com/chainreactors/utils) — 共享工具库 & PTY 管理器
- [parsers](https://github.com/chainreactors/parsers) — 协议和数据解析器

---

<p align="center">
  <a href="https://star-history.com/#chainreactors/aiscan&Date">
    <img src="https://api.star-history.com/svg?repos=chainreactors/aiscan&type=Date" alt="Star History" width="600">
  </a>
</p>
