# aiscan 外部接入 API

本文档描述外部程序集成 aiscan 时使用的两组 API。

| 功能组 | 传输 | 语义 |
|--------|------|------|
| Application WebSocket | 双向、长连接、二进制 protobuf | Session/Turn 生命周期和实时事件流 |
| ConnectRPC | unary 请求/响应 | 会话历史、扫描、配置、Agent、系统状态和 SCO 管理 |

第三方语言的 protobuf 生成和接入流程见 [integration.md](integration.md)。Go 可运行示例见 [`examples/acp/README.md`](../examples/acp/README.md)。字段级自动生成文档见 [api/aop.md](api/aop.md) 和 [api/rpc.md](api/rpc.md)。

## 功能边界

实时对话必须使用 Application WebSocket：

- 创建或打开 session
- 发送自然语言输入
- 接收 `message_delta`、工具事件和 `turn_ended`
- 取消正在运行的 Turn
- 持续订阅及断线续传

管理查询使用 ConnectRPC：

- 查询 session 列表和持久化历史
- 重置或删除 session
- 提交、查询和取消扫描
- 查询或更新配置
- 查询 Agent、系统状态和 SCO 数据

`SessionService/ListEvents` 只返回已持久化历史，不替代 WebSocket `WatchEvents`。

## Application WebSocket API

### 1. Endpoint 和鉴权

```http
GET /api/aop/application/ws HTTP/1.1
Authorization: Bearer <access-key>
Upgrade: websocket
```

| 服务地址 | WebSocket 地址 |
|----------|----------------|
| `http://host:8080` | `ws://host:8080/api/aop/application/ws` |
| `https://host` | `wss://host/api/aop/application/ws` |

- 外部 client 使用 Bearer token。
- 浏览器登录后也可以使用 `aiscan_session` cookie。
- 鉴权失败时 upgrade 返回 HTTP 401。
- `aiscan web` 未指定 `--token` 时会自动生成 access key，而不是关闭鉴权。
- Application Endpoint 不需要握手消息。首个 envelope 如果包含 `AgentHello`，会返回 `WRONG_ENDPOINT`。

每个 WebSocket message 必须是 BinaryMessage，内容为一个序列化的 `aop.Envelope`。文本 JSON frame 不属于 aiscan Application WebSocket wire format。

### 2. Envelope

```proto
message Envelope {
  string id = 1;
  string reply_to = 2;
  string delivery_cursor = 3;
  google.protobuf.Any payload = 4;
}
```

| 字段 | 发送请求 | 接收响应/事件 |
|------|----------|---------------|
| `id` | client 生成的唯一请求 ID | server 生成的消息 ID |
| `reply_to` | 通常为空 | 原请求 envelope 的 `id` |
| `delivery_cursor` | 空 | 持久化事件的恢复 cursor；瞬时 delta 为空 |
| `payload` | `Any<aop.ProtocolMessage>` | 对应 namespace 的 protobuf 消息 |

Go 中使用：

```go
envelope, err := aop.Wrap(id, "", message)
message, err := aop.Unwrap(envelope)
```

其他语言使用 protobuf `Any.pack` / `unpack`。不要自行添加 namespace 字段；消息类型由 `Any.type_url` 决定。

### 3. 请求关联和并发

client 至少维护两张表：

```text
pending[requestEnvelopeID]      -> 单次响应等待者
subscriptions[watchEnvelopeID] -> 长期事件消费者
```

接收 envelope 时：

1. 使用 `reply_to` 查找订阅。
2. 如果 payload 是 `ProtocolMessage.event`，交给订阅消费者。
3. 否则使用 `reply_to` 完成对应 pending request。

事件和 `RunTurnResponse` 可能并发到达，不能假设回执一定先于事件。

真实实现可参考 [`examples/acp/client/client.go`](../examples/acp/client/client.go)：

- `Dial`：转换 ws/wss URL，设置 Bearer header，启动 `readLoop`
- `readLoop`：解码 Envelope，通过 `reply_to` 分发响应和事件
- `call`：创建 envelope ID，发送请求并等待单次响应
- `Watch`：以 watch envelope ID 建立长期事件 channel

### 4. OpenSession

#### OpenSessionRequest

