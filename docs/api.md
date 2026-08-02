# aiscan 接入 API 参考

本文档是外部程序接入 aiscan 的 **API 参考**，与可运行示例 `examples/acp` 逐段对应。概念与拓扑见 [integration.md](integration.md)；本文聚焦：字段、时序、错误码与从 0 集成的每一步。

- 所有实时语义：`/api/aop/ws` 上的二进制 protobuf WebSocket（AOP 应用平面）
- 所有管理查询：ConnectRPC unary（AIScan 管理平面，见 §8）
- wire 类型唯一真相：`aop/*.proto`（Go: `github.com/chainreactors/aiscan/aop`）

---

## 1. 从 0 集成：五步走

| 步骤 | 动作 | 对应示例代码 |
|------|------|-------------|
| 0 | 启动服务端，拿到 access key | `examples/acp/server` 或 `aiscan web` |
| 1 | WebSocket 拨入 `/api/aop/ws`，Bearer 鉴权 | `client.go: Dial` |
| 2 | `OpenSession` 在目标 agent 节点上开会话 | `client.go: OpenSession` |
| 3 | `WatchEvents` 建立事件订阅 | `client.go: Watch` |
| 4 | `RunTurn` 发送自然语言，消费事件流直到 `turn_ended` | `client.go: RunTurn` + `main.go: printEvent` |
| 5 | `CloseSession` / 关闭连接 | `client.go: CloseSession` |

```bash
# 步骤 0：启动（二选一）
aiscan web --addr 127.0.0.1:8080 --token demo          # 含 web UI
go run ./examples/acp/server --token demo              # headless，仅 RPC

# 步骤 1-5：一条命令跑通
go run ./examples/acp/client --server http://127.0.0.1:8080 --token demo \
  --node local -p "用 bash 列出当前目录"
```

最小依赖：`gorilla/websocket` + `google.golang.org/protobuf` + `chainreactors/aiscan/aop`。

---

## 2. 连接与鉴权

```http
GET /api/aop/ws HTTP/1.1
Authorization: Bearer <access-key>
```

| 项 | 说明 |
|----|------|
| access key | 服务端 `--token`；为空则 dev 模式全放行 |
| 失败 | `401 invalid or missing access key` |
| 免鉴权路径 | `/health`、`/api/auth/*` |
| 首条 envelope | 决定角色：`AgentHello` → 节点；其余 → browser 对端（本文 client 角色） |

帧格式：每条 WebSocket **BinaryMessage** 是一个 `aop.Envelope` protobuf。服务端不回 ping/pong 之外的应用层心跳，断线检测依赖 WS 层。

## 3. Envelope

`aop.Wrap(id, replyTo, message)` 构造，`aop.Unwrap(envelope)` 解包。

| 字段 | 类型 | 规则 |
|------|------|------|
| `id` | string | 请求方生成，需唯一（示例用随机 8 字节 hex） |
| `reply_to` | string | **关联键**：响应/事件的 `reply_to` = 请求的 `id` |
| `delivery_cursor` | string | 仅事件流使用，对应 session 内的持久化 cursor |
| `payload` | `anypb.Any` | 命名空间消息（见下） |

client 侧的请求/响应模型（`client.go: call`）：

```
发送:  Envelope{id: X, payload: OpenSessionRequest}
接收:  Envelope{reply_to: X, payload: OpenSessionResponse}   ← 按 reply_to 路由
订阅:  Envelope{reply_to: <watch 的 id>, payload: Event}     ← 按订阅 id 路由
```

### 3.1 命名空间

| 消息类型 | 用途 | client | tool node |
|----------|------|--------|-----------|
| `aop.ProtocolMessage` | 核心：hello/session/turn/event/error | ✓ | ✓ |
| `aop.tool.ProtocolMessage` | tool.Call / Progress | 仅观察事件 | ✓ |
| `aop.file.ProtocolMessage` | 文件读写（browser 端仅 upload） | 可选 | ✓ |
| `aop.pty / exec / sco` | 终端、执行、SCO 增量 | — | ✓ |

## 4. Session 生命周期

### 4.1 OpenSession

`aop.OpenSessionRequest`

