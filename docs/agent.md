# Agent 使用指南

[使用者指南](README.md#使用者指南) · 前一篇：[快速开始](getting-started.md) · 下一篇：[会话与上下文](user/sessions.md)

Agent 接受任务，向模型提供当前上下文与工具，然后根据模型的选择执行、读取结果并继续。这使它能处理事先无法写成固定步骤的工作。一次任务可能包含多次模型请求和工具调用，最后一段回答只是这个过程的结果。

## Inbox 常驻路由

`aiscan agent` 由 prompt／REPL 驱动。长期处理消息流使用独立的 `inbox` 子命令：

```sh
aiscan inbox --state-dir .cyber/active --max-active 4
aiscan inbox --ioa-url http://127.0.0.1:8765 --space team -p "分析当前待处理告警"
```

`inbox` 的初始 prompt 和后续每行输入都交给常驻 Router；Router 只做分发，由 subagent 执行任务。外部监控通过 IOA 或嵌入 API `Router.Submit` 接入。无消息时不请求模型，stdin EOF 只断开输入，仍继续监听；输入 `/exit` 或取消宿主会停止准入并排空资源。`inbox` 默认整体超时为 0，可用 `--timeout <秒>` 显式限制，目前只支持 local transport。

内部 `tmux` 同时管理 Router 与当前执行实例，不要求安装系统 tmux：

```text
!tmux ls
!tmux capture-pane -t <工作单元 ID>
!tmux send-keys -t <工作单元 ID> "补充输入" Enter
!tmux kill -t <执行实例 ID>
```

向执行实例输入会关联其稳定 task ID；向 Router 输入则重新判断目标。停止 Router 会结束主动宿主。执行实例结束后释放，后续定向消息可恢复该任务历史并创建新实例。任务历史隔离，文件与工作目录共享。`.cyber/active` 保存任务、路由、结果和 IOA 游标；重启须使用相同目录，一个目录只能由一个宿主持有。内部 tmux 随宿主退出；再次启动恢复任务进度，不恢复旧 OS 进程。副作用中断后的核对与来源历史保留限制见[架构说明](architecture.md#inbox-与后台生产者)。

Worker 可单独调用 `inbox_wait` 等待消息；自动等待后台 producer 也使用相同生命周期。等待期间保留原执行和 tmux 工作单元，释放 `max-active` 名额；消息到达或等待超时后，先重新取得名额再继续，避免一批 Worker 同时唤醒突破上限。这是协作式挂起，不会暂停正在运行的 shell 命令或任意 OS 进程。等待执行仍占内存，`max-active` 也不限制 Worker 自行创建的子 Agent 数量。

Worker 完成反馈必须回复原请求者，或交给已有协调任务继续处理；对本地 `subagent_feedback` 选择 `record` 也会进入结果 outbox。普通系统事件和无需继续处理的远端 `task_result` 仍可只记录。发送失败保留 outbox 并在重启后重试。

配置 IOA 后，subagent Session 的开始/结束会自动记为 delegate/return；active inbox 的 Router 每次向 task 或 live Session 投递也会自动写入 IOA handoff 记录，包含输入、来源、目标与稳定 handoff ID。它覆盖 Router 的初次派发、后续纠正和 worker 间转交，无需 agent 再调用 `ioa send`。记录使用现有 SQLite outbox，IOA 不可用时会重试；网络确认丢失时允许重发，使用 handoff ID 识别同一逻辑交接。

## 交互模式

配置模型后，运行：

```sh
aiscan agent
```

先输入 `/status` 查看当前模型与工具，再输入任务。例如让它只读查看当前目录、说明各模块职责，然后追问某个文件。交互模式会保留已有对话，后续输入可以引用之前的结果。

`/help` 显示当前宿主实际安装的命令。需要直接执行已知命令时使用 `!` 前缀，例如 `!gogo -h`；此时不经过模型选择，仍使用当前运行环境。工具调用可能访问文件、启动进程或发送网络请求，任务说明应明确范围和预期产物。

## 一次性任务

把任务作为启动参数传入，Agent 完成后退出：

```sh
aiscan agent -p "只读查看当前目录并解释主要模块"
aiscan agent --task-file task.md
aiscan agent -p task.md
```

`-p` 指向已有文件时读取文件内容。任务文本依次取 prompt、task-file、非空 stdin；只有 `-i` 目标时生成默认任务，目标也会附加到选中的任务文本中。输入不是把所有来源按任意顺序拼接。

```sh
echo "只读检查当前目录" | aiscan agent
aiscan agent -p "检查已授权本地靶场，保留证据" -i http://127.0.0.1:3000
```

自动化中可用 `-o json` 获取最终结果，或用 `-o jsonl` 消费流式事件。`-o jsonl -f run.jsonl` 保存可恢复的历史记录；完整区别见[会话与上下文](user/sessions.md#记录)。

## 任务推进与控制

模型每轮可以直接回答，也可以请求工具。同一轮的多个工具调用可能并行执行；需要前一步结果的工作进入后续轮次。工具失败会作为结果交回模型，模型可以修正输入后继续，因此一次错误不一定结束整个任务。

`/stop` 请求停止当前工作，`/continue` 基于已有上下文继续，`/followup <任务>` 在当前任务完成后提交新输入。停止不会撤销已经发生的外部修改。后台命令与子 Agent 尚未返回时，Agent 可以等待它们的结果；查看执行状态见[工具与环境](user/tools.md)。

本地 Console 的 Ctrl+C 分阶段处理：先停止当前工作，再取消根上下文退出，最后强制退出；连续按键间隔超过 5 秒会重置计数。`!` 命令使用独立取消路径。

自然结束、达到轮次限制和用户取消是不同状态。需要判断自动化任务是否达成目标，应同时读取结束状态、最终结果和证据；内部停止条件见[Agent 运行时](architecture.md#停止条件)。

## Goal Evaluation

目标评估为任务增加一个反馈循环：Agent 完成一轮工作后，评估器根据验收标准检查轨迹与输出，需要继续时把反馈交回 Agent。

```sh
aiscan agent -p "核对当前目录中的报告" -e "每个结论都有来源；明确列出未验证事项" --eval-rounds 3
```

验收标准应描述可检查的结果。交互模式用 `/eval <标准>` 设置，`/eval off` 关闭。评估请求独立于执行请求，默认仍使用当前 Provider 和模型，因此不能将它视为另一套独立事实来源。

`--eval-rounds` 的数字限制评估轮数，自然语言则提供节奏指导；默认硬上限为 20。评估器也可以认为未达标但已无法继续，此时正常收尾不等于通过验收。返回字段、重试和上下文继承见[评估机制](architecture.md#goal-evaluation)。

## Agent 与扫描

直接 `scan` 按规则推进扫描流水线，Agent 则根据模型响应选择下一步。单扫描器的顶层 `--ai` 入口把命令、参数和意图组织成 Agent 任务：

```sh
aiscan --ai -p "解释结果并注明证据" gogo -i 127.0.0.1 -p 80,443
```

这里由 Agent 调用工具并分析，不能假定宿主先确定性执行一次扫描器再总结 stdout。`scan --verify` 属于扫描结果的后续验证，当前能力与限制见[扫描指南](scan.md)。

## 知识与协作

工具集合由发行版和扩展决定；Skills 为这些能力提供领域方法。选择自定义知识时使用 `-s review`，文件结构与加载顺序见[Skills 与知识](user/knowledge.md)。

`--ioa-url` 只增加消息协作连接：`agent -p` 完成一次任务后退出，`inbox` 则继续监听。长期协作、Web 工作台及远程执行节点见 [Web 与协作](user/web.md)。跨语言宿主使用的 stdio/AOP 属于[开发者接入](developer/hosting.md)。

下一篇[会话与上下文](user/sessions.md)解释连续输入、压缩、记录和恢复，让长任务可以被管理与复核。
