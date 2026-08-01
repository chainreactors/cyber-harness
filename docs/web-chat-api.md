# AIScan ConnectRPC Chat 接入手册

AIScan Chat 现在以 protobuf 为唯一接口模型，由 ConnectRPC 同时提供 Connect、
gRPC-Web 和原生 gRPC。这里没有额外的 JSON-RPC 2.0 envelope，也不需要维护一套
手写 REST DTO 或 SSE 事件协议。

旧的 `/api/chat/*` 与 `/api/scans/*` REST/SSE 已下线。Chat、会话管理和扫描都通过
同一套 protobuf + ConnectRPC 接口提供。

## 对外接口形态

外部接入者仍只需要理解 `aop.ChatService` 的六个方法：

| 方法 | 语义 | Connect/gRPC procedure |
| --- | --- | --- |
| `OpenSession` | 打开一个 Agent 会话 | `/aop.ChatService/OpenSession` |
| `RunTurn` | 提交一轮输入 | `/aop.ChatService/RunTurn` |
| `CancelTurn` | 按 `session_id + turn_id` 精确取消 | `/aop.ChatService/CancelTurn` |
| `CloseSession` | 关闭会话 | `/aop.ChatService/CloseSession` |
| `ListEvents` | 按 cursor 读取持久化历史 | `/aop.ChatService/ListEvents` |
| `WatchEvents` | 单向服务端流式监听事件 | `/aop.ChatService/WatchEvents` |

AIScan 产品自己的会话管理能力放在独立的
`aiscan.chat.SessionService`，不会污染通用 AOP Chat 语义：

- `ListSessions`、`GetSession`、`DeleteSession`
- `ResetSession`
- `ListCommands`、`ExecuteCommand`
- `UploadSessionFile`

其 procedure 前缀为 `/aiscan.chat.SessionService/`。

扫描能力位于 `aiscan.scan.ScanService`：

- `SubmitScan`、`GetScan`、`ListScans`、`CancelScan`
- `WatchScanEvents`（服务端单向流式）
- `GetScanReport`

外部 Go 调用方不需要分别初始化这些生成 client。稳定入口
`aop/aiscan.Client` 将它们统一暴露为 `Chat`、`Sessions`、`Scans` 三个 API group。
底层 group 仍保持独立的 protobuf service 边界，但共享同一个 HTTP client、base URL、
认证 interceptor 和 Connect 选项。

## 传输形态

```text
Browser / TypeScript
  createConnectTransport + generated client
                 │ Connect protobuf JSON（或 binary）
                 ▼
          AIScan HTTP handler
                 │ 同一 protobuf service implementation
       ┌─────────┼──────────┐
       │         │          │
    Connect   gRPC-Web   native gRPC
```

ConnectRPC 解决的是“同一 protobuf API 适配浏览器和 gRPC 客户端”，不是把 gRPC
转换成 JSON-RPC 2.0。浏览器默认使用标准 Protobuf JSON；grpc-go 使用 protobuf
binary，但两者的方法名、字段、错误码和流式终止语义完全相同。

服务端同时接受：

- Connect protocol（浏览器和普通 HTTP client）
- gRPC-Web
- 原生 gRPC（需要 HTTP/2）

Connect handler 最大 wire message 为 72 MiB（给 Protobuf JSON 的 bytes/base64 留出
空间），业务文件上传上限仍严格为 50 MiB。

## 独立 Go 工具完整接入

公共生成代码位于可被仓库外模块导入的路径：

```text
github.com/chainreactors/aiscan/aop
github.com/chainreactors/aiscan/aop/aiscan
```

不要引用 `aop/aiscan/transport`。`aop/aiscan/transport` 只服务 AIScan AgentTransport，
不属于外部 Chat SDK。

### 1. 启动 AIScan Web 和 Agent

```bash
aiscan web --addr 127.0.0.1:8080 --token dev-token
```

确认至少一个 Agent 已连接，然后取得它的 participant ID：

```bash
curl -H "Authorization: Bearer dev-token" \
  http://127.0.0.1:8080/api/agents
```

取返回数组中的 `id`，例如 `agent-1`。该值用于 `OpenSession.participant`。

### 2. 创建完全独立的 Go module

```bash
mkdir aiscan-connect-client
cd aiscan-connect-client
go mod init example.com/aiscan-connect-client
go get connectrpc.com/connect@v1.20.0
go get github.com/chainreactors/aiscan@latest
```

如果是在 AIScan 源码 checkout 内验证尚未发布的版本，可临时添加：

```go
replace github.com/chainreactors/aiscan => /absolute/path/to/aiscan
```

发布后的独立项目应删除 `replace` 并锁定明确的 AIScan tag/version。

业务代码只初始化一次根客户端：

