# 扩展开发

[开发者指南](../development.md) · 下一篇：[会话与宿主集成](hosting.md)

第一个例子把 hello 工具加入应用。实际扩展通常还需要告诉模型怎样使用工具，复用已有命令，或管理一个长连接。本章沿着这些需求扩展同一个组合，先保持业务对象独立，再为它们建立明确的生命周期。

## 工具与业务代码

模型工具实现 `core/tool.Tool`。名称用于调用路由，描述说明适用范围，Definition 提供参数 schema，Execute 接受 context 与 JSON 参数并返回结构化结果。[hello 示例](../../examples/custom/main.go)展示了完整实现与注册。

描述和 schema 应使模型能判断何时调用、需要提供什么输入；它们不替代业务校验。涉及网络或长时间计算的实现必须响应 context。结果中给模型的文本应足以推进下一步；需要保留完整数据时使用结构化产物，避免把大量原始输出全部塞入对话。

工具可以并行调用，因此共享字段需要自己的同步策略。扩展关闭时，注册表会处理该贡献的撤销与在途调用；工具背后的共享连接、缓存或后台线程仍由它们的所有者关闭。

## 复用命令能力

当功能本来就是一个带参数的命令，贡献 `tool.Command` 可以复用现有 CLI 知识。Agent 通过 bash 工具调用它，终端路由把已注册命令交给进程内实现，其余命令交给宿主 shell。

这种接入适合扫描器、查询和格式校验。它不需要额外生成同名可执行文件，也不必为每个 flag 再定义一个模型工具。若应用需要专门的结构化交互，再增加 Tool 表面，并让两种入口调用同一业务实现。

[OKF 扩展](../../pkg/exts/okf/extension.go)是一个小而完整的参考：Load 同时贡献命令、说明文档与提示词策略。阅读它时，可以沿着 `tools/okf` 的命令实现确认业务逻辑并没有依赖 Scope。

## 知识与提示词

面向最终使用者的可编辑知识适合放入 [Skill 文件](../user/knowledge.md)。随功能扩展分发的知识可贡献 `skills.Bundle`，由统一的 Store 提供虚拟路径和读取。工具说明因此可以跟着功能一起安装与移除，无需复制到多个系统提示词里。

需要调整系统行为时贡献 `prompt.Contribution`：为它指定稳定名称与目标，再在 Apply 中操作具名 section。例如 OKF 只向主 Agent 和扫描 Agent 加入 Markdown 产出策略。指定 target 能避免把同一要求无差别加入压缩器和评估器。

Prompt 在一次 Run 开始时解析。运行中改变贡献不会追溯修改已经发给模型的请求；一次 Apply 失败也不会提交半份修改。执行准入应由工具或 hooks 控制，提示词只提供行为指导。组装细节见[上下文与知识](../architecture/context.md)。

## 共享服务与借用

扩展若拥有一个可复用服务，在 Load 中初始化它并用 `Provide[Service]` 发布；后面的消费者用 `Use[Service]` 借用。接口类型要与发布时一致。只让消费者拿到业务接口，资源本身的启动和关闭保留在所有者中。

组合顺序由宿主显式写出。一个读取服务的工具扩展排在服务扩展之后，会话和宿主消费者再排在工具之后。可选服务可以由组合根决定是否安装整组功能；需要稳定接口时也可以提供明确的禁用实现，如基础组合的 NoEgress。

不要把借来的工具执行器或 Skill Store 塞入全局状态供任何代码随取随用。显式借用让关闭关系可追踪，也让一个扩展的依赖在 Load 中可见。

## 配置与生命周期

只有构造配置的简单应用可以直接使用 Go 值。要把功能加入统一 CLI 和配置文件时，功能包提供 `Declare`，向宿主预先定义的 CLI、配置与连接测试贡献点提交声明。宿主解析后把确定的值传入构造函数；`Declare` 不负责启动服务。

Load 中的初始化使用 `scope.Init()`，持续后台工作使用 `scope.Lifetime()`。扩展若持有 goroutine、订阅或连接，还应实现 `Close(context.Context) error`：停止接收新工作，结束已有工作并释放资源。只贡献静态条目的 hello 扩展不需要空的 Close。

关闭 deadline 到达而资源仍在工作时，返回 `extension.ErrCloseIncomplete` 或可识别的 context 错误，以便 Set 保留依赖并重试。完整的撤销顺序和失败回滚见[扩展装配](../architecture/composition.md)。

## 验证扩展

先直接测试业务实现的参数、结果和取消，再通过实际组合验证注册与调用。共享资源和后台工作还需要覆盖部分加载失败、在途调用中的撤销、关闭超时后的重试；这些是单测普通 Execute 无法验证的行为。

```sh
go run ./examples/custom
go test ./core/resource ./core/extension ./core/registry ./core/tool
```

支持 CGO 与 race detector 的平台可进一步执行 `go test -race ./core/resource ./core/extension ./core/registry`。当扩展已能独立工作，下一步是在[宿主中打开会话](hosting.md)，让模型使用它。
