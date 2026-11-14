# Agent 可观测性：现有 AOP 接入 OTel

状态：2026-10-11，工作区已实现 traces / logs / metrics 并完成 SigNoz / Phoenix 本地验收。本文的验收记录来自本地工作区，测试和验收脚本保留在本地。运行方式见[接入示例](../examples/observability/README.md)。

现有 AOP 是采集输入，`exts/otel` 是转换和导出边界。Agent、工具和机制继续发布已有事件；OTel 扩展解释这些事实，生成兼容 GenAI / OpenInference 的 span、关联日志和指标。经同一个 Collector，traces 写入 SigNoz 和 Phoenix，logs / metrics 写入 SigNoz。

AOP protobuf 继续服务于 UI、归档、回放和扩展通信。trace/span ID 由独立 OTel SDK 在消费时生成；标准属性属于导出协议，不要求业务侧新增 OTel hooks。

```mermaid
flowchart LR
    Agent[Agent / Session] --> AOP[现有 AOP Stream]
    Observe[observe 工具与资源事件] --> AOP
    Mechanisms[compact / eval / JEV / guardrail] --> AOP
    AOP --> Bridge[exts/otel AOP bridge]
    Bridge --> SDK[独立 Trace / Log / Metric SDK]
    SDK --> Collector[Collector]
    Collector -->|traces / logs / metrics| SigNoz
    Collector -->|traces| Phoenix
```

## Pi 参考

