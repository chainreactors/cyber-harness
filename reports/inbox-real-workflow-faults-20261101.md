# Inbox 真实仓库工作流：失败续接与跨节点结果重试

日期：2026-11-01。平台：Windows / PowerShell。阶段：内部验证。

按本轮要求，GitHub issue #192 已关闭，读取 API 确认状态为 `CLOSED`。本报告和新增测试仅保留在本地，不新建 issue、不发布评论、不推送代码。

本轮使用真实仓库 CI 与发布审计作为业务负载，发现并修复两个额外缺陷。结合[上一轮八项故障](inbox-deep-faults-20261101.md)，累计已有十项先复现失败、再修复的缺陷。这里的“真实”指实际 Agent Loop、Session、Bash、Go/git 命令、SQLite 和本地 IOA HTTP/SSE；Provider 选择工具及故障时点仍由测试控制，不代表真实模型自主规划能力已验证。

## 案例一：CI 执行中追加检查，模型请求恰好失败

入口：`exts/inbox/repository_ci_test.go` 的 `TestRepositoryCIContinuationAfterProviderFailure`。

1. CI task 的 worker 通过真实 Bash 运行仓库 `agent/inbox` 的 `TestInterruptAdmissionAndDrain`，保存 `go test -json` 原始输出。
2. 工具成功返回后，阻塞下一次模型请求。
3. 同时启动独立 release-notes task，通过真实 `git log -1 --format='%H%n%s'` 保存当前 revision 和提交标题。它必须在 CI 仍阻塞时完成，验证并发调度。
4. 模拟 CI monitor 追加一条 peer 输入，要求再执行 `agent/session` 的 `TestDeliveryGatesRoutingAndCapacity`；输入持久准入后才释放阻塞，注入模型连接失败。
5. 核验第一次反馈为 failed，随后同一 task 使用新 execution 处理尚未读取的追加要求；恢复历史包含第一条实际工具结果，不能重跑已完成的 Go 测试。
6. 独立解析两份 Go JSONL，分别要求目标测试和 package 各有一次 pass，且没有 fail；实际 git 产物也必须与当前 HEAD 一致。
7. 关闭宿主、用同一 SQLite 目录重开，再提交相同 monitor 输入，验证不新增执行。

### 缺陷 9：失败结束误消费未读追问

修复前，checkpoint 对所有非 completed 状态都消费整个 `ex.inputs`。已经落盘但尚未 drain 的追加输入因此被标为 done，Session suite 永远不执行。

真实红测试输出：

```text
accepted CI scope update never executed after model failure:
task=failed acknowledged_inputs=2
```

修复：`exts/inbox/active.go` 只确认 history、snapshot 或 `Result.NewMessages` 中实际存在的输入；显式取消保留终止已认领输入的行为。failed execution 未读输入重新排队。反馈链也从实际确认的输入构建，避免失败反馈声称处理了未读来源。

修复后，该 CI task 有两次 attempt：一次 failed、一次 completed；初始输入和 monitor 更新都为 done。mailbox suite、Session suite、独立 git 审计三条命令各执行一次。未读追问的 ancestry 不出现在第一次 failed 反馈中，而出现在后续真正处理后的 completed 反馈中。

## 案例二：接收方已写审计，发送方丢失确认

入口：`exts/inbox/delivery_retry_test.go` 的 `TestRepositoryCIResultDeliveryRetryIsIdempotent`。

1. producer worker 执行真实 `git rev-parse HEAD`，生成当前仓库 revision。
2. durable outbox 的 `Notify` 经实际 IOA SDK、HTTP server 与 SSE，将结果送到 collector Inbox。
3. collector worker 使用实际 Bash 将 revision 追加到 `release-audit.txt`，等待其任务完成并提交状态。
4. 此时向发送方注入“接收方已提交，但发送确认丢失”的错误，让 outbox 真实重试。重试产生不同的 IOA wire ID，但保留相同的逻辑 `feedback_id`。
5. 在队列、执行和 outbox 均静止后，要求两次发送仅有一个 collector 输入和一条实际审计记录；Receipt 仍引用首次 wire ID。
6. 通过相同 HTTP/SSE 链路分别加入三个独立事件：另一 sender 的同 feedback ID、同 sender 的普通 `ci_notice`、另一 source_session 的同 result ID。每个都必须分别执行，最终有四条实际审计记录。
7. 关闭完整 collector IOA graph，再用同一 SQLite 重开宿主，重投此前捕获的第二条实际 envelope。等待恢复工作全部静止，核验 attempt 数及实际审计内容不变。这一步验证持久准入重放，不声称重新建立了一轮 IOA SSE 连接。

### 缺陷 10：跨节点结果重试重复执行

修复前，IOA adapter 丢弃 `feedback_id`，Router 只按新的传输 ID 去重。接收方把同一结果当成两个不同输入，审计被追加两次。

真实红测试输出：

```text
same CI feedback executed twice after acknowledgement loss:
inputs=3 audit_commands=2 build_commands=1 sends=2
```

其中 inputs 包含一个 collector 初始化输入；实际审计文件内有两行相同的真实 revision。

修复：`exts/ioa/client/ioa.go` 保留输入的 `feedback_id`；`exts/inbox/extension.go` 对 peer 的 `kind=task_result` 使用 sender/source_session 作用域内的稳定 feedback 身份准入。原始 wire ID 仍保留在 metadata 和 Receipt，不改变回复引用。普通消息仍使用自身传输 ID，未改数据库 schema。

