# AgentRuntime 统一语义与路径收敛

## 结论

Agent 执行结构已经收敛为两个领域概念：

```text
Session
  └─ Run (AOP: Turn)
```

- `Session` 持有累计对话、Inbox、LoopScheduler 和命令状态；
- 一次 `Run` 是一个完整 ReAct loop，对外使用 `run_id`，AOP 使用同值的 `turn_id`；
- `Command` 是唯一不创建 Turn 的直接执行接口；
- REPL、stdio、WebSocket、one-shot 和 scanner 均通过 `OpenSession`、`Session.Run`、`Session.Command` 执行。

不存在 Runtime `Execute/Submit/ExecuteLine/SubmitLine` 兼容路径，也不存在从 Runtime 配置直接创建 Agent 的执行路径。

## 生命周期

### Session

Session 必须显式打开和关闭：

```text
session.start
  turn.start (turn_id = run_id)
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

stdio 与 WebSocket 共用 `webproto.Message` 语义帧：

```text
session.open / session.opened
session.close / session.closed
run / run.cancel
command / command.result
aop
error
```

- Web Run 只使用 `run_id` 关联；AOP envelope 的 `turn_id` 与其严格相等；
- Runner 身份使用注册消息中的 `NodeRef.ID`；它是节点路由身份，不进入 `Session → Run` 领域模型，也不与 `run_id` 混用；
- direct structured tool execution 使用 `command / command.result`，不再把 inbound AOP 当 RPC；
- PTY、file RPC、node status/config 仍属于各自控制或终端平面，不伪装成 Agent Turn。

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
- SQLite 保留 sessions、messages、assets、records；
- 旧 `chat_aop_events` 在 migration version 2 时一次性清空，因为旧 Session/Turn 边界不能安全重解释。
