# AIScan 协议与传输架构

本文定义 AIScan 的目标协议职责，以及当前迁移边界。Runner 已使用统一 AOP WebSocket；ConnectRPC 承担产品管理与查询。浏览器单 AOP WebSocket 仍延期：当前保留 Chat/WatchEvents ConnectRPC 与独立 Terminal WebSocket，后续迁移不改变本文件定义的最终边界。

## 1. 唯一真相

跨进程、跨语言和跨前后端的数据类型只在 protobuf 中定义。业务代码可以拥有领域对象或 UI view model，但不得再定义与 protobuf 同构的 wire DTO，也不得在 AOP、Connect、REST 或 JSON-RPC 之间做同一语义的多次转换。

libcstx 独占安全事实模型：IP、Port、URL/Web、App、Framework、Vulnerability 等节点由 libcstx 定义。AIScan 只记录操作、会话和这些节点之间的关系，不再定义 Asset、Service、WebProbe、Framework Vulnerability 等平行事实类型。

## 2. 两个平面

| 平面 | 传输 | 职责 |
| --- | --- | --- |
| AOP 应用平面 | `AOPService.Connect` 双向 Envelope 流；浏览器兼容 WebSocket `/api/aop/ws` | Agent 会话、Turn、事件、工具、命令、file、exec、PTY、SCO 增量、取消和实时 scan 事件 |
| AIScan 管理平面 | ConnectRPC unary | 查询、配置、Agent 列表与本地进程生命周期、Session 历史、Scan CRUD、SCO 查询/导入、系统状态 |

目标态不存在 JSON-RPC、AOP ChatService、独立 Agent socket、独立 terminal socket 或额外的 WebSocket wire。AOP 只定义一个 `Connect(stream Envelope)` 双向流；Connect/gRPC 与浏览器 WebSocket 适配到同一个 `EnvelopeStream` 服务核心。当前 Agent 默认仍使用 WebSocket，新增 gRPC 服务端不改变旧 Agent 或浏览器连接。管理 RPC 与 AOP 流由同一个 Connect handler 注册，但职责仍按 service 分离。

该边界按“语义”而不是按“调用者”划分：Runner 只通过 WebSocket 接入 Web；浏览器的管理/历史查询走 ConnectRPC，但浏览器的实时 Session/Turn、命令、文件与 PTY 也走 WebSocket。Web 服务拥有 Agent Pool、调度、持久化和管理 RPC，节点只拥有自身 Runtime、工具与执行状态。

Agent 对外只需要一个 `--server-url` 作为 AIScan Web/AOP 基址；旧 `--web-url` 是同一字段的兼容别名。IOA 使用独立的 `--ioa-url`，但 Web 默认托管同源 IOA，因此 Web Agent 未指定 `--ioa-url` 时自动使用 `<server-url>/ioa`。

## 3. Namespace 所有权

### AOP

`cyber-ui/packages/aop/proto/aop` 定义跨产品的语义：

- `aop.ProtocolMessage`：Agent 注册与 Session/Turn 生命周期；
- `aop.Event`：message、tool、usage、status、error 和生命周期事件；
- `aop.file`、`aop.exec`、`aop.pty`、`aop.tool`、`aop.sco`：通用扩展协议。

这些扩展不是 AIScan DTO。PTY 和 file 对任何 AOP Agent 都成立，因此由 AOP 拥有。

### AIScan

`proto/types` 与 `proto/rpc` 只定义 AIScan 产品机制：

- `aiscan.command`：AIScan 命令目录、请求、结果与 receipt；
- `aiscan.scan`：Scan 状态、快照和实时事件；
- `aiscan.reload`：AIScan 配置热重载；
- `aiscan.agent/config/chat/sco/system`：Connect 管理服务及其返回类型。

AIScan 专有元数据通过 `google.protobuf.Any` 携带 namespace-owned message；protobuf full name / `Any.type_url` 是唯一类型身份，不得再增加 namespace 字符串或把 protobuf 编码成 JSON bytes。

### Cairn

