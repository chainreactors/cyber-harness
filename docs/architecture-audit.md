# 架构迁移后的遗留机制审计

审计日期：2026-09-10。范围是当前工作区的 App、Runtime、Host、Console/TUI 及入口调用关系。
本文记录已核实的现状和建议；下列代码整改尚未实施。

包依赖方向已经拆开，但仍有通过回调保留的旧边界、重复执行路径和重复状态，不能据此宣称彻底解耦。

## 优先收拢 Console/TUI 的执行职责

| 现状与证据 | 影响 | 简化方向与验收 |
| --- | --- | --- |
| `pkg/tui/commands.go` 的 `AppInfo` 同时存 Provider 配置快照、资源指针和八个操作回调；`pkg/console/console.go` 逐一装配 | 原来为了避免 TUI 导入 runner 而存在的回调边界，在 Runtime 独立后仍被保留。状态有多个来源，依赖被隐藏在函数中 | 让使用 Session/Runtime 的交互编排直接归 Console，持有真实对象；TUI 留显示和终端行为。删除 AppInfo 的业务回调及重复快照，不以新接口或新参数包代替 |
| `pkg/tui/console.go:handleInputLine` 根据回调是否齐全选择 Runtime 路径或直接 Agent 路径；`controller.go` 仍直接调用 Run、Continue 和 evaluator | 同一种输入有两套执行语义；测试还依赖无 Runtime 的旧入口。仅迁移文件没有消除双实现 | 所有产品 REPL 输入经过同一 Session 入口；终端命令由 Console 处理。迁移依赖旧入口的行为测试，再删除分支及直接执行代码 |
| `pkg/tui/controller.go` 的 pendingRun、pending、drainPending 保存执行闭包并排队；Runtime 已有有界 session.ops 队列 | TUI 队列没有数量上限，输入直到出队才进入 Runtime，绕过 Runtime 的准入上限。停止时 TUI 清空自己的队列，也形成另一套取消语义 | 输入及时交给 Runtime 准入；Console 只跟踪显示和自己提交的任务。先明确取消当前任务与取消该入口排队任务的语义；不得误取消其他入口的任务，再删除 TUI 执行队列 |
| `pkg/runtime/runtime_session.go:RunResult` 从 agent.Result 拷贝四个字段；Console 再构造 agent.Result | 出现 Agent → Runtime → Console 的结果往返转换，仅 Usage 字段名也要改两次 | 优先复用已有结果类型；保留取消、错误及空结果的处理。直接测试完成、失败、取消三种结果，删除往返转换，不能换名再建一份结果结构 |

这些项目应一起收拢，否则删除 AppInfo 后很容易在别处重新出现同样的回调集合。

## 其他可简化项

| 现状与证据 | 判断与方向 |
| --- | --- |
| `pkg/runtime/runner.go:New` 接收完整 CLI Option，可创建 App，也可借用 ExistingApp；ownsApp 决定 Close 是否关闭 App | Runtime 仍兼任产品资源装配，并修改 option.OutputFile。后续把 CLI 解析与 App 构造收回入口，Runtime 借用显式 App，只接收自身运行配置；同步迁移错误清理，不能只是增加一层工厂 |
| Runtime 保存 rt.bus，但它来自 App.EventBus，仅供 Subscribe 使用 | 可直接通过 App 订阅，删除重复指针。先确认总线在 App 生命周期内保持同一实例；共享事件序号和 Recorder 的回归测试需保留 |
| Runtime.SetLogger 先调用 App.SetLogger，后者已更新 Commands；随后 Runtime 又对 config.Tools 做匿名 SetLogger 接口断言 | 当前 config.Tools 就是 App.Commands，重复传播并引入不必要的动态接口。由 App 更新资源 Logger，Runtime 只更新其模板和 Session |
| `pkg/runtime/tool_call.go:ToolExecutor` 只有一个生产调用方，传入具体 CommandRegistry；内部又断言回 CommandRegistry | 接口没有隔开真实依赖，其他实现主要用于测试。审查是否直接使用现有注册表并让测试注册受控工具；不再新增替代接口 |
| `pkg/tui/render.go:LiveView` 保存 status sink；output.SetReadlineMode 同时传 writer 与 status 回调，生产入口两者来自同一个 readlineConsoleBridge | 这是显示层局部 sink，不是 Host 的事件转发层，但同一对象被拆成两个参数仍属多余间接关系。让 readline 输出绑定直接由真实 bridge 负责；保留远程终端直接渲染语义，不能只给 sink 改名 |
| `NewAgentConsoleWithWriters` / `ExecuteLineAndWait` 的注释仍声称服务 Web chat bridge，而当前引用只在 TUI 测试 | 生产 Web 已脱离 TUI。随双路径清理迁移测试，删去失效入口或明确其测试用途；不能为了旧测试长期保留产品兼容路径 |

## 已核实应保留的机制

- Host 的 admission 锁、active 等待、sendMu 和首个写错误分别处理关闭准入、等待同步派发、串行写出和异步写失败。它们不持有 Session/Run，不应为减少字段而删除。
- Stdio 只做 Envelope 的 ProtoJSONL 编解码，借用 IO；没有 queuedEnvelopeStream 或额外传输 DTO。
- REPL 的 cancel/done 分别表示请求取消和实际结束。检查当前 utils/pty 依赖的 CreateInteractiveFuncWithOptions 及 superviseInteractiveFunc 后，确认成功返回会启动 callback，取消会关闭读取端；失败时不返回 REPL。因此未发现需要添加 once、兜底完成结构或额外 goroutine 的证据。Close 等待 Console callback 和 Session 清理，不代表等待 PTY Manager 完成其最终状态更新。
- Session 的逻辑句柄在 resume/continuation 后指向新状态，Run 有真实完成和取消生命周期。不能仅因它们是结构体就当作 DTO 删除。
- Provider 在运行开始时读取配置快照，保障进行中的任务不受热更新破坏；这种快照有明确并发语义，与展示层反复复制配置不同。

## 本轮验证

- Runtime 的生产导入闭包不含 runner、Console、TUI、Host、Node、Web 或 cmd。
- 架构检查及 App、Runtime、Console、Host、Node、Web 的 race 测试通过；AOP 独立模块测试通过。
- `go test ./...` 唯一失败为 `tools/katana/TestE2EHeadlessReusesDiscoveredBrowser`，报错 `browser never reached the authenticated workspace`，与接手摘要中的失败一致。本轮没有对其根因作新的归因。
- 修正部分旧 runner 文档路径及过期构造示例。测试通过只说明现有覆盖内行为成立，不证明上述重复机制已消除。
