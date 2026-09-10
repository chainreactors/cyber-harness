# 第二阶段：收拢终端交互，删除旧执行边界

状态：设计，尚未实施。基线提交：`88a59ccd`。

## 目标与决定

下一阶段完成 Console 的真实职责收拢：输入直接调用 Runtime Session，执行只在 Runtime 排队，终端只负责输入、展示和自身生命周期。

**合并 `pkg/tui` 到 `pkg/console`，删除内部 TUI 包。** 当前 TUI 的生产调用方只有 Console；其余外部引用是 `cmd/aiscan/cli_test.go`。两包之间没有独立使用需求，却需要 AppInfo、Controller、订阅函数类型和状态回调来连接。因此合并比继续设计显示层接口更简单。外部 `github.com/chainreactors/tui` 仍提供 readline、terminal 和 Cobra 集成。

目录数量减少不是验收标准；必须同时删除回调集合、重复状态和双执行路径。

```text
cmd / runner / node
  ├─ app       产品资源、工具、Provider、公共事件及 Recorder
  ├─ runtime   Session、Run、有界执行队列、恢复和运行协议
  ├─ console   终端输入、交互命令、readline、PTY REPL、渲染
  │    └─ 直接使用 runtime / app 和现有外部终端库
  └─ host      inline / stdio 通信，继续只依赖 AOP

runtime → app → 既有 agent / commands / tools
console → runtime → app
```

Runtime 的导入闭包仍不得含 Console、Host、Node、Web、runner 或 cmd。终端代码不得进入 Runtime。

## 一、项目结构与直接调用

| 当前文件/机制 | 实施位置和最终状态 |
| --- | --- |
| `pkg/tui/console.go` | 移至 `pkg/console/interactive.go`，避免覆盖现有 console.go。AgentConsole 持有 `*runtime.AgentRuntime`、`*runtime.Session` 及终端/显示状态 |
| `pkg/tui/ioa.go` | 移至 `pkg/console/ioa_commands.go`，与现有 IOA CLI 入口共存 |
| TUI 其余源文件及测试 | 原名迁入 Console；保留 escape_unix.go、escape_other.go 的平台约束 |
| `pkg/console/console.go` | 删除 consoleAppInfo 和 consoleAppInfoForSession；保存会话枚举逻辑移入 `history.go`，继续复用 runtime.ReadHistory |
| `pkg/tui/commands.go` | 删除 AppInfo、展示层 Session 和 Controller 接口；命令闭包直接使用 AgentConsole，采用已有 cobra.Command，删除 Command/ArgSpec/wrapCommand 中间转换 |
| remote_console.go | 直接从 Runtime 订阅事件；删除 AOPEventSubscriber 和 variadic subscribers，终端参数继续使用现有 Terminal |
| local_repl.go、remote_repl.go、task.go、ioa.go | 调用同包实现，消除 tui 前缀。保持现有产品入口函数 |
| cmd/aiscan/cli_test.go | 将命令解析及 skill 菜单的行为测试迁至 Console；命令集测试使用真实 Runtime 和受控 Provider/工具 |

构造交互控制器只需一个具体内部函数，形如：

```go
newAgentConsole(ctx context.Context, rt *runtime.AgentRuntime,
    session *runtime.Session, option *config.Option,
    terminal *terminal.Terminal) *AgentConsole
```

不再传 AppInfo、单独的 Agent、输出回调或订阅接口。Agent 从 session.Agent() 读取；Provider、Skills、Commands 从 rt.App() 读取。不新增 Dependencies、Bindings、ContextBag 等替代结构。完整 CLI Option 的进一步缩减不在这一步混入。

## 二、唯一输入执行路径

输入在一个位置分类，删除 handleRuntimeInputLine 与旧 handleInputLine 的择一路由。

