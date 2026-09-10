# Runtime：会话与执行所有权

`runtime.New(ctx, option, logger, config)` 创建具体的 `AgentRuntime`。导入时可使用
`runtimepkg` 别名，避免与 Go 标准库 `runtime` 冲突。

Runtime 拥有 Session、Run、Inbox、调度器、JSONL 恢复状态及自己的 IOA 会话订阅。
它不导入 runner、Console、TUI、Host、Node 或 Web；其传递依赖也受架构测试约束。
Runtime 仍是本产品的运行核心，会使用 App、Skills、Commands 和产品配置；它不是另一套通用 Agent 框架。

## API 与数据流

- `New`：沿用现有 `RuntimeConfig`。`ExistingApp` 表示借用；未提供时创建并拥有 App。
- `OpenSession` / `EnsureSession` / `CloseSession`：会话身份、上下文与生命周期。
- `Session.Run` / `RunSession` / `CancelSessionRun`：运行调度、取消与结果等待。
- `Run.Wait`：直接返回 `(*agent.Result, error)`；完成结果视为只读，未执行即取消也返回相同结果类型。
- `Session.Command` / `Session.Resume`：状态命令和已有 JSONL 历史恢复。
- `RegisterNamespaces` 及既有 core/command handlers：向调用方的 `aop.NamespaceMux` 注册协议行为。
- `Subscribe`：观察 App 的原始 AOP 事件；订阅者负责调用返回的 unsubscribe。
- `SetProvider` / `ReloadProvider`：App 安装配置，Runtime 更新自己的模板及现有会话，运行中的 Run 保留快照。
- `App()` / `Context()`：返回实际借用资源和 Runtime 生命周期，不创建接口或配置副本。

消息始终使用既有 AOP protobuf；响应由 `aop.Reply` / `aop.NewProtocolError` 构造。
Host 的通信生命周期与 Runtime 的会话生命周期互相独立。

`ReadHistory` 公开原有 JSONL 重放算法及其 `History` 结果，包含实际重建的消息、父会话解析结果、
模型和消息计数。Console 的保存会话列表复用这个结果，没有第二套恢复算法或展示数据转换包。

## 主会话与关闭

`RuntimeConfig.PrimarySessionID` 默认 `task`。需要交互会话时，入口显式设置为 Console 的
`main-repl`，并设置 `option.SaveSession`；Runtime 不识别 REPL 模式，也不自动创建终端。
该主会话身份用于恢复历史、心跳和已有异步消息投递。

`Close` 取消自己的 context，阻止新会话和协议命令进入，等待会话、运行与后台订阅结束，
最后仅关闭自己创建的 App。借用 App 时，调用方应先关闭 Console，再关闭 Runtime，最后关闭 App。
关闭的并发入队、Host 重连、共享 App 事件序号、Provider 快照与 JSONL 恢复均有回归测试。

迁移后旧的 `runner.AgentRuntime` 和 `runner.NewAgentRuntime` 不再存在，没有兼容别名或门面。
