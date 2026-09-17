# 插件与资源架构

系统只有两个通用机制：`core/resource` 连接强类型资源，`core/extension` 管理固定生命周期。
业务依赖通过构造参数传递。系统没有 Extension ID、依赖 DAG、Service Locator、通用
Capability Catalog、实例声明 DTO 或类型化 ID 层级。

## Core Model

```mermaid
flowchart TB
    Root["Composition Root"]
    Profile["Profile"]
    Set["Set"]
    Extension["Extension"]
    Scope["Scope"]
    ResourceRegistry["Resource Registry"]
    Point["Point[T] / Domain Registry"]
    Resource["Resource T<br/>Tool / Command / Skill / Binding"]
    Handle["Handle"]
    Consumer["Consumer"]

    Root -->|constructs| Profile
    Root -->|constructs and injects dependencies| Extension
    Profile -->|owns lifecycle| Set
    Profile -->|publishes stable domain views| Consumer
    Set -->|owns ordered members| Extension
    Set -->|owns one shared| ResourceRegistry
    Set -->|creates one per Extension| Scope
    Extension -->|uses during Load| Scope
    Scope -->|holds a scoped view| ResourceRegistry

    Extension -->|defines Point[T] through Scope| ResourceRegistry
    Extension -->|owns or creates| Resource
    Resource -->|Add[T] through Scope| ResourceRegistry
    ResourceRegistry -->|routes by Go type| Point
    Point -->|accepts and publishes| Resource
    Point -->|returns| Handle
    Scope -->|owns| Handle
    Consumer -->|uses domain API| Point
```

这不是一条 Profile、Extension、Scope、Capability、Contribution、Registry 逐层包装的链。
系统由 Profile 边界、生命周期和 typed resource 三组关系组成：

- `Profile` 是宿主可加载、关闭和替换的完整应用。它拥有 `Set`，并只在整个 Set Active 后
  向 runner、node、transport 和 Web 发布 App、Runtime、Namespace、Console 等稳定领域视图。
- `Set` 是生命周期控制器。它拥有一个有序 Extension 列表、一个共享 Resource Registry，并为
  每个 Extension 创建一个 Scope。
- `Extension` 是业务对象到 harness 生命周期的 adapter。它通过构造参数接收直接依赖，在
  `Load` 中初始化所拥有的对象、定义 Point 或注册 Resource，并在需要时负责关闭后台工作和
  外部资源。业务消费者不以 Extension 作为能力接口。
- `Scope` 是单个 Extension 的生命周期账本。它提供初始化和存活 context，并拥有该 Extension
  产生的所有 Handle；它不是 Service Locator，也不向插件暴露任意服务查询。
- `Resource Registry` 仅用 Go 类型把 Resource 路由到唯一的 `Point[T]`。Tool Registry、Command
  Registry、Skill Store、Namespace Registry 等 Domain Registry 才负责具体资源的校验、重复
  规则、发布、调用准入和排空。
- `Handle` 表示一次 Point 定义或一次原子资源注册，可由 Scope 在回滚和关闭时精确撤销。

`Capability` 不是框架实体或通用接口，只是 Tool、Command、Skill、Runtime 等领域对象对外
能力的描述。`Contribution` 也不是通用容器或 DTO；`Add[T]` 是贡献动作，传入的 `T` 就是
Resource，返回的 Handle 表示这次注册。`cli.Contribution` 只是 CLI 领域自己的具体资源类型。

某些对象可以同时承担多个角色，例如 Tool Registry 既是定义 `tool.Tool` Point 的 Extension，
也是 `Point[tool.Tool]` 和 Tool Executor。这是同一对象实现多个必要角色，不代表框架增加了一层。

`Set` 按顺序 Load `Extension`；全部成功后冻结新的 Point 定义并发布 Active。关闭时按相反顺序
处理 Extension：先停止对应 Scope，再逆序关闭它拥有的 Handle，最后关闭 Extension 自身持有的
后台工作和外部资源。构造参数表达直接业务依赖，Resource Registry 只连接动态资源。

这张图只描述 runtime Extension model。Config、CLI 和连接测试也使用 `core/resource` 的 typed
Point，但在参数解析前由组合根直接定义和注册，不由 `Set`、`Scope` 或 runtime lifecycle 管理，
因此不属于这张核心关系图。

## Architecture Invariants

