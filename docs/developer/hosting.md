# 会话与宿主集成

[开发者指南](../development.md) · 前一篇：[扩展开发](extensions.md) · 深入：[Agent 运行时](../architecture/runtime.md)

宿主把使用者输入交给会话，并把运行结果呈现给使用者。它可以是终端、Web 服务或你的 Go 应用。无论界面形式如何，都需要明确持有应用生命周期、会话身份、请求取消和事件订阅。

## 嵌入一个会话

从仓库根目录运行 `go run ./examples/session`。[完整源码](../../examples/session/main.go)沿用工具示例的基础组合，再加入 `loopext.New(agent.StandardLoop{})` 和 Session 扩展，最后用一个消费者借出 `*session.Runtime`。所有操作在 Set.Load 成功后开始。

Runtime 管理多个会话；OpenSession 创建保留历史的会话，Session.Run 提交一次输入，返回的 Run.Wait 等待本次工作结束。同一个 Session 上的后续 Run 会继续使用已有历史。

```go
session, err := runtime.OpenSession(ctx, agentsession.SessionOptions{ID: "main"})
if err != nil {
    return err
}
turn, err := session.Run(ctx, agentsession.RunInput{
    Content: []*aop.Content{aop.Text("解释当前目录")},
})
if err != nil {
    return err
}
result, err := turn.Wait()
```

代码片段位于已完成装配的宿主中，import、清理和错误处理以完整例子为准。例子的演示 Provider 只统计用户消息：第一次看到 1 条，第二次看到 2 条。它帮助验证历史连续性，而不要求模型密钥或产生外部调用。

## 接入实际模型

演示程序将启动 Provider 设为 Disabled，再通过 Runtime.SetProvider 注入本地实现。实际应用通常在 `harness.BaseConfig.Provider` 中选择 `StartupRequired`，提供 `ProviderConfig` 的协议、端点、密钥和模型；删除演示 Provider 的注入，让基础扩展完成初始化。

也可以实现 `provider.Provider` 接入自己的后端；支持流式返回时再实现 `StreamingProvider`。框架上层使用统一的 AOP 消息，供应商的 wire format 留在适配器中。模型配置与重试语义见[上下文与知识](../architecture/context.md#provider-选择与容错)。

## 结果、事件与取消

Wait 返回最终结果与错误，结果还包含 Stop、用量和消息。自然结束、轮次耗尽、预算耗尽与取消有不同含义；界面应保留这些状态，不能仅凭有一段 Output 就展示为成功。

需要实时展示时，用 Runtime.Observe 订阅 AOP 事件。会话示例观察 TurnEnded；实际 UI 可以同时消费消息、工具与状态事件。观察回调应快速返回，将慢速持久化或网络发送交给自己管理的有界队列，并处理拥塞。订阅与队列需要跟随宿主关闭。

宿主可以取消传给 Run 的 context，也可以调用 `CancelSessionRun(sessionID, turnID)` 停止特定工作。TurnID 标识外部提交的一次工作，不是模型循环轮次。停止请求需要传播到执行层，等待结束后再更新终态。

示例用 Ctrl+C 取消请求，但关闭使用独立 context。生产宿主可以为关闭设置 deadline；若返回 ErrCloseIncomplete，保留 Set 并用新的 context 重试。直接用已经取消的请求 context 关闭，会使清理尚未完成就返回。

## 应用与会话的存活期

打开多个会话不需要重新加载同一套扩展。会话拥有对话和运行状态，工具环境可能由多个会话共享；并行会话写同一个文件或控制同一个浏览器时，隔离和冲突策略由应用决定。

对话结束时用 Runtime.CloseSession 释放会话；应用退出时关闭整个 Set，取消剩余工作并释放底层资源。需要重启后续接任务，应保存事件或历史，再重建会话；这不会恢复操作系统进程和已有网络连接。持久化边界见[事件与数据](../architecture/data.md)。

## 跨进程宿主

当界面与 Agent 不在同一个 Go 进程时，通过 AOP 接入。WebSocket 传输二进制 protobuf Envelope，stdio 传输逐行 ProtoJSON Envelope；Envelope 包装请求、响应或事件，并提供操作关联与取消。

stdio 的 Envelope 流与 CLI 的 Event JSONL 历史文件不同，不能互相替代。Web 环境还把会话执行与配置、历史查询分开：实时执行走 AOP，管理查询使用 ConnectRPC。建立连接、创建会话、提交与取消的具体调用见[外部接入教程](../integration.md)，字段见 [API 参考](../api.md)。

本地 subagent、IOA 和 Web Node 也有不同的状态归属。subagent 派生对话但可共享工具环境；IOA 在 Space 中交换消息；Web Node 提供远程执行位置。使用与部署见[Web 与协作](../user/web.md)，不能仅因它们都涉及多个 Agent 就使用同一种身份或恢复策略。

## 借用已安装能力

嵌入入口使用 `harness.New`，需要自定义顺序时使用 `harness.BaseExtensions` 配合具体功能 Extension。
不要在宿主中直接调用 Session Resource、Provider 初始化或 BashTool 构造方法。

加载后通过 `Runtime()` 运行会话，通过 `Providers()`、`Events()`、`Progress()`、`Processes()` 借用
需要的能力。取得的对象由 Profile 拥有；宿主仅关闭自身订阅、连接和 Profile。Console 持久 REPL
显式接收进程 Manager，不通过 Session 获取具体 BashTool。共享事件流的订阅和发布使用同一实例。


需要 Session 协议时，在命名空间注册表和 Session 之后安装 `sessionext.NewProtocol()`；不要在宿主里
调用 `Runtime.NamespaceBindings()` 再手工贡献。IOA 查询只安装 `ioaclient.New(config)`；需要协作时再安装
`NewCollaboration`，展示通过 `NewConsole` 借用同一个已安装 Service。

Web 宿主使用 `webext.New(webext.Config{Database: path, ...})`。数据库、Service 和 AgentPool 由 Extension
创建并关闭；业务操作从 `Service()` 借用。HTTP 停止后关闭整个 Set，不能先关闭 Set 再继续使用路由快照。
可运行的完整实现见 [ACP Server](../../examples/acp/server/main.go)。


## 公共配置入口

配置属于 cyber-harness，而非 aiscan 产品层。`pkg/config` 提供文件发现、分层合并、profile 选择、校验、最小模板及原子文件保存；`pkg/cli/configuration` 提供可接入宿主的 `init`、`config`、`doctor` 命令。

CLI 宿主在普通运行时解析前调用 `configuration.Run(ctx, args, configuration.Host{...})`，并通过 `RegisterHelp` 将命令加入帮助。Host 只提供名称、I/O、配置 Sections 和可选的检查回调；公共层不加载 agent、扫描器或 Web。`cmd/agent` 是最小接入示例，`cmd/aiscan` 额外贡献扫描和协作连接检查。

嵌入式宿主可直接调用 `config.ResolveRuntimeConfig`，通过 `Option.Context` 注入工作目录、用户目录、二进制路径和环境变量查询函数。`Context.Replacements` 只用于验证待保存的配置层，保留其他文件和运行时覆盖；不应把暂存文件当成新的 `-c`。构造和解析不会创建目录或启动资源。

配置文件未知的基础字段会报错。未由当前宿主注册的扩展保留在文件中，并报告不可用，不加载对应代码。宿主注册扩展的字段仍严格校验。Web 编辑沿用现有配置协议，保存仅修改选定文件中的编辑值。
