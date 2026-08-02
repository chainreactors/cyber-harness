# 第三方语言接入 aiscan

本文档面向 Android/Kotlin、Java、Swift、Python、TypeScript 等非 Go 客户端，说明如何从 aiscan protobuf schema 生成代码，并接入两组外部 API。

| 功能组 | 传输 | 用途 |
|--------|------|------|
| Application WebSocket | 二进制 protobuf 长连接 | 创建会话、发送自然语言、接收流式回答、取消 Turn |
| ConnectRPC | protobuf unary RPC | 查询会话历史、扫描、配置、Agent、系统状态和 SCO |

详细字段与错误语义见 [api.md](api.md)。Go 开发者请直接阅读 [`examples/acp/README.md`](../examples/acp/README.md)。

## 1. 获取 protobuf schema

Application/AOP schema：

```text
web/frontend/cyber-ui/packages/aop/proto/aop/
```

ConnectRPC service 和 aiscan 类型：

```text
proto/rpc/
proto/types/
```

生成 ConnectRPC client 时需要同时配置两个 include root：

```text
-I web/frontend/cyber-ui/packages/aop/proto
-I proto
```

schema 的自动生成字段文档：

- [api/aop.md](api/aop.md)
- [api/rpc.md](api/rpc.md)

## 2. protobuf 代码生成

### 2.1 Application WebSocket

WebSocket 只需要 protobuf message classes，不需要生成 RPC service：

```bash
protoc \
  -I web/frontend/cyber-ui/packages/aop/proto \
  --<language>_out=<out-dir> \
  web/frontend/cyber-ui/packages/aop/proto/aop/*.proto
```

常见平台：

| 平台 | generator | runtime |
|------|-----------|---------|
| Android/Kotlin | `--java_out=lite:` | `protobuf-javalite` |
| Java | `--java_out:` | `protobuf-java` |
| Swift | `--swift_out:` | `SwiftProtobuf` |
| Python | `--python_out:` | `protobuf` |
| TypeScript | `protoc-gen-es` | `@bufbuild/protobuf` |

Android 示例：

```kotlin
plugins {
    id("com.google.protobuf") version "0.9.4"
}

protobuf {
    protoc { artifact = "com.google.protobuf:protoc:4.31.0" }
    generateProtoTasks {
        all().forEach { task ->
            task.builtins { create("java") { option("lite") } }
        }
    }
}

dependencies {
    implementation("com.google.protobuf:protobuf-javalite:4.31.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
}
```

将 `aop/*.proto` 保持原目录结构放入 `app/src/main/proto/`。

> 当前 AOP proto 没有设置 `java_package` 和 `java_multiple_files`，Java/Kotlin 默认会生成按文件嵌套的类。本文代码为突出协议流程使用简化类名；实际项目应按生成结果导入，或在 vendored schema 中增加自己的 Java options。

### 2.2 ConnectRPC

ConnectRPC 除 protobuf message generator 外，还需要对应语言的 Connect client generator。入口文件是：

```text
proto/rpc/*.proto
```

这些 service 会引用 `proto/types` 和 AOP messages，因此两个 include root 都必须存在。

推荐使用对应生态的官方 generator：

- Web/TypeScript：Connect-ES
- Android/Java/Kotlin：Connect Java/Kotlin
- Swift：Connect-Swift
- Python：支持 Connect 协议的生成器，或使用 gRPC client 访问同一 handler