| 字段 | 必填 | 说明 |
|------|------|------|
| `session_id` | 否 | client 指定的 session ID；空时由 server 生成 |
| `node_id` | 是 | 在线且能够处理 chat 的 agent node ID |
| `title` | 否 | session 标题 |
| `parent_session_id` | 否 | 子会话的父 session |
| `parent_tool_call_id` | 否 | 创建子会话的 tool call |
| `extensions` | 否 | namespace 自有的 protobuf `Any` 扩展 |

最小请求：

```text
OpenSessionRequest{node_id: "local"}
```

#### OpenSessionResponse

```text
oneof outcome:
  accepted: Session{id,state,node_id,title}
  rejected: Rejection{code,message,retryable}
```

常见 rejection：

| code | 原因 |
|------|------|
| `INVALID_ARGUMENT` | 缺少 `node_id` |
| `UNAVAILABLE` | node 不在线或无法打开 chat session |
| `ALREADY_EXISTS` | 指定的 session ID 已绑定到其他 node，或 envelope ID 冲突 |
| `NOT_FOUND` | 扩展引用的资源不存在 |

成功后必须使用 `accepted.id`，不能假设它等于请求中的 `session_id`。

真实示例：`Client.OpenSession(ctx, nodeID, title)`。

### 5. WatchEvents

#### WatchEventsRequest

| 字段 | 必填 | 说明 |
|------|------|------|
| `session_id` | 是 | 要订阅的 session |
| `after_cursor` | 否 | exclusive cursor；空表示从当前可用历史开始重放 |

WatchEvents 没有单独的 response message。服务端持续发送：

```text
Envelope{
  reply_to: "<watch-envelope-id>"
  delivery_cursor: "<cursor-or-empty>"
  payload: Any<ProtocolMessage{event: Event{...}}>
}
```

推荐顺序：OpenSession 成功后先 WatchEvents，再 RunTurn。这样可以收到用户消息和最早的瞬时 delta。

真实示例：`Client.Watch(sessionID, afterCursor)`。

当前示例 client 演示最小实时订阅；生产 client 还应保留收到的非空 `delivery_cursor`，并在重连后重新建立 WatchEvents。

### 6. RunTurn

#### RunTurnRequest

| 字段 | 必填 | 说明 |
|------|------|------|
| `session_id` | 是 | OpenSession 返回的 session ID |
| `turn_id` | 否 | client 指定的 Turn ID；空时由 server 生成 |
| `input` | 通常是 | 用户 `Message`；`continue_session=true` 时允许没有内容 |
| `continue_session` | 否 | 继续已有 agent 上下文，不发布新的用户消息 |
| `max_turns` | 否 | 本次执行允许的最大内部 Turn 数 |
| `extensions` | 否 | AIScan 或其他 namespace 的请求扩展 |

普通自然语言输入：

```text
RunTurnRequest{
  session_id: "session-1"
  input: Message{
    role: "user"
    content: [Content{text: TextContent{text: "列出当前目录"}}]
  }
}
```

#### RunTurnResponse

```text
oneof outcome:
  accepted: TurnReceipt{session_id,turn_id,state:"running"}
  rejected: Rejection{code,message,retryable}
```

该响应只表示 server 已接受并拥有本次操作。回答、工具调用、usage 和最终状态全部通过 WatchEvents 发送。

常见 rejection：

| code | 原因 |
|------|------|
| `INVALID_ARGUMENT` | 缺少 session 或输入内容 |
| `NOT_FOUND` | session 不存在 |
| `UNAVAILABLE` | session 绑定的 node 已离线 |
| `ALREADY_EXISTS` | envelope ID 与另一请求冲突 |

真实示例：`Client.RunTurn(ctx, sessionID, text)`。

### 7. CancelTurn 和 CloseSession

取消正在运行的 Turn：

```text
CancelTurnRequest{
  session_id: "session-1"
  turn_id: "turn-1"
  reason: "user_requested"
}
```

响应为 `CancelTurnResponse`，accepted 中仍使用 `TurnReceipt`。被接受的 Turn 最终仍应收敛到 `turn_ended`，通常带有 canceled stop reason。

关闭 session：

```text
CloseSessionRequest{
  session_id: "session-1"
  reason: "completed"
}
```

响应为 `CloseSessionResponse`，accepted 中返回最终 `Session`。

### 8. CancelOperation

`CancelOperation` 取消 envelope ID 标识的长操作。终止 WatchEvents：

```text
CancelOperation{
  target_id: "<watch-envelope-id>"
  reason: "client_closed"
}
```

