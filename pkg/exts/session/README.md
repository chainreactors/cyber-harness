# Session Manager

`session.New(application, ioa, option, logger, config)` 构造无副作用的 `Resource`。
AIScan Profile 将它放在已加载 App 和选定的 Agent 扩展之后，由同一 `core/extension.Set` 管理，
只向调用方借出 `Resource.Manager`。`Resource` 是唯一拥有 Load/Close 的扩展；
`Manager` 只提供会话操作，不能关闭宿主，也不实现另一套 Extension。

Manager 拥有 Session、Run、Inbox、调度器、有界请求准入、取消、历史重建和自己的 IOA
订阅。Agent 执行通过 Profile 注入的 `agent.Loop` 调用，其宿主生命周期由 `pkg/exts/agent`
拥有。App、Provider、Tool、Command、Loop 与可选 IOA Service 都是借入资源；Manager 不关闭它们，
也不创建嵌套生命周期图。

## API 与数据流

- `OpenSession`、`EnsureSession`、`CloseSession`：会话身份和生命周期。
- `Session.Run`、`RunSession`、`CancelSessionRun`：有序执行和取消。
- `Run.Wait`：只等待并返回只读结果，不发起取消。
- `Session.MessagesSnapshot`：返回深拷贝；传入的初始历史也在接收时复制。
- `Session.Command`、`Session.Resume`：状态命令和 ProtoJSONL 历史恢复。
- `RegisterNamespaces`：在调用方拥有的 `aop.NamespaceMux` 上安装 core/command 行为。
- `Subscribe`：提供 App 事件流的只读观察；`EmitEvent` 统一经 `App.Emit` 发布。
- `SetProvider`、`ReloadProvider`：更新后续运行，活跃 Run 保留自己的快照。

会话事实始终使用既有 AOP protobuf。Manager 不拥有输出实现或第二个事件总线；
`pkg/exts/eventoutput` 是唯一通用 JSONL 输出扩展。历史读取不会修改或隐式重新打开来源。

## 生命周期

`Resource.Load` 将 Manager context 绑定到 Extension 寿命，开始准入和自有订阅。`Resource.Close` 拒绝新工作，
取消并排空已接纳 operation，发布最终会话事件，然后只释放 Manager 自己的状态。
Profile 随后关闭 Agent 扩展，再关闭 Registry、App 与资源。

单个 Session 关闭由 Manager 持有的清理任务完成；`CloseSession(ctx, ...)` 的 context
只限制本次等待。等待超时不会中止清理，也不会重复启动清理。实例保留到操作退出、资源释放
和结束事件发布完成后才移除；期间拒绝新操作和重用同一 ID。再次关闭不存在的非空 ID 为
幂等成功。结束事件订阅者的异常在释放资源后报告；不能将异常当作未完成的执行。

`OpenSession` 的调用方 context 只能缩短会话寿命，不能使其脱离 Extension 寿命。
队列发布和关闭准入共享同一同步边界，消费循环退出后不会遗留新的排队操作。

Session 句柄绑定具体实例，不再按逻辑 ID 自动重绑定。显式恢复/清理产生续接时只更新
发起操作的句柄；其他旧句柄必须重新 `EnsureSession`。不再公开 `Session.Agent()`，
调用方通过 Session API 操作和读取状态，不能取得内部可变 Agent。

`Config.Loop` 为 nil 时不启用推理，会话状态和控制命令仍可使用；失败执行不会将输入遗留
到 Inbox。Loop 由 Profile 选择，
不提供会话级覆盖入口；Session 不绕过已安装的 Agent 生命周期扩展。

`Config.PrimarySessionID` 默认 `task`；交互入口显式选择 `main-repl`。Manager 不识别终端模式，
也不创建 Console。

当前边界清理不等于全部状态实现迁移：会话内部仍使用 `agent.Agent` 保存跨轮数据，
业务执行适配及旧 Agent 实现的移除不属于本轮通用生命周期修复。
