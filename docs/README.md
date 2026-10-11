# cyber-harness 文档

cyber-harness 将模型、工具和运行环境组合为可以持续执行任务的 Agent。`aiscan` 提供扫描能力，`cyber-audit` 提供审计能力，完整发行版和独立 Web Hub 管理执行节点与会话；开发者可以选择所需扩展构建自己的应用。

本目录描述当前实现，按使用方法、集成契约和机制组织。每项行为由一个主章节维护，其他页面按需要链接。

## 使用者指南

从[基本概念](concepts.md)了解模型、工具、会话和发行版的关系，再按任务进入相应指南：

| 需要做什么 | 指南 |
| --- | --- |
| 安装并完成第一次运行 | [快速开始](getting-started.md) |
| 初始化模型、项目配置和协作连接 | [配置与初始化](configuration.md) |
| 提交任务、控制运行与评估结果 | [Agent](agent.md) |
| 连续对话、压缩、记录和恢复 | [会话与上下文](user/sessions.md) |
| 文件、命令、后台任务、代理与审批 | [工具](user/tools.md) |
| 选择与编写 Skill | [Skills 与知识](user/knowledge.md) |
| 执行扫描或审计 | [安全扫描](scan.md) · [cyber-audit](../cmd/audit/README.md) |
| Web、执行节点、子 Agent 与 IOA 通信 | [Web 与协作](user/web.md) |

## 开发者指南

[开发者指南](development.md)从可运行的 Go 工具组合推进到会话应用；[扩展开发](developer/extensions.md)说明工具、命令、知识与共享服务的贡献方式；[宿主集成](developer/hosting.md)说明生命周期及终端、Web、协议和协作接入。

已有 MCP 服务可通过 [MCP → CLI 扩展](mcp.md)自动成为 bash 命令，供模型按需发现和调用。

构建入口、产物和依赖见[源码构建](../README_CN.md#构建与嵌入)，选择所需能力见[自定义最小应用](../README_CN.md#自定义最小应用)。非 Go 客户端见[第三方语言集成](integration.md)，字段与错误语义见[API 参考](api.md)。

## 架构

[架构](architecture.md)沿应用结构介绍内部机制，各章节可直接索引：

| 机制 | 主章节 |
| --- | --- |
| Extension、资源贡献与借用、回滚和关闭 | [扩展装配](architecture.md#扩展装配与生命周期) |
| Agent 循环、Session、Inbox、子任务与取消 | [运行时](architecture.md#agent-运行时) |
| JEV 学习、编译、验证、复用与交接 | [JEV 与 Reflex](jev.md) |
| Tool、Command、进程、出口与工具准入 | [执行环境](architecture.md#执行环境) |
| Provider、Prompt、Skills、压缩与预算 | [上下文与知识](architecture.md#上下文与知识) |
| AOP 事件、记录、Artifact 与资产投影 | [事件与数据](architecture.md#事件与数据) |
| 宿主连接、namespace 与 Web 能力挂载 | [宿主集成](developer/hosting.md) |

## 构建、参考与维护

[内嵌 Arsenal 工具](arsenal-bundles.md)说明工具包的构建、离线释放和更新；[原生录屏](record.md)说明平台依赖与捕获。参数、默认值和环境变量见[参考手册](reference.md)，protobuf 字段文档可按[接入教程](integration.md#23-生成字段文档)生成。

测试说明分别见[仓库 harness](../cmd/harness/README.md)、[Web 前端](../web/frontend/e2e/README.md)和[审计测试](../cmd/audit/tests/README.md)。升级时阅读 [Changelog](changelog.md)；已发布版本的行为以对应 Git tag 为准。

文档正文以中文维护，[项目 README](../README.md)提供英文入口。参与维护请阅读[文档写作与验证](maintaining-docs.md)。
