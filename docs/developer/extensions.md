# 扩展开发

[开发者指南](../development.md) · 下一篇：[会话与宿主集成](hosting.md)

第一个例子把 hello 工具加入应用。实际扩展通常还需要告诉模型怎样使用工具，复用已有命令，或管理一个长连接。本章沿着这些需求扩展同一个组合，先保持业务对象独立，再为它们建立明确的生命周期。

## 工具与业务代码

模型工具实现 `core/tool.Tool`。名称用于调用路由，描述说明适用范围，Definition 提供参数 schema，Execute 接受 context 与 JSON 参数并返回结构化结果。[hello 示例](../../examples/custom/main.go)展示了完整实现与注册。

描述和 schema 应使模型能判断何时调用、需要提供什么输入；它们不替代业务校验。涉及网络或长时间计算的实现必须响应 context。结果中给模型的文本应足以推进下一步；需要保留完整数据时使用结构化产物，避免把大量原始输出全部塞入对话。

工具直接返回 `(*aop.ToolResult, error)`，定义直接使用 `*aop.ToolDefinition`。使用 `tool.TextResult` 返回文本，在 `ToolResult.Output` 中保留文本、媒体等内容；执行失败返回 Go error。构造参数显式传入工具所需的目录、客户端或业务服务，避免运行时查找全局注册表。

普通执行错误通过 `core/tool/hooks.Feedback` 追加到模型可见结果，并设置 `IsError`；已有部分输出和媒体继续保留。原始 error 仍交给直接调用方和完成 hooks，支持 `errors.Is` / `errors.As`。相同错误经过多个执行入口不会重复追加，普通错误不会自动终止父 Agent。工具自行提供可操作的纠错原因，公共层不推断重试或再次执行副作用。

工具可以并行调用，因此共享字段需要自己的同步策略。扩展关闭时，注册表会处理该贡献的撤销与在途调用；工具背后的共享连接、缓存或后台线程仍由它们的所有者关闭。

## 复用命令能力