断开 watcher 不会自动取消其观察的 session 或 Turn。

### 9. Event

Event 公共字段：

| 字段 | 说明 |
|------|------|
| `id` | Event ID |
| `emitted_at` | 产生时间 |
| `session_id` | 所属 session |
| `turn_id` | 所属 Turn；会话级事件可以为空 |
| `emitter` | 事件来源 |
| `seq` | session 内严格递增的事件序号 |
| `extensions` | 附加元数据 |

`Event.seq` 是业务事件顺序，`delivery_cursor` 是持久化位置；二者不能互换。

#### Event payload

| payload | 关键字段 | 说明 |
|---------|----------|------|
| `session_started` | `model`, parent IDs | session 已启动 |
| `session_ended` | `reason` | session 已结束 |
| `turn_started` | — | Turn 已启动 |
| `turn_ended` | `stop_reason`, `error`, `usage`, `context_tokens` | Turn 的唯一终止事件 |
| `message` | `Message` | 完整权威消息 |
| `message_delta` | `message_id`, `content_index`, `operation`, value | 实时消息增量 |
| `tool_call` | `id`, `name`, `arguments` | 完整工具调用 |
| `tool_call_delta` | `call_id`, `index`, `name`, `arguments` | 工具参数增量 |
| `tool_result` | `call_id`, `output`, `is_error`, `duration_ms` | 工具最终结果 |
| `usage` | input/output/total tokens | 用量更新 |
| `error` | `code`, `message`, `retryable` | 非终止或附加业务错误 |
| `status` | `state` | 运行状态 |
| `provider_frame` | provider 原始 frame | 仅在启用相关策略时出现 |
| `extension` | `Any` | 产品自定义主 payload |

#### MessageDelta

`operation` 使用 `START`、`APPEND`、`REPLACE`、`END`。value 可以是：

- `text`
- `reasoning`
- `refusal`
- `data`
- `tool_arguments`
- 完整 `Content`

常见文本流使用 `APPEND + text`。client 不应假设每个 provider 都只产生 text。

#### Message 和 Content

`Message.role` 常见值：`system`、`user`、`assistant`、`tool`。

`Content` oneof：

| 类型 | 用途 |
|------|------|
| `text` | 普通文本及 annotations |
| `reasoning` | reasoning 文本 |
| `refusal` | 模型拒绝信息 |
| `media` | 图片、音频等资源 |
| `tool_call` | 内嵌工具调用 |
| `tool_result` | 内嵌工具结果 |

完整 `message` 是最终权威内容。delta 只用于实时投影。

### 10. 持久化和断线续传

- `message_delta` 和 `tool_call_delta` 不写入 SQLite，`delivery_cursor` 为空。
- 其他完整 Event 会进入 session 历史并获得 cursor。
- `WatchEvents.after_cursor` 是 exclusive cursor。
- 重连时使用最近收到的非空 `delivery_cursor`，而不是 `Event.seq`。
- 迟到或重连 watcher 会重放完整消息和其他持久化事件，但不会重放 delta。

标准恢复流程：

```text
disconnect
  -> reconnect websocket
  -> WatchEvents{session_id, after_cursor:lastCursor}
  -> replay events after lastCursor
  -> continue live events
```

### 11. 错误和幂等

两层业务结果：

| 类型 | 何时使用 |
|------|----------|
| `ProtocolMessage.protocol_error` | envelope 解码、namespace、路由或无法形成正常 response 的执行错误 |
| `*Response.rejected` | 请求已被解析，但参数、状态或策略不允许执行 |

client 收到 `ProtocolError` 时仍通过 envelope `reply_to` 查找原请求或订阅。

Envelope `id` 同时是幂等 ID：

- 相同 `id`、相同请求体重发：返回首次响应，不创建第二个逻辑操作。
- 相同 `id`、不同请求体：返回冲突。
- 网络超时后重试原请求：复用原 `id`。
- 新的用户操作：生成新的 `id`。

### 12. Go WebSocket 示例的真实调用链

[`examples/acp/client/main.go`](../examples/acp/client/main.go) 的核心调用顺序：

```go
client, err := Dial(ctx, serverURL, "", token)
session, err := client.OpenSession(ctx, nodeID, title)
events, err := client.Watch(session.GetId(), "")
receipt, err := client.RunTurn(ctx, session.GetId(), prompt)

for event := range events {
    if printEvent(event) {
        break
    }
}
```

