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

cyber-harness 是面向网络安全的 Agent 运行框架。它把模型循环、工具执行、知识、观测和协作按同一套 Extension 生命周期组装；你可以使用现成发行版，也可以在 Go 应用里选择自己的能力组合。

`aiscan` 是参考发行版：Agent、核心扫描器、代理、Skills 和 IOA 协作；`aiscan-full` 进一步提供 Web 工作台、浏览器自动化、被动测绘和深度爬取。

> 请只在明确授权的目标上使用。

## 开始使用

从 [GitHub Releases](https://github.com/chainreactors/cyber-harness/releases/latest) 下载对应平台的 `aiscan` 或 `aiscan-full`，解压并加入 PATH。安装、PowerShell 配置、模型连接和首个任务的完整步骤见 [快速上手](docs/getting-started.md)。

```sh
# 已配置模型后：完成一次 Agent 任务，保存事件记录
aiscan agent -p "只读查看当前目录并解释文件结构" -o first-run.jsonl

# 对自己的本地靶场执行规则扫描，关闭 AI 验证
aiscan scan -i http://127.0.0.1:3000 --verify=off

# 完整版：启动 Web 工作台，使用启动时打印的 access key 登录
aiscan-full web
```

Agent 由模型决定下一步工具调用；`scan` 由规则和扫描事件驱动，可按需启用 AI 阶段。二者的关系见 [概念与执行全景](docs/concepts.md)。

## 模型配置

工作目录中的 `cyber.yaml`：

```yaml
llm:
  provider: openai
  base_url: https://api.deepseek.com/v1
  model: deepseek-chat
```

通过 `OPENAI_API_KEY` 提供密钥。Anthropic-compatible 服务使用 `provider: anthropic` 和相应配置。协议、profile、环境变量优先级见 [配置参考](docs/reference.md)。

## 文档

从[文档首页](docs/README.md)进入。正文描述当前源码，使用 release 时选择对应 Git tag。

[基本概念](docs/concepts.md)解释 harness、模型、工具、会话和知识的关系，不要求先了解 Go。

[使用者指南](docs/user/README.md)从安装和第一次任务开始，逐步介绍会话、工具、Skills、扫描与协作。

[开发者指南](docs/development.md)面向基于框架构建应用的人，从可运行的工具组合推进到会话嵌入、扩展开发和宿主接入。

[架构](docs/architecture.md)解释装配与生命周期、Agent 循环、执行环境、上下文及数据流。完整配置与命令单独放在[参考页](docs/reference.md)。

## 构建与嵌入

```sh
git clone --recurse-submodules https://github.com/chainreactors/cyber-harness.git
cd cyber-harness
make          # 标准发行版
make agent    # 最小本地 Agent
make full     # 前端 + 完整发行版
```

Go 版本见 [go.mod](go.mod)；full 还需要 Node.js/npm，standard 与 full 均使用 CGO_ENABLED=0。构建标签由 [editions.env](editions.env) 定义；原生录屏需要 CGO，另见 [record](docs/record.md)。

自定义发行版使用 `base.New(config)` 取得基础扩展，追加自己的扩展后交给 `extension.New`，由宿主持有 Load/Close；完整参考发行版使用 `pkg/aiscan.New`。可运行例子见 [examples/custom](examples/custom)，注册工具的最小示例见 [扩展开发](docs/development.md)。

## 贡献

先阅读 [扩展开发](docs/development.md) 与 [文档维护标准](docs/maintaining-docs.md)。提交 PR 时描述具体行为变化、影响的入口和验证结果；行为变化应同步更新对应教程或参考页。

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
