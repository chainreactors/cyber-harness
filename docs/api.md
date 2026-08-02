# aiscan Chat 接入 API（多语言开发者版）

本文档只讲一件事：**外部程序如何给 aiscan 的 agent 发自然语言、并流式拿到回答**。

- 通道：`/api/aop/application/ws` 上的二进制 protobuf WebSocket，或 `AOPService.Connect`
- 输入：`RunTurnRequest`（自然语言文本）
- 输出：`WatchEvents` 推送的 `Event` 流（流式增量 + 完整消息 + 结束信号）
- 以 Android（Kotlin）为完整示例，同样适用于 iOS / 桌面 / 其他后端
- Go 参考实现：`examples/acp/client`；协议规范：`web/frontend/cyber-ui/packages/aop/SPEC.md`
- **字段级权威参考**（从 proto 自动生成）：[api/aop.md](api/aop.md)（实时平面）、[api/rpc.md](api/rpc.md)（管理平面）

---

## 1. 生成 client 代码

AOP 只有 message 没有 service，生成消息类即可。proto 源文件：

```
web/frontend/cyber-ui/packages/aop/proto/aop/*.proto   # package aop
```

依赖仅 protobuf well-known types（`any`/`timestamp`/`struct`），protoc 自带。

```bash
protoc \
  -I web/frontend/cyber-ui/packages/aop/proto \
  --java_out=lite:<out-dir> \
  web/frontend/cyber-ui/packages/aop/proto/aop/*.proto
```

| 平台 | 参数 | 运行时 |
|------|------|--------|
| **Android** | `--java_out=lite:` | `com.google.protobuf:protobuf-javalite` |
| iOS | `--swift_out:` | `SwiftProtobuf` |
| Python | `--python_out:` | `protobuf` |
| TypeScript | 仓库自带 `buf.gen.yaml`（`protoc-gen-es`） | `@bufbuild/protobuf` |

Android 用 Gradle 插件更省事：

```kotlin
// app/build.gradle.kts
plugins { id("com.google.protobuf") version "0.9.4" }
protobuf {
    protoc { artifact = "com.google.protobuf:protoc:4.31.0" }
    generateProtoTasks { all().forEach { it.builtins { create("java") { option("lite") } } } }
}
dependencies { implementation("com.google.protobuf:protobuf-javalite:4.31.0") }
// aop 的 .proto 放入 app/src/main/proto/aop/（保持目录结构）
```

> **类名注意**：proto 未设置 `java_package`/`java_multiple_files`，默认生成按文件嵌套的类（`aop.Protocol.ProtocolMessage`）。vendor 时建议加 `option java_package = "com.example.aop"; option java_multiple_files = true;`。下文示例按扁平类名书写。

---

## 2. 连接

```http
GET /api/aop/application/ws HTTP/1.1
Authorization: Bearer <access-key>     # 服务端 --token；空则免鉴权
Upgrade: websocket
```

- 每条 **BinaryMessage** = 一个序列化的 `aop.Envelope`
- `AgentHello` 只允许发送到 `/api/aop/node/ws`；Application 直接发送业务请求
- Android 模拟器访问宿主机：`ws://10.0.2.2:<port>/api/aop/application/ws`

```kotlin
val request = Request.Builder()
    .url("ws://10.0.2.2:8080/api/aop/application/ws")
    .header("Authorization", "Bearer $accessKey")
    .build()
val ws = okHttpClient.newWebSocket(request, listener)
```

---

## 3. Envelope（唯一的外层结构）

`aop.Envelope`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 请求方生成，唯一；重试复用原 id（服务端幂等） |
| `reply_to` | string | 响应/事件的 `reply_to` = 请求的 `id`，据此关联 |
| `delivery_cursor` | string | 事件流游标，断线续传用 |
| `payload` | `google.protobuf.Any` | 业务消息，type URL 如 `type.googleapis.com/aop.ProtocolMessage` |

发送（打包 → 二进制帧）：

```kotlin
fun send(ws: WebSocket, id: String, msg: ProtocolMessage) {
    val env = Envelope.newBuilder().setId(id).setPayload(Any.pack(msg)).build()
    ws.send(ByteString.of(*env.toByteArray()))
}
```

接收（解包 → 按 reply_to / payload 类型分发）：

```kotlin
override fun onMessage(ws: WebSocket, bytes: ByteString) {
    val env = Envelope.parseFrom(bytes.toByteArray())
    if (!env.payload.`is`(ProtocolMessage::class.java)) return
    dispatch(env.replyTo, env.payload.unpack(ProtocolMessage::class.java))
}
```

---

## 4. 输入：建立会话并发送自然语言

### 4.1 `OpenSessionRequest` —— 开一次对话

| 字段 | 必填 | 说明 |
|------|------|------|
| `node_id` | ✓ | agent 节点 ID（服务端内嵌 agent 默认 `local`；离线返回 `UNAVAILABLE`） |
| `title` | | 标题；留空取首轮输入前 60 字符 |

响应 `OpenSessionResponse`：`accepted: Session{id, node_id, ...}` 或 `rejected: Rejection{code, message}`。取 `accepted.id` 作为后续 `session_id`。

```kotlin
val req = ProtocolMessage.newBuilder().setOpenSessionRequest(
    OpenSessionRequest.newBuilder().setNodeId("local")
).build()
send(ws, newId(), req)
```

