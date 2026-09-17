# Extending Cyber

Agent 工具实现 `core/tool.Tool`；Bash 原生命令使用 `pkg/commands.Command`。二者是不同
调用协议，不应通过适配器伪装成一个万能抽象。

Tool Registry 和 Command Registry 分别定义 `tool.Tool` 与 `commands.Command` typed Point。插件在 Extension.Load 中调用
`extension.Add` 原子贡献，Scope 关闭时自动撤销自己的批次并 drain；运行时可热增删，
其他批次不受影响。

工具使用 `tool.TextResult` 返回文本，使用 `tool.Result.Details` 返回结构化数据；真实执行
失败返回 Go error，并遵守 context 取消。构造函数显式接收 scanner、proxy、IOA 或工作
目录等依赖。不得增加全局 Registry、工厂清单、依赖容器、兼容包装或平行传输模型。

`tools/*` 保存原始领域实现与 Tool/Command 声明，`pkg/exts/*` 适配 Profile 生命周期和
Registry 贡献，`pkg/profile` 只提供 `Application`、`Factory` 与 `Request`。
具体 Profile 图由 `cmd/aiscan` 和 `cmd/agent` 声明，不能放回共享包。`pkg/runner` 仅保留
aiscan 的共享运行模式逻辑。

Flags、Configs、静态 Skill、协议声明和宿主绑定也可独立提供，不必实现 Extension。
复用所属领域的类型和声明函数，参见 [系统架构](../docs/architecture.md)。
