# Extending Cyber

Agent 工具实现 `core/tool.Tool`；Bash 原生命令使用 `pkg/commands.Command`。二者是不同
调用协议，不应通过适配器伪装成一个万能抽象。

Tool 声明调用 `pkg/toolset.Registry.Register(tools...)`，Command 声明调用
`pkg/commands.Registry.Register(group, commands...)`。纯声明由 Profile 直接注册，
依赖资源初始化的声明在 Extension.Load 中注册。Registry 原子持有固定声明，失败组合
整体废弃；Scope 只提供初始化与寿命上下文。Profile 让 Registry 依赖实际资源所有者，
因此 Registry 先关闭并 drain，资源随后释放。

工具使用 `tool.TextResult` 返回文本，使用 `tool.Result.Details` 返回结构化数据；真实执行
失败返回 Go error，并遵守 context 取消。构造函数显式接收 scanner、proxy、IOA 或工作
目录等依赖。不得增加全局 Registry、工厂清单、依赖容器、兼容包装或平行传输模型。

`tools/*` 保存原始领域实现与 Tool/Command 声明，`pkg/exts/*` 适配 Profile 生命周期和
Registry 贡献，`pkg/profile` 只提供 `Application`、`Factory` 与 `Request`。
具体产品图由 `cmd/cyber`、`cmd/runner` 等可执行入口声明，不能放回共享包。

Flags、Configs、静态 Skill、协议声明和宿主绑定也可独立提供，不必实现 Extension。
复用所属领域的类型和声明函数，参见 [系统架构](../docs/architecture.md)。
