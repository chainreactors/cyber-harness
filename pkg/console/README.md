# Console：终端任务与展示所有权

Console 直接接收 `*runtime.AgentRuntime` 和 `*runtime.Session`，内部使用现有 TUI。
Runtime 不保存输出接口、PTY Manager 或终端模式；`RunOutput` 已删除。

- `AttachLocalREPL(ctx, rt, option)`：直接使用进程终端，避免把 readline 控制序列写入可重放 PTY 缓冲。
- `StartPersistent(rt, option)`：在 App 的 Bash Manager 中创建一个持久 REPL。传输断开只解除监视，重连复用同一终端。
- `RunTask(...)`：拥有一次静态展示与事件订阅，调用已有 Session/Run，在会话关闭后注销订阅。
- IOA CLI 展示入口在本包；IOA 服务端启动仍属于 runner 的产品入口。

返回的 `REPL` 只保存自己的 cancel 和完成 channel。这两个状态保证显式关闭可取消并等待
终端任务结束；它们不包装 Runtime 数据，也不关闭借用的 Runtime、App 或 Bash Manager。
`Close` 可重复调用，Runtime 取消也会传播到终端任务。

现有 TUI 的 `AppInfo` 绑定集中在 `console.go`，未新增 Sink、DTO 或另一套回调接口。
Session/Turn 事件、Provider 安装和 JSONL 恢复仍由其实际所有者处理，Console 只负责显示与交互。

入口负责在创建 Runtime 前指定 `PrimarySessionID: MainREPLName` 与录制选项。
释放顺序为 `REPL.Close()` → `Runtime.Close()` → 调用方拥有的 `App.Close()`。
