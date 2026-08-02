# 外部接入指南：基于 AOP 协议接入 aiscan

> 字段级 API 参考（请求/响应、事件全表、错误码、幂等与重放语义）见 [api.md](api.md)。

本文档面向**外部程序的开发者**：如何不修改 aiscan 代码，通过网络协议接入 aiscan 的能力。两种典型角色：

| 角色 | 方向 | 用途 | 可运行示例 |
|------|------|------|-----------|
| **应用集成方（client）** | 外部程序 → aiscan | 自然语言输入，流式事件输出 | `examples/acp/client` |
| **工具提供方（tool node）** | 外部程序 → aiscan | 把自有工具注册给 agent 远程调用 | `examples/rmcp` |

两种角色走**同一条通道**：`/api/aop/ws` 上的二进制 protobuf WebSocket，语义全部由 `aop/*.proto` 定义（唯一真相，见 [protocol-architecture.md](protocol-architecture.md)）。

服务端形态可以是完整的 `aiscan web`（含 UI），也可以是 headless 部署（只有 RPC + AOP WebSocket，见 `examples/acp/server`）。

---

## 1. 拓扑

```
                ┌──────────────────────────────┐
                │      aiscan server (hub)     │
   browser UI ──▶│  /api/aop/ws   ConnectRPC    │◀── tool node（注册工具，rmcp）
   acp client ──▶│  AgentPool · SQLite store    │◀── agent node（执行 Loop + LLM）
                └──────────────────────────────┘
```

- hub 拥有 Agent Pool、会话持久化和事件总线；节点只拥有自身 runtime 与工具
- 同一会话的事件对**所有** watcher 实时扇出：acp client 驱动的交互，web UI 与任何其他 ws 客户端都能同步看到；迟到者通过 store 按 cursor 重放

## 2. 连接与鉴权

```
GET /api/aop/ws
Authorization: Bearer <access-key>
Upgrade: websocket
```

- access key 即服务端启动时的 `--token`；空 token 表示 dev 模式（不鉴权）
- 鉴权失败返回 401；`/health` 与登录端点不需要凭证
- 连接后的**首条 envelope 决定角色**：`AgentHello` → 节点（agent/tool）；其余 → browser 对端（client）

## 3. Envelope 规则

所有消息都是 `aop.Envelope` 二进制 protobuf（`websocket.BinaryMessage`）：

| 字段 | 规则 |
|------|------|
| `id` | 请求方生成的唯一 ID |
| `reply_to` | 响应方回填请求 `id`——请求/响应靠它关联 |
| `payload` | `anypb.Any` 包装的命名空间消息，用 `aop.Wrap`/`aop.Unwrap` 编解码 |

命名空间：

| 消息类型 | 职责 |
|----------|------|
| `aop.ProtocolMessage` | 核心：节点注册、Session/Turn 生命周期、事件流、错误 |
| `aop.tool.ProtocolMessage` | `tool.Call` / `tool.Progress` / 工具结果 |
| `aop.file / pty / exec / sco` | 文件、终端、执行、SCO 增量 |

错误统一为 `ProtocolError{code, message}`（以 `reply_to` 关联）；业务级拒绝为 `*Response_Rejected{code, message}`，不算传输错误。

## 4. Client 接入时序（自然语言 → 流式事件）

参考实现：`examples/acp/client/client.go`（约 200 行，可整文件拷贝改造）。

```
client                                hub                       agent
  │── OpenSession{node_id} ──────────▶│── OpenSession ─────────▶│
  │◀──────── OpenSessionResponse ─────│◀────────────────────────│
  │── WatchEvents{session_id} ───────▶│  （建立订阅：重放 + 实时）
  │── RunTurn{session_id, input} ────▶│── RunTurn ─────────────▶│
  │◀──────── RunTurnResponse ─────────│   (receipt: running)
  │◀════════ Event: message(用户输入) ═│   （hub 广播）
  │◀════════ Event: message_delta ════│◀══ 事件（LLM 流式）═════│
  │◀════════ Event: tool_call/result ═│◀══ 事件（工具调用）═════│
  │◀════════ Event: message(完整回答) ═│◀════════════════════════│
  │◀════════ Event: turn_ended ══════│◀════════════════════════│
  │── CloseSession{session_id} ──────▶│
```