Cairn 复用 `aop.Envelope`、AOP namespace 和同一条应用 WebSocket。只有 Cairn 自己拥有的产品语义才进入 Cairn namespace；不得在 AIScan 中创建 Cairn DTO、registry 或转发协议。

## 4. Envelope 语义

`aop.Envelope` 是唯一 framing 单元：

- `id`：本次 operation 的唯一标识，也是 request/reply correlation key；
- `reply_to`：响应或输出所对应的 request `id`；
- `payload`：`google.protobuf.Any`，type URL 决定 protobuf namespace；
- `delivery_cursor`：持久化订阅的位置，只用于恢复，不等同于 `Event.seq`。

请求 message 内不再重复 `request_id`。同步响应、流式输出和取消都围绕同一个 Envelope ID：

```text
request.id = op-1
reply.reply_to = op-1
stream item.reply_to = op-1, delivery_cursor = 42
CancelOperation.target_id = op-1
```

`Event.seq` 是 Session 内的事件语义顺序；`delivery_cursor` 是存储/投递位置。两者不能互换。

WebSocket 本身提供连续字节传输，但不提供业务 correlation、可恢复 cursor 或精确取消，因此 Envelope 仍然必要；`WatchEventsResponse` 之类再包装则没有必要，事件直接作为 reply stream item 发送。

## 5. 连接和并发

每个浏览器应用实例和每个 Runner 各自使用一条 AOP 应用流。浏览器继续使用 WebSocket；原生客户端可以使用 `AOPService.Connect` 的 Connect/gRPC 双向流。连接只有一个 reader；所有输出通过一个 FIFO writer。协议不引入优先级队列。

浏览器最终唯一连接所有者是 `@cyber/aop` 的 `AOPClient`。Terminal、Chat、Command、File 和 Scan watcher 将只提交 protobuf message，不创建 socket。该浏览器 cutover 当前延期，现有 Chat/WatchEvents ConnectRPC 与 Terminal WebSocket 暂时保留。

服务端第一帧若是 `AgentHello`，进入 Agent peer loop；其他支持的 request 进入 browser peer loop。顶层 namespace 消息（名称以 `ProtocolMessage` 结尾）通过应用实例拥有的 `NamespaceMux` 注册；namespace 内部 oneof 继续使用显式 type switch。Mux 不拥有 Connection、PendingOperations、Session 或 Turn 生命周期。

Go 传输边界只有：

```go
type EnvelopeStream interface {
    Recv() (*Envelope, error)
    Send(*Envelope) error
}
```

Context 由调用者显式传入，Stream 不拥有 Session、Turn 或 operation 状态。

## 6. Framing

- WebSocket：一条 binary message 对应一个 protobuf binary `Envelope`；
- stdio：一行 protobuf JSON 对应一个 `Envelope`。

两种 framing 进入相同的 Runtime protobuf loop。stdio 不是第二套协议，不存在 `ServerFrame/AgentFrame` 或 JSON DTO。

## 7. Agent 身份

`AgentHello.node_id` 是 Web 作用域内唯一的节点 ID。Pool、`Session.node_id`、PTY 路由和前端选择状态直接使用同一个值。

`server-url` 只决定节点连接到哪个 Web，不进入节点身份。IOA 使用独立的 `ioa-url` 和 IOA Node ID，不参与 Web 的 Chat/PTY 路由。

## 8. 类型与管理服务

- `aop/`：AOP core 与官方 `aop.*` 生成类型；
- `pkg/types/`：Agent、Runner、TUI、Web 共用的 AIScan protobuf message 与 typed extension helper，单一 Go 包且不依赖 Connect；
- `pkg/rpc/`：AIScan ConnectRPC service descriptor、client 和 handler，`.pb.go` 与 `.connect.go` 位于同一 Go 包；
- `pkg/web/api/`：协议无关的管理 API；直接接收/返回 protobuf message，不依赖 Connect、HTTP 或 WebSocket；
- `pkg/web/connect.go`：唯一生成 RPC 暴露适配器，注册管理服务与 `AOPService`，并映射认证和传输错误；
- `pkg/web/` 其余代码：AOP WebSocket、AgentPool、Runner 委派、Hub 与持久化基础设施；
- `cmd/gen/`：唯一 protobuf/TypeScript 生成入口。