```go
client := aiscan.NewClient(
    http.DefaultClient,
    "http://127.0.0.1:8080",
    connect.WithProtoJSON(),
)

// 通用对话协议
client.Chat.OpenSession(...)
client.Chat.WatchEvents(...)

// AIScan 会话管理
client.Sessions.ListSessions(...)

// AIScan 扫描
client.Scans.SubmitScan(...)
client.Scans.WatchScanEvents(...)
```

原生 gRPC 也使用相同分组形态，并复用一条 `grpc.ClientConnInterface`：

```go
client := aiscan.NewGRPCClient(conn)
client.Chat.RunTurn(...)
client.Sessions.GetSession(...)
client.Scans.GetScan(...)
```

### 3. 运行可复制的完整客户端

仓库提供了一个拥有自己 `go.mod` 的独立示例：

```bash
cd examples/external-go-client
go run . \
  -url http://127.0.0.1:8080 \
  -token dev-token \
  -agent '<agent-id>' \
  -prompt '请用一句话介绍你的能力'
```

这个程序通过公共的 `aop/aiscan.Client` 门面初始化一次，并使用 `client.Chat` 完整执行：

```text
OpenSession
  ├─ 并发建立 WatchEvents
  ├─ RunTurn 发送自然语言 Message
  ├─ 持续输出 message_delta
  ├─ 使用完整 message 作为可靠结果
  ├─ 收到 turn_ended 后结束
  └─ 断线时使用最后的 EventDelivery.cursor 自动重连
```

预期输出形态：

```text
我是 AIScan，可以协助分析安全目标。
stop=completed cursor=6 session=session-... turn=turn-...
```

实现文件：`examples/external-go-client/main.go`。

仓库的端到端回归会把该目录作为 `example.com/aiscan-external-client` 独立 module，
启动真实 HTTP Connect handler 后以子进程执行 `go run .`。因此它能捕获误用
`internal` 包、认证失败、procedure 不兼容和流式终止缺失等问题。

### 4. SDK 重新生成

修改 protobuf 后执行：

```bash
go generate ./proto
```

生成代码统一位于 `aop/`；AgentTransport 位于 `aop/aiscan/transport`，但它是服务端与
Agent 之间的内部运行时协议，不属于外部工具的公共业务 API。生成后必须同时运行独立
module 编译和端到端测试。

该入口同时生成 Go、Connect-Go 与前端 TypeScript 文件；前端依赖尚未安装时，先在
`web/frontend` 执行 `npm install`。

## TypeScript 接入

```ts
import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { ChatService, ScanService, SessionService } from '@cyber/aop'

const transport = createConnectTransport({
  baseUrl: window.location.origin,
  useBinaryFormat: false, // 标准 Protobuf JSON，便于浏览器调试
})

const aiscan = {
  chat: createClient(ChatService, transport),
  sessions: createClient(SessionService, transport),
  scans: createClient(ScanService, transport),
}
```

一次完整调用：

```ts
const sessionId = crypto.randomUUID()

const opened = await aiscan.chat.openSession({
  requestId: crypto.randomUUID(),
  sessionId,
  participant: agentId,
  title: 'demo',
})
if (opened.outcome.case !== 'accepted') throw new Error(opened.outcome.value.message)

let cursor = ''
const controller = new AbortController()

void (async () => {
  while (!controller.signal.aborted) {
    try {
      for await (const response of aiscan.chat.watchEvents(
        { sessionId, afterCursor: cursor },
        { signal: controller.signal },
      )) {
        const delivery = response.delivery
        if (!delivery?.event) continue
        cursor = delivery.cursor

        const event = delivery.event
        if (event.payload.case === 'messageDelta') {
          const delta = event.payload.value
          if (delta.value.case === 'text') console.log(delta.value.value)
        }
        if (event.payload.case === 'turnEnded') {
          console.log(event.payload.value.stopReason)
        }
      }
    } catch {
      // 使用最后确认的 delivery cursor 重连；服务端先订阅 live stream，
      // 再从 SQLite replay，因此重连窗口不会丢 durable event。
    }
  }
})()

const turnId = crypto.randomUUID()
const run = await aiscan.chat.runTurn({
  requestId: crypto.randomUUID(),
  sessionId,
  turnId,
  input: {
    id: crypto.randomUUID(),
    role: 'user',
    name: 'operator',
    content: [{ value: { case: 'text', value: { text: '你好' } } }],
  },
})
if (run.outcome.case !== 'accepted') throw new Error(run.outcome.value.message)
```

`WatchEvents` 是 Connect 的 server-streaming RPC。浏览器端表现为生成 client 提供的
异步迭代器，底层使用 HTTP response stream；不再使用 `EventSource`，也没有旧的
`event: aop` / `data:` 文本帧。

## grpc-go 接入

原生 gRPC 客户端继续使用同一个 `aop.ChatServiceClient`，无需迁移业务调用：