- 一个 Profile 恰好拥有一个 Set。Profile 不把 Extension、Scope 或 Resource Registry 暴露给宿主。
- 一个进程可以有多个互相独立的 Set，例如可热替换的 Profile、HTTP listener 附属资源和一次性
  命令各自具有不同生命周期。它们必须由宿主或用例入口并列持有；Extension、App 和领域对象不得
  在 Load 内创建或隐藏子 Set。
- Extension 可以同时实现 Point，或者返回一个不含 Load/Close 的窄领域视图；消费者不得把
  `extension.Extension` 当作业务能力接口，也不得取得资源所有权。
- 稳定、必需的业务依赖通过构造参数注入。只有需要被其他插件动态增加、撤销或枚举的对象才进入
  Resource Registry；Registry 不是通用依赖注入容器。
- 所有 runtime Point 定义和资源注册都经由 Scope 的 `extension.Define/Add`，由 Scope 持有 Handle。
  Extension 不自行维护另一套 owner ID、注销表或依赖图。
- 每种 Resource 使用一个具体 Go 类型和一个 Point。领域名称和重复规则属于该 Point，不再建立
  Resource ID、Capability Catalog 或声明 DTO 作为第二事实源。
- `core/` 只向下依赖 `aop/`（wire 层），不依赖 `pkg/`、`agent/` 或 `tools/`。因此 core 使用的
  基础类型与机制（例如 `core/types`）必须住在 core 内部；core 需要的应用行为从 core 上移出去，
  而不是把应用包下沉进 core。
- `pkg/exts/` 是唯一把功能挂到 harness core 的包装层：每个 Extension 包装一个 `tools/*`、
  `agent/*` 或 `pkg/*` 机制，自身不写业务实现。机制住在 `pkg/` 或 `tools/`，生命周期住在
  `pkg/exts/`。每个 `pkg/exts/<feature>` 子树至少声明一个 Extension，由该目录的守卫测试强制。
- 整块功能通过 Extension 挂载，新增协议、能力或资源不得由宿主（`pkg/node`、`pkg/web` 等）
  硬编码。宿主只做连接 IO、生命周期与装配；它不认识的协议无从接入。
- 每条连接一个实例的协议用 `aop.ConnectionBinding` 贡献，而不是在 Extension 或宿主里维护
  跨连接的实例表。
- `tools/` 是工具与命令的实现层，`pkg/commands` 保留 Command Point、Registry、Execution
  记录和命令文本工具，bash/tmux 的执行实现住在 `tools/terminal`。需要 Bash 行为的 Profile 直接借用具体
  `terminal.BashTool`，不为分层外观增加转发 DTO 或只有一个实现的接口。

## Typed Resource

资源类型就是 Go 类型。宿主定义 Point，插件贡献该类型的值：

```go
type Point[T any] interface {
    Add(...T) (resource.Handle, error)
}

resource.Define[tool.Tool](resources, tools)
resource.Add[tool.Tool](resources, read, write, glob)
```

这里几个词只描述同一个注册动作的不同角色，不是多层框架：

- **Point** 是某种 Go 类型的接收端，例如 `cli.Registry` 接收 `cli.Contribution`。
- **Resource** 是传给 `resource.Add[T]` 的 `T` 值，例如一个 Tool、一个 CLI contribution 或一个
  Config section。
- **Contribution** 只是“被贡献的 Resource”的领域命名；框架没有通用 Contribution 接口。
- **Declare** 是扩展包的初始化入口，在构建期调用若干次 `resource.Add`；它不是 Resource、Point、
  生命周期接口或另一套注册机制。Declare 只贡献资源，不定义 Point，也不需要 declaration 子包。

因此系统只有一套连接机制：`resource.Define[T]` 建立 typed Point，`resource.Add[T]` 向它提交
Resource。不同的 `T` 需要不同 Point，是因为 CLI、Config、Tool 的重复和 Seal 规则不同；这不等于
存在多套 Point 机制。`config.Sections.ConnectionPoint()` 只是 Go 方法不能重载所需的窄适配器：
同一个 `Sections` 类型不能同时声明 `Add(...Section)` 和 `Add(...Connection)`。

Resource Registry（`resource.Registry`）内部用 `reflect.TypeFor[T]()` 定位 Point，因此资源类型没有字符串名称，
也不存在需要同步维护的 Resource ID。资源自身的领域标识仍由领域类型负责，例如 Tool 的
名称、CLI flag、配置 section key 和 protobuf namespace full name；这些不是插件 ID。

