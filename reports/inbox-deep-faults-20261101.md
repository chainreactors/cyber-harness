# Inbox 深度故障测试：8 项真实缺陷及修复

日期：2026-11-01。平台：Windows / PowerShell。范围：独立 `aiscan inbox`、持久 Router、subagent / Session、SQLite journal 与输入队列。

这是一份问题发现报告。此前正常场景、对账任务和冒烟链路通过，仍然遗漏了下面 8 项缺陷。本轮先让回归测试在缺陷代码上真实失败，再修复根因；未将正常流程通过当作可靠性结论。

## 发现的问题

严重度是本次工程判断：P1 表示已接收消息可能丢分发、重复执行或恢复语义错误；P2 表示长期运行的资源泄漏。

| 编号 | 严重度 | 触发条件与修复前行为 | 修复 | 回归入口 |
| --- | --- | --- | --- | --- |
| 1 | P1 | 同一消息 fanout 到两个接收方，一个成功，一个返回 `ErrInboxFull`；路由模型随后结束。Receipt 错误地整体成功，失败目标不再重试 | 持久投递也记录每个目标的错误；未完成目标保持 pending，成功目标保留确认 | `TestActiveFanoutDoesNotAcknowledgeFailedRecipient` |
| 2 | P1 | 执行中崩溃，任务已恢复为 interrupted；真正恢复执行前新消息到达。enqueue 把 interrupted 改成 queued，恢复时没有副作用核对警告 | 新输入保留 interrupted 状态，恢复执行仍注入核对说明 | `TestInterruptedTaskKeepsRecoveryStateWhenFollowupArrives` |
| 3 | P2 | 崩溃前 journal 已写入；重启时合入 history；任务正常结束。旧 execution 的 journal 永不删除，每次中断继续积累 | 同一 claim 事务原子写恢复快照、删除已合并的旧 journal、创建新 attempt | `TestRecoveredJournalIsRetiredWithSnapshot` |
| 4 | P1 | 运行中 mailbox 被 64 条同优先级输入填满，再提交 interrupt correction。消息已落盘，首次投递被拒；容量释放后仍不补投递，worker 的后续模型请求无法收到纠正 | scheduler 扫描 running task 时也补投递 SQLite 中的 pending 输入 | `TestActiveMailboxPressureRetriesDurableCorrections` |
| 5 | P1 | 实际自动上下文压缩移除了原始输入；完成时仅从压缩后的 snapshot 识别消费记录，已处理输入被排队再次执行 | 消费判断同时使用恢复历史、最终 snapshot 和本 Run 的 `Result.NewMessages`；保留尚未 drain 的晚到输入 | `TestActiveCompactionDoesNotReplayConsumedInput` |
| 6 | P1 | peer / system / session 来源输入触发真实 Bash 副作用，ToolResult 落盘后强杀宿主。仅 user 来源输入会发 canonical Message，其他来源恢复时被重新追加到 ToolResult 后面 | 所有已 drain 的输入均写 canonical Message，保留 envelope、身份及相对工具结果的顺序 | `TestActiveCrashRecovery/{user,peer,system,session}` |
| 7 | P1 | 并发槽位被占用时，第二个任务积压 71 条输入。原 claim 一次拿走全部，Session mailbox 只能容纳 64，尚未请求模型就 `inbox full` 失败，并将已认领输入结束处理 | 每次 claim 受实际 mailbox 容量限制，预留恢复警告位置；仅认领选中的输入，其余留在 pending | `TestActiveBacklogLargerThanMailbox` |
| 8 | P1 | SessionStart 已注册投递入口，但初始化 seed 尚未入 mailbox；此时收到 64 条 follow-up，抢满容量，最初任务输入无法入队 | 以真实 TurnStarted 标记执行已就绪；seed 完成前，follow-up 只进入持久队列，随后补投递 | `TestActiveFollowupBeforeWorkerReady` |

## 修复前的实际失败证据

以下为本轮测试输出的关键片段；动态 task / execution ID 已省略。

```text
1. partial fanout acknowledged as complete: targets=[session:first] err=<nil>
2. follow-up erased interrupted state and recovery warning: State:queued
3. recovered journal retained forever after snapshot checkpoint: 1
4. durably accepted interrupt stranded after mailbox capacity became available
5. consumed input replayed after context compaction in execution-...
6. recovery missing journal, input order, input dedup, or recovery instruction:
   result=true inputs=1 inputIndex=3 resultIndex=2
7. durable backlog failed before execution: status=failed output=inbox full
8. followups filled mailbox before initial seed: status=failed output=inbox full
```

第 6 项中，user 来源先通过；peer、system、session 三个来源各自失败。修复后四个来源全部通过，恢复后的输入在工具结果之前，且副作用文件各自只有一行。

### 测试本身的校正

- 第 4 项最初用普通优先级填满队列，再提交高优先级纠正。底层 mailbox 会淘汰较低优先级消息，因此该设置没有稳定验证 `ErrInboxFull`。最初断言又只看最后一条 user 消息，会漏掉排在前面的高优先级输入。最终用同优先级填满队列、扫描整个请求识别纠正，并在移除补投递修复后重新跑出红测试，再恢复修复验证通过。
- 第 5 项最初的 8192 context window 配置在请求预算检查阶段失败，没有走到压缩，不能算复现证据。最终通过真实 BeforeModel hook 注入长观察记录，调用实际自动 compactor；测试确认确有 summary 请求后，才断言是否发生第二次执行。修复前确实发生重放，修复后没有。
- fanout 接收方顺序没有公开契约，断言先排序后比较；不把 map / 并行工具的完成顺序当作生产缺陷。

