# Console：终端任务与展示所有权

Console 直接接收 `*agent.Runtime` 和 `*agent.Session`，使用外部终端库 `github.com/chainreactors/tui`。
Agent Runtime 不保存输出接口、PTY Manager 或终端模式。

- `AttachLocalREPL(ctx, rt, option, bindings)`：直接使用进程终端，避免把 readline 控制序列写入可重放 PTY 缓冲。
- `StartPersistent(rt, option, bindings)`：在 App 的 Bash Manager 中创建一个持久 REPL。传输断开只解除监视，重连复用同一终端。
- `RunTask(...)`：拥有一次静态展示与事件订阅，调用已有 Session/Run，在会话关闭后注销订阅。
- 可选命令、补全和状态行通过 `pkg/console/api.Bindings` 注入；Console 不依赖任何协作协议。IOA 的展示适配位于 `pkg/exts/ioa/client/console`。

返回的 `REPL` 只保存自己的 cancel 和完成 channel。这两个状态保证显式关闭可取消并等待
终端任务结束；它们不包装 Runtime 数据，也不关闭 Profile 拥有的 Runtime、App 或 Bash Manager。
`Close` 可重复调用，Runtime 取消也会传播到终端任务。

交互实现已直接位于 `pkg/console`，通过具体 Runtime 直接协作，不设置中间回调或传输模型。
Session/Turn 事件、Provider 安装和 JSONL 恢复仍由其实际所有者处理，Console 只负责显示与交互。

Session 是唯一执行入口，Runtime 的有界队列决定准入和顺序。Console 只保存按 Turn ID
登记的输入预览文本；`Run.Wait()` 用于等待完成，正文与错误统一由 AOP 事件显示。
`/stop` 和 Ctrl-C 取消当前终端提交的运行及排队任务，随后允许新输入，不取消同一 Session
中其他入口的工作。Console 关闭时停止准入、取消并等待自己的工作，再注销展示订阅。

入口负责在创建 Runtime 前指定 `PrimarySessionID: MainREPLName` 与录制选项。
释放顺序为 `REPL.Close()` → Profile 关闭其 Agent Extension、App 与资源图。