```go
import aop "github.com/chainreactors/aiscan/aop"

conn, err := grpc.NewClient(
    "127.0.0.1:8080",
    grpc.WithTransportCredentials(insecure.NewCredentials()),
)
if err != nil { /* handle */ }
defer conn.Close()

ctx := metadata.AppendToOutgoingContext(
    context.Background(),
    "authorization", "Bearer "+token,
)
client := aop.NewChatServiceClient(conn)

opened, err := client.OpenSession(ctx, &aop.OpenSessionRequest{
    RequestId: "open-1",
    SessionId: "demo",
    Participant: agentID,
})
```

仓库示例：

```bash
# 原生 grpc-go
go run ./examples/aop-chat -addr 127.0.0.1:8080 -token dev-token -agent '<agent-id>' -prompt '你好'

# 浏览器兼容的 Connect protobuf JSON
go run ./examples/web-chat -url http://127.0.0.1:8080 -token dev-token -agent '<agent-id>' -prompt '你好'

# 拥有独立 go.mod、只使用公共 SDK 的外部工具形态
cd examples/external-go-client
go run . -url http://127.0.0.1:8080 -token dev-token -agent '<agent-id>' -prompt '你好'
```

## Terminal WebSocket

浏览器 terminal WebSocket 与 AgentTransport 共用生成的
`aiscan.transport.TerminalFrame`。浏览器传输使用标准 ProtoJSON，AgentTransport 的
gRPC bidi 使用 protobuf binary，Agent WebSocket 使用相同 message 的 ProtoJSON。
`pkg/web/terminal` 是 `pty.Frame` 与生成类型之间唯一的 Go codec；浏览器不再发送一套
手写 snake_case terminal DTO。

## 单向流式与重连语义

原 SSE 的单向输出由 `WatchEvents` 完整替代：

1. client 提交 `session_id` 和可选 `after_cursor`。
2. server 先注册 live subscription，再读取 SQLite backlog。
3. 每个响应包含 `EventDelivery { cursor, event }`。
4. client 处理成功后保存 `cursor`。
5. 网络断开后以该 cursor 重建 `WatchEvents`。

`Event.seq` 是 AOP session 内的语义顺序；`EventDelivery.cursor` 是持久化位置。重连
只能使用 cursor，不能拿 `seq` 代替。

`message_delta` 是低延迟增量，允许在背压下丢弃；完整 `message`、`turn_ended` 和
生命周期事件是可靠结果。UI 应用完整 `message` 覆盖增量拼接结果，并以唯一的
`turn_ended` 结束本轮。

## 请求幂等与错误

`OpenSession`、`RunTurn`、`CancelTurn`、`CloseSession` 以及 AIScan 的变更类 RPC
都要求非空 `request_id`：

- 同一方法、同一请求体重试：返回 SQLite journal 中的原响应，不重复执行。
- 同一 ID 对应不同方法或请求体：返回 `ALREADY_EXISTS` rejection。
- 业务拒绝位于 response 的 `rejected` oneof；传输/认证故障使用 Connect/gRPC code。

浏览器认证可使用现有 HttpOnly 登录 cookie，也可发送：

```text
Authorization: Bearer dev-token
```

## 全链路验收

```bash
go generate ./proto
go test ./...

cd examples/external-go-client
GOWORK=off go test ./...

cd ../../web/frontend
npm run build
npx playwright test
```

## ResetSession（`/clear`）

前端 `/clear` 不再清空或覆盖原 session，而是调用原子的产品 RPC：

```text
ResetSession(old_session)
  ├─ 创建同 participant 的 clean session
  ├─ 关闭 old session，reason = "reset"
  └─ 返回 { previous, current }
```

旧 session 的消息和事件历史完整保留，只新增一次 `session_ended(reason=reset)`；新
session 只包含自己的 `session_started` 生命周期，不继承旧 turn/message。相同
`request_id` 重试不会重复创建或重复生命周期事件。

## 调试要点

- `UNAUTHENTICATED`：Bearer token/cookie 缺失或无效。
- `ALREADY_EXISTS`：`request_id` 被不同请求复用。
- `UNAVAILABLE`：participant 对应 Agent 未连接，或代理未正确转发 HTTP/2。
- accepted 后没有最终答案：继续读取 `WatchEvents`；`RunTurn` accepted 只代表接收。
- 重复事件：持久化并提交 delivery cursor。
- 取消错轮次：调用 `CancelTurn` 时必须同时传准确的 `session_id` 和 `turn_id`。
- `/api/chat/*` 返回 404：这是预期 cutover；改用生成的 Connect/gRPC client。

## 验证命令

```bash
# 独立 Go module 编译
cd examples/external-go-client && go test ./...

# 独立进程 → Connect HTTP → Hub → fake Agent → WatchEvents 全链路
go test ./pkg/web -run TestExternalGoModuleConnectClientEndToEnd -count=1

# AIScan Web：CRUD、真实 LLM round-trip、独立 Go client、断线 cursor replay
cd web/frontend
npx playwright test e2e/aiscan-web.spec.ts \
  --grep "Chat Session CRUD|Chat LLM round-trip|External Go Connect client|Connect stream reconnect"
```