其中：

- `Dial` 默认路径为 `/api/aop/application/ws`
- `OpenSession` 检查 accepted/rejected outcome
- `Watch` 使用独立 envelope ID 注册订阅 channel
- `RunTurn` 返回 `TurnReceipt`，但不返回回答
- `printEvent` 在 `turn_ended` 或 `session_ended` 时结束

运行：

```bash
go run ./examples/acp/client --server http://127.0.0.1:8080 --token demo --node local -p "列出当前目录"
```

## ConnectRPC API

### 1. 定位

本节的 ConnectRPC 指 aiscan 的 unary 管理服务。它与 Application WebSocket 使用相同的 server base URL 和 access key，但解决不同的问题。

> `AOPService.Connect` 是 Application 协议的双向流投影，不属于 unary 管理功能组。普通 Web/ACP client 应优先使用 `/api/aop/application/ws`；本节重点描述管理 RPC。

### 2. 传输和鉴权

生成的 Go client：

```go
client := rpc.NewSessionServiceClient(http.DefaultClient, "http://127.0.0.1:8080")
request := connect.NewRequest(&types.ListSessionsRequest{Limit: 100})
request.Header().Set("Authorization", "Bearer demo")
response, err := client.ListSessions(ctx, request)
```

- 请求使用 Connect 协议，默认 binary protobuf。
- server 同时兼容 Connect、gRPC 和 gRPC-Web handler。
- Bearer token 通过 Connect interceptor 校验。
- 鉴权失败返回 Connect code `Unauthenticated`。
- 业务参数和状态错误使用标准 Connect code，例如 `InvalidArgument`、`NotFound`、`Unavailable`。

### 3. Service 总览

#### SessionService

| Method | Request | Response | 用途 |
|--------|---------|----------|------|
| `ListSessions` | `ListSessionsRequest` | `ListSessionsResponse` | 分页查询 session |
| `GetSession` | `GetSessionRequest` | `GetSessionResponse` | 查询单个 session |
| `ResetSession` | `ResetSessionRequest` | `ResetSessionResponse` | 关闭旧 session 并创建新 session |
| `DeleteSession` | `DeleteSessionRequest` | `DeleteSessionResponse` | 删除 session |
| `ListCommands` | `ListCommandsRequest` | `ListCommandsResponse` | 查询 session 可用命令 |
| `ListEvents` | `aop.ListEventsRequest` | `aop.ListEventsResponse` | 查询持久化事件 |

HTTP procedure 示例：

```text
/aiscan.rpc.chat.SessionService/ListSessions
/aiscan.rpc.chat.SessionService/ListEvents
```

#### ScanService

| Method | 用途 |
|--------|------|
| `SubmitScan` | 提交扫描 |
| `GetScan` | 查询扫描 |
| `ListScans` | 查询扫描列表 |
| `CancelScan` | 取消扫描 |
| `GetScanReport` | 获取扫描报告 |

#### ConfigService

| Method | 用途 |
|--------|------|
| `GetConfig` | 查询当前配置视图 |
| `UpdateConfig` | 更新配置 |
| `ActivateProfile` | 激活 LLM profile |
| `TestLLM` | 测试 LLM 调用 |
| `ListModels` | 查询 provider models |
| `TestConnection` | 测试外部依赖连接 |

#### AgentService

| Method | 用途 |
|--------|------|
| `ListAgents` | 查询在线 Agent、capabilities、状态和统计 |

#### SystemService

| Method | 用途 |
|--------|------|
| `GetStatus` | 查询系统状态 |

#### SCOService

| Method | 用途 |
|--------|------|
| `ListNodes` | 查询 SCO nodes |
| `GetNode` | 查询单个 SCO node |
| `GetStats` | 查询 SCO 统计 |
| `DeleteNodes` | 删除 SCO nodes |
| `ImportNodes` | 导入结构化 nodes |
| `ListArtifacts` | 查询支持的 artifact 类型 |

完整字段见 [api/rpc.md](api/rpc.md)。

### 4. Session 管理字段

#### ListSessionsRequest

| 字段 | 说明 |
|------|------|
| `after_cursor` | 分页 cursor；当前实现为非负 offset 字符串 |
| `limit` | 页大小；0 使用服务端默认值 |
| `include_closed` | 是否包含关闭的 session |

`ListSessionsResponse.next_cursor` 非空表示还有下一页。