## 修复边界的额外验证

这些测试验证修复没有引入相邻问题，不另外计入 8 项缺陷：

- `TestRecoveredJournalClaimRollbackPreservesEvidence`：通过真实 SQLite trigger，在新 attempt 插入阶段注入失败。事务回滚后，旧 session、interrupted 状态和 journal 均保留；取消注入后重试，历史只恢复一份，旧 journal 正常退休。
- `TestActiveLateUnconsumedCorrectionIsRequeued`：在 RunEnd 阶段、最后一次 drain 之后投递纠正。首次 checkpoint 不得把它算作已消费；后续新 execution 在同一 task 下处理，两个反馈均为 completed。
- `TestActiveFanoutRestartRetriesOnlyUnacknowledgedRecipient`：接收方一成功、二失败后关闭整个宿主，再用同一目录启动；只补发第二方，第一方不重复投递，也不重新请求模型选择路由。
- 积压测试除了核对 71 条输入确实进入模型请求，还读取真实 SQLite，等待该任务 71 条输入全部成为 done，避免把“worker 启动过”或“Provider 看见了输入”当成任务完成。

## 回归结果

以下均为修复生产代码后的本轮结果，不能与此前报告的计数混用：

- 10 个包的 `-race` 矩阵：525 个顶层测试通过，18 个跳过，0 失败。
- 10 个故障/修复边界测试随机顺序重复 10 轮：100 个顶层测试轮次通过，0 跳过、0 失败；包含四种来源的真实强杀与重启。shuffle seed：`1793546687201274900`。
- `full sqlite` 标签下 `cmd/aiscan`：105 个顶层测试通过，4 个跳过，0 失败。
- 加强后的 71 条积压完成检查，另行 `-race -count=5` 通过。
- 新增跨重启 fanout 确认检查，另行 `-race -shuffle=on -count=5` 通过；seed：`1793546801998674300`。
- 包含最终新增断言及重启 fanout 的整个 `exts/inbox` 随机回归：31 个顶层测试通过，5 个跳过，0 失败；seed：`1793546926406807900`。
- subagent 扩展、架构边界、harness、terminal 与 tmux 的补充 `-race` 回归：5 个包、58 个顶层测试通过，30 个平台/依赖条件跳过，0 失败。
- `go vet ./agent ./agent/session ./agent/subagent/sessionexec ./exts/inbox ./exts/ioa/client ./cmd/aiscan` 通过。

18 个跳过项包括显式 opt-in 的真实模型测试、由父测试启动的 subprocess helper、当前平台或构建未安装的能力，均不计为通过。`full sqlite` 中的 4 个跳过项为 CLI subprocess helper、两个 live provider 测试和 record tool 构建能力测试。

## 复现命令

```powershell
# 单项故障和相邻一致性边界；修复版本应全部通过
go test -race ./exts/inbox -run '^Test(ActiveCompactionDoesNotReplayConsumedInput|ActiveMailboxPressureRetriesDurableCorrections|RecoveredJournal|InterruptedTask|ActiveFanout|ActiveBacklogLargerThanMailbox|ActiveLateUnconsumedCorrectionIsRequeued|ActiveCrashRecovery|ActiveFollowupBeforeWorkerReady)' -shuffle=1793546687201274900 -count=10 -timeout 220s -v

# 模型循环、mailbox、Session、subagent、Router、IOA、CLI 和 harness
go test -race ./agent ./agent/inbox ./agent/session ./agent/subagent/... ./exts/inbox ./exts/ioa/client ./pkg/config ./cmd/aiscan ./cmd/harness -count=1 -timeout 180s

go test -tags 'full sqlite' ./cmd/aiscan -count=1 -timeout 180s
go vet ./agent ./agent/session ./agent/subagent/sessionexec ./exts/inbox ./exts/ioa/client ./cmd/aiscan
```

本机 JSON 测试日志在 `$env:TEMP/aiscan-inbox-deep-{regression,stress,full}-20261101.jsonl`；红测试的 pressure、boundaries、startup 日志也保存在 `$env:TEMP/aiscan-inbox-*-red-20261101.log`。本报告保留必要片段，未将机器目录、密钥或运行数据库发布到仓库。

## 仍未覆盖的风险

- 本轮使用可控 Provider 决策，但 SQLite、Session、自动 compactor、工具、IOA 和 CLI 使用实际实现；证明的是指定故障下的基础设施语义，不是模型自主规划质量。此前指定模型端点的连接未成功，真实模型长时任务不能标为通过。
- 副作用完成、ToolResult 尚未落盘的窗口仍有不确定性；恢复警告要求执行者核对现实状态，不能提供任意外部操作的 exactly-once。
- 尚无数小时/跨日耐久性结论；任务、消息、attempt 和已发送 outbox 的长期保留策略、磁盘耗尽恢复，以及峰值吞吐仍需后续验证。本次 journal 退休修复不等于所有持久表均有自动 GC。
- 内部 tmux 是宿主内工作单元管理，不是系统 tmux 的进程保活。宿主退出依赖持久化恢复业务任务，不会保留旧 OS 进程。
- 历史来源已经删除的事件无法补回；outbox 网络发送成功而本地确认尚未提交时，结果可能重发，接收方仍须按稳定 feedback ID 去重。