server handler 同时支持 Connect、gRPC 和 gRPC-Web。具体 service 与 method 见 [api.md#connectrpc-api](api.md#connectrpc-api)。

## 3. Application WebSocket 接入

### 3.1 连接

```http
GET /api/aop/application/ws HTTP/1.1
Authorization: Bearer <access-key>
Upgrade: websocket
```

| 服务 URL | WebSocket URL |
|----------|---------------|
| `http://host:8080` | `ws://host:8080/api/aop/application/ws` |
| `https://host` | `wss://host/api/aop/application/ws` |

- 每个 WebSocket message 都必须是 binary frame。
- 每个 binary frame 只包含一个 protobuf `aop.Envelope`。
- 不要连接 `/api/aop/node/ws`，也不要发送 `AgentHello`。
- 鉴权失败时 WebSocket upgrade 返回 HTTP 401。

Kotlin/OkHttp：

```kotlin
val request = Request.Builder()
    .url("ws://127.0.0.1:8080/api/aop/application/ws")
    .header("Authorization", "Bearer $accessKey")
    .build()

val socket = okHttpClient.newWebSocket(request, listener)
```

Python/websocket-client：

```python
import websocket

ws = websocket.create_connection(
    "ws://127.0.0.1:8080/api/aop/application/ws",
    header=["Authorization: Bearer demo"],
)
```

### 3.2 Envelope 编解码

```proto
message Envelope {
  string id = 1;
  string reply_to = 2;
  string delivery_cursor = 3;
  google.protobuf.Any payload = 4;
}
```

请求时由 client 生成唯一 `id`。响应和事件通过 `reply_to` 指回原请求 ID。

Python 发送一个 core message：

```python
import uuid
from aop import envelope_pb2, protocol_pb2, chat_pb2

core = protocol_pb2.ProtocolMessage(
    open_session_request=chat_pb2.OpenSessionRequest(node_id="local")
)

env = envelope_pb2.Envelope(id=str(uuid.uuid4()))
env.payload.Pack(core)
ws.send_binary(env.SerializeToString())
```

Python 接收：

```python
raw = ws.recv()
env = envelope_pb2.Envelope.FromString(raw)

core = protocol_pb2.ProtocolMessage()
if env.payload.Unpack(core):
    message_type = core.WhichOneof("message")
    print(env.reply_to, message_type)
```

Kotlin 发送：

```kotlin
fun send(socket: WebSocket, id: String, message: ProtocolMessage) {
    val envelope = Envelope.newBuilder()
        .setId(id)
        .setPayload(Any.pack(message))
        .build()
    socket.send(ByteString.of(*envelope.toByteArray()))
}
```

client 至少维护：

```text
pending[request_id]       -> 等待一次响应
subscriptions[watch_id]  -> 持续接收事件
```

不要假设响应按发送顺序返回。事件也可能先于 `RunTurnResponse` 到达。

## 4. 最小会话流程

### 4.1 OpenSession

```text
OpenSessionRequest{
  node_id: "local"
}
```

等待相同 `reply_to` 的 `OpenSessionResponse`：

```text
accepted: Session{id,node_id,state,title}
rejected: Rejection{code,message,retryable}
```

保存 `accepted.id` 作为 `session_id`。

### 4.2 WatchEvents

在发送第一条用户输入前订阅：

```text
WatchEventsRequest{
  session_id: "<session-id>"
  after_cursor: ""
}
```

记录 WatchEvents 请求的 envelope `id`。服务端后续事件 envelope 的 `reply_to` 都等于该 watch ID。

WatchEvents 是长期订阅，不会返回一个独立的 `WatchEventsResponse`。

### 4.3 RunTurn

```text
RunTurnRequest{
  session_id: "<session-id>"
  input: Message{
    role: "user"
    content: [Content{text: TextContent{text: "你好"}}]
  }
}
```

`RunTurnResponse.accepted` 只返回 `TurnReceipt{session_id,turn_id,state:"running"}`。模型回答来自 WatchEvents。

Python 创建输入：

```python
from aop import chat_pb2, content_pb2, protocol_pb2

request = chat_pb2.RunTurnRequest(
    session_id=session_id,
    input=content_pb2.Message(
        role="user",
        content=[
            content_pb2.Content(
                text=content_pb2.TextContent(text="你好，请介绍一下自己")
            )
        ],
    ),
)

core = protocol_pb2.ProtocolMessage(run_turn_request=request)
```

## 5. 消费事件

收到 `ProtocolMessage.event` 后按 Event payload 分发：

| payload | client 行为 |
|---------|-------------|
| `message_delta` | 将 `text` 或 `reasoning` 增量追加到当前内容 |
| `message` | 使用完整消息作为最终权威内容 |
| `tool_call` | 展示工具名称和参数 |
| `tool_result` | 展示工具输出和错误状态 |
| `usage` | 更新 token 用量 |
| `error` | 展示业务错误 |
| `turn_ended` | 结束 loading，本轮完成 |

Python 分发示意：

```python
if core.WhichOneof("message") == "event":
    event = core.event
    event_type = event.WhichOneof("payload")

    if event_type == "message_delta" and event.message_delta.WhichOneof("value") == "text":
        print(event.message_delta.text, end="", flush=True)
    elif event_type == "turn_ended":
        print()
        turn_finished = True
```

一轮交互唯一稳定的终止信号是 `turn_ended`。不要使用 `RunTurnResponse`、某个完整 message 或 WebSocket 静默判断结束。

## 6. cursor、重连和取消

保存事件 envelope 中最近一个非空的 `delivery_cursor`。

重连后发送：

```text
WatchEventsRequest{
  session_id: "<session-id>"
  after_cursor: "<last-delivery-cursor>"
}
```

- `after_cursor` 是 exclusive cursor。
- `message_delta` 和 `tool_call_delta` 不持久化，因此 cursor 为空且不会重放。
- 完整 message、工具事件和 Turn 结束事件等会持久化并可重放。
- `delivery_cursor` 与 `Event.seq` 是不同概念，不能混用。

取消 Turn：

```text
CancelTurnRequest{
  session_id: "<session-id>"
  turn_id: "<turn-id>"
  reason: "user_requested"
}
```

取消 WatchEvents：

```text
CancelOperation{
  target_id: "<watch-envelope-id>"
}
```

## 7. ConnectRPC 接入

ConnectRPC 使用与 WebSocket 相同的 base URL 和 access key：

```text
Authorization: Bearer <access-key>
```

生成 client 后，以 `SessionService` 为例：

```text
ListSessions(ListSessionsRequest) -> ListSessionsResponse
GetSession(GetSessionRequest) -> GetSessionResponse
ListEvents(aop.ListEventsRequest) -> aop.ListEventsResponse
```

主要 procedure：

```text
/aiscan.rpc.chat.SessionService/ListSessions
/aiscan.rpc.chat.SessionService/ListEvents
/aiscan.rpc.scan.ScanService/ListScans
/aiscan.rpc.agent.AgentService/ListAgents
/aiscan.rpc.system.SystemService/GetStatus
```

第三方语言应通过生成的 Connect/gRPC client 调用，不需要手写这些 HTTP body。

ConnectRPC `ListEvents` 返回持久化的 `EventDelivery`，适合初始化历史页面或审计查询；实时回答仍通过 WebSocket `WatchEvents` 获取。

完整 service、request、response 与 Connect error 见 [api.md#connectrpc-api](api.md#connectrpc-api)。

## 8. 错误和幂等

WebSocket 有两层错误：

| 类型 | 说明 |
|------|------|
| `ProtocolMessage.protocol_error` | envelope、namespace、路由或服务执行错误 |
| `*Response.rejected` | 请求已解析，但参数或业务状态不允许执行 |

ConnectRPC 使用标准 Connect code，例如：

- `Unauthenticated`
- `InvalidArgument`
- `NotFound`
- `AlreadyExists`
- `FailedPrecondition`
- `Unavailable`

WebSocket envelope `id` 是请求幂等 ID：

- 相同 ID 和相同请求体重发，返回首次响应。
- 相同 ID 搭配不同请求体，返回冲突。
- 网络超时重试原请求时复用原 ID。

## 9. 接入检查清单

1. 根据业务选择 Application WebSocket 或 ConnectRPC。
2. 从原始 proto 生成本语言类型，不手写 wire DTO。
3. WebSocket 只发送 binary protobuf Envelope。
4. 使用唯一 envelope ID，并按 `reply_to` 关联响应。
5. OpenSession 后先 WatchEvents，再 RunTurn。
6. 用 delta 实时渲染，用完整 message 定稿。
7. 以 `turn_ended` 判断本轮完成。
8. 保存非空 `delivery_cursor`，重连后使用 `after_cursor`。
9. 不用 ConnectRPC `ListEvents` 轮询实时回答。