| 字段 | 必填 | 说明 |
|------|------|------|
| `node_id` | ✓ | 目标 agent 节点 ID（内嵌 agent 默认 `local`；未连接返回 `UNAVAILABLE`） |
| `session_id` | | 留空由 hub 生成；复用已有 ID 可重开 |
| `title` | | 会话标题；留空时 hub 取首轮输入前 60 字符 |
| `scan_id` | | 关联已有扫描 |

响应 `OpenSessionResponse`：`accepted: aop.Session{id, state, node_id, title}` 或 `rejected`。

注意：hub 会把请求**转发给 agent 节点**并等待其确认（10s 超时），因此 agent 离线/无响应分别得到 `UNAVAILABLE` / 超时拒绝。

### 4.2 RunTurn

`aop.RunTurnRequest`

| 字段 | 必填 | 说明 |
|------|------|------|
| `session_id` | ✓ | |
| `input` | ✓* | `aop.Message{role: "user", content: [Text(...)]}`；`continue_session=true` 时可空 |
| `turn_id` | | 留空由 hub 生成 |
| `max_turns` | | agent loop 最大轮次，0 = 默认 |

响应只是回执 `TurnReceipt{session_id, turn_id, state: "running"}`——**结果不在响应里，在事件流里**。hub 收到后会立即把用户输入作为 `Event_Message` 广播给所有 watcher。

### 4.3 CancelTurn / CloseSession / CancelOperation

| 请求 | 作用 |
|------|------|
| `CancelTurnRequest{session_id, turn_id}` | 中断进行中的 turn |
| `CloseSessionRequest{session_id}` | 关闭会话（转发 agent 并落库） |
| `CancelOperation{target_id}` | 终止某条 WatchEvents 订阅（target_id = watch 请求的 envelope id） |

## 5. 事件流（WatchEvents）

`WatchEventsRequest{session_id, after_cursor}` → 持续推送 `ProtocolMessage_Event`。

- `after_cursor` 为空：重放全部持久化事件后接实时流
- `after_cursor = N`：断点续传（cursor 在每条事件 envelope 的 `delivery_cursor` 上）
- 单连接可多订阅，按 `reply_to` 区分

### 5.1 `aop.Event` payload 全表

| payload | 持久化 | 消费建议 |
|---------|--------|---------|
| `session_started` / `session_ended` | ✓ | 生命周期标记 |
| `turn_started` | ✓ | 一轮 LLM 交互开始 |
| `message_delta` | ✗（瞬时） | **流式输出**：append `text`/`reasoning` 增量 |
| `message` | ✓ | 完整消息（user/assistant），迟到重放的权威内容 |
| `tool_call` | ✓ | 工具调用（名称 + JSON 参数） |
| `tool_call_delta` | ✗（瞬时） | 参数流式拼接 |
| `tool_result` | ✓ | 工具返回（`output[]` 内容块，`is_error` 标记） |
| `usage` | ✓ | token 用量 |
| `status` | ✓ | agent 状态（provider/model 等） |
| `error` | ✓ | `ProtocolError{code, message, retryable}` |
| `provider_frame` | ✓ | 原始 provider 帧（敏感，默认关闭） |
| `turn_ended` | ✓ | **turn 终止信号**：`stop_reason`（stop/cancelled/…），可能带 `error` 与 `usage` |

事件均带 `session_id`、`turn_id`、`seq`、`emitted_at`；client 通常以 `turn_ended` 作为一轮的结束条件（`main.go: printEvent`）。

### 5.2 持久化语义（重要）

`MessageDelta`/`ToolCallDelta` **不落库**——历史与重放只含完整 `Message`。含义：

- 实时 watcher：delta 流 + 完整 message 都收到（展示流式，最终以 message 为准）
- 迟到 watcher（web UI 打开历史会话）：只看到完整 message
- 同一 session 的 watcher 数量无上限，hub 扇出

## 6. 错误模型

两层，均带机器可读 code：

**传输层** `ProtocolError`（响应位置出现，关联 `reply_to`）：