主要参考 [NikiforovAll/pi-otel](https://github.com/NikiforovAll/pi-otel/tree/bf00f530d3667375a5317a0f425d0918e6cfac7e)，并对照 [stnly/pi-otel](https://github.com/stnly/pi-otel/tree/398d40a72a1ba3a599e8596f26c3f148df1e7296)。两者是 Pi 生态扩展，各自有独立实现。

| Pi 的做法 | Cyber 接入方式 |
| --- | --- |
| interaction / LLM / tool 生命周期 | 消费已有 Session、Turn、llm_request、operation 生命周期 |
| assistant 消息完成与稍后到达的 usage 分开 | LLM 优先结束于 assistant 消息时间，避免计入工具耗时 |
| 按 call ID 关联并行工具 | 使用已有 operation.Ref 的 operation_id、parent_operation_id、call_id |
| compact/retry/cancel 与因果关联 | 解释现有 status、typed Any、失败和 delegation 信息；缺失事实标注未知 |
| OpenInference / GenAI 与内容开关 | bridge 同时投影，业务层不引入产品 SDK |
| metrics/logs | 在同一 AOP 消费边界投影；与 traces 共享 Resource 和关联上下文 |
| shell W3C 传播 | 当前尚未实现跨进程传播 |

语义固定到 [OTel GenAI v1.41.0 完整发布文档](https://github.com/open-telemetry/semantic-conventions/tree/v1.41.0/docs/gen-ai)，同时核对 [Agent spans](https://github.com/open-telemetry/semantic-conventions/blob/v1.41.0/docs/gen-ai/gen-ai-agent-spans.md)、[模型 spans](https://github.com/open-telemetry/semantic-conventions/blob/v1.41.0/docs/gen-ai/gen-ai-spans.md)、[metrics](https://github.com/open-telemetry/semantic-conventions/blob/v1.41.0/docs/gen-ai/gen-ai-metrics.md)及[错误记录](https://github.com/open-telemetry/semantic-conventions/blob/v1.43.0/docs/general/recording-errors.md)。Pi 提供生命周期与关联设计参考；其历史自定义字段不自动成为标准字段。

GenAI 仍在演进，新仓库中的 inference duration 等定义需要单独迁移。当前三信号 instrumentation version 为 `0.2.0`，Resource 中 `cyber.otel.gen_ai.version=1.41.0` 标明语义口径。SDK 版本独立管理，不宣称覆盖所有 GenAI 可选字段。

## 代码边界

| 文件 | 职责 |
| --- | --- |
| [extension.go](../exts/otel/extension.go) | 订阅、clone、队列、独立 SDK、exporter、Flush/Close 与错误反馈 |
| [bridge.go](../exts/otel/bridge.go) | AOP 关联状态、父子关系、来源 links、完成上下文与去重缓存 |
| [events.go](../exts/otel/events.go) | 原有 Session / Turn / operation / LLM / usage 转换 |
| [mechanisms.go](../exts/otel/mechanisms.go) | 原有 compact/eval/budget status、JEV RuntimeEvent、guardrail Review 适配 |
| [signals.go](../exts/otel/signals.go) | 包装 span 投影，按开始/结束生成日志、计数和耗时；独立于 trace 采样 |
| [lifecycle.go](../exts/otel/lifecycle.go) | Session 生命周期与 accepted AOP fact 日志；选择相关 trace/span |
| [providers.go](../exts/otel/providers.go) | 独立 Log/Metric SDK、导出错误与订阅丢弃反馈 |
| [options.go](../exts/otel/options.go) | OTLP 环境配置优先级、完整 signal URL 与支持协议的校验 |

转换器只接收 `*aop.Event`，不读取 Agent、provider、hooks、调用栈或其他扩展运行时对象。JEV/guardrail 根据现有 Any full name 和已注册 protobuf descriptor 解码；OTel 生产代码不导入它们的实现。未识别或未注册的扩展保留为 AOP fact，并记录 type name。

## 现有事实的映射

| AOP 输入 | OTel 输出与语义 |
| --- | --- |
| SessionStarted / SessionEnded / DelegationDetail | 保存 model、父 Session/call、Agent 类型与运行模式；记录 started/completed 日志、活跃数与生命周期耗时；不创建长期 Session 根 span |
| TurnStarted / TurnEnded | `invoke_agent [agent name]`，INTERNAL / AGENT；标准 operation、conversation ID、已知 Agent name/id，以及 stop_reason、context_tokens、最终错误和汇总 usage |
| operation.Started / Completed + Ref | 工具为 `execute_tool <name>`，INTERNAL / TOOL，标准 operation/tool name/call ID；command/process 为 CHAIN 子操作；按显式 parent_operation_id 关联，Completed.StartedAt 支持缺开始事件的重建 |
| Completed.Failure | 执行 ERROR、有限分类 `error.type`、failure.kind、异常事件；无 StartedAt 且未见 Started 时标记 start_missing |
| llm_request + LLMRequestDetail | `chat [model]`，CLIENT / LLM；标准 operation/provider、已知 request model、max_tokens、stream，以及 cyber 消息数和请求序号 |
| assistant Message + Usage | 消息时间用于结束模型，usage 到达后补计量；未见响应时结束时间标记 estimated |
| compact_start/end/error + CompactDetail | `agent.compact` CHAIN，压缩前后 context tokens、保留消息数、错误 |
| eval_start/end/error + EvalDetail | `agent.eval` CHAIN，round/max_rounds/pass；pass=false 不自动标为运行错误 |
| token_budget_warning + BudgetWarning | 结构化事件，包含 context_tokens 和 token_budget |
| Recap | 已完成展示文本对应 AOP fact，不虚构模型调用或耗时 |
| JEV DecisionRequest / DecisionResult | `jev.decision` CLIENT / LLM，自定义 operation `evaluate`、provider `jev`；request_id、purpose、usage、错误；内部 HTTP 重试按 logical_request 计量 |
| JEV Generation | 按 request_id / parent_request_id 建立层次；compiler_round、claim_llm、parameters_llm 为 CLIENT / LLM、operation `chat`、provider `unknown`；validation 为 CHAIN |
| JEV reflex_llm | 编译汇总 CHAIN，usage 仅用 cyber.tokens.*；不再次累计 compiler_round 的标准 token |
| JEV observation / dispatch / result / handoff / library change 等 | 前台为 AOP fact，后台为带来源关联的即时 span |
| guardrail Review pending / resolved | `guardrail.review` 等待 span，action/state/resolution_source；拒绝、过期、取消是策略 outcome，工具 Failure 另行表达 |
| 其他 file / HTTP / 扩展事实 | 附到明确的活动 operation 或 Turn；迟到事实保留来源，不延长已结束 Turn |

`gen_ai.provider.name` 表示客户端接口的 provider dialect。正式 CLI 取宿主配置，通常为 `openai` / `anthropic`；兼容网关的上游供应商不能从模型名推断。嵌入宿主用 `Options.ProviderName` 提供，未提供为 `unknown`。配置在扩展加载时固定，不能反映运行中的 provider reload。JEV 的模型事实缺少 provider/model 信息时保留未知，不借用主 Agent 配置。

JEV 沿用框架约定的 `jev.<purpose>` span 名称；`evaluate` 是自定义 operation，而非 v1.41 的预定义值。请求 effort 使用 `cyber.jev.requested_effort`。成功 span 不写 `error.type`；已恢复的 retry/业务错误保留为事实，terminal failure 才设置 ERROR 和有限的错误分类。工具失败或工具阶段取消不污染已完成的模型响应。

## 关联、后台与重复事件

工具、命令、进程用 operation ID 索引；call ID 的执行上下文只指向真实工具，不被继承同一 call ID 的 command/process 覆盖。已结束操作的 SpanContext 暂存，因此父操作导出后到达的子操作仍可关联。父 Started 从未观察到且无可用上下文时退回 Turn 并标记 parent.missing。

主模型和工具是 invocation 下的兄弟 span。assistant 消息中的真实 tool-call ID 对应触发模型，工具按精确匹配添加 source link；无匹配时不猜最近模型。不同 call ID 的并行工具独立关联。

同步子 Agent 用 SessionStarted.parent_session_id / parent_tool_call_id 查找父工具并加入同一 trace。background delegation 新建 trace，link 到父工具。缺父工具时保留业务父 ID 和 missing 标记。context_mode=fork/fresh 作为属性保存，不等同于前台/后台运行模式。

JEV background=true 不表示源 Turn 仍在执行。后台 request 新建 trace 并 link 到已知源 Turn，compiler rounds 按 parent_request_id 加入编译 trace。源 Turn 未观察到时独立运行并标记 source.missing，不伪造 TurnStarted。完成的 Turn 不因迟到事件重开；迟到 fact 使用即时 span 和来源 link。

标准 OTel links 同时投影 `cyber.source.trace_id` / `cyber.source.span_id` 属性。Phoenix 20.20.0 的 REST/GraphQL Span 接口不暴露 links，因此验收检查 SigNoz 原生 links 和 Phoenix 来源属性，不宣称已验证 Phoenix 原生 link 展示。

event ID、完成 Turn、完成 scope、结束 Session、工具执行/来源 context 缓存各保留最近 4096 个身份。窗口内重复 ID 和重复 completion 不重复导出；缓存淘汰后不保证任意历史重放的全局幂等。消费者按交付顺序处理，保留 Event.seq 属性，当前不实现任意乱序重排。

## 模型计量

普通请求只在对应 LLM span 写入 `llm.token_count.prompt/completion/total` 和 `gen_ai.usage.input_tokens/output_tokens`。Turn 总计只用 `cyber.tokens.*`，避免根和叶重复累计。

主循环一次逻辑调用包含自动重试。首次 llm_request 打开一个 LLM span，在 Usage 到达前的重试记在同一 span 上，`cyber.llm.observed_attempts` 保留观察次数；累计 usage 只写一次，duration 与 token histogram 使用同一逻辑调用单位。span 的结束时间取最后一次有效 assistant 响应，Usage 晚于工具不延长模型耗时。空 assistant 消息代表丢弃的流式响应，清除该结束时间和输出；未见响应时标记 `cyber.llm.end_estimated`。

已经观察到有效响应、但 Usage 前又出现请求时，现有 AOP 无法证明是否属于同一调用，标记 `cyber.llm.grouping.uncertain`。缺少响应且最终无法收尾的请求标记 incomplete。`Usage.Model` 来自配置别名，只能说明 request model；没有真实响应模型事实时省略 `gen_ai.response.model`。

`cache_read`、`cache_write`、`reasoning` 分别投影到 GenAI cache read、cache creation、reasoning token 属性，原 detail 保留在 `cyber.usage.detail.*`。缓存与 reasoning 是 input/output 的子集，不额外相加。`usage_missing` 表示覆盖不完整：部分 attempts 缺失时保留已报告 tokens；全部缺失或没有 Usage 时不写虚构的标准 0 tokens。

compiler_round 是 JEV 编译的模型计量来源；reflex_llm 汇总永远不再次写标准计数，包括汇总先到、round 后到的情况。旧版只有汇总的记录展示 cyber.tokens.*，无法证明原子调用覆盖。decision 和其他 generation 的 usage 口径来自原有事件，不声称已拆分内部 provider attempts。

## 日志、指标与 Agent 生命周期

每个窗口内去重后的非流式 AOP fact 生成一条结构化日志，保存 event ID、seq、Session/Turn、类型和已有执行元数据。scope 开始/结束另外生成投影日志，区分收到的事实与推导的生命周期。日志带唯一 `cyber.log.id`；Turn、模型、工具、机制日志关联相应 trace/span，迟到事实使用保留上下文。Session 开始尚无 Turn 时用 Session ID 关联，不伪造 trace。这不是对任意应用 stdout 或现有 logger 的自动采集。

Session 日志为 `agent.session.started/completed/incomplete`，Turn 为 `agent.turn.started/completed/incomplete`。完成日志的 `cyber.outcome` 表达 success / error / canceled / incomplete；guardrail 另支持 rejected / expired。SessionEnded 立即收尾该 Session 拥有的孤儿 Turn/scope，标记 incomplete，并保留终态防止迟到事实重开。不同 Session 的后台子 Agent 依其自身生命周期结束。Close 对剩余工作收尾。工具失败不会自动把最终成功的 Turn 记作失败。

| 指标 | 口径 |
| --- | --- |
| `cyber.aop.events` / `cyber.otel.events.dropped` | 已消费且去重的事实 / Flush 或 Close 观察到的订阅丢弃 |
| `cyber.agent.sessions` | Session 生命周期转换，按 `cyber.lifecycle.state` 区分 started / completed / incomplete；不能把开始和结束相加当 Session 数 |
| `cyber.agent.sessions.active` / `cyber.agent.session.duration` | 已观察到开始且未关闭的 Session 数 / 生命周期秒数 |
| `cyber.agent.turns` / `.active` / `cyber.agent.turn.duration` | 完成 Turn 数与 outcome / 活跃数 / 秒数 |
| `cyber.scope.completed` / `.active` / `.duration` | tool / command / process / compact / eval / guardrail / JEV 等 scope 的完成、活跃和秒数 |
| `cyber.model.requests` / `cyber.model.usage.missing` | 逻辑模型调用及未报告完整 usage 的调用；自动重试合并，编译汇总不计请求 |
| `cyber.model.tokens` | 模型叶子已报告 usage；`gen_ai.token.type=input/output/total`，总量只筛 total，不能将三个类型相加 |
| `gen_ai.client.operation.duration` / `gen_ai.client.token.usage` | 同一逻辑模型调用的耗时秒数 / input、output token histogram；估算结束时间在 trace/log 标注 |

标准模型指标使用 operation/provider、已知 request model 标签；failed duration 的 `error.type` 与 span 一致。token histogram 只使用 input/output，且只计 LLM 叶子。duration buckets 采用 v1.41 推荐的 `0.01 × 2ⁿ`（n=0…13）秒，token buckets 为 `1 × 4ⁿ`（n=0…13）。

自定义指标补充有限的 scope kind、outcome、state、token type、usage scope，不把 Session/Turn/operation ID 或命令文本放入指标维度。三个信号共享 `service.name`、每个扩展实例的 `service.instance.id` 和 project Resource；SDK 合并 `OTEL_RESOURCE_ATTRIBUTES`，宿主可用 `ResourceAttributes` 覆盖。日志/指标在 span 投影时产生，关闭 trace 采样仍保留生命周期和 usage；此时关联的 trace ID 不保证有已导出的 trace。

设置 `Options.Endpoint` 默认启用三种信号，分别发送至基地址的 `/v1/traces`、`/v1/logs`、`/v1/metrics`。`TraceEndpoint` / `LogEndpoint` / `MetricEndpoint` 是完整 URL，覆盖全局基地址并保留原路径；也可以单独启用任一种信号。嵌入宿主可设 `DisableLogs` / `DisableMetrics` 或传入独立 exporter，扩展拥有其关闭生命周期。MetricInterval 默认 10 秒；Flush / Close 也导出一次。

CLI 的 `OptionsFromEnv` 遵循 [OTLP exporter 配置优先级](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.55.0/specification/protocol/exporter.md)：signal-specific endpoint/protocol 优先全局设置，仅全局 endpoint 追加 signal 路径。目前支持 `http/protobuf`，显式配置其他协议会报错。headers、证书、resource 和 trace sampling 使用 SDK 的环境配置；exporter 与 batch export timeout 固定为 5 秒，不宣称完整透传 OTLP timeout 环境变量。

## 内容、队列与关闭

默认导出关联、执行元数据和 usage。`CaptureContent=true` 才导出消息、机制 output、delegation task 和事件 payload，截断到 4096 字节边界并保持 UTF-8；截断 payload 作为文本，不保证仍是完整 JSON。没有新增自动脱敏或内容分级系统。

input 是最近用户消息，不是 provider 最终有效请求快照。delta 和 raw provider frames 不进入三个信号；辅助机制请求正文没有在现有 AOP 中完整记录。内容开关同样约束日志。

订阅默认 512 条、16 MiB，发布端不等待网络导出。Trace SDK batch queue 2048，队满可阻塞异步消费者；AOP 订阅队满则丢弃，由 Flush/Close 报告 signals incomplete，同时记录丢弃指标和 warning 日志。Log SDK batch queue 2048，饱和时可能丢日志；当前未接入 SDK 内部丢弃计数，因此不宣称在过载时完整无损。三个 checked exporter 保留异步导出错误并通过 Flush/Close 返回。独立 SDK 不改变全局 provider。

Flush 先排空事件，再刷新已完成 spans、日志和指标，不强行结束运行中的任务。Close 排空订阅，将未结束 Session/scope/Turn 标记 incomplete 并减少活跃数，最后关闭三个 SDK。调用者应先关闭生产者与后台工作，再关闭 OTel。

SDK Shutdown 是一次性操作，扩展拥有其清理 goroutine。调用者超时返回 `ErrCloseIncomplete`，清理继续；后续 Close 可以等待同一次清理完成。已完成 shutdown 的 exporter timeout 保留诊断，但不能使 extension.Set 误认为资源仍未释放。Set 也负责部分 Load 失败后的 Close。

## 已验证与当前边界

- 真实 StandardLoop / Session / 工具 / 命令 / 本地进程，模型使用本地 HTTP/SSE 夹具：7 spans、2 LLM 请求、250 tokens、1 个故意工具失败；SigNoz 另收到 39 logs、14 类指标 / 42 序列，两端 trace 与 usage 一致。
- AOP protobuf JSON 回放和失败/取消/未完成夹具：7 traces / 23 spans / 210 tokens，SigNoz 113 logs、15 类指标 / 99 序列；6 个 Turn outcome 为 success=3 / error=1 / canceled=1 / incomplete=1，Session 完成 5、未完成 1。覆盖重试、重复 completion、已完成父操作、同步/后台子 Agent、compact/eval、guardrail、JEV background compiler 和迟到 fact。
- 两端逐项核对机制属性、cache/reasoning、missing、LLM kind、汇总不双计和来源关联；SigNoz 日志关联、histogram sum/count 与 trace 一致，活跃数最终归零。
- focused Go tests、vet、OTel / observe / terminal race tests，以及 CLI 对应构建标签通过。回归覆盖终态错误归属、准确工具来源、Session 孤儿收尾、retry/discarded stream、部分/全部 missing usage、OTLP wire 字段/buckets、独立信号及采样关闭。
- 本轮正式 `cmd/cyber-scan` 用正常 OTel 环境配置和真实 `gpt-6-sol` 执行 AOP 归档分析、Python 自检及工具失败修复，未注入 recording exporter：10 个模型调用、10 个工具调用（4 个失败）、114,242 tokens。SigNoz 158 logs / 29 spans，Phoenix 相同 29 spans；98 个非流式 canonical AOP facts、CLI usage、trace/metric tokens、operation 父子和失败状态逐项一致，任务成功且活跃归零。
- Agent 生成的 analyzer/report 另行核对：从历史销售/SLO/审计归档计算 input=132,272、output=6,025、total=138,297，22 请求、28 calls / 28 results、2 个失败工具。此为被分析的历史输入，不是本轮模型消耗。

回放证明现有事件的转换；本轮真实模型验收覆盖普通主 Agent 和工具。复杂机制的真实模型执行仍需独立验收。运行方式见[示例](../examples/observability/README.md)。

实时看板区分已报告 tokens、工具执行、阶段状态及最终 outcome。它不虚构百分比。当前 StandardLoop 的 usage 在本轮工具结果收集后发布，长工具可能延后该响应的 tokens 和模型投影 active 收尾；active 不保证表示网络请求仍在进行。startup 探测、recap 等辅助模型调用缺少完整 AOP usage，已观察总量不能当成整个进程的模型账单。

正常 CLI 验证暴露了后端查询问题：ClickHouse Map 属性顺序变化可让 `DISTINCT fingerprint,metric_name,attrs` 返回同一 fingerprint 多次，导致查询重复累计。本地验收查询按 fingerprint + metric_name 分组选择一组属性，再取累计末值，已用真实 CLI 的 JSONL、结果和 trace 数量核对；示例 README 提供相应 SQL。此修复不改变 OTel 采集口径。

现有 AOP 不提供主循环每个 attempt 的精确结束、usage、稳定 request ID，也没有独立 Agent Run/Decision 身份。Goal 同一 Turn 内多个 Run 仍投影到同一 Turn，不新造协议身份。compact/eval/recap 辅助模型 usage、完整有效请求快照、首 token、原生搜索计量、跨进程 W3C 传播仍缺少足够事实，转换层无法补造。

当前已有三种客户端信号和本地验收看板。Phoenix 接收 traces；logs / metrics 在 SigNoz 验收。费用账本、生产看板与告警规则未实现；本地看板固定到单次夹具或真实执行，用累计末值检查任务，不作为长期服务的速率看板。

可重复命令见[示例 README](../examples/observability/README.md)，追踪事项见 [Issue #175](https://github.com/chainreactors/cyber-harness/issues/175)。