要点：

1. **`OpenSession`**：必须指定在线 agent 的 `node_id`（web 控制台内嵌 agent 默认叫 `local`）；节点未连接时返回 `UNAVAILABLE`
2. **`RunTurn`**：`input` 是 `aop.Message{role: "user", content: [Text(...)]}`；响应只是回执，**结果不走响应，走 WatchEvents 事件流**
3. **`WatchEvents`**：单条长订阅，事件以 `ProtocolMessage_Event` 持续推送；`after_cursor` 支持断点续传
4. **事件类型**（`aop.Event` payload）：`session_started/ended`、`turn_started/ended`、`message`（完整消息）、`message_delta`（流式片段）、`tool_call`/`tool_call_delta`/`tool_result`、`usage`、`error`、`status`
5. **持久化语义**：`message_delta`/`tool_call_delta` 是瞬时的，**不落库**；完整 `message` 才持久化。实时方看流式 delta，迟到方重放完整消息——web UI 的历史视图与实时视图因此一致
6. 取消：`CancelTurn{session_id, turn_id}`；终止订阅：`CancelOperation{target_id: <watch envelope id>}`

最小 Go 依赖：`google.golang.org/protobuf` + `github.com/gorilla/websocket` + `github.com/chainreactors/aiscan/aop`。

## 5. Tool node 接入时序（远程工具注册）

参考实现：`examples/rmcp/main.go`，核心是对 `pkg/web/agent.RunToolNode` 的薄封装。自实现时序：

```
tool node                             hub
  │── AgentHello{node_id, tools,      │
  │    capabilities:[pty,file,exec,   │
  │    tool,sco]} ───────────────────▶│
  │◀──────────── AgentAccepted ───────│
  │◀═══ tool.Call{bash, args} ════════│   （agent 决策调用你的工具）
  │═══ tool.Progress{...} ═══════════▶│   （可选：执行期回显）
  │═══ tool.result（Event_ToolResult）▶│
```

- `AgentHello.tools` 携带 `ToolDefinition`（名称/描述/JSON Schema），agent 侧据此向 LLM 声明工具
- 工具执行发生在**你的进程里**：agent 执行与工具部署由此完全分离，可独立扩缩、独立权限边界
- 断线自动重连由 `RunToolNode` 内置（指数退避）；节点身份以 `node_id` 为准，重连覆盖旧 slot

## 6. 管理面（ConnectRPC）

会话历史、扫描 CRUD、配置、系统状态等查询走 unary ConnectRPC：

```
POST /aiscan.rpc.chat.SessionService/ListSessions
POST /aiscan.rpc.chat.SessionService/ListEvents   # 轮询式历史，实时仍用 WatchEvents
```

原则：**实时语义走 WebSocket，管理查询走 ConnectRPC**，不要为实时需求轮询管理面。

## 7. 端到端演示

```bash
# 1. hub（任选其一）
aiscan web --addr 127.0.0.1:8080 --token demo        # 完整版（含 UI）
go run ./examples/acp/server --addr 127.0.0.1:8080 --token demo   # headless 版

# 2. 工具节点（可选）
go run ./examples/rmcp --server http://127.0.0.1:8080 --token demo --id rmcp-1

# 3. client：自然语言 → 流式事件
go run ./examples/acp/client --server http://127.0.0.1:8080 --token demo \
  --node local -p "用 bash 列出当前目录"

# 同时打开 http://127.0.0.1:8080 的 web 控制台，可实时看到同一会话的交互
```

各示例自带的测试（`go test ./examples/...`）在进程内复现上述全部链路，包括多 watcher 实时扇出与迟到重放（`examples/acp/server/multiwatcher_test.go`）。
