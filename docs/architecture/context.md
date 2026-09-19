# 上下文与知识

[架构概览](../architecture.md) · 前一篇：[执行环境](execution.md) · 下一篇：[事件与数据](data.md)

模型只能根据一次请求中实际收到的内容工作。cyber-harness 分别管理 system prompt、对话历史、按需知识、工具定义和模型配置，避免把全部信息永久拼进同一段提示词。

## Prompt 的组装

prompt 扩展定义 `prompt.Contribution` 贡献点，并发布 `prompt.Resolver`。功能扩展按注册顺序贡献具名 section；贡献可以追加、替换、删除或重置内容。target 区分主 Agent、扫描 Agent、评估器和压缩器等不同请求用途。

一次修改先在 Document 副本上执行，失败或 panic 产生 diagnostic，不提交半份修改；renderer 同样隔离失败。一次 Run 在 `BeforeRun` 前解析 system prompt，之后保持不变。这样开发者可以精确调整某个部分，而不必复制整个系统提示词。

Prompt 是模型行为指导，执行控制需要工具/命令边界的 hooks。新增或删掉某段文字本身不会改变工具权限。

## Skills 的发现和读取

Skill 提供描述、位置和正文。内置知识在编译时嵌入；本地 Skill 从项目目录加载；功能扩展还可以贡献 Bundle 和虚拟文件。常见位置：

```text
内置资源 < 扩展 Bundle < .cyber/skills/ < .agent/skills/ < CLI paths
```

同名条目按来源优先级覆盖。Skill 库中“存在”不等于正文全部进入上下文；模型可以读取目录后按需读取引用，调用方也可以通过 `-s` 显式选择。当前参考发行版默认加入 `cyber` 基础 Skill，许多工具说明已组织为它下面的 OKF 参考文档，不能把所有文件名都当成可直接 `-s` 选择的 Skill 名。

虚拟位置如 `cyber://skills/cyber/SKILL.md` 通过 Skill Store 的读取接口解析，不是可由网络下载的 URL。IOA 的 Bundle 只在安装客户端扩展时加入，见 [IOA Skills](../ioa.md#skills)。

本地 Skill 的创建、选择和 Agent 类型定义见[知识与 Skills](../user/knowledge.md)。框架层负责统一发现与读取，正文是否进入请求仍由运行时的选择决定。

## 知识的分发

OKF（Open Knowledge Format）用于把知识组织为渐进阅读的 Markdown bundle：索引、概念文件、历史记录，以及来源/验证等元数据。它与 Skill 可配合使用：Skill 告诉模型何时使用知识，OKF 组织具体知识和引用。

`pkg/exts/okf` 同时贡献 Markdown 产出策略、`okf` 校验命令和虚拟参考文档。参考发行版安装它，最小 `cmd/agent` 不自动安装。`okf validate <path>` 检查格式，`okf test <path>` 做更严格的生产检查；它们不会执行文档里的 executor/attester。具体契约见 [OKF 内置说明](../../pkg/exts/okf/assets/okf.md)。

## 上下文压缩

对话增长到上下文窗口减去预留空间的阈值时，标准循环尝试自动压缩。压缩器保留最近的消息，将较早历史发送给模型摘要；切分不从孤立的 tool result 开始，单个超长用户任务必要时会单独摘要前缀。

成功后，后续请求使用“历史摘要 + 最近消息”。摘要为空、被输出限制截断，或没有减少估算长度时视为失败，不替换原历史。它是一种有损压缩；需要逐字保留的证据应使用文件或原始 artifact，不能只依赖对话摘要。

主动阈值压缩失败会记录 warning；Provider 报上下文溢出时还有一次压缩恢复机会。同一次失败不能无限反复压缩重试。手动 `/compact [focus]` 可以在会话中指定摘要重点。

## 三种 token 限制

| 配置/概念 | 限制什么 | 常见误解 |
| --- | --- | --- |
| `context_window` | 一次请求可容纳的上下文窗口 | 不是累计计费 token |
| `max_tokens` | 单次请求的最大输出 | 不是整个任务的输出或预算 |
| Run 的 `TokenBudget` | 本次 Run 累计用量 | 不是账户/跨会话的总费用上限 |

请求前会根据估算输入和安全预留裁剪最大输出。token 估算不是精确 tokenizer，实际用量取决于 Provider；错误的窗口配置可能导致过早压缩或服务端拒绝。

## Provider 选择与容错

`openai` 和 `anthropic` 表示线协议，服务商由端点和模型选择。`llm.providers` 可以保存多个配置，`active_profile` 选择当前项；没有自动跨 profile fallback。切换配置和对同一个请求重试是不同操作。

标准循环对可重试网络/服务错误退避重试，支持带抖动的指数退避和有效的 `Retry-After` 秒数。鉴权/参数等不可重试错误直接返回；上下文溢出走压缩恢复路径。重试复用逻辑消息 ID，消费端应按 ID 合并流式片段和最终消息。

实现：[Prompt](../../agent/prompt/prompt.go)、[贡献扩展](../../pkg/exts/prompt/extension.go)、[Skill Store](../../agent/skills/embed.go)、[压缩](../../agent/compact.go)、[重试](../../agent/retry.go)。验证入口：[Prompt 测试](../../agent/prompt/prompt_test.go)、[压缩测试](../../agent/compact_test.go)、[重试测试](../../agent/retry_test.go)。