定义资源类型也是动态组合的一部分。只有已经定义的类型可以接收贡献，完整 Profile 加载后
只冻结新的类型定义。已有 Point 是否继续接受 Add、Handle 关闭后如何撤销，以及消费者何时看到
变化，全部属于领域语义：Tool 和 Command 是实时目录，Namespace Binding 对后续连接生效，
Console Bindings 与 Web Route 由宿主物化为固定快照。通用 Resource Registry 不猜测重复规则、
快照、调用准入或排空行为，也不承诺所有 Resource 都支持运行时热更新。

资源定义顺序也是可见性边界：较早的 Extension 不能保留 Scope 后向较晚才定义的资源类型
注册。这样无需依赖图，也能阻止隐式反向依赖。

同一个领域可以有两种绑定形态，用不同的 Go 类型区分。`aop.NamespaceBinding` 携带
Profile 级 handler，`aop.ConnectionBinding` 携带 opener，由连接建立时调用一次生成该连接
私有的 handler。两者由 Namespace Registry 用窄适配器 `ConnectionPoint()` 分列两个 Point，
共享同一命名空间空间和顺序，因为 Go 方法不能重载 `Add`。按连接的实例因此不需要任何跨连接
表：mux 只引用自己那个 handler，连接结束时它随连接 context 一起结束，贡献者无需追踪或释放。

## 声明期资源

Config 与进程 CLI 在解析前组合，仍使用同一套 typed resource 机制：

- `cli.Registry` 是 `Point[cli.Contribution]`。
- `config.Sections` 是 `Point[config.Section]`；它的 `ConnectionPoint()` 接收可选的
  `config.Connection`，并由同一个配置目录按 section 分发。
- 拥有外部连接的扩展直接贡献 `config.Connection`。没有聚合 Provider DTO、Catalog、Probe
  Registry 或额外生命周期。

需要参与解析前组合的扩展在自己的包中提供 `Declare(*resource.Registry, ...)`，直接向这些 Point
添加自己拥有的资源；没有这类资源的扩展不需要空 Declare，也不需要伪造生命周期 Extension。
底层 Add 仍返回 Handle，但当前声明流程是一次性 builder：成功后 Seal，
失败时丢弃整个 builder，不承诺逐插件卸载。CLI contribution 按注册顺序物化，因此一个扩展可以
扩展较早扩展声明的命令。

运行时资源与声明期资源的差异只在生命周期和领域约束：参数解析完成后不能增加 flag/config
schema；运行时 Point 可以按自己的协议选择实时增删、只影响新消费者，或在首次物化后拒绝贡献。

## Extension 生命周期

```go
type Extension interface {
    Load(*extension.Scope) error
}

set, _ := extension.New(commands, tools, skills, provider, files, terminal)
```

`Set` 按声明顺序 Load，失败时或关闭时按相反顺序处理。真正持有后台工作或打开资源的插件
额外实现 `Close(context.Context) error`；纯资源贡献插件只需 Load。没有 Entry、ID、DependsOn
或拓扑排序；构造函数参数表达业务依赖，切片顺序表达生命周期。

每个 Scope 提供：

- `Init()`：本次初始化 context。
- `Lifetime()`：Extension 开始关闭时取消。
- typed resource Registry：通过 `extension.Define/Add` 使用。
- owned handles：Scope 在调用 Extension.Close 前逆序撤销。

Profile 只有全部 Extension 成功加载后才 Active。Close 开始即停止发布，先取消 Scope、撤销
资源贡献，再调用 Extension.Close。`ErrCloseIncomplete` 表示资源仍未排空，Set 保留当前位置
和更早的依赖，允许使用新的 context 重试；普通错误会报告但不阻止其余逆序清理。

## 领域 Point

| 资源类型 | Point 所有者 | 贡献者示例 |
| --- | --- | --- |
| `tool.Tool` | Tool Registry | files、terminal、scanner、search、browser |
| `commands.Command` | Command Registry | terminal、scanner、native |
| `skills.Bundle` | Skill Library | IOA 及未来知识插件 |
| `*console/api.Bindings` | TUI | Session、IOA presentation |
| `aop.NamespaceBinding` | Namespace Registry | Session、Proxy 等协议插件 |
| `aop.ConnectionBinding` | Namespace Registry connection point | PTY |
| `web.Route` | Web route Registry | Management API、IOA server |
| `cli.Contribution` | CLI Registry | IOA commands、Session flags |
| `config.Section` | Config Sections | IOA client/server、record |
| `config.Connection` | Config Sections connection point | scanner、search、IOA client |

