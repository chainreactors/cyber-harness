# Go 接入 aiscan

本目录提供两个可运行的 Go client，分别对应 aiscan 的实时功能组和管理功能组。

| 示例 | 功能组 | 用途 |
|------|--------|------|
| [`client`](client) | Application WebSocket | 创建 session、发送自然语言、消费流式事件 |
| [`connectrpc`](connectrpc) | ConnectRPC | 查询 session 列表和持久化事件 |

非 Go 客户端的 protobuf 生成和接入说明见 [`docs/integration.md`](../../docs/integration.md)。完整 API 见 [`docs/api.md`](../../docs/api.md)。

## 1. 启动服务

```bash
aiscan web --addr 127.0.0.1:8080 --token demo
```

确保已经配置可用的 LLM，并且存在在线 agent。默认内嵌 agent 的 `node_id` 是 `local`。

## 2. Application WebSocket 示例

运行：

```bash
go run ./examples/acp/client \
  --server http://127.0.0.1:8080 \
  --token demo \
  --node local \
  -p "你好，请介绍一下自己"
```

调用入口位于 [`client/main.go`](client/main.go)：

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

### Dial

[`client/client.go`](client/client.go) 中的 `Dial`：

- 把 `http`/`https` 转为 `ws`/`wss`
- 默认连接 `/api/aop/application/ws`
- 设置 `Authorization: Bearer <token>`
- 初始化 pending requests 和 watch subscriptions
- 启动唯一的 WebSocket `readLoop`

### call

`call` 为每个请求创建唯一 envelope ID：

```go
envelope, err := aop.Wrap(id, "", message)
```

响应通过 `Envelope.reply_to` 找到对应 pending channel。它被 `OpenSession`、`RunTurn` 和 `CloseSession` 复用。

### OpenSession

```go
response, err := client.OpenSession(ctx, "local", "")
```

内部发送 `OpenSessionRequest{node_id:"local"}`，并检查 `OpenSessionResponse` 的 accepted/rejected outcome。

### Watch

```go
events, err := client.Watch(session.GetId(), "")
```

Watch 使用独立 envelope ID 注册长期 channel。服务端事件的 `reply_to` 指向该 watch ID。

当前示例专注最小实时流程。生产 client 应进一步保存 envelope 的非空 `delivery_cursor`，并在断线后使用 `after_cursor` 恢复订阅。

### RunTurn

```go
receipt, err := client.RunTurn(ctx, session.GetId(), prompt)
```

内部构造：

```go
&aop.Message{
    Role:    "user",
    Content: []*aop.Content{aop.Text(prompt)},
}
```

`TurnReceipt` 只是运行回执。回答来自 `events` channel。

### printEvent

示例处理：

- `message_delta`：打印实时文本
- `tool_call`：打印工具名称
- `tool_result`：打印工具输出摘要
- `error`：打印错误
- `turn_ended`：结束本轮

## 3. ConnectRPC 示例

查询 session 列表：

```bash
go run ./examples/acp/connectrpc \
  --server http://127.0.0.1:8080 \
  --token demo
```

查询指定 session 的持久化事件：

```bash
go run ./examples/acp/connectrpc \
  --server http://127.0.0.1:8080 \
  --token demo \
  --session <session-id>
```

[`connectrpc/main.go`](connectrpc/main.go) 使用生成的 Go client：

```go
client := rpc.NewSessionServiceClient(http.DefaultClient, serverURL)

request := connect.NewRequest(&types.ListSessionsRequest{
    Limit:         100,
    IncludeClosed: true,
})
request.Header().Set("Authorization", "Bearer "+token)

response, err := client.ListSessions(ctx, request)
```

传入 `--session` 时改为调用：

```go
client.ListEvents(ctx, connect.NewRequest(&aop.ListEventsRequest{
    SessionId: sessionID,
    Limit:     limit,
}))
```

程序使用标准 protobuf JSON 输出 response。

`ListEvents` 是有限历史查询，不会返回未持久化的 `message_delta` 和 `tool_call_delta`。实时回答必须使用 Application WebSocket `WatchEvents`。

## 4. 依赖

Application WebSocket 示例：

```text
github.com/chainreactors/aiscan/aop
github.com/gorilla/websocket
google.golang.org/protobuf
```

ConnectRPC 示例还需要：

```text
connectrpc.com/connect
github.com/chainreactors/aiscan/pkg/rpc
github.com/chainreactors/aiscan/pkg/types
```

## 5. 测试

```bash
go test ./examples/acp/client ./examples/acp/connectrpc
```

测试内容：

- WebSocket：鉴权、OpenSession、WatchEvents、RunTurn、delta 和 `turn_ended`
- ConnectRPC：Bearer header、ListSessions、ListEvents 和 protobuf JSON 输出
