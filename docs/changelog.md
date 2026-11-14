# Changelog

本页保留当前版本的用户可见变化。历史版本的说明见 [GitHub Releases](https://github.com/chainreactors/cyber-harness/releases) 和对应 Git tag。

## v1.0.0-rc10

rc10 增加可恢复的常驻 Inbox，让后台任务与跨节点反馈更可靠；完善 Web 执行复盘和长会话回放，修复连续恢复会话、命令等待输入及 Windows 快速连接的问题。发布入口统一为 Web Hub、扫描节点和审计节点，升级时需同步调整二进制名称与输出参数。

### New Features

- **让 Agent 持续接收和处理工作。** 新增 `cyber-scan inbox` 常驻路由，将用户输入、监控事件与子 Agent 反馈分配到独立任务；同一任务串行处理，不同任务可并发执行，等待 Inbox 消息时释放执行名额。任务历史、未处理输入、结果 outbox 与来源游标保存在本地状态目录，进程重启后可继续处理。启动、并发和恢复方式见 [Inbox 指南](https://github.com/chainreactors/cyber-harness/blob/v1.0.0-rc10/docs/agent.md#inbox-常驻路由)。

### Improvements

- **更容易看清每一步实际做了什么。** Web 的“执行复盘”保留各次模型调用、推理和工具步骤，可按执行顺序或 Token 用量排序，点击定位原始卡片；区分工具已返回、失败、进行中和未收到结果，展示已有耗时、缓存命中率及包含重试的请求次数。子 Agent 和后台步骤标明执行者，重连后仍可回看中间过程。
- **长会话回放减少一次性加载。** 历史事件按游标分批读取，再衔接实时更新；过滤后的空页不会截断后续记录，断线恢复继续使用已收到的位置。
- **扫描、Agent 与回放共用输出方式。** 使用 `-o <格式>` 选择 `text`、`json`、`jsonl`、`markdown` 或 `csv`，用 `-f <文件>` 指定目标，省略文件则输出到 stdout。CSV 可导出产物与验证状态，Markdown 便于阅读和交付；原生扫描器的终端文本不会混入所选事件输出。示例见[会话与记录](https://github.com/chainreactors/cyber-harness/blob/v1.0.0-rc10/docs/user/sessions.md#记录)。

### Bug Fixes

- **修复会话恢复后无法再次恢复。** 连续 `--resume` 或交互式 `/resume` 保存的新 JSONL 包含完整恢复上下文快照，可独立用于下一次恢复；源文件保持只读，历史不会重复加入。修复 [#201](https://github.com/chainreactors/cyber-harness/issues/201)。
- **修复普通命令空等输入。** 前台 `read`、`cat` 等命令默认收到 EOF，不再因无人提供 stdin 一直等到超时；需要交互时，仍可显式使用后台等待和 `tmux send-keys`。
- **修复后台反馈遗漏或重复。** Inbox 保留运行期间到达的未读更新，对同一结果的重试去重；任务恢复和跨节点反馈沿用正确的来源与任务地址，避免反馈进入其他任务或触发反复转发。
- **修复 Windows 快速连接解压失败。** 下载接入命令兼容 Windows PowerShell 5.1，避免本机 `Expand-Archive` 模块与 .NET 版本不匹配导致节点无法启动。
- 修复模型用量在重试和父子汇总中重复累加的问题；未报告的用量保留未知，完成评估与工具实际结果分别展示。
- 修复扫描器原生参数与全局参数相互干扰的问题，保留子命令后的原生参数及其顺序。

### Architecture And Compatibility

- **升级时更换下载项和启动命令。** 发布 `cyber-harness`（Web Hub）、`cyber-scan`（扫描与通用 Agent 节点）和 `cyber-audit`（审计节点），替代原 `aiscan` / `aiscan-full` 下载项。Web 使用 `cyber-harness --token <访问密钥>` 启动，再通过快速连接或 `cyber-scan agent --server-url ...` 接入执行节点；`cyber-scan` 不再内置 Web 宿主。三个产品覆盖 Linux、macOS、Windows 的 amd64/arm64，共 18 个 ZIP 与校验文件。完整流程见[入门指南](https://github.com/chainreactors/cyber-harness/blob/v1.0.0-rc10/docs/getting-started.md)。
- **旧输出命令需要调整。** 原 `-o run.jsonl` 改为 `-o jsonl -f run.jsonl`；回放同样使用 `-o/-f`，移除回放专用格式参数。`stream-json`、`md` 分别保留为 `jsonl`、`markdown` 的别名。gogo 等原生扫描器子命令之后的参数仍按该工具自身语法解析。
- Inbox 目前在本地宿主运行，不与 Web、stdio transport 或 `--server-url` 组合；默认状态目录为 `.cyber/active`，同一目录只允许一个宿主持有。恢复使用整个状态目录，不能以 Agent 的 JSONL `--resume` 替代；任务共享宿主工作目录，任务历史隔离不等于文件隔离。

### Known Limitations

- 崩溃恢复可以重新处理未完成工作，不能撤销已经发生的外部写入；业务操作仍需使用幂等键或核对回执，不能据此假定所有外部效果只发生一次。
- 本轮完成 Agent、Web、IOA、终端及受控 Provider 的回归；未新增完整真实模型的 JEV 浏览器冷学习／热接管验收，Reflex 的适用范围与限制继续见 [JEV 指南](https://github.com/chainreactors/cyber-harness/blob/v1.0.0-rc10/docs/jev.md)。