Command 和 Tool 是不同执行协议，但共同使用 `core/registry.Store[T]` 管理批次、运行时准入、
取消和 drain。Skill 是知识资源，不并入执行 Registry。Console 和 Namespace Binding 也保留
自己的领域类型，不通过字符串形式的万能资源表。CLI 与 Config 由各自 Point 校验，不经过聚合
Catalog。

每个连接从 Namespace Registry 绑定一次当前快照，快照同时包含静态 handler 和按连接 opener。
关闭 contribution handle 会移除后续连接的绑定，已建立连接仍由自己的 `aop.NamespaceMux` 和
连接 context 管理，这避免运行期跨连接共享可变路由状态。Namespace 的唯一身份是 protobuf
full name，且同一个 namespace 不能既是静态又是按连接；Mux 不再维护字符串 owner 或按
owner 注销的第二套生命周期，连接关闭时统一停止准入并排空已接受的 dispatch。

Session Extension 只拥有 Runtime 的启动和关闭，不探测 Namespace Point 是否存在。需要暴露
Session 协议的组合根显式贡献 `Runtime.NamespaceBindings()`；Node 连接也不再硬编码同一组
Command handler。连接私有的调用取消在 Namespace 分发前处理，因为它依赖单个连接的
运行中调用表，不是可跨传输复用的资源。

Agent Loop 与 Session Runtime 在每个 Profile 的组合根就地组装：先安装 Agent Loop
Extension，再把 `Loop()` 交回的句柄放回 `agentsession.Config.Loop`，最后安装 Session
Extension。`cmd/aiscan`、`cmd/agent` 和 Scanner AI 都使用 Profile 返回的同一 Session
Runtime；runner 不再创建隐藏的子 Set 或第二套 Runtime。

## Service 与 Capability

旧 Service 表已删除。App、Session、IOA、Proxy 等依赖都由组合根直接构造并注入；业务对象
不暴露 Load/Close，拥有者 Extension 保留资源生命周期。可选依赖使用普通 nil/interface
字段表达，而不是服务查询或 capability gating。

旧 Capability Catalog 和 `pkg/edition` 已删除。Scanner CLI 的可用命令与帮助由 scanner 包直接提供；
Browser、Record 等功能是否存在由 build tag 和实际 Extension 组合决定。远端节点需要通告的
核心线协议能力仍保留在 `AgentHello.capabilities`，但由连接实现根据它实际支持的协议直接
生成，不再通过插件贡献 `node.Capability` 后二次投影。插件功能由实际注册的 Namespace、Tool、
Command 等资源表达，避免展示标签与真实可用功能产生两个事实源。

## 组合根

- `cmd/aiscan`：完整应用，按 build tag 选择 scanner、search、proxy、IOA、browser、record、Web。
- `cmd/agent`：最小本地 Agent，只包含 hooks/event stream、Tool/Command/Skill Point、files、bash、App、
  Provider、Agent loop、Session 和 TUI；依赖测试禁止引入上述应用功能。
- `pkg/runner`：保留共享的 aiscan 运行模式逻辑，不是可执行命令，也不改名。

每个 Profile 只有一个线性 Set。宿主可以为不同生命周期建立并列 Set，例如 Web listener
附属资源、CSTX importer 或一次性 IOA client；这些 Set 不嵌套在 Profile 中，也不共享 Scope
或 Resource Registry。App 和 Extension 不选择插件、不创建子 Set，也不关闭借用的资源。
新增插件时，在组合根或明确的用例入口构造依赖并把 Extension 放到正确顺序；新增资源种类时
先定义具体 Go 类型和领域 Point，再由基础 Extension 定义、后续 Extension 贡献。

## 事件与关闭

`core/events.Stream` 是 AOP 事实流，负责 Event ID、时间和序号。Hooks 负责控制与观察，
Operation 负责调用关联；它们不与资源注册或生命周期互相替代。Telemetry、Console、Web 和
Node 只消费实际事件。

关闭时入口先停止接收工作，Set 再逆序撤销贡献并排空调用。Registry handle 只影响自己的
批次，不会取消其他插件的调用。资源替换通过 Add 新批次和 Close 旧 handle 完成；整个 Profile
组合变化则构造新 Profile。
