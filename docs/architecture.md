# 架构概览

[文档首页](README.md) · 前置：[基本概念](concepts.md) · 开发：[构建应用](development.md)

cyber-harness 的核心结构是宿主、扩展组合和执行运行时。宿主接收输入并管理应用寿命；扩展组合提供运行需要的能力；运行时使用这些能力推进任务。安全扫描器、Web 和 IOA 位于这套结构之上，形成 aiscan 发行版。

## 应用结构

```mermaid
flowchart TD
    Host[CLI / Console / Web / 嵌入式宿主] --> Composition[发行版组合与生命周期]
    Composition --> Runtime[Agent 与 Session]
    Composition --> Capabilities[工具 / 命令 / 知识 / Provider]
    Runtime --> Capabilities
    Capabilities --> Business[扫描引擎 / 文件 / 终端 / 网络]
    Runtime --> Events[AOP 事实流]
    Business --> Events
    Events --> Presentation[展示 / 记录 / 传输]
```

`agent/` 提供模型循环和会话机制，`core/` 提供资源、hooks、操作关联和事件等基础设施。业务实现多位于 `tools/`，对应扩展在 `pkg/exts/` 中将实现接入资源与生命周期。

`pkg/base` 返回有序的基础扩展；`pkg/harness` 在此之上提供工具宿主和会话 Agent 的通用组合；`pkg/aiscan` 再加入扫描器、代理和其他产品能力；`cmd/aiscan` 解析配置并选择运行入口。最小 `cmd/agent` 使用同一基础组合，但不安装安全扫描、IOA 和 Web。应用功能由明确的组合决定。

## 装配与生命周期

宿主先选择扩展，构造一个线性的 `extension.Set`，再加载它。较早的扩展定义贡献点或发布共享能力，较晚的扩展贡献内容或借用能力。例如工具注册表接收多个工具实现，同时向 Session 提供统一的执行接口。

加载顺序显式写在组合代码中，关闭按相反顺序进行。扩展撤销自己的注册，排空自己持有的工作，最后释放对其他能力的借用。因此 Session 结束执行时，仍然可以使用尚未关闭的工具与事件设施。加载失败会回滚，关闭超时则保留未回收资源供重试。

[扩展组合](architecture/composition.md)详细说明贡献与借用、加载顺序、关闭阶段和能力所有权。这里的线性组合与扫描流水线的任务图分属不同层次。

## Agent 与 Session

Session 持有对话状态和输入队列，一次外部提交形成一次 Turn。Agent 的标准循环在执行过程中多次请求模型：准备上下文、调用 Provider、执行工具、追加结果，直到满足结束条件。目标评估位于循环外层，可以根据验收反馈再次执行。

后台命令、子 Agent 和协作消息通过 Inbox 回到运行时。活跃的后台生产者使循环能够等待尚未返回的结果；上下文增长则由压缩策略处理。Session、模型轮次和后台工作各自有明确的状态，避免将网络连接的寿命直接当作任务寿命。

具体推进、并发与收尾见 [Agent 运行时](architecture/runtime.md)，模型请求内容的构造见[上下文与知识](architecture/context.md)。

## 执行环境

模型调用经过 Tool Registry；其中 `bash` 将命令路由到 Command Registry 或外部 shell。内置函数、PTY 进程和管道进程统一纳入工作单元管理，但保留各自的能力差异。代理扩展发布共享出口，使内置工具和支持代理环境变量的外部程序使用相同的网络路由。

执行链上的 hooks 提供准入、结果处理与观察，operation 传递调用关联及取消信息。领域实现依赖这些明确的边界，不需要知道任务来自哪个界面。详细过程见[执行环境](architecture/execution.md)。

## 事件与数据

AOP Event 记录消息、工具调用、结果和生命周期等事实。终端、持久化输出和 Web 可以消费同一事实流，展示不必重新实现任务执行。hooks 用于执行中的控制，事件用于记录事实，二者具有不同的失败和调用语义。

扫描原生产物以 Artifact 形式保存。当前 Web 路径由 Go 归档原始事件，浏览器使用 CSTX WASM 生成规范化资产视图。CLI 的历史文件保存 Event，而跨连接传输使用 Envelope；恢复历史、消费实时事件和查询资产不是同一个操作。完整说明见[事件与持久化](architecture/data.md)。

## 宿主与协议

Go 宿主直接持有组合和 Session Runtime。跨进程宿主通过 AOP 发送请求、订阅事件和取消操作；Web 另有 ConnectRPC 管理面处理配置、历史及节点查询。IOA 则提供独立的消息协作空间。

宿主拥有监听和连接，运行时拥有执行状态，协议适配负责传输与错误映射。这些边界使本地 CLI、Web 和远程节点能够复用既有执行机制。接入实现见[宿主集成](developer/hosting.md)，线协议与迁移状态见[协议架构](protocol-architecture.md)。

## 源码阅读

从 [base.New](../pkg/base/base.go)、[aiscan 的能力选择](../pkg/aiscan/extensions.go)和 [Profile](../pkg/aiscan/profile.go)可以读到完整装配顺序。随后沿 [Session](../agent/session/session.go)进入 [StandardLoop](../agent/loop.go)，再跟进所调用的工具。各专题末尾提供对应实现与测试入口。
