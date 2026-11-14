# 会话与宿主集成

[开发者指南](../development.md) · 前一篇：[扩展开发](extensions.md) · 深入：[Agent 运行时](../architecture.md#agent-运行时)

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

演示程序将启动 Provider 设为 Disabled，再通过扩展借用 `*provider.State`，调用其 `Set` 注入本地实现。实际应用通常在 `harness.BaseConfig.Provider` 中选择 `StartupRequired`，提供 `ProviderConfig` 的协议、端点、密钥和模型；删除演示 Provider 的注入，让基础扩展完成初始化。

也可以实现 `provider.Provider` 接入自己的后端；支持流式返回时再实现 `StreamingProvider`。框架上层使用统一的 AOP 消息，供应商的 wire format 留在适配器中。模型配置与重试语义见[上下文与知识](../architecture.md#provider-选择与容错)。

## 结果、事件与取消

Wait 返回最终结果与错误，结果还包含 Stop、用量和消息。自然结束、轮次耗尽、预算耗尽与取消有不同含义；界面应保留这些状态，不能仅凭有一段 Output 就展示为成功。

需要实时展示时，用 Runtime.Observe 订阅 AOP 事件。会话示例观察 TurnEnded；实际 UI 可以同时消费消息、工具与状态事件。观察回调应快速返回，将慢速持久化或网络发送交给自己管理的有界队列，并处理拥塞。订阅与队列需要跟随宿主关闭。

宿主可以取消传给 Run 的 context，也可以调用 `CancelSessionRun(sessionID, turnID)` 停止特定工作。TurnID 标识外部提交的一次工作，不是模型循环轮次。停止请求需要传播到执行层，等待结束后再更新终态。

示例用 Ctrl+C 取消请求，但关闭使用独立 context。生产宿主可以为关闭设置 deadline；若返回 ErrCloseIncomplete，保留 Set 并用新的 context 重试。直接用已经取消的请求 context 关闭，会使清理尚未完成就返回。

## 应用与会话的存活期

打开多个会话不需要重新加载同一套扩展。会话拥有对话和运行状态，工具环境可能由多个会话共享；并行会话写同一个文件或控制同一个浏览器时，隔离和冲突策略由应用决定。

对话结束时用 Runtime.CloseSession 释放会话；应用退出时关闭整个 Set，取消剩余工作并释放底层资源。需要重启后续接任务，应保存事件或历史，再重建会话；这不会恢复操作系统进程和已有网络连接。持久化边界见[事件与数据](../architecture.md#事件与数据)。

## 跨进程宿主

当界面与 Agent 不在同一个 Go 进程时，通过 AOP 接入。WebSocket 传输二进制 protobuf Envelope，stdio 传输逐行 ProtoJSON Envelope；Envelope 包装请求、响应或事件，并提供操作关联与取消。

stdio 的 Envelope 流与 CLI 的 Event JSONL 历史文件不同，不能互相替代。Web 环境还把会话执行与配置、历史查询分开：实时执行走 AOP，管理查询使用 ConnectRPC。建立连接、创建会话、提交与取消的具体调用见[外部接入教程](../integration.md)，字段见 [API 参考](../api.md)。

本地 subagent、IOA 和 Web Node 也有不同的状态归属。subagent 派生对话但可共享工具环境；IOA 在 Space 中交换消息；Web Node 提供远程执行位置。使用与部署见[Web 与协作](../user/web.md)，不能仅因它们都涉及多个 Agent 就使用同一种身份或恢复策略。

### inline 与 stdio Host

`pkg/host` 用同一个 `Host.Handle` 和连接级 `NamespaceMux` 处理 Envelope。宿主先加载 Profile 并绑定协议，再接收请求：

```go
mux := aop.NewNamespaceMux(ctx)
if err := profile.RegisterNamespaces(mux); err != nil {
    return err
}
h := host.New(mux)
defer h.Close()
// inline 调用：send 接收响应，也可被处理器异步调用。
err := h.Handle(request, send)
// stdio 宿主：Stdio 只负责逐行 ProtoJSON 编解码。
stream := host.NewStdio(input, output)
err = h.Serve(stream)
```

两种入口按宿主选择使用，完整 inline 例子见 [example_test.go](../../pkg/host/example_test.go)，子进程通信见 [process_test.go](../../pkg/host/process_test.go)。它们验证协议往返，不调用模型。

`Handle` 返回只表示分发返回，业务拥有者仍需等待自己的异步工作。`Serve` 遇正常 EOF 返回 nil，不关闭 Host；宿主随后排空业务、发出最后事件、撤销订阅，再 Close 并检查 `h.Err()`。Close 停止准入、取消通信并等待在途分发与写入，关闭本连接的 mux，不关闭借用的 Runtime 或输入输出流。首个写入错误取消 Host 并保留在 Err，后续发送不重试。

每次连接使用独立 mux，重连不能复用已关闭的实例。任意 Reader/Writer 无法仅凭 context 解除阻塞，流所有者需关闭流或设置 IO deadline。send 回调不能同步重入同一 Host 的 Handle、Send 或 Close；关闭由连接所有者从外部发起。Web 和 Node 使用 `pkg/aopconn.Connection`，接线见[连接与协议处理](#连接与协议处理)。

## 借用已安装能力

嵌入入口使用 `harness.New`，需要自定义顺序时使用 `harness.BaseExtensions` 配合具体功能 Extension。
不要在宿主中直接调用 Session Resource、Provider 初始化或 BashTool 构造方法。

加载后通过 `Runtime()` 运行会话，通过 `Providers()`、`Events()`、`Progress()`、`Processes()` 借用
需要的能力。取得的对象由 Profile 拥有；宿主仅关闭自身订阅、连接和 Profile。Console 持久 REPL
显式接收进程 Manager，不通过 Session 获取具体 BashTool。共享事件流的订阅和发布使用同一实例。


需要 Session 协议时，在命名空间注册表和 Session 之后安装 `sessionext.NewProtocol()`，由 Profile 统一绑定协议贡献。IOA 的查询与协作安装见[协作宿主](#协作宿主)。

Web 宿主使用 `webext.New(webext.Config{Database: path, ...})`。数据库、Service 和 AgentPool 由 Extension
创建并关闭；业务操作从 `Service()` 借用。HTTP 停止后关闭整个 Set，不能先关闭 Set 再继续使用路由快照。
可运行的完整实现见 [ACP Server](../../examples/acp/server/main.go)。


## 协作宿主

IOA 客户端由 `ioaclient.New(config)` 拥有连接和 Service，查询命令与连接测试仅安装该扩展即可。需要会话收信、派发记录和持续协作时，在连接之后安装 `ioaclient.NewCollaboration(options)`，借用同一个 Service 并安装 Agent hooks、消息订阅和所选 Skills；Session 随后安装。终端展示通过 `ioaclient.NewConsole(...)` 借用已有 Service。

协作扩展按真实会话身份维护路由，消息交给现有 Inbox 和会话准入。派发记录同步保存后才启动子任务，返回记录引用原派发消息；记录失败向调用端报告。协作消费者排空后才关闭连接。IOA Node ID 与 Web Node ID 各自归所属系统，配置见[协作连接配置](../configuration.md#协作连接配置)。

服务端安装 `ioaserver.New(config)`，浏览器桥接安装 `NewBrowser(config)`；扩展管理 Store、Service、认证与 HTTP/SSE handler，监听器和 HTTP Server 由宿主持有。独立 IOA 服务默认使用内存 SQLite，宿主决定是否使用持久数据库；应用 Profile 重载不关闭宿主持有的 IOA 服务。停止 HTTP 准入、取消并排空请求后，再关闭服务和 Store；排空超时保留资源供重试。

## 终端展示

终端宿主先安装 `tui.New()`，再安装 Session、IOA 等功能的 `NewConsole` 展示贡献。TUI 定义 `*console/api.Bindings` 贡献点并发布 `*console/api.Registry`；扩展贡献命令、补全和状态，Profile 加载完成后宿主取得组合的快照。加载 TUI 本身不会打开终端。命令或 alias 冲突会报错，贡献随其 Scope 撤销，命令在挂接终端时才绑定当前 Session。

三种命令表面各有入口：`pkg/cli` 处理进程参数，Command Registry 提供 bash 中的命令，Console Bindings 提供 REPL 展示。没有 TUI 时 Session 协议命令仍可使用；向 Console 传 nil bindings 不会自动安装 Session 命令。

| Console 入口 | 用途 |
| --- | --- |
| `AttachLocalREPL(ctx, rt, option, bindings)` | 使用本地终端，不把 readline 控制序列写入可重放 PTY 缓冲 |
| `StartPersistent(rt, manager, option, bindings)` | 借用 `core/proc.Manager` 创建持久 REPL；断线解除监视，重连复用终端 |
| `RunTask(...)` | 持有一次任务的展示与事件订阅，调用 Session/Run 并在会话结束后撤销订阅 |

Console 使用 Runtime 队列提交工作，正文和错误通过 AOP 事件展示；`Run.Wait()` 等待结束。`/stop` 和 Ctrl+C 取消当前终端提交的运行与排队输入，不取消同一会话中其他入口的工作。返回的 REPL 只持有自身取消与完成状态，不关闭借用的 Runtime 或 Manager。宿主先执行 `REPL.Close()`，排空终端工作并撤销展示，再关闭 Profile。入口示例见[最小 Agent](../../cmd/agent)。

## 公共配置入口

配置属于 cyber-harness，而非 cyber-scan 产品层。`pkg/config` 提供文件发现、分层合并、profile 选择、校验、最小模板及原子文件保存；`pkg/cli/configuration` 提供可接入宿主的 `init`、`config`、`doctor` 命令。

CLI 宿主在普通运行时解析前调用 `configuration.Run(ctx, args, configuration.Host{...})`，并通过 `RegisterHelp` 将命令加入帮助。Host 只提供名称、I/O、配置 Sections 和可选的检查回调；公共层不加载 agent、扫描器或 Web。`cmd/agent` 是最小接入示例，`cmd/cyber-scan` 额外贡献扫描和协作连接检查。

嵌入式宿主可直接调用 `config.ResolveRuntimeConfig`，通过 `Option.Context` 注入工作目录、用户目录、二进制路径和环境变量查询函数。`Context.Replacements` 只用于验证待保存的配置层，保留其他文件和运行时覆盖；不应把暂存文件当成新的 `-c`。构造和解析不会创建目录或启动资源。

配置文件未知的基础字段会报错。未由当前宿主注册的扩展保留在文件中，并报告不可用，不加载对应代码。宿主注册扩展的字段仍严格校验。Web 编辑沿用现有配置协议，保存仅修改选定文件中的编辑值。

## 通用 Web 托管

`pkg/web/host.Serve` 接收监听地址、静态文件、`webext.Config` 与可选 Extension，统一管理 HTTP 和 Web Extension 的生命周期。`cyber-harness` 使用这套机制作为独立 Hub，不加载产品 profile。

执行节点通过 `node.RunWebSocket(ctx, constructor, option, logger)` 接入。constructor 接收 `profile.Request` 并返回产品自己的 `profile.Profile`；scan、audit 和自定义节点共用协议，无需在 Hub 注册产品名单。需要本地节点的宿主可提供 `host.Config.StartNode`；产品路由或应用 profile 通过 `Management` 和 `Extensions` 显式贡献。

`host.FileConfigStore` 负责分层配置、密钥保留、暂存与原子提交。`SharedConfigCodec` 暴露公共配置；产品宿主可提供自己的 `ConfigCodec`。有效模型设置通过 `RuntimeLLM` 分发给节点，运行时凭据不会自动写回配置文件。

### 挂载 Web 能力

Hub 通过 `GET /api/manifest` 发布服务器能力与可连接的节点 profile。`cyber-harness` 安装 core Hub 和 IOA 浏览器能力；scan、audit 执行由连接的节点提供。Quick Connect 读取 manifest 中的 profile 描述，节点在 AOP 注册时报告自身能力，前端据此启用对应插件。

为 Web 增加具有存储或协议的功能时，在 `webext.Config.Capabilities` 中提供 [service.Capability](../../pkg/web/service/capability.go)：实现 manifest、schema modules、routes、namespace 注册和 Close。功能包持有自己的 protobuf、存储和处理器，Web Extension 负责调用安装与关闭接口。独立路由或现有 Extension 则通过 host 的 `Management`、`Extensions` 贡献；无需为每个功能另建 Hub。

配置声明可以独立于执行能力注册。通用 Hub 使用 `SharedConfigCodec` 暴露公共配置，产品宿主提供自己的 codec 和扩展声明；Hub 不因此安装节点的执行工具。文件中未声明的 section 保留在磁盘，由对应产品节点加载。可运行装配见 [cyber-harness 入口](../../cmd/cyber-harness/main.go)。

### 连接与协议处理

Application 与 Node 使用不同 endpoint：Application 接收会话请求，Node 首帧使用 `AgentHello` 注册。二者在初始化后共用 `pkg/aopconn.Connection` 管理单 reader、FIFO writer、context 和错误；业务状态分别归 Application dispatcher 与 AgentPool，连接机制不持有 Session、Turn 或执行任务。

功能 Extension 通过 `aop.Binding` 贡献顶层 namespace：Prototype 定义消息类型，Open 为每条连接创建 handler，共享 handler 使用 `aop.Shared`。Profile 的 `RegisterNamespaces` 将已安装贡献绑定到实例级 `NamespaceMux`；Mux 按 protobuf full name 分发，namespace 内部处理自己的消息。扩展拥有的协议不在宿主中重复注册，宿主自身的连接状态由其 dispatcher 管理。传输字段与 endpoint 契约见 [API 参考](../api.md)。