| 输入 | 唯一处理者 | 语义 |
| --- | --- | --- |
| 普通输入、`/followup`、`/skill:*` | session.Run | 使用已有 RunInput 和 AOP Content；粘贴引用在 Console 展开，skill 在 Runtime 展开一次 |
| `/continue` | session.Run，Continue=true | 使用同一队列、取消及事件路径 |
| `/clear`、`/compact`、`/eval`、`/goal`、`/loop`、`!cmd` | session.Command | 删除 Console 直接 Reset、Compact、RunForeground/Execute、evaluator 调用及本地 evalCriteria 副本 |
| `/resume` | Console 选择路径后 session.Resume | 仍用原 Session 逻辑句柄；取消运行中的恢复操作必须遵守现有 Runtime 忙碌检查 |
| `/status` | Runtime 命令生成运行状态，Console 显示终端补充信息 | Provider 健康和运行状态以实际所有者为准；不另存 StatusInfo/ProviderInfo 镜像 |
| `/provider`、`/model` | Console 做交互选择，rt.ReloadProvider/SetProvider 安装配置 | 保留已有模型列表与选择行为；删除 Console 再次直接 ag.SetProviderConfig 的重复写入 |
| `/help`、IOA 浏览、渲染模式与终端按键 | Console | 使用已有具体对象和终端库；不进入 Agent 执行 |
| `/stop`、Ctrl-C、`/exit` | Console 生命周期 | 按下文取消规则处理 |

Cobra 注册表既用于补全、帮助也用于终端命令分派。Runtime 业务命令注册闭包只调用 Session，不再实现第二份业务逻辑。

同步 Session.Command 暂保留现有语义，不为这次清理新增 Future 或 Operation DTO。长时间 `!cmd` 执行期间，独立按键中断仍能取消它；必须回归验证。不得用无界 goroutine 队列绕开阻塞问题。fast 模式保留顺序读取命令的行为。

## 三、排队、停止和关闭

**只有 Runtime 的 session.ops 决定执行顺序和准入。** 删除 agentRunFunc、pendingRun、pending 执行闭包和 drainPending；Console 提交 Run 后立即得到已有 Run 句柄，满额错误立即展示。

不新增调度器或任务容器。交互状态直接放在 AgentConsole 中：

- attachment context：终端任务整体生命周期。
- submit context/cancel：当前终端提交批次的取消根；所有从该终端提交的 Run 和状态命令都派生自它。
- WaitGroup：只等待本终端已接受 Run 的结果观察协程结束，不负责派发任务。
- 必要的互斥：保护批次替换、关闭准入和显示登记；不在持锁时等待 Run、调用输出或关闭 Runtime。

| 动作 | 明确行为 |
| --- | --- |
| 提交普通输入 | 同步调用 session.Run 完成准入，随后观察 run.Wait；观察协程不启动下一任务、不打印最终正文 |
| 队列满 | 返回现有 pending limit 错误，不创建 Console 候补队列，不后台重试 |
| `/stop` 或 Ctrl-C | 取消当前 submit context，覆盖本终端当前运行及排队任务；在 attachment 仍打开时换成新的 submit context，允许下一次输入 |
| 其他 inline/stdio 入口的任务 | 使用各自 context，不因 Console 的 stop 被取消；不通过 CancelSessionRun 扫描整个 Session 清空任务 |
| `/exit` 或 Console.Close | 先关闭 Console 准入，再取消其批次和附件 context，等待自己的观察协程，最后注销事件订阅 |
| Console 借用已有 Session | 关闭 Console 不关闭借用 Session、Runtime 或 App |
| AttachLocalREPL / StartPersistent | 入口创建的专属 REPL Session 由该入口在终端任务结束后关闭；传输 Router 断开继续只解绑，不结束持久 REPL |
| `/clear`、`/compact`、`/resume` | 当前或排队 Run 尚未清理完时继续使用 Runtime 的忙碌拒绝。stop 是请求取消，不把取消请求当作已经完成 |

取消后已占用的 Runtime 准入计数随 reject/完成释放，不承诺 `/stop` 返回后队列立即腾空；不为此添加第二份队列删除算法。既有 Run/Event 完成语义保留。

若保留排队输入预览，只在 Console 保存 `map[turnID]string` 的原始显示文本：提交前以明确 TurnID 登记，准入失败或收到 TurnStarted/TurnEnded 时删除。容量受 Runtime 限制，不存执行闭包、输入对象或取消对象。提前登记防止“事件先于 Run 返回”导致旧预览残留。这个状态只用于现有用户界面；执行不得读取它。

