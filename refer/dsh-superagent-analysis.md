# dsh-superagent：常驻 worker 与恢复机制分析

分析日期：2026-11-01。

- 上游：<https://github.com/deepseek-ai/dsh-superagent-prototype>
- 本地参考副本：`refer/dsh-superagent-prototype/`
- 阅读版本：`6337fc823ce20251c8de8085948165c81aaa701e`，浅克隆；未修改上游源码。
- 本次范围：源码与测试设计分析、aiscan 现有 Session 行为验证；未启动上游生产服务。

## 结论

这个原型采用的是**常驻宿主 + 每个 Ticket 持久化的 Session/workspace + 可重新调度的 Assignment**。worker 保留会话历史、待收消息和工作目录，不能称为完全无状态的 worker 池。原型中明确称为 stateless 的是 Policy 对象（`docs/007-architecture.md:138`）。

如果“无状态”指执行实例退出后可以重建，那么它通过持久化任务和会话实现了这种效果；它没有恢复正在运行的 shell、模型请求或调用栈。

## 1. tmux 托管的是整个宿主

入口：[scripts/dsh-superagent](dsh-superagent-prototype/scripts/dsh-superagent)，`start()`，第 159 行。

启动脚本在真实系统 tmux 中运行 `dsh --profile <profile> --no-open`：

- tmux Session 名为 `dsh-<profile>`，复用已有 Session 时创建 `server` 窗口。
- 使用 `exec` 替换 shell，取得宿主 PID；保存日志与 `service.pid`。
- 启动前检查进程及管理接口，避免重复启动；支持 stop/restart/status。
- 人工停止有单独标记，避免恢复逻辑立即把主动停止的服务重新拉起。

因此 tmux 解决终端断开后的进程存活，不负责逐个 worker 的暂停、序列化或重启恢复，也不提供机器重启后的进程快照恢复。脚本使用 `fcntl`、POSIX shell、`ps` 等，不能直接在本次 Windows 环境原生运行。

## 2. worker 生命周期按 Ticket 固定

入口：[src/executor/dsh.ts](dsh-superagent-prototype/src/executor/dsh.ts)，`workerSessionId()` 第 24 行、`launch()` 第 424 行；[src/app/sessions.ts](dsh-superagent-prototype/src/app/sessions.ts)，`ensure()` 第 138 行。

1. 一个 Ticket 对应一个稳定的 `worker-${ticketId}` Session，以及独立工作目录。
2. `ensure()` 优先解析已有 Session，只有不存在才创建，随后应用 worker preset、模型及工具。
3. 第一次 Assignment 发送任务和来源上下文；后续 Assignment 检查历史是否已包含首次消息，通常只发送增量。
4. worker 本轮结束后可以 idle；后续输入继续进入同一个 Session，保留此前上下文和 workspace。
5. 并发槽位限制当前活动工作；没有后台工作的 idle Session 可以释放槽位，持久化 Session 并不等于一直占用模型调用。

这不是每次消息启动一个完全空白进程，也不是固定数量进程轮流租赁任意任务。

## 3. 常驻监听与模型执行分开

入口：[src/app/passes.ts](dsh-superagent-prototype/src/app/passes.ts)、[src/sources/github.ts](dsh-superagent-prototype/src/sources/github.ts)、[src/sources/poll.ts](dsh-superagent-prototype/src/sources/poll.ts)、[src/executor/dsh.ts](dsh-superagent-prototype/src/executor/dsh.ts)。

- 宿主负责外部事件监听、轮询、游标、重试和调度；模型按事件被唤醒。
- `steer` 把输入送入当前/后续步骤，`followup` 排入下一轮，`inject` 只记录历史而不唤醒。
- Executor 监听 `agent/status`、`agent/inbox/claimed`、`session/event`、`agent/error`，推进 worker 生命周期。
- 每两秒运行的 passes 是宿主调度检查，并不意味着每两秒请求一次模型。
- GitHub 的轮询游标、回看重叠区间和去重记录，以及飞书历史游标/已见消息记录，用于补齐离线期间事件；只重连连接不能替代缺口补偿。

## 4. 长命令通过后台作业让出前台

入口：[cordis.patch.yml](dsh-superagent-prototype/cordis.patch.yml)、[src/executor/background.ts](dsh-superagent-prototype/src/executor/background.ts)、[src/executor/dsh.ts](dsh-superagent-prototype/src/executor/dsh.ts)，`workerIdle()` 第 601 行。

worker preset 把 shell 与 jobs 的单次前台等待设为 5 秒。长命令转为宿主管理的后台 job，前台调用返回；这不是到 5 秒就杀死命令。后台任务或子 agent 完成后，通过事件使同一 worker 继续处理。

`untilQuiet()` 汇合 shell jobs 与子 agent；监听完成/移除事件，避免每段输出都触发轮询。worker idle 后仍有后台工作时通常保留活动槽位，并有 30 分钟后台结算限制：超限 shell jobs 会被结束并记入 Session；子 agent 的处理有所区别。向人交接时可以先释放槽位，留下预览服务等 jobs；Assignment 结束后再清理所属后台工作。因此这里有明确的作业所有权和清理边界，不是所有 shell 命令都可以无限运行。

## 5. 状态放在哪里

| 状态 | 保存位置 / 机制 | 用途 |
| --- | --- | --- |
| Trigger、Thread、Ticket、Assignment、Decision | `<stateDir>/orchestrator.sqlite` | 业务事实和任务进度 |
| Outbox 与 Effects | 同一 SQLite；事务内入队，提交后执行 | 待发送消息、待执行副作用与重试 |
| Session 历史及待处理 inbox | DSH Session persistence；投递后 flush | 会话恢复与投递去重 |
| 工作目录、文件和 git 状态 | Ticket workspace | 继续处理实际任务产物 |
| 外部监听游标、已见消息 | KV 中的 source 状态 | 重连补偿及去重 |
| 执行中的对象、槽位、临时 Promise | 宿主内存 | 重启时重新建立；不做运行栈快照 |

