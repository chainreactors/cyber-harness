# AgentRuntime 统一语义与路径收敛

## 结论

Agent 执行结构已经收敛为两个领域概念：

```text
Session
  └─ Run (AOP: Turn)
```

- `Session` 持有累计对话、Inbox、LoopScheduler 和命令状态；
- 一次 `Run` 是一个完整 ReAct loop；`Run` 只是 API 名，唯一执行标识始终是 `turn_id`；
- `Command` 是唯一不创建 Turn 的直接执行接口；
- REPL、stdio、WebSocket、one-shot 和 scanner 均通过 `OpenSession`、`Session.Run`、`Session.Command` 执行。

不存在 Runtime `Execute/Submit/ExecuteLine/SubmitLine` 兼容路径，也不存在从 Runtime 配置直接创建 Agent 的执行路径。

## 生命周期

### Session

Session 必须显式打开和关闭：

```text
session.start
  turn.start (turn_id)
    message / status / tool.call / tool.result / usage
  turn.end
  ... more Runs ...
session.end
```

`session.start/end` 只表达真实 Session 生命周期。`session.end` 不再承担某次执行的完成信号。

### Run / Turn

- 一个 Run 恰好产生一个 `turn.start` 和一个 `turn.end`；
- ReAct 内部的多次 LLM/tool 迭代不产生额外生命周期事件；
- `turn.end` 是 Run 的唯一 terminal outcome，携带 stop、usage 和可选 error；
- Run 的事件日志可在完成后可靠重放，`Wait` 可在事件消费前后调用。

### Command

- Command 不产生 Turn；
- Command 输出以无 `turn_id` 的 Session AOP `message` 写入历史；
- Command 不写入 LLM transcript；
- `/continue`、`/followup`、`/skill:*` 进入 Run；
- `/stop`、`/exit` 是 adapter control。

## Transport

stdio 与 WebSocket 共用 `aop.Envelope` 和同一个 Runtime protobuf loop：

- WebSocket 使用 protobuf binary；
- stdio 使用 protobuf JSONL；
- ConnectRPC 只处理管理/query，不进入 Agent Runtime。

```text
open_session / close_session
run_turn / cancel_turn
command / command_result
event
protocol_error
```

- Web Run API 只使用 `turn_id` 关联；协议中不存在独立的 `run_id`；
- Runner 注册携带 Web 作用域内的 `node_id`；Chat、Command 与 PTY 直接使用该 ID 路由，不与 `session_id`、`turn_id` 混用；
- direct structured tool execution 使用 `tool_call` / AOP `tool_result`；
- PTY、file、exec、tool 属于 AOP namespace，不伪装成 Agent Turn，也不创建独立传输。

## 并发与异步输入

- 同一 Session 的 Run/Command 有界 FIFO，默认 pending limit 为 64；
- 不同 Session 并发运行；
- active Run 收到异步输入时写入当前 Inbox，继续同一个 ReAct loop；
- idle Session 收到异步输入时创建新的 automatic Run；
- WebSocket 断开取消该连接拥有的 Run；Run 取消由 context 传播；
- Runtime close 取消所有 Session，等待 worker 收敛后发出 `session.end`。

## 存储切换

这是一次性 breaking cutover：

- 不双读、双写旧协议；
- SQLite 保存 protobuf Session/Scan 与 AOP Event ProtoJSON；
- Scanner 输出只保存 libcstx SCO JSONL；
- 不保留 assets、records 或旧协议双读写。
