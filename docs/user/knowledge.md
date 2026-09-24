# Skills 与知识

[使用者指南](README.md) · 前一篇：[工具](tools.md) · 下一篇：[安全扫描](../scan.md)

Skill 将一项工作的经验整理为模型可读取的说明。它可以规定报告结构、解释工具用法，或把相关知识连接起来。工具决定 Agent 能执行什么，Skill 帮助 Agent 理解何时执行、怎样组织结果。

## 内置知识

aiscan 默认使用 `cyber` 基础 Skill，工具说明和安全工作方法通过其中的引用逐步展开。知识库里存在一份文档，并不意味着它的全文会在每轮请求中发送给模型；模型可以根据任务按需读取。

许多工具说明是 `cyber` 下的参考文档，不是独立 Skill。因此，工具名存在并不保证可以直接用 `-s <工具名>` 选择。IOA 等扩展还会贡献自己的知识，在未安装对应扩展时不加载。

Agent 可能看到 `cyber://skills/...` 这样的虚拟路径。这些资源由安装它们的扩展以 Bundle 或虚拟文件挂载提供，不是 HTTP 下载地址。使用者通常不需要手工解压这些文件。

## 自定义 Skill

在当前项目创建 `.cyber/skills/review/SKILL.md`：

```markdown
---
name: review
description: 核对报告中的证据、验证范围和未解决事项。
---

阅读已有报告，逐条核对结论的证据来源。
区分已验证事实与推断，保留证据文件路径。
在结尾列出尚未验证的事项。
```

然后启动新的运行：

```sh
aiscan agent -s review -p "检查当前目录中的报告"
```

`name` 用于选择 Skill，`description` 用于说明适用范围，也是加载校验项。正文写工作方法，需要较长材料时放进相邻文件并使用相对链接。Skill 中的命令必须由当前运行环境提供；文档不会自动安装依赖或赋予工具权限。

## 加载与覆盖

同名 Skill 按来源优先级选择：扩展 Bundle、项目的 `.cyber/skills/`、`.agent/skills/`，最后是 CLI 指定路径。后面的来源覆盖前面的同名内容，使项目可以定制默认知识。

`-s` 也支持本地 Skill 文件路径；启动时读取文件并将选中的正文加入任务。修改后使用新运行验证加载情况，不要假定所有宿主都支持热重载。

面向特定子 Agent 的 Skill 可以使用 `agent: true` 等元数据定义 Agent 类型。其意义是给派生 Agent 选择任务配置，并不会创建独立的文件系统环境。子 Agent 的运行方式见[Web 与协作](web.md#子-agent)。

## OKF

OKF（Open Knowledge Format）把较大的知识集合组织为索引、概念文档、引用和历史。Skill 可以作为入口，再通过 OKF 索引逐层读取；知识条目的来源、验证状态与更新时间帮助使用者判断信息是否适用。

aiscan 的 OKF 扩展还向 Agent 提供 Markdown 产出策略和校验命令。`okf validate <path>` 检查格式，`okf test <path>` 检查更严格的生产要求，均不执行文档中声明的计算程序。具体格式见[内置 OKF 说明](../../pkg/exts/okf/assets/okf.md)。

开发者可通过 Bundle 或 Prompt contribution 接入知识与指令，见[扩展开发](../developer/extensions.md)。应用自己的系统提示词、Skill 正文和当前对话各有不同作用，架构层面的组合过程见[上下文](../architecture/context.md)。