入口：[src/app/store.ts](dsh-superagent-prototype/src/app/store.ts)、[src/app/effects.ts](dsh-superagent-prototype/src/app/effects.ts)、[src/app/delivery.ts](dsh-superagent-prototype/src/app/delivery.ts)、[src/kv.ts](dsh-superagent-prototype/src/kv.ts)。

SQLite 使用 WAL 与 `synchronous=FULL`；stateDir 有进程锁。Effects 持久化待执行工作，带顺序、代次和失败重试。AgentDelivery 使用稳定消息 ID，检查已消费历史和待处理 inbox，投递后 flush Session，完成后才确认成功。消息投递去重不意味着外部 API 副作用也能严格 exactly-once。

## 6. 重启恢复的是业务进度

入口：[src/app/runtime.ts](dsh-superagent-prototype/src/app/runtime.ts)，`initialize()` 第 201 行、`shutdown()` 第 230 行；[src/executor/dsh.ts](dsh-superagent-prototype/src/executor/dsh.ts)，`resume()` 第 524 行；[src/core/assignments.ts](dsh-superagent-prototype/src/core/assignments.ts)，`interrupted()`。

- 初始化存储、Session hooks 和 Executor 后，按顺序扫描未结束 Ticket。
- 正在执行的旧 Assignment 标记 interrupted；必要时取消残留执行并保留 inbox。
- 活动任务生成带 recovery note 的新 Assignment，复用原 Session/workspace；要求 worker 先核对文件、git 和外部操作结果，防止重复执行。
- 已处于 waiting/ready/blocked/failed 且没有更新输入的任务保持等待，不盲目重跑。
- 正常关闭先停止接收/调度、取消活动轮次并保留 inbox，再清理后台工作、flush Session、关闭 SQLite 和释放锁。

核心是 reconciliation：通过已记录事实重建下一步执行；并非从被中断的机器指令或模型 token 原地继续。

## 7. 对 aiscan 的最小接入建议

本节记录接入前的源码分析。后续已选择“稳定任务历史 + 可释放的 subagent 执行实例”：由独立 inbox 子命令中的 Router 和 scheduler 组合已有 Session 后端，未采用每任务永久保留一个 live Session。当前实现、恢复契约和测试见 [架构说明](../docs/architecture.md#inbox-与后台生产者)与[扩展文档](../docs/developer/extensions.md#inbox-路由-agent)，以下“目前”均指分析时的基线。

保持当前职责：`外部监听/IOA → profile Inbox Router → 目标 Session 的 inbox → worker`。

- **总线与邮箱同时存在。** `exts/inbox` 作为 profile 级统一路由入口；`agent/inbox` 仍是每个 Session 自己的收件箱。多 profile / 多进程的全局总线是额外部署边界，不能由一个内存 Router 自动获得。
- **沿用已有 Session 调度。** 普通 Session 已支持 idle 输入自动启动下一轮，以及运行期间输入转向；不需要另造一套轮询模型的 worker 框架。
- **区分一次性子任务与常驻 worker。** `agent/subagent/sessionexec/tool.go:120` 当前打开 `Attached:true, SingleTask:true` Session；常驻 worker 应采用独立、非 SingleTask 的 Session 生命周期，明确由谁关闭、如何恢复以及并发槽何时释放，不能仅靠 tmux 包住现有调用。
- **补齐 Router 的重启语义。** `exts/inbox/extension.go` 的队列、receipt 和去重目前保存在内存。长监听已具备事件唤醒基础；若要求服务重启不丢任务，需要把待路由消息、稳定消息 ID、目标投递结果和待执行副作用持久化，并明确何时确认上游已接收。
- **监听器独立管理连接与游标。** IOA 重连、断点补读、重复消息去重由 source/transport 层承担；Router 负责识别并分发，业务执行及执行进度由 worker/Session 承担。
- **tmux 作为部署选项。** Linux/WSL 可把整个 aiscan 宿主放进系统 tmux。项目内 `tools/terminal/tmux.go` 管理的是命令任务，不会自动让宿主 Agent 在退出后继续存活。可靠重启仍需要进度持久化与恢复逻辑。

建议先验证“一个稳定 worker Session 连续处理输入、空闲不调用模型、宿主重启后恢复未确认投递”，再考虑多 worker 调度；复用已有类型和存储边界，不直接移植整个原型框架。

## 8. 验证记录与边界

2026-11-01，在 aiscan 执行以下现有行为测试，结果通过（`agent/session`，0.150s）：

```powershell
go test ./agent/session -run 'Test(SingleTaskDoesNotScheduleLateInput|IdleAsyncInputCreatesAutomaticRun|ActiveRunSteersAsyncInputWithoutSecondLifecycle)$' -count=1
```

分别覆盖 SingleTask 不自动调度、idle 输入自动创建新轮次、运行中输入转向而不建立第二套并发轮次。这只能证明当前 Session 的相关行为，不代表已实现宿主崩溃后的消息持久化恢复。

已阅读上游 `tests/delivery.spec.ts`、`tests/background.spec.ts`、`tests/runtime.spec.ts`、`tests/effects.spec.ts`、`tests/service-script.spec.ts` 的相关案例，关注投递 flush 失败重试、后台任务完成、运行中重启和未读消息保留。本次未执行上游 Vitest：本地未安装其依赖与 pnpm；也未安装 dsh/tmux，Windows 原生环境不满足服务脚本依赖。未进行真实 GitHub/飞书外部联调。
