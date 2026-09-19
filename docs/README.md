# cyber-harness 文档

cyber-harness 将模型、工具和运行环境组合为可以持续执行任务的 Agent。`aiscan` 是它的安全领域发行版，可以直接使用；开发者也可以复用框架，构建自己的应用。

## 基本概念

[基本概念](concepts.md)介绍 harness 的定位，以及模型、工具、会话、知识和扩展之间的关系。它是使用者与开发者的共同起点，不要求先了解 Go 或内部实现。

## 使用者指南

[使用者指南](user/README.md)介绍 aiscan 的日常使用。从安装与模型配置开始，逐步进入 Agent、会话、工具与知识、安全扫描，以及 Web 和协作。正文解释各项能力的行为，完整参数单独放在参考页。

首次使用可以直接进入[快速开始](getting-started.md)。已经运行过 aiscan 的读者可以从 [Agent](agent.md)或[会话与上下文](user/sessions.md)继续。

## 开发者指南

[开发者指南](development.md)面向基于 harness 构建应用的开发者。从一个可运行的 Go 组合开始，介绍扩展、工具、配置、知识、会话与宿主集成，再进入构建和分发。无需先阅读所有内部架构文档。

## 架构

[架构概览](architecture.md)解释系统如何组合和运行。随后按扩展装配、Agent 运行时、执行环境、上下文和数据流深入，说明各层的状态归属与生命周期。它为实现和修改提供依据，也为使用指南中的行为提供内部解释。

## 参考与维护

[配置与命令](reference.md)、[外部 API](api.md)及其[生成式字段参考](api/README.md)用于查阅；[协议架构](protocol-architecture.md)、[IOA](ioa.md)和[原生录屏](record.md)提供专题细节。

正文描述当前源码。使用 release 时选择对应 Git tag；升级时阅读 [v1 迁移](v1.0.0.md)与 [Changelog](changelog.md)。[Composition RFC](rfc-composition.md)保留设计历史，不作为当前开发教程。

文档正文以中文维护，[项目 README](../README.md)提供英文入口。参与维护请阅读[文档写作与验证](maintaining-docs.md)。