## 实际产物与数据库复核

最终单次验证的产物位于本地 `outputs/inbox-real-cases-20261101-verified/`：

- `repository-ci-538136094/`：`mailbox.jsonl`、`session.jsonl`、`revision.txt`、`case-summary.json`、`state/active.sqlite`。
- `result-delivery-701678749/`：`producer-revision.txt`、`release-audit.txt`、`case-summary.json`、`producer-state/active.sqlite`、`collector-state/active.sqlite`。

两项案例读到的 HEAD 均为 `e03699e3836e71c0a32ca0d8d3461f88c759b545`；运行时的修复在该提交之上的工作树中，不能把 HEAD 当成包含本轮修复的提交。

用独立 Python sqlite3 直接读取保留数据库，得到：

| 数据库 | Tasks | Inputs | Attempts | Outbox | Messages | Journal events |
| --- | --- | --- | --- | --- | --- | --- |
| CI + notes | completed 2 | done 3 | failed 1 / completed 2 | sent 3 | done 6 | 0 |
| collector | completed 1 | done 5 | completed 5 | sent 5 | done 10 | 0 |
| producer | completed 1 | done 1 | completed 1 | sent 1 | done 2 | 0 |

CI 表中的三条输入包含独立 notes task 的一条输入；CI 自身只有两条输入、两次 attempt。collector 的五条输入包含初始化和四个逻辑结果。两条不同 wire ID 的重试 pair 只产生一次审计，另外三个独立事件各产生一次，最终 `release-audit.txt` 恰好四行，均与采集时的仓库 revision 一致。上述数据库均无 pending/running 输入或待发 outbox。

### 对测试断言的进一步收紧

首次检查保留数据库时发现 collector 的最后一条内部反馈仍在 pending；原测试在业务完成后立即结束，没有等待异步反馈排空。这不能证明生产丢消息，也不能算完整收尾验证。最终测试在重启重投之后也等待 messages、inputs、tasks 和 outbox 静止，并再次核验实际审计内容；上述最终数据库的十条 collector 消息已全部 done。

三个独立事件除了核对工具调用计数，还检查实际审计文件的四条内容，并将真实 Bash 错误作为测试失败，避免把“模型发出工具请求”当作“业务操作完成”。

## 回归结果

- 两项新增案例随机顺序重复 10 轮：20 个顶层测试轮次通过，0 跳过、0 失败，启用 `-race`；seed `1793548156885572700`。该轮已包含四条实际审计内容检查；随后增加的最终异步收尾等待在最终产物验证中通过。
- 完整受影响矩阵：10 个包，528 个顶层测试通过、18 个跳过、0 失败，启用 `-race`。
- `full sqlite` 标签下 `cmd/aiscan`：105 个顶层测试通过、4 个跳过、0 失败，启用 `-race`。
- 最终产物验证：两个新增案例均通过，启用 `-race`；已直接读取实际文件和 SQLite 复核。
- 包含最终收尾断言的整个 `exts/inbox` 随机回归：33 个顶层测试通过、5 个跳过、0 失败，启用 `-race`；seed `1793548219644972500`，日志 `aiscan-inbox-real-cases-final-package-20261101.jsonl`。
- `go vet` 受影响的十个包通过；`git diff --check` 通过。

跳过项包括显式 opt-in 的 live model 测试、Windows 不支持的 Unix 交互场景、由父测试启动的 subprocess helper，以及当前构建未链接的 record 能力。跳过不计为通过。完整矩阵日志：`$env:TEMP/aiscan-inbox-real-cases-regression-20261101.jsonl`；重复案例日志：`aiscan-inbox-real-cases-final-20261101.jsonl`；带标签日志：`aiscan-inbox-real-cases-full-20261101.jsonl`；最终产物验证日志：`aiscan-inbox-real-cases-verified-20261101.jsonl`。

## 复现

```powershell
# 运行两项真实仓库案例并保留证据
$env:CYBER_INBOX_CASES_OUTPUT = Join-Path (Get-Location) 'outputs/inbox-real-cases'
go test -race ./exts/inbox -run '^TestRepositoryCI' -shuffle=1793548156885572700 -count=10 -timeout 180s -v
Remove-Item Env:CYBER_INBOX_CASES_OUTPUT

# 受影响包与实际命令入口
go test -race ./agent ./agent/inbox ./agent/session ./agent/subagent/... ./exts/inbox ./exts/ioa/client ./pkg/config ./cmd/aiscan ./cmd/harness -count=1 -timeout 180s
go test -race -tags 'full sqlite' ./cmd/aiscan -count=1 -timeout 180s
go vet ./agent ./agent/inbox ./agent/session ./agent/subagent/... ./exts/inbox ./exts/ioa/client ./pkg/config ./cmd/aiscan ./cmd/harness
```

## 验证边界

- 指定模型端点本轮重探仍在 TLS 握手失败，未成功调用真实 LLM。测试没有把连接失败回退伪装为 live 模型通过；可控 Provider 的基础设施验证与模型规划、路由质量是两个结论。
- 结果消息准入去重不等于任意外部副作用 exactly-once。副作用已发生、工具结果尚未持久化的崩溃窗口仍需核对现实状态。
- 本轮不是数小时或跨日 soak；磁盘耗尽、长期持久表保留策略、持续模型成本和真实外部监控源速率仍需进一步实验。
- worker 使用宿主内部 Manager / tmux 托管；宿主重启恢复的是持久任务进度，不保留旧 OS 进程。
