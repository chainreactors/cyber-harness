# Inbox 场景验证记录

本记录早于故障深挖。正常链路通过仍遗漏了 8 项缺陷，已复现并修复；问题、红测试证据及修复后回归见 [Inbox 深度故障测试](inbox-deep-faults-20261101.md)。本记录的计数仅代表当时版本。

日期：2026-11-01。环境：Windows / PowerShell。目标是独立的 `aiscan inbox` 子命令；Router 仅负责分发，执行由现有 subagent / Session 承担。

## 验证范围

以下通过项使用真实的 CLI 子进程、HTTP provider 协议、本地 IOA HTTP 服务和 SSE、Session、内部 tmux、Bash、文件系统及 SQLite。离线测试中的模型决策由脚本生成，因此它们验证基础设施及消息处理语义，不代表真实模型的理解和路由质量已经验证通过。

| 场景 | 实际验证 | 结果 |
| --- | --- | --- |
| CLI 完整执行链路 | 子进程运行真实参数解析、配置加载和 Profile；HTTP 模型返回工具调用；3 个独立 worker 通过 Bash 各写入一个临时文件 | 通过，3 个任务 / 3 次执行，文件内容各只有一行 |
| Router 的 tmux 操作 | `tmux ls` 找到常驻 Router；`send-keys` 向 Router 投递第三个任务；`capture-pane` 读取路由日志 | 通过 |
| worker 的 tmux 操作 | 向运行中的 worker `send-keys`；后续请求保留原任务上下文；`tmux kill` 取消执行并产生该任务的 canceled 反馈 | `-race -count=5` 通过 |
| CLI 生命周期 | `/exit` 正常关闭；同状态目录重启；stdin EOF 后继续存活，随后按整体 timeout 关闭 | 通过 |
| 空闲与已完成任务 | 已完成任务重启不重新写文件；就绪后空闲期间没有新的模型请求 | 通过；启动时的 provider 探测请求不计为空闲请求 |
| 状态目录互斥 | 第二个真实 CLI 子进程尝试打开已被占用的状态目录 | 正确拒绝，错误包含 `already owned` |
| 复杂任务交错 | 研究任务未完成时独立文档任务可运行；研究 / 验证分发；高优先级纠正；结果返回协调者；同批重复目标去重 | 通过 |
| 并发上限 | `MaxActive=1` 时第二个任务等待；第一个任务收到纠正后继续；排队任务随后执行 | 通过 |
| 持久任务隔离 | 同一任务重启后保留历史并创建新执行 Session；独立任务不带入前一个任务的历史 | 通过 |
| 消息身份与重复 | 相同来源 / ID 不重复执行；不同 sender 的同名 ID 作为不同输入处理 | 通过 |
| 连续外部监听 | 实际 IOA HTTP / SSE；主动切断连接；整个宿主离线期间继续产生消息；宿主重启补读；重复消息再次提交 | 90 秒轮次通过：341 条源事件、3 次 SSE 连接，没有丢失或重复执行 |
| 历史分页与游标故障 | 300 条来源历史；第 100 条注入游标保存失败；第 200 条注入准入失败；失效游标；空来源 | 通过，失败时不越过缺口，恢复后所有来源事件只准入一次 |
| 路由故障与反馈循环 | 单条持续失败的消息不阻塞健康任务；返回同一任务造成的反馈循环被拒绝 | 通过 |
| 后台任务取消 | 实际启动 `sleep 30`，取消 worker 后验证其拥有的后台作业停止且执行 Session 释放 | 通过 |
| 崩溃恢复 | 真实测试子进程完成 Bash 文件副作用且 ToolResult 已落盘后被强杀；新进程恢复任务并返回结果 | 通过，副作用文件仅一行 |
| outbox 恢复 | 目的地离线时持久化结果；宿主重启后重试发送，不重新运行 worker | 通过 |
| 命令与配置边界 | `agent` 不接受 inbox 专属参数及旧 `--mode`；timeout 默认值及显式覆盖；不兼容 transport 拒绝；加载失败释放锁 | 通过 |

## 重复与构建验证