### 4.2 `RunTurnRequest` —— 发送一轮自然语言

| 字段 | 必填 | 说明 |
|------|------|------|
| `session_id` | ✓ | 4.1 拿到的会话 ID |
| `input` | ✓ | `Message{role: "user", content: [{text: {text: "你的问题"}}]}` |

响应 `RunTurnResponse`：`accepted: TurnReceipt{turn_id, state: "running"}`。**这只是回执——回答不在响应里，在 §5 的事件流里。**

```kotlin
val input = Message.newBuilder().setRole("user").addContent(
    Content.newBuilder().setText(TextContent.newBuilder().setText(prompt))
).build()
val req = ProtocolMessage.newBuilder().setRunTurnRequest(
    RunTurnRequest.newBuilder().setSessionId(sessionId).setInput(input)
).build()
send(ws, newId(), req)
```

---

## 5. 输出：WatchEvents 事件流

`WatchEventsRequest{session_id, after_cursor}`：

| 字段 | 说明 |
|------|------|
| `session_id` | 订阅的会话 |
| `after_cursor` | 空 = 全量重放 + 实时；非空 = 断点续传（取最近事件的 `delivery_cursor`） |

发送后，服务端持续推送 `Event`（envelope 的 `reply_to` = 本请求的 id）。

### 5.1 `Event` 的 chat 相关 payload

| payload | 说明 | 渲染建议 |
|---------|------|---------|
| `turn_started` | 一轮开始 | 显示"思考中" |
| `message_delta` | **流式增量**：`text`（回答）/`reasoning`（思考过程），append 语义 | 追加到当前气泡，实时刷新 |
| `message` | **完整消息**（`role` + `content[]`），历史的权威内容 | 以它替换 delta 拼接结果 |
| `tool_call` | agent 调用工具：`{name, arguments}` | 展示工具调用 chip |
| `tool_result` | 工具返回：`output[]`（Content 列表）、`is_error` | 折叠展示 |
| `usage` | `TokenUsage{input/output/total_tokens}` | 用量展示 |
| `error` | `ProtocolError{code, message, retryable}` | 错误提示 |
| `turn_ended` | **一轮结束**：`stop_reason`，可带 `error`/`usage` | 结束 loading，定稿气泡 |

> **关键语义**：`message_delta` 是瞬时的、不落库；`message` 才持久化。所以实时渲染用 delta，定稿/归档以完整 `message` 为准。迟到进入会话的 watcher 重放到的也只有完整 `message`。

### 5.2 Kotlin：一轮问答的完整消费

```kotlin
private val answer = StringBuilder()

fun dispatch(replyTo: String, msg: ProtocolMessage) {
    when (msg.messageCase) {
        ProtocolMessage.MessageCase.OPEN_SESSION_RESPONSE -> {
            msg.openSessionResponse.rejected?.let { fail(it.message) }
                ?: run { sessionId = msg.openSessionResponse.accepted.id; openWatch() }
        }
        ProtocolMessage.MessageCase.RUN_TURN_RESPONSE ->
            msg.runTurnResponse.rejected?.let { fail(it.message) }
        ProtocolMessage.MessageCase.EVENT -> onEvent(msg.event)
        ProtocolMessage.MessageCase.PROTOCOL_ERROR -> fail(msg.protocolError.message)
        else -> {}
    }
}

fun onEvent(event: Event) {
    when (event.payloadCase) {
        Event.PayloadCase.MESSAGE_DELTA -> {
            answer.append(event.messageDelta.text)
            renderStreaming(answer)                 // 流式刷新
        }
        Event.PayloadCase.TOOL_CALL -> showToolChip(event.toolCall.name)
        Event.PayloadCase.TURN_ENDED -> {
            answer.clear()
            finishTurn(event.turnEnded.error)       // 结束本轮
        }
        else -> {}
    }
}
```

---

## 6. 错误与幂等

两层错误，都带机器可读 `code`：

| 层 | 位置 | 常见 code |
|----|------|-----------|
| `ProtocolError` | envelope 响应位 | `INVALID_PAYLOAD`、`UNSUPPORTED_MESSAGE`、`OPEN_SESSION_FAILED` |
| `Rejection` | `*Response.rejected` | `INVALID_ARGUMENT`（缺字段）、`UNAVAILABLE`（节点离线/超时）、`NOT_FOUND`、`ALREADY_EXISTS`（id 复用冲突） |

**幂等**：hub 按 envelope `id` 记账——同 id 同体重发返回首次响应；同 id 不同体拒绝。网络重试请复用原 id。

---

## 7. 收尾与检查清单

结束会话：`CloseSessionRequest{session_id}`；中断本轮：`CancelTurnRequest{session_id, turn_id}`。

1. `id` 唯一；响应按 `reply_to` 关联，不假设顺序
2. `RunTurn` 响应只是回执，回答消费 WatchEvents
3. 以 `turn_ended` 结束一轮
4. delta 只做实时渲染，定稿以完整 `message` 为准
5. 断线重连：重发 `WatchEvents{after_cursor: <最后 delivery_cursor>}`

协议行为的可执行参照：`go test ./examples/acp/...`（脚本化 hub 重放本文全部时序）。