#### ListEventsRequest

| 字段 | 说明 |
|------|------|
| `session_id` | 必填 |
| `after_cursor` | exclusive 持久化 cursor |
| `limit` | 最大事件数量 |

`ListEventsResponse.events` 是 `EventDelivery{cursor,event}` 列表，`next_cursor` 是本次返回的最后 cursor。

它不会包含 `message_delta` 或 `tool_call_delta`，因为这两类事件不持久化。

#### ResetSessionRequest / DeleteSessionRequest

修改类管理 RPC 使用独立 `request_id` 做幂等标识：

- `ResetSessionRequest`：`request_id`、`session_id` 必填，`new_session_id` 和 `title` 可选。
- `DeleteSessionRequest`：`request_id`、`session_id` 必填。
- response 使用 accepted/rejected outcome，而 transport 失败使用 Connect error。

### 5. Connect 错误

| Connect code | 常见原因 |
|--------------|----------|
| `Unauthenticated` | token 缺失或错误 |
| `InvalidArgument` | 缺少必填字段或 cursor 非法 |
| `NotFound` | session、scan 或 node 不存在 |
| `AlreadyExists` | request/session ID 冲突 |
| `FailedPrecondition` | 对应 service 或 runtime 不可用 |
| `ResourceExhausted` | 达到并发或容量限制 |
| `Unavailable` | Agent 或外部依赖不可用 |
| `Internal` | 未映射的服务端错误 |

### 6. Go ConnectRPC 示例

[`examples/acp/connectrpc/main.go`](../examples/acp/connectrpc/main.go) 使用真实生成 client：

```go
client := rpc.NewSessionServiceClient(http.DefaultClient, serverURL)

request := connect.NewRequest(&types.ListSessionsRequest{
    Limit:         100,
    IncludeClosed: true,
})
request.Header().Set("Authorization", "Bearer "+token)

response, err := client.ListSessions(ctx, request)
```

查询 session 列表：

```bash
go run ./examples/acp/connectrpc --server http://127.0.0.1:8080 --token demo
```

查询指定 session 的持久化事件：

```bash
go run ./examples/acp/connectrpc --server http://127.0.0.1:8080 --token demo --session <session-id>
```

示例以标准 protobuf JSON 输出 response，方便直接检查字段。

## Protobuf 代码生成

### 1. Schema 位置

Application/AOP schema：

```text
web/frontend/cyber-ui/packages/aop/proto/aop/*.proto
```

ConnectRPC service 和 AIScan 类型：

```text
proto/rpc/*.proto
proto/types/*.proto
```

自动生成的字段参考：

- [api/aop.md](api/aop.md)
- [api/rpc.md](api/rpc.md)

### 2. 只生成 Application WebSocket 消息

Application WebSocket 不需要生成 service client，只需要 protobuf messages：

```bash
protoc \
  -I web/frontend/cyber-ui/packages/aop/proto \
  --java_out=lite:<out-dir> \
  web/frontend/cyber-ui/packages/aop/proto/aop/*.proto
```

Android Gradle 示例：

```kotlin
plugins { id("com.google.protobuf") version "0.9.4" }

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
}
```

proto 当前没有设置 `java_package` 和 `java_multiple_files`。直接生成时 Java 类默认按 proto 文件嵌套；vendor 到自己的 SDK 时可以添加符合项目规范的 Java options。

### 3. 生成 ConnectRPC client

ConnectRPC 除 protobuf message generator 外，还需要对应语言的 Connect client generator。生成时同时提供两个 include root：

```text
-I web/frontend/cyber-ui/packages/aop/proto
-I proto
```

需要编译的入口是 `proto/rpc/*.proto`，其 imports 会引用 `proto/types` 和 AOP schema。

仓库内 Go 代码统一通过：

```bash
go run ./cmd/gen
```

生成的 Go clients 位于 `pkg/rpc/*connect.go`。

### 4. 编码注意事项

- WebSocket 使用 binary protobuf Envelope。
- ConnectRPC 默认使用 binary protobuf，也可以协商标准 protobuf JSON。
- protobuf JSON 中 `bytes` 是 base64 字符串。
- enum 使用符号名称。
- oneof 使用生成的 JSON 字段名。
- 未知 `Any.type_url` 应保留，不应按 JSON shape 猜测类型。

## 验证

```bash
go test ./examples/acp/client ./examples/acp/connectrpc
```