- 最终 `-race` 回归矩阵覆盖 10 个包：369 个顶层测试通过，8 个按设计跳过，没有失败。跳过项为 5 个需要显式配置的 live 测试、2 个仅由父测试启动的子进程入口，以及 1 个当前构建未链接的原生 record 扩展测试。它们不能计为真实模型验证通过。
- 涉及 Router / IOA 的测试开启 `-race`、随机顺序并重复 5 轮：110 个顶层测试轮次通过，5 个子进程 helper 轮次按设计跳过，没有失败。
- 随机种子：`exts/inbox` 为 `1793544662861464500`，`exts/ioa/client` 为 `1793544662759388700`。
- 新增的 worker tmux 输入 / 取消测试另行开启 `-race` 重复 5 次，全部通过。
- `full sqlite` 构建标签下的 `cmd/aiscan` 回归通过，包含新增 CLI 子进程测试。单独 `full` 不是本仓库要求的完整标签组合。
- 另外运行 `go test -race ./tools/terminal ./exts/tmux -count=1 -timeout 120s`：47 个顶层测试通过，30 个受平台或依赖条件限制的测试跳过，没有失败；跳过项目包括 Windows 上不适用的 Unix shell 测试。
- `go vet ./exts/inbox ./exts/ioa/client ./cmd/aiscan` 通过。

## 真实模型的外部阻塞

已使用用户指定的 `deepseek-flash` 和 `https://sub.chainreactors.cn/v1` 尝试 `TestLiveRouterInterleavedTasks`，首次请求约 21 秒后失败：

```text
POST /v1/chat/completions
dial tcp 8.217.100.148:443: connectex: connection attempt failed
```

后续无凭据连接诊断：直连 HTTPS 连接超时；通过本机已配置代理建立 CONNECT 后 TLS 握手失败。同一代理访问 GitHub HTTPS 成功。当前证据指向该模型端点的连接路径不可用，尚未到达可以验证鉴权、模型名称或模型行为的阶段。

因此，真实模型路由、真实模型连续监控、真实模型 worker 文件任务均不能标为通过。新增 `TestLiveActiveWorkersFileTasksAndRestart` 已纳入显式 opt-in 测试入口，待端点恢复后运行；默认离线回归会跳过 live 测试。测试源码及本报告均不保存用户密钥。

## 复现

```powershell
# 回归矩阵，包含子进程崩溃恢复与 CLI 完整链路
go test -race ./pkg/config ./cmd/aiscan ./exts/inbox ./exts/ioa/client ./agent/session ./agent/subagent/sessionexec ./exts/subagent ./pkg/internal/architecture ./pkg/harness ./cmd/harness -count=1 -timeout 180s

# 打乱执行顺序，多轮验证调度和 IOA 故障路径
go test -race ./exts/inbox ./exts/ioa/client -run '^Test(Active|Router|IOA)' -shuffle=on -count=5 -timeout 180s

# 单独验证 worker 操作
go test -race ./exts/inbox -run '^TestActiveTmuxWorkerFollowupAndCancellation$' -count=5 -v -timeout 90s

# 延长真实 IOA / SSE 监听；仅改变本次 shell 的测试时长
$env:CYBER_ACTIVE_SOAK_DURATION='90s'
go test -race ./exts/inbox -run '^TestActiveIOAMonitorRestartAndContinuous$' -count=1 -v -timeout 3m
Remove-Item Env:CYBER_ACTIVE_SOAK_DURATION

# 完整构建标签
go test -tags 'full sqlite' ./cmd/aiscan -count=1 -timeout 180s

# 网络恢复后：从本机安全环境设置 CYBER_API_KEY，测试不会写入密钥
$env:CYBER_INBOX_LIVE='1'
$env:CYBER_MODEL='deepseek-flash'
$env:CYBER_BASE_URL='https://sub.chainreactors.cn/v1'
# 如果需要显式代理，设置 CYBER_PROXY；Provider 不自动继承 HTTP_PROXY
go test ./exts/inbox -run '^TestLiveRouterInterleavedTasks$' -count=1 -v -timeout 4m
go test ./exts/inbox -run '^TestLiveContinuousMonitorIOARouting$' -count=1 -v -timeout 5m
go test ./exts/inbox -run '^TestLiveActiveWorkersFileTasksAndRestart$' -count=1 -v -timeout 6m
```

本次 JSON 测试日志位于 `$env:TEMP`，文件前缀为 `aiscan-inbox-`，后缀为 `-20261101.jsonl`；`matrix` 是最后的整体回归，`stress` 是随机重复轮次，`full` 是构建标签验证。

## 结论边界

90 秒轮次可以验证连续输入及指定故障注入，不能替代数小时或跨日的运行验证。测试按每次输入等待反馈，未测量峰值吞吐。内部 tmux 管理宿主内的工作单元，不是系统 tmux 服务；宿主退出后依赖持久状态恢复任务，不会保留原 OS 进程。崩溃测试验证的是副作用和 ToolResult 都已经持久化的边界，不承诺对任意外部副作用提供 exactly-once。