## 四、显示与结果只有一个来源

1. Console 建立一次 Runtime 事件订阅，直接处理原始 AOP Event。删除传订阅函数的参数层。空事件不解引用。
2. 从 Session 逻辑句柄解析当前物理 ID；过滤同 App 的其他 Session 和 subagent 事件。切换前后事件与准入的顺序用现有 emit 路径锁定测试，不用缓存整个事件流。
3. TurnStarted/Message/ToolResult/TurnEnded 驱动开始、正文和结束显示。将现有 output.Final 的收尾逻辑并入对应 TurnEnded 分支，利用当前 turn 的 lastAssistant 等已有状态。删除控制器和 RunTask 等待结束后的重复 Final。
4. run.Wait 只用于调用方获取结果/错误及生命周期等待，观察协程不得把上一个 turn 的正文插入下一 turn。无正文、取消、错误时同样只收尾一次。
5. 删除只为显示而保存的 Provider 配置镜像；状态/模型列表读取 App.ProviderState。正在运行的 Agent 配置快照继续保留，因为它承担并发隔离。
6. LiveView 直接持有同包的具体 readlineConsoleBridge，调用其 UpdateStatus；AgentOutput 绑定同一个 bridge。删除 SetStatusSink、sink func、重复 writer/status 参数。
7. readlineConsoleBridge 直接持有现有 readline.Shell，使用实际方法提交文本与刷新。审查并删除仅为测试替换而保存的 commit/redraw 函数字段；输入活跃/ready 状态由实际 Console 事件更新，避免换个名字保留 callback bag。

本地 readline 仍直接使用进程终端，远程 REPL 仍使用 PTY 缓冲。保留原生 scrollback、100ms footer 刷新、CRLF、光标复位、取消阻塞读取及 attach 重放规则。

## 五、同步删除 Runtime 的小型遗留

这些修改与交互清理同阶段完成，不新增包：

- 删除 RunResult，Run.Wait 返回 `(*agent.Result, error)`；取消/失败但 Agent 未返回结果时构造同一种 agent.Result，填 Stop/Err。Run 保存的完成结果视为只读；不修改调用方可见结果，不增加结果镜像。AOP 输出继续使用 TotalUsage，wire schema 不变。
- 删除 rt.bus，通过 rt.app.EventBus 订阅。保留共享事件序号、同一个 Recorder 的测试。
- Runtime.SetLogger 去掉对 config.Tools 的匿名接口断言和重复更新；App 更新 Commands，Runtime 更新模板和已有 Session。
- ExecuteToolRequest 直接接收 `*commands.CommandRegistry`，删除 ToolExecutor 及内部回转断言。测试注册现有工具接口的受控工具，覆盖结构化输出、错误、panic、bash 前台进度及超时，保留真实执行路径。
- `AgentRuntime.cleanup` 中 IOA 子 context 的取消目前随 rt.ctx 取消，且额外 cancel 在 wg.Wait 后才调用；删除这条重复取消链。独立保留已有 handoff 订阅的 unsubscribe，关闭时显式调用，避免累积事件订阅。不引入通用 cleanup 列表。

## 六、按可验证步骤实施

| 顺序 | 提交内容 | 必须同时完成的验证 |
| --- | --- | --- |
| 1 | 合并 TUI 到 Console，处理文件重名，更新入口/测试/文档/架构检查，删除旧包 | Console、cmd、Node 测试；平台源文件约束正确；没有旧 tui 导入 |
| 2 | 改为直接持有 Runtime/Session，统一命令分派，删除 AppInfo、Controller、包装命令和直接 Agent 执行 | provider/model 更新、skill、eval、bash、resume/clear/compact；旧测试迁至真实 Runtime |
| 3 | 事件驱动统一输出，再删除 Console 排队和执行控制器，落实批次取消 | FIFO、满额立即拒绝、多入口取消隔离、stop 后新输入、关闭并发准入、无重复/乱序正文；race 通过 |
| 4 | 删除 readline sink 与本地回调拆分，删除结果转换和其他 Runtime 小型遗留 | 本地交互/远程 PTY、重连、空/错/取消结果、工具前台进度、IOA 订阅关闭 |
| 5 | 更新文档与结构约束，删除测试专用旧入口和兼容别名 | 全仓测试、AOP 模块测试、标准及 full 构建；核对删除清单 |