| code | 触发 |
|------|------|
| `INVALID_PAYLOAD` | envelope 解包失败 |
| `UNSUPPORTED_MESSAGE` / `UNSUPPORTED_NAMESPACE` | 角色不支持的消息 |
| `OPEN_SESSION_FAILED` / `RUN_TURN_FAILED` / … | 服务端内部错误 |
| `WATCH_EVENTS_FAILED` | 订阅异常终止 |

**业务层** `Rejection`（`*Response.rejected`）：

| code | 触发 |
|------|------|
| `INVALID_ARGUMENT` | 缺字段（如无 `node_id`、空 `session_id`） |
| `UNAVAILABLE` | 节点未连接 / 打开会话超时 |
| `NOT_FOUND` | session / turn 不存在 |
| `ALREADY_EXISTS` | envelope id 重复且请求体不同（幂等冲突） |
| `FAILED_PRECONDITION` | agent 侧拒绝 |

**幂等**：hub 按 envelope `id` 记账（请求日志），同一 `id` + 相同请求体 → 重放响应；同 `id` 不同体 → `ALREADY_EXISTS`。重试时请复用原 envelope id。

## 7. Tool node API（远程工具）

见 `examples/rmcp`。首条消息：

```
AgentHello{
  node_id, name,
  capabilities: ["pty","file","exec","tool","sco"],
  tools: [ToolDefinition{name, description, schema}...],
  runtime: {os, arch, cores, metadata}
}
→ AgentAccepted{node_id, capabilities}
```

之后 hub 按 agent 决策下发：

| 方向 | 消息 | 说明 |
|------|------|------|
| hub → node | `tool.Call{session_id, turn_id, call:{id, name, arguments}}` | `arguments` 为 `EncodedValue` JSON |
| node → hub | `tool.Progress{tool, target, text}` | 执行期回显（可选），`reply_to` = call id |
| node → hub | `Event_ToolResult{call_id, name, output[], is_error}` | 终态结果 |

- 节点身份以 `node_id` 为准；重连覆盖旧连接
- `capabilities` 声明决定 hub 路由哪些命名空间；tool-only 节点收到 chat 消息会被拒
- 生产接入建议直接复用 `pkg/web/agent.RunToolNode`（内含重连退避、file/pty/sco 内置 RPC），只需提供 `commands.CommandRegistry`

## 8. 管理面（ConnectRPC）

实时语义之外的管理/查询，unary JSON 或 protobuf：

| service | 代表方法 |
|---------|---------|
| `aiscan.rpc.chat.SessionService` | `ListSessions` / `GetSession` / `ListEvents`（轮询历史） / `DeleteSession` |
| `aiscan.rpc.scan.ScanService` | 扫描 CRUD 与查询 |
| `aiscan.rpc.agent.AgentService` | agent 列表与生命周期 |
| `aiscan.rpc.config.ConfigService` | 配置读写 |
| `aiscan.rpc.sco.SCOService` | SCO 图查询/导入 |
| `aiscan.rpc.system.SystemService` | 系统状态 |

原则：**实时用 WebSocket，查询用 ConnectRPC**，不要轮询管理面模拟实时。

## 9. 集成检查清单

1. 拨入即发请求（首条 envelope 即业务消息，无需握手帧）
2. 所有请求 envelope `id` 唯一；重试复用原 `id`
3. 响应按 `reply_to` 关联，不要假设顺序
4. `RunTurn` 响应只是回执；结果消费 WatchEvents
5. 以 `turn_ended` 结束一轮；同时处理 `error` payload
6. delta 仅用于实时渲染，落库/归档以完整 `message` 为准
7. 断线重连后：重发 `WatchEvents{after_cursor: <最后收到的 delivery_cursor>}`
8. 需要工具远程化时优先复用 `RunToolNode` 而非自实现节点协议

## 10. 验证

`go test ./examples/...` 在进程内复现本文全部链路：

| 测试 | 覆盖 |
|------|------|
| `examples/acp/client` | §4-§6：会话、turn、事件流、拒绝路径（脚本化 hub） |
| `examples/acp/server` | §1-§3：headless 组装、鉴权、无 agent 拒绝 |
| `examples/acp/server/multiwatcher_test.go` | §5.2：多 watcher 实时扇出 + 迟到重放（真实 server + 脚本 agent） |
| `examples/rmcp` | §7：tool node 注册与远程调用 |