非 `full` 构建不得依赖 `pkg/rpc` 或 `connectrpc.com/connect`。当前 Runner transport 对 `pkg/web/agent` 的依赖由独立迁移负责，不在本轮通过移动 RPC 类型解决。

Web 管理面暴露以下 unary 服务：

- `aiscan.rpc.system.SystemService`
- `aiscan.rpc.config.ConfigService`
- `aiscan.rpc.agent.AgentService`
- `aiscan.rpc.chat.SessionService`
- `aiscan.rpc.scan.ScanService`
- `aiscan.rpc.sco.SCOService`

AOP 应用面只额外暴露一个双向流服务：

- `aiscan.rpc.aop.AOPService/Connect`

生成流程只生成 protobuf 与 Connect-Go 代码，不生成 grpc-go service/client。Go 插件由 `go.mod` 的 `tool` 指令固定，统一入口为 `go run ./cmd/gen`（或 `make proto-gen`）；CI 会重新生成并要求零 diff。Connect-Go 的同一 handler 原生支持 Connect、gRPC 与 gRPC-Web；浏览器因双向流限制继续使用薄 WebSocket 适配，不存在第二套业务实现。REST `/api/*` 仅保留认证和 `/api/aop/ws`；未知管理 REST 返回 404。`/health` 和原生 `/ioa/` 不属于 AIScan RPC。

## 9. 持久化边界

- Session 和 Scan 以 protobuf 为存储真相；
- AOP 历史只存 `aop.Event` ProtoJSON；
- Scanner 文件输出只写 libcstx SCO JSONL；
- 不保留旧扁平 DTO 列、Record/Timeline 双写或 fallback read。

历史读取是纯查询，不派发 Agent frame、不收敛 operation，也不复制 terminal event。

## 10. 抽象预算

允许的协议/传输抽象只有 Go `EnvelopeStream`、实例级 `NamespaceMux` 和浏览器 `AOPClient`。其余逻辑使用具体 owner；顶层 namespace 由 Mux 注册，namespace 内部 oneof 使用显式 switch：

- Session/Turn 状态属于 Runtime/Service；
- Agent pending task 属于具体 `remoteAgent`；
- browser subscription 与 PTY route 属于该 browser peer loop；
- `pkg/web/api` 拥有 Web 原生管理语义；Agent/Session 执行通过能力接口委派给既有 AOP/AgentPool/Runner，不复制执行逻辑。
- Connect handler 只做 request wrapper、服务注册与错误映射。

新增抽象必须证明至少有两个真实 owner、不能由 protobuf message + 普通函数表达，并在本文补充职责和生命周期。允许的 namespace 注册抽象只做 full-name → handler 路由；不得扩展成全局 schema registry、通用 pending manager、link、wire 或兼容 adapter。

## 11. 实现位置与验收

- AOP schema：`web/frontend/cyber-ui/packages/aop/proto/aop`
- AIScan message schema：`proto/types`
- AIScan RPC schema：`proto/rpc`
- AIScan Go message：`pkg/types`
- AIScan Go RPC：`pkg/rpc`
- 生成入口：`cmd/gen`
- WebSocket endpoint：`pkg/web/aop_endpoint.go`
- browser peer：`pkg/web/aop_ws.go`
- Agent peer：`pkg/web/agent_stream.go`
- Runtime loop：`pkg/runner/runtime_protocol.go`
- stdio framing：`pkg/runner/stdio.go`
- Browser client：`web/frontend/cyber-ui/packages/aop/src/client.ts`
- Connect boundary：`pkg/web/connect.go`

完成态验收：全仓只能由 `AOPClient` 创建浏览器 WebSocket；不存在 ChatService、WatchEventsResponse、WatchScanEventsResponse、AgentTransport frame、terminal 专用 socket、手写 wire DTO 或 grpc-go service 生成物。