当功能本来就是一个带参数的命令，贡献 `tool.Command` 可以复用现有 CLI 知识。Agent 通过 bash 工具调用它，解释器把已注册命令交给进程内实现，其余命令按调用环境启动外部程序；组合语义见[执行链](../architecture.md#工具与命令的执行链)。

这种接入适合扫描器、查询和格式校验。它不需要额外生成同名可执行文件，也不必为每个 flag 再定义一个模型工具。若应用需要专门的结构化交互，再增加 Tool 表面，并让两种入口调用同一业务实现。

[OKF 扩展](../../exts/okf/extension.go)是一个小而完整的参考：Load 同时贡献命令、说明文档与提示词策略。阅读它时，可以沿着 `tools/okf` 的命令实现确认业务逻辑并没有依赖 Scope。

已有标准 MCP 服务时可使用 [MCP → CLI 扩展](../mcp.md)：Load 使用 mcp-go 连接服务、发现工具并贡献以服务别名命名的命令，如 `ida decompile --addr 0x401000`。Agent 从 bash 按需列举、查看工具帮助并使用 Schema 参数调用工具。

## 按导入组合可选能力

browser、passive 和 Katana 可以直接安装到 `harness.BaseExtensions` 之后，无需 `full` 标签。三个独立示例在仓库根目录运行，只初始化注册和说明文档，不调用模型或外部目标：

```sh
go run ./examples/browser
go run ./examples/passive
go run ./examples/crawl
```

对应入口分别是 [browser](../../examples/browser/main.go)、[passive](../../examples/passive/main.go)、[crawl](../../examples/crawl/main.go)。每个入口只导入所需扩展；passive 不依赖 scanner 或浏览器，Katana 不依赖 scanner 或 Playwright。实际浏览器操作仍需要本机 Chrome，测绘查询按来源要求配置凭证。

`exts/scanner.Config.Crawler` 接受 `scan.Crawler`；组合根传入 `katana.Crawl` 后，quick 扫描增加普通爬取，full 扫描增加浏览器爬取。未注入 crawler 时，scan 只运行自身能力。scan 保留目标过滤、去重和流水线事件，Katana 适配器负责网络与浏览器生命周期。

passive 的配置声明位于 `exts/passive/config`，运行扩展位于 `exts/passive`。宿主可以声明并保存配置而不链接运行实现；`recon` section、环境变量和现有 CLI flag 名称保持兼容，是否显示 flag 由宿主决定。

`cmd/aiscan` 用成对的 `discovery_full.go` / `discovery_standard.go` 选择产品 imports，保留 `full` 作为全量发行版组合。无需新增 browser、passive 或 crawl 标签。`record` 仍受原生 SDK、CGO 和平台约束，可以独立于 `full` 组合。

## 子 Agent 贡献

子任务定义使用 `agent/subagent.Subagent`，在组合根按依赖顺序安装：

```go
subagentext.New()       // 定义唯一的 Subagent 贡献点和执行接口
scannerext.New(...)    // 可选，贡献 verify / sniper
sessionext.New(...)    // 需要会话时安装
subagentext.NewTools() // 借用贡献点和 Runtime，提供模型调用工具
```

上面的包别名对应 `exts/subagent`、`exts/scanner` 和 `exts/session`。直接同步执行扫描子任务不需要 Session 或 NewTools；普通 Session 也可以不安装子 Agent。扩展通过 Scope 贡献定义：

```go
extension.Add(scope, subagent.Subagent{
    Name: "verify", Description: "Verify a finding",
    DefaultMode: subagent.Sync,
    Prepare: prepareVerify,
})
```

`Prepare` 返回本次配置快照与非空任务文本，不执行任务或创建需要另行关闭的资源；匿名调用跳过名称解析和 Prepare。消费者借用 `Executor`，同步 Execute 使用 `agent.RunTask`，会话工具使用 Start 接入 Session。运行时 Add 返回的 Handle 由贡献者关闭；重复名称拒绝，撤销隐藏定义、取消任务并排空准备、执行及通知，之后才允许同名注册。接口见 [agent/subagent](../../agent/subagent)，状态与租约见[运行机制](../architecture.md#subagent-委派)，模型调用方式见[子 Agent 使用说明](../user/web.md#子-agent)。

## Inbox 路由 Agent

`exts/inbox` 借用 Agent Loop、Provider、hooks 与事件流，拥有一个跨 Run 保留上下文的路由 Agent。它只安装私有的 `inbox_route` 工具执行器，宿主的 shell、文件和 subagent 工具不会进入路由模型。接收者目录由实际 SessionStart/SessionEnd 构成，回调仍由 Session 控制准入和取消。

inbox 组合在 Session、subagent tools 和 IOA collaboration hooks 之后启动 Router，确保恢复任务发出的事件能被全部观察：

```go
router := inboxext.New(inboxext.Config{
    StateDir: ".cyber/active", MaxActive: 4,
    Notify: notifyResult, // 返回错误会保留在 durable outbox 重试
})
collaboration := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{
    Receive: router.Router().Deliver,
    LoadCursor: router.Router().LoadCursor,
    SaveCursor: router.Router().SaveCursor,
})
// 顺序：base / prompt / loop / subagent 定义 → IOA client
//     → sessionext → subagentext.NewTools() → collaboration → router
```

Router 借用 `*sessionexec.Tool` 调用与模型 subagent 工具相同的 Prepare、定义租约和 Session 执行后端，以及内部 `*proc.Manager` 管理工作单元。Router 模型仅能分发，scheduler 负责并发与实例生命周期；不另建 Worker 进程或 worktree。

其他入口调用 `Router.Submit(ctx, message)`，成功即完成 durable 准入，Receipt 的 `Wait(ctx)` 只观察路由目标与准入结果，不等待业务执行；取消 Wait 不撤销输入。调用方必须提供非 nil context 和从 `New` 获取的 Router。持久 Router 的失败路由持久退避重试，无最大次数；已经确认的目标不会再次投递。`Notify` 是结果发送边界，必须只在外部发送成功后返回 nil；重发时将稳定的反馈 Message ID 放入 `feedback_id`，并标记 `kind=task_result`，同时转发 `source_task_id`、execution 对应的 `source_session_id` 和 `feedback_chain`。IOA adapter 保留这些字段；持久 Router 对 peer 结果以 sender/source_session/feedback_id 去重，保留首次 wire ID 作为 Receipt.ID 和回复引用。peer 结果也受禁止 new_task、反馈环路及 16 跳上限约束；无需执行时可 record，普通 peer 消息不允许直接 record。普通消息仍按传输 ID 去重，其他接收方需自行实现结果幂等。存储失败取消宿主，不能返回虚假的成功确认。

路由投影只包含文本和媒体描述。执行输入保留媒体、附件与来源，但其内部 Message ID 按 origin/sender/source_session/id 规范化，避免不同来源相同 ID 在恢复时被误去重；原外部 ID 留在 `Meta.message_id` 和 Receipt。对 live Session 的直接投递仍保留原消息。明确的目标不能被模型改投：inbox 的 `task_id` 或历史 execution ID 可恢复原任务，非任务 Session 必须仍存活；重名 Session 拒绝。

每个 inbox Profile 一个 Router，每个 Session 各自拥有 mailbox，IOA 承担跨节点传输。监控采集仍由 producer 或外部进程完成。IOA 先订阅再补读，持久游标只在按序 durable 准入后推进；首次读取全部保留历史，之后分页，并每秒补读以弥补在线 SSE 缺口。进程重启使用由 hostname 和状态目录派生的同一 IOA identity。已被来源删除的消息无法补齐。

inbox 输入、路由、任务历史、执行 journal、反馈和 outbox 都在同一状态目录 SQLite 中保存。完成快照与输入消费原子提交；执行失败只确认已读取的输入，未读取的实时追问保留 pending 并另起 execution 续接，反馈链也只记录已确认输入的来源。显式取消仍终止该次已认领输入。重启将旧执行置为 interrupted，恢复任务历史并核对外部副作用；不恢复旧进程，也不承诺副作用 exactly-once。关闭先停准入和调度，再取消／排空 Session 与所属后台工作，最后释放订阅、数据库和目录锁。

空 `StateDir` 仍可选择原内存 Router，安装在消息源与会话之前以建立 live Session 目录。它只转发到存活 Session，消息与最近 1024 条完成回执仅在内存保留，没有持久任务调度、恢复与游标接口。`aiscan agent` 不安装 Router；独立的 `aiscan inbox` 子命令选择持久实现，宿主以运行期 `Option.Command` 识别入口，该字段不能由 YAML 或远程配置选择。Inbox 专属配置位于 `inbox` section。

持久 Router 的集成测试使用脚本 Provider 驱动真实 Agent Loop、Session、内部 Manager、HTTP IOA server/SDK，覆盖重启续接、强杀恢复、历史隔离、并发与纠正、反馈循环、outbox 重发、后台进程清理和超过一页的 IOA 补读：

```powershell
go test -race ./exts/inbox ./exts/ioa/client ./agent/subagent/sessionexec ./cmd/aiscan -count=1 -timeout 2m
$env:CYBER_ACTIVE_SOAK_DURATION='65s'
go test -race ./exts/inbox -run '^TestActiveIOAMonitorRestartAndContinuous$' -count=1 -v -timeout 2m
```

65 秒持续用例还会主动断开 SSE 并重启整个 inbox 宿主，检查离线事件补读与重复抑制。它不调用真实模型服务，不能替代真实模型的路由质量验证。

`TestRepositoryCIContinuationAfterProviderFailure` 让 worker 实际运行仓库 Go 测试，插入实时追加的 CI 检查和模型请求失败，同时执行独立 revision 审计；核验追加检查最终执行、已完成工具不重跑以及重启后不重放。`TestRepositoryCIResultDeliveryRetryIsIdempotent` 用实际 git 结果和 HTTP/SSE，在接收方落盘后注入发送确认丢失，核验重试只写一条审计记录，且不同 sender、消息类型或 source_session 的独立事件不被误去重。两项用例都使用可控 Provider；业务工具、持久状态和 IOA 传输均为真实实现。设置 `CYBER_INBOX_CASES_OUTPUT` 可保留 SQLite、命令产物和 `case-summary.json`，用于内部复核：

```powershell
$env:CYBER_INBOX_CASES_OUTPUT = Join-Path (Get-Location) 'outputs/inbox-real-cases'
go test -race ./exts/inbox -run '^TestRepositoryCI' -shuffle=on -count=10 -timeout 180s -v
```

`TestReleaseReviewFeedbackSafetyAcrossIOA` 由 builder worker 生成当前提交的 go.mod SHA-256，通过 HTTP/SSE 交给 reviewer worker 写实际审计文件；再重启接收宿主，补读离线回传的 cyclic feedback，并测试隐式新任务、过长链、历史 execution 地址及仅有 source_task_id 的回投。路由模型故意先选择错误目标，必须收到工具错误后改为 record；五个结果都不得新增执行，独立普通通知仍能处理。`cmd/aiscan` 的 `TestInboxCLIProcessWorkflow` 还从独立进程的真实 Profile 验证外部 IOA 请求、回包身份/反馈链、tmux 输入与宿主重启，避免只验证手工拼装的测试宿主。

离线复杂场景使用脚本 Provider 驱动真实 Agent Loop、HTTP IOA server/SDK 和 Session；执行 `go test -race ./exts/inbox ./exts/ioa/client`。`TestContinuousMonitorIOARouting` 覆盖忙碌接收者、独立任务、5 批监控事件、空闲后续跑、SSE 重连、中断纠正、结果交回协调者、IOA 回执和停止准入。设置 `CYBER_INBOX_MONITOR_INTERVAL=10s` 可把用例扩展为约 71 秒的持续验证，默认使用短间隔保持离线测试快速。

`TestIOABurstWaitsForRouterCapacity` 用真实 IOA 订阅向容量为 1 的 Router 突发发送 10 条消息，先阻塞路由再释放，验证满队列退避后的顺序和消息 ID。客户端边界另测准入失败不记去重、重试可取消，以及 SSE 解码失败前的缓冲事件排空。

真实模型验证使用 `TestLiveRouterInterleavedTasks` 与 `TestLiveContinuousMonitorIOARouting`，只将路由决策发送给真实模型，下游执行 Provider 保持确定性。配置 `CYBER_API_KEY`、`CYBER_MODEL`、`CYBER_BASE_URL`，需要代理时配置 `CYBER_PROXY`，再设置 `CYBER_INBOX_LIVE=1`：

```powershell
go test ./exts/inbox -run '^TestLive(RouterInterleavedTasks|ContinuousMonitorIOARouting)$' -count=1 -v -timeout 5m
```

模型地址需包含 API 前缀（如 `/v1`）。密钥仅通过当前进程环境传入，不写入源码或测试报告；模型连接失败作为测试失败报告，不回退到脚本 Provider。

## 知识与提示词

面向最终使用者的可编辑知识适合放入 [Skill 文件](../user/knowledge.md)。随功能扩展分发的知识可贡献 `skills.Bundle`，由统一的 Store 提供虚拟路径和读取。工具说明因此可以跟着功能一起安装与移除，无需复制到多个系统提示词里。

需要调整系统行为时贡献 `prompt.Contribution`：为它指定稳定名称与目标，再在 Apply 中操作具名 section。例如 OKF 只向主 Agent 和扫描 Agent 加入 Markdown 产出策略。指定 target 能避免把同一要求无差别加入压缩器和评估器。

Prompt 在一次 Run 开始时解析。运行中改变贡献不会追溯修改已经发给模型的请求；一次 Apply 失败也不会提交半份修改。执行准入应由工具或 hooks 控制，提示词只提供行为指导。组装细节见[上下文与知识](../architecture.md#上下文与知识)。

## 共享服务与借用

扩展若拥有一个可复用服务，在 Load 中初始化它并用 `Provide[Service]` 发布；后面的消费者用 `Use[Service]` 借用。接口类型要与发布时一致。只让消费者拿到业务接口，资源本身的启动和关闭保留在所有者中。

组合顺序由宿主显式写出。一个读取服务的工具扩展排在服务扩展之后，会话和宿主消费者再排在工具之后。可选服务可以由组合根决定是否安装整组功能；需要稳定接口时也可以提供明确的禁用实现，如基础组合的 NoEgress。

不要把借来的工具执行器或 Skill Store 塞入全局状态供任何代码随取随用。显式借用让关闭关系可追踪，也让一个扩展的依赖在 Load 中可见。

## 配置与生命周期

只有构造配置的简单应用可以直接使用 Go 值。要把功能加入统一 CLI 和配置文件时，功能包提供 `Declare`，向宿主预先定义的 CLI、配置与连接测试贡献点提交声明。宿主解析后把确定的值传入构造函数；`Declare` 不负责启动服务。

基础配置的字段归属统一定义在 `config.Option`：`local:"true"` 表示本地设置，适用于身份、传输、数据目录和输出；没有配置键的进程参数也留在本地。文件加载与远端替换共用合并、环境覆盖和校验逻辑，远端替换不再逐项复制本地字段。扩展配置仍以各自的 `Section` 声明为准，通过 `Resolved` 提供已校验的值。文件层状态仅用于发现、编辑与诊断，不参与远端配置替换，也不承载运行服务。

Load 中的初始化使用 `scope.Init()`，持续后台工作使用 `scope.Lifetime()`。扩展若持有 goroutine、订阅或连接，还应实现 `Close(context.Context) error`：停止接收新工作，结束已有工作并释放资源。只贡献静态条目的 hello 扩展不需要空的 Close。

关闭 deadline 到达而资源仍在工作时，返回 `extension.ErrCloseIncomplete` 或可识别的 context 错误，以便 Set 保留依赖并重试。完整的撤销顺序和失败回滚见[扩展装配](../architecture.md#扩展装配与生命周期)。

## 验证扩展

先直接测试业务实现的参数、结果和取消，再通过实际组合验证注册与调用。共享资源和后台工作还需要覆盖部分加载失败、在途调用中的撤销、关闭超时后的重试；这些是单测普通 Execute 无法验证的行为。

```sh
go run ./examples/custom
go test ./core/resource ./core/extension ./core/registry ./core/tool
```

支持 CGO 与 race detector 的平台可进一步执行 `go test -race ./core/resource ./core/extension ./core/registry`。当扩展已能独立工作，下一步是在[宿主中打开会话](hosting.md)，让模型使用它。
