# Extensions

`pkg/exts` 是行为实现与产品生命周期之间的适配层。`tools/` 和 `agent/` 保持普通业务类型，
需要初始化、后台工作或清理时才由这里的 Extension 持有。

Tool、Command、Skill 和 Runtime 才是调用者使用的 domain capabilities；Extension 本身不是
业务能力接口。它只把这些对象包装成 harness 可管理的生命周期单元，并将其发布到对应 Point。
这里的 capability 是领域含义，不引入通用 Capability 类型、Catalog 或 gating。

Extension 通过构造参数接收依赖，通过 `extension.Define/Add` 使用 typed resources。它们不得
创建 Service Locator、第二个 Set、通用 DTO 聚合器或字符串资源表。业务消费者只拿不含
Close 的对象，例如 Files、ProxyHub、IOA Runtime 或 Agent Runtime；拥有者 Extension 负责
停止准入、排空并关闭底层资源。App 只是这些对象的借用视图，本身没有空生命周期包装。

Tool Registry 与 Command Registry 直接定义各自 Point，Skill Library 定义 Bundle Point，
TUI 定义 Console Bindings Point。具体插件贡献资源并由 Scope 自动撤销。Config、CLI 和 Probe 是解析前的
声明资源，插件以 `Declare(*resource.Registry)` 直接贡献，不需要生命周期 Extension。

依赖顺序由产品组合根中的线性列表表达。可选功能由是否构造对应 Extension 决定，不使用
Descriptor、Provides/Requires 或 capability gating。IOA client/server 相互独立；产品兼容
映射留在 `cmd/aiscan`。完整约定见 [系统架构](../../docs/architecture.md)。
