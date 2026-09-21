# Subagent

subagent 扩展拥有一个 `resource.Point[subagent.Subagent]`，提供匿名与具名任务的统一执行能力。
业务实现位于 `agent/subagent`；`agent` 和 `agent/session` 不依赖它。

## 抽象与边界

| 抽象 | 职责 |
| --- | --- |
| `Subagent` | 具名的任务准备逻辑：名称、说明、默认模式、`Prepare` |
| `Registry` | 唯一 Point 的实现；注册、枚举、名称解析、执行租约 |
| `Input` | 文本 Prompt 与贡献者拥有的结构化 Payload |
| `Request` | 本次调用的注册名、实例标签、输入、模式与超时 |
| `Run` | 一次执行的配置、上下文、delegation 和租约；调用者必须在收尾后 `Finish` |
| `Executor` | 消费者借用的准备与执行接口，不提供安装/关闭操作 |

`Prepare` 不执行任务，也不创建需要另行关闭的资源。它返回配置快照及非空任务文本。
同一次具名调用只准备一次；匿名调用跳过名称解析和 Prepare。

`Executor.Execute` 同步执行，使用通用 `agent.RunTask`，不创建 Runtime Session。
会话工具借用 `Executor.Start`，通过 Session 的公开 API 执行，租约持续到 Session 关闭和通知结束。
Session 只管理队列、附属会话和生命周期，不知道注册表或 subagent 工具参数。

## 安装与贡献

```go
// 组合根：按顺序显式安装。
subagentext.New()       // 唯一 Point 和前台执行能力
scannerext.New(...)    // 贡献 verify / sniper
sessionext.New(...)    // 普通 Session Runtime
subagentext.NewTools() // 借用已有 Point 与 Runtime，安装统一工具
```

扫描命令无需安装 Session 和 Tools；普通 Session 可以不安装 subagent。
两个安装入口共享同一个 Point，不创建第二套注册表。

```go
// 贡献者的 Load：注册随 Scope 自动撤销。
extension.Add(scope, subagent.Subagent{
    Name: "verify", Description: "Verify a finding",
    DefaultMode: subagent.Sync,
    Prepare: prepareVerify,
})
```

运行时增删使用从 Scope 借用的 `*subagent.Registry`：`Add` 返回的 Handle 由贡献者关闭。
重复名称拒绝；撤销立即隐藏名称、拒绝新调用、取消关联任务，并等待准备、执行、最终记录和通知全部结束。
排空结束前名称保持保留。关闭超时返回 `ErrCloseIncomplete`，允许稍后重试，不提前释放依赖。
这不意味着热加载整个 Extension。

## 调用语义

```text
subagent(prompt="分析日志")
subagent(name="verify", prompt="验证这个发现")
subagent(name="sniper", label="检查 nginx", prompt="分析指纹", mode="async")
subagent(action="catalog")
subagent(action="list")
subagent(action="kill", session_id="执行 ID")
```

- `name` 可选，只表示注册名；省略是匿名，未知名称报错。
- `label` 只表示本次运行的可读标签，允许重复；唯一身份是 `session_id`。
- `catalog` 实时列出定义，`list` 列出运行实例。名称不是工具 schema 的静态 enum。
- 显式 mode 优先；具名默认使用定义配置（未填为 sync），匿名默认 async。timeout 仅支持 sync。
- async/fork 脱离工具调用上下文的取消，但仍绑定父会话与扩展生命周期。
- fork 只复制最后一个完整工具调用批次边界之前的历史。
- 完成通知由 subagent 发送，在 Session 最终记录之后、父 inbox 释放之前完成。

旧 `type` 参数已删除，旧实例 `name` 改为 `label`。AOP 保留原有结构：`AgentType` 承载注册名，
`AgentName` 承载实例标签；匿名注册名为空。delegation 来源于实际执行，不从 tool-call 参数猜测。

## Scanner

scanner 贡献 verify/sniper 的 Subagent、skill 与 prompt 装配。扫描传入 `scan.WorkerPromptPayload`，
模型调用传入文本；Payload 非空时必须合法，Prompt 可补充说明。
`tools/scan` 只通过注入的 `Worker` 回调委派，保留候选筛选、结果解析和标注。
