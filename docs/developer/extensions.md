# 扩展开发

[开发者指南](../development.md) · 下一篇：[会话与宿主集成](hosting.md)

第一个例子把 hello 工具加入应用。实际扩展通常还需要告诉模型怎样使用工具，复用已有命令，或管理一个长连接。本章沿着这些需求扩展同一个组合，先保持业务对象独立，再为它们建立明确的生命周期。

## 工具与业务代码

模型工具实现 `core/tool.Tool`。名称用于调用路由，描述说明适用范围，Definition 提供参数 schema，Execute 接受 context 与 JSON 参数并返回结构化结果。[hello 示例](../../examples/custom/main.go)展示了完整实现与注册。

描述和 schema 应使模型能判断何时调用、需要提供什么输入；它们不替代业务校验。涉及网络或长时间计算的实现必须响应 context。结果中给模型的文本应足以推进下一步；需要保留完整数据时使用结构化产物，避免把大量原始输出全部塞入对话。

使用 `tool.TextResult` 返回文本，`tool.Result.Details` 返回结构化数据；执行失败返回 Go error。构造参数显式传入工具所需的目录、客户端或业务服务，避免运行时查找全局注册表。

工具可以并行调用，因此共享字段需要自己的同步策略。扩展关闭时，注册表会处理该贡献的撤销与在途调用；工具背后的共享连接、缓存或后台线程仍由它们的所有者关闭。

## 复用命令能力

当功能本来就是一个带参数的命令，贡献 `tool.Command` 可以复用现有 CLI 知识。Agent 通过 bash 工具调用它，解释器把已注册命令交给进程内实现，其余命令按调用环境启动外部程序；组合语义见[执行链](../architecture.md#工具与命令的执行链)。

这种接入适合扫描器、查询和格式校验。它不需要额外生成同名可执行文件，也不必为每个 flag 再定义一个模型工具。若应用需要专门的结构化交互，再增加 Tool 表面，并让两种入口调用同一业务实现。

[OKF 扩展](../../exts/okf/extension.go)是一个小而完整的参考：Load 同时贡献命令、说明文档与提示词策略。阅读它时，可以沿着 `tools/okf` 的命令实现确认业务逻辑并没有依赖 Scope。

已有标准 MCP 服务时可使用 [MCP → CLI 扩展](../mcp.md)：Load 使用 mcp-go 连接服务、发现工具并贡献以服务别名命名的命令，如 `ida decompile --addr 0x401000`。Agent 从 bash 按需列举、查看工具帮助并使用 Schema 参数调用工具。

## 子 Agent 贡献

子任务定义使用 `agent/subagent.Subagent`，在组合根按依赖顺序安装：

```go
subagentext.New()       // 定义唯一的 Subagent 贡献点和执行接口
scannerext.New(...)    // 可选，贡献 verify / sniper
sessionext.New(...)    // 需要会话时安装
subagentext.NewTools() // 借用贡献点和 Runtime，提供模型调用工具
```

上面的包别名对应 `exts/subagent`、`exts/scanner` 和 `exts/session`。直接同步执行扫描子任务不需要 Session 或 NewTools；普通 Session 也可以不安装子 Agent。扩展通过 Scope 贡献定义：

```go
extension.Add(scope, subagent.Subagent{
    Name: "verify", Description: "Verify a finding",
    DefaultMode: subagent.Sync,
    Prepare: prepareVerify,
})
```

`Prepare` 返回本次配置快照与非空任务文本，不执行任务或创建需要另行关闭的资源；匿名调用跳过名称解析和 Prepare。消费者借用 `Executor`，同步 Execute 使用 `agent.RunTask`，会话工具使用 Start 接入 Session。运行时 Add 返回的 Handle 由贡献者关闭；重复名称拒绝，撤销隐藏定义、取消任务并排空准备、执行及通知，之后才允许同名注册。接口见 [agent/subagent](../../agent/subagent)，状态与租约见[运行机制](../architecture.md#subagent-委派)，模型调用方式见[子 Agent 使用说明](../user/web.md#子-agent)。

## 知识与提示词

面向最终使用者的可编辑知识适合放入 [Skill 文件](../user/knowledge.md)。随功能扩展分发的知识可贡献 `skills.Bundle`，由统一的 Store 提供虚拟路径和读取。工具说明因此可以跟着功能一起安装与移除，无需复制到多个系统提示词里。

需要调整系统行为时贡献 `prompt.Contribution`：为它指定稳定名称与目标，再在 Apply 中操作具名 section。例如 OKF 只向主 Agent 和扫描 Agent 加入 Markdown 产出策略。指定 target 能避免把同一要求无差别加入压缩器和评估器。

Prompt 在一次 Run 开始时解析。运行中改变贡献不会追溯修改已经发给模型的请求；一次 Apply 失败也不会提交半份修改。执行准入应由工具或 hooks 控制，提示词只提供行为指导。组装细节见[上下文与知识](../architecture.md#上下文与知识)。

## 共享服务与借用

扩展若拥有一个可复用服务，在 Load 中初始化它并用 `Provide[Service]` 发布；后面的消费者用 `Use[Service]` 借用。接口类型要与发布时一致。只让消费者拿到业务接口，资源本身的启动和关闭保留在所有者中。

组合顺序由宿主显式写出。一个读取服务的工具扩展排在服务扩展之后，会话和宿主消费者再排在工具之后。可选服务可以由组合根决定是否安装整组功能；需要稳定接口时也可以提供明确的禁用实现，如基础组合的 NoEgress。

不要把借来的工具执行器或 Skill Store 塞入全局状态供任何代码随取随用。显式借用让关闭关系可追踪，也让一个扩展的依赖在 Load 中可见。

## 配置与生命周期

只有构造配置的简单应用可以直接使用 Go 值。要把功能加入统一 CLI 和配置文件时，功能包提供 `Declare`，向宿主预先定义的 CLI、配置与连接测试贡献点提交声明。宿主解析后把确定的值传入构造函数；`Declare` 不负责启动服务。

基础配置的字段归属统一定义在 `config.Option`：`local:"true"` 表示本地设置，适用于身份、传输、数据目录和输出；没有配置键的进程参数也留在本地。文件加载与远端替换共用合并、环境覆盖和校验逻辑，远端替换不再逐项复制本地字段。扩展配置仍以各自的 `Section` 声明为准，通过 `Resolved` 提供已校验的值。文件层状态仅用于发现、编辑与诊断，不参与远端配置替换，也不承载运行服务。

Load 中的初始化使用 `scope.Init()`，持续后台工作使用 `scope.Lifetime()`。扩展若持有 goroutine、订阅或连接，还应实现 `Close(context.Context) error`：停止接收新工作，结束已有工作并释放资源。只贡献静态条目的 hello 扩展不需要空的 Close。

关闭 deadline 到达而资源仍在工作时，返回 `extension.ErrCloseIncomplete` 或可识别的 context 错误，以便 Set 保留依赖并重试。完整的撤销顺序和失败回滚见[扩展装配](../architecture.md#扩展装配与生命周期)。

## 验证扩展

先直接测试业务实现的参数、结果和取消，再通过实际组合验证注册与调用。共享资源和后台工作还需要覆盖部分加载失败、在途调用中的撤销、关闭超时后的重试；这些是单测普通 Execute 无法验证的行为。

```sh
go run ./examples/custom
go test ./core/resource ./core/extension ./core/registry ./core/tool
```

支持 CGO 与 race detector 的平台可进一步执行 `go test -race ./core/resource ./core/extension ./core/registry`。当扩展已能独立工作，下一步是在[宿主中打开会话](hosting.md)，让模型使用它。