第 1 步只是可编译的移动提交，不作为阶段完成。第 2—4 步结束前不能把旧路径留作 fallback。

现有 `TestRunnerIsSingleTagFreeImplementation` 把整个 Console 也要求为 tag-free；合并后需要修正这条过宽规则：runner/runtime/cmd-runner 保持单实现，Console 只允许终端必需的平台文件约束，不允许按产品构建模式保留两套 Console。

### 必须覆盖的行为场景

- 一个被阻塞的 Run 加排队输入直到默认限额；下次提交立即失败。释放后按准入顺序执行，取消任务不调用 Provider。
- Console 与 inline 在同一借用 Session 提交，stop/close Console 只取消 Console 的任务；专属 Session 的关闭仍拒绝新工作。
- Run 在 session.Run 返回前就发出开始甚至结束事件；显示登记、WaitGroup、关闭不泄漏或越过准入锁。
- 前一 turn 结束后下一 turn 立即开始；正文不重复、不交错，错误与空结果均收尾一次。
- resume/clear/compact 后使用原 Session 句柄继续；新事件显示，旧 Session/兄弟 Session 不串入。
- Provider 热更新更新后续 Run；运行中的 Run 保持快照，状态页不依赖启动时的副本。
- PTY 创建失败、立即取消、远程断开重连、本地非终端输入和两次 Close。
- 保留 markdown、verbosity、粘贴引用、多行输入、历史选择、模型选择、Ctrl-C 及 native scrollback 的已有行为覆盖。

运行相关测试及 race 后执行 `go test ./...`、AOP 模块测试、标准 aiscan/runner 与 full 构建。已有 Katana 浏览器 E2E 失败单独记录；任何新增失败都须修复。测试禁止靠新接口/fake callback bag 绕开产品路径。

### 阶段完成的删除清单

`pkg/tui`、AppInfo、展示层 Session/Controller、consoleAppInfo*、AOPEventSubscriber、两条输入分支、agentRunFunc/pendingRun/drainPending、Console 的 evaluator/直接 Agent 执行、RunResult、SetStatusSink/sink 字段、ToolExecutor、rt.bus 及重复 Logger 分发。

RunInput、Session、Run、REPL 不删除：它们分别表达实际执行参数、可切换的会话身份、任务完成生命周期和终端生命周期。标准 io.Reader/io.Writer、context 及现有协议回调继续使用，不新造等价抽象。

## 七、随后独立收拢 App 构造权

第二阶段验收后再处理 Runtime.New 的双所有权问题，避免在改交互取消时同时改全部入口装配：

- 将 New 改为明确借用 `*app.App`；删除 ExistingApp、ownsApp、ProviderOptional、RuntimeConfig.IOA 及内部 app.New/InitIOA 分支。
- App 创建、ProviderOptional 决策、IOA 身份与工具初始化归 cmd/runner/node 等现有组合入口。Runtime 保留其 Session 订阅，App 保留客户端资源。
- 将 JSONL 目标路径选择、覆盖检查和 option.OutputFile 回写移至入口；Runtime 继续负责恢复算法以及 Session 切换时协调已有 App Recorder。
- 在改构造签名前逐项列出 Runtime 实际使用的配置，缩减现有 RuntimeConfig，配置只包含实际运行语义；不复制 CLI Option，不传 callback bag，也不增加工厂接口。
- 所有入口明确清理次序 Console → Runtime → App，构造失败关闭已创建资源；共享 App 的 Runtime 关闭不销毁 App。

这部分是下一份独立实施方案的范围；本方案交付点是 Console 机制收拢及上述遗留删除完成，不把整个产品都变成通用可注入框架。
