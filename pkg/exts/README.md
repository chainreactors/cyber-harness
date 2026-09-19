# Extensions

`pkg/exts` 是行为实现与 Profile 生命周期之间的适配层。`tools/` 和 `agent/` 保持普通业务类型，
需要初始化、后台工作或清理时才由这里的 Extension 持有。

Tool、Command、Skill 和 Runtime 才是调用者使用的 domain capabilities；Extension 本身不是
业务能力接口。它只把这些对象包装成 harness 可管理的生命周期单元，并将其发布到对应 Point。
这里的 capability 是领域含义，不引入通用 Capability 类型、Catalog 或 gating。

Extension 通过构造参数接收依赖，通过 `extension.Define/Add` 使用 typed resources。Extension
的 Load、App 和领域对象不得创建或隐藏子 Set，也不得创建 Service Locator、通用 DTO 聚合器
或字符串资源表。宿主和用例入口可以为互不相同的生命周期建立并列 Set。业务消费者只拿不含
Load/Close 的窄接口或借用对象，例如 Files、ProxyHub、Importer、IOA Service 或 Agent Loop；
拥有者 Extension 负责停止准入、排空并关闭底层资源。App 只是这些对象的借用视图，本身没有
空生命周期包装。

Tool Registry 与 Command Registry 直接定义各自 Point，Skill Library 定义 Bundle Point，
Prompt Extension 定义 `prompt.Contribution` Point 并提供 `prompt.Resolver`，TUI 定义 Console
Bindings Point。具体插件贡献资源并由 Scope 自动撤销。Config、CLI 和连接测试
是解析前 Resource；需要参与这段组合的扩展在自己的包中提供 `Declare`，直接向
`cli.Contribution`、`config.Section` 和 `config.Connection` Point 注册。Declare 不定义 Point，
也不引入 declaration 子包、生命周期 Extension、聚合 Provider DTO 或独立 Probe Registry。

依赖顺序由 Profile 组合根或明确用例入口中的线性列表表达。可选功能由是否构造对应 Extension
决定，不使用 Descriptor、Provides/Requires 或 capability gating。IOA client/server 相互独立；
兼容映射留在 `cmd/aiscan`。完整约定见 [系统架构](../../docs/architecture.md)。

## 规则

- 每个 `pkg/exts/<feature>` 子树至少声明一个 Extension，由 `pkg/exts` 的守卫测试强制。
  不属于任何功能树的胶水应该移回拥有该资源的功能包，而不是留在这里当裸函数。
- 协议统一用 `aop.Binding` 贡献，只有一个类型：Extension 提供 opener，每个连接建立时由
  Namespace Registry 打开自己的 handler；共享一个 handler 的用 `aop.Shared` 表达，它的
  owner 自己决定何时停止服务。Extension 不需要为这类协议追踪实例或生命周期。
- 由 Extension 资源支撑的协议不得再由宿主（`pkg/node`、`pkg/web`）硬编码注册，走 Registry。
  宿主自己连接态支撑的协议是另一回事：它们的 handler 闭包捕获的是连接自己的状态，本来就
  该由宿主注册，`RegisterNamespaces` 就是两者的接缝。
