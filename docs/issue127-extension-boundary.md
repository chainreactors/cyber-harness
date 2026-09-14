# Issue 127：统一 Extension、Registry 与观察边界

状态：核心机制、产品装配与生命周期边界已收敛。本文记录当前架构约定；
旧 Issue 中的 Context 服务容器、Registrar/Registration 和 Bundle/Patch 方案不再作为实施要求。

## 单一生命周期图

每个独立运行时由命令入口声明并拥有一张固定的 `core/extension.Set` 图。Web 宿主的
IOA Server 图与可替换的应用 Profile 图寿命不同，不共享实例，也不由 Extension 创建子 Set。
需要被 Web、Node 或 Runner
复用的 AIScan 图由命令入口的具体 Profile 持有业务能力，通过 `pkg/profile.Application`
供 host 使用。具体 Profile 直接持有 Set，不增加 Assembly 包装或 active/closing 状态；
具体 `aiscanProfile` 位于 `cmd/aiscan`，发布状态统一读取 `Set.Active()`。构造函数注入真实依赖，
`Entry.DependsOn` 只决定 Load 与逆序 Close；`extension.Scope` 只提供初始化 context、
寿命 context，不是服务容器，也不生成 owner ID 或资源 Ref。

构造必须无副作用。实例所有权在 Load 时取得，完全关闭后释放。Load/Close panic 由 Set
转换为错误；Load 失败会逆序回滚；Close 请求发起时立即撤销 `Active` 发布门，再关闭依赖者。Extension.Close 返回 context
取消或超时时由 Set 自动归类为 `extension.ErrCloseIncomplete`，表示依赖仍受保护，调用方可用新 context 重试 Close。
Extension 组合变化通过关闭整张旧图并创建新 Profile 完成。Provider 配置更新沿用现有
Run 快照语义：活跃 Run 保留原 Provider，后续 Run 使用新配置。

## Registry 的统一范围

所有命名执行能力共享 `core/registry.Store[T]`：

```text
collecting → active → draining → closed
```

批次注册在 collecting 阶段原子完成，同名冲突不产生部分发布。Active 后声明不可变。
执行前取得 lease；关闭先拒绝新调用、取消已接纳调用并 drain，随后贡献者才能释放资源。

Tool 与 Command 不是重复抽象：

| 运行时边界 | 调用契约 | 消费者 |
| --- | --- | --- |
| `pkg/toolset.Registry` | JSON Schema、`ExecuteTool`、结构化 Tool Result | Agent、ToolNode、外部框架 |
| `pkg/commands.Registry` | argv、cwd/env、stdio、PTY、`Execution` | Bash 伪命令、CLI、扫描工具 |

两者保留领域校验和适配，只共享 Store 的注册、发布、准入、取消和 drain。
`Registry` 专指活跃且可执行的运行时边界；`Catalog` 只用于 edition 能力描述或协议发现等
不可执行静态投影。

## Hook、Operation 与 Event

`core/hooks.Registry` 是唯一 typed Hook 总线。`core/operation` 提供进程内 operation 身份、
父子关系和协作取消。Tool、Command、Process、File、HTTP 各有独立 hook point；控制点
fail-closed，事实观察点不能改变已经完成的结果。

`core/events.Stream` 是每个 Profile 唯一的 AOP stamping 和发布入口，其底层 Bus 不公开，负责 Event ID、时间和
session 内序号。生产者只使用 `Publish`，同步观察者实现 `Observer` 并通过 `Observe` 接入，
持久化消费者实现 `Consumer` 并通过有界、可排空的 `Consume` 接入。`pkg/exts/observe` 将已选择的 typed hook 转为 AOP 观测；operation 关联使用
Event typed extension 中的 `aop.operation.Ref`，不在每种 payload 中重复 `tool_id`。
这里的 `Ref` 是跨进程 typed message，只携带不可变的关联 ID；它不是 Go 资源引用、生命周期
handle 或服务定位入口，也没有 Load/Close/lookup 能力。进程内代码统一以
`operation.Correlation(ctx)` 生成它，不传播泛化的 `*Ref` 包装。
`pkg/exts/eventoutput` 是唯一通用 JSONL 输出扩展。它只订阅 AOP Stream，不导入 Tool、File 或
Traffic 领域。无 session 的根观察同样是合法事件。

控制决策由作出决策的策略 Extension 直接发布为 typed `aop.operation.Decision`。Observe
只投影执行边界事实，不能代替策略发布决策或将异步消费者带回同步准入路径。

```text
execution boundary → typed hooks → Observe → AOP Stream → EventOutput / transport
```

文件操作由 `tools/files` 在真实 IO 边界发 hook；`pkg/exts/files` 是唯一文件插件。
`tools/proxy` 提供原始 Hub、FlowStore 和无状态的 Traffic namespace 注册函数；
`pkg/exts/proxy.Extension` 是 Hub 唯一的宿主生命周期适配。连接自己的 `NamespaceMux` 负责
Traffic 请求准入和 drain，使用 Profile 发布的 ProxyHub，不创建第二个 Extension。代理在
FlowStore 完成提交和 body finalization 后发 HTTP hook。Traffic 协议只查询快照，使用
`FlowRecord{operation, flow}`；实时事实只走同一 Event + operation Ref 形状。

## 所有权

| 位置 | 唯一职责 |
| --- | --- |
| `core/extension` | 固定图、Load 回滚、逆序 Close |
| `core/registry` | 领域无关的命名运行时状态机 |
| `core/hooks`、`core/operation`、`core/events` | 控制/观察、执行身份、AOP 发布 |
| `pkg/exts/*` | 资源或贡献的 Extension 所有者 |
| `pkg/toolset` | Agent Tool Registry |
| `pkg/commands` | 原生命令 Registry 与进程执行 |
| `tools/*` | 原始实现和领域声明 |
| `agent` | Agent 状态与 loop，只依赖 `tool.Executor` |
| `pkg/app` | 内置产品状态与业务访问面；不选择插件、不生成 Entries |
| `pkg/profile` | host 接口、Factory 输入和无产品状态的 Set 组装器 |
| `cmd/aiscan`、`cmd/runner` | 实现具体 Profile、构造实例并声明唯一固定产品图 |

AIScan 的主要加载顺序是 EventOutput、Observe、Proxy/IOA、App 与能力贡献者、Command
Registry、Tool Registry、可选 Agent 扩展；关闭严格逆序。Output 可独立记录 Agent 事件，
Observe 只在明确选择观察种类时安装。

`pkg/exts/agent.Extension` 是 Agent 执行的唯一生命周期所有者，通过 `Runtime()` 发布不含
Load/Close 的业务能力。该 Runtime 同时实现受控 `agent.Loop` 并拥有 Session、Run、Inbox、
准入、取消和 drain；不再存在第二个 `pkg/exts/session` 插件或 Agent/Session 双扩展链。
会话级 Loop 覆盖入口已移除。仅用于扫描命令的原始 Loop 由 Scanner Extension 的 Registry
准入和寿命保护，不伪造一个没有 Session 的 Agent 宿主。

`agent.Agent` 是单次会话中的领域状态，subagent 是受父调用 context 约束的临时执行，
二者都不拥有 Extension、Registry 或 Profile 生命周期。这是执行模型本身，而不是第二套
宿主生命周期或待迁移兼容层。

Files、Proxy、IOA 在原始实现中拆分为生命周期 `Resource` 与业务对象；各自 Extension
只持有 Resource，消费者直接取得本身没有 Open/Start/Close 的 Files、ProxyHub 或 Runtime。
不存在 Borrow、Handle、私有 seal 或 owner token。插件之间不相互导入。ACP/Pi 互操作由 Issue 124 独立跟踪，
不属于本边界的兼容层。

## 禁止回归

- 不恢复 `commands.Catalog`、`toolset.Catalog`、Registrar/Registration 或第二套 Registry。
- 不恢复 `filetools`、`workspacefiles`、`toolgroup`、第二套日志扩展或独立 FileAccess 事件管线。
- App 不生成 Entries；App 和 Extension 不创建子 Set，不维护通用 cleanup bag 或服务定位器。
- App 不暴露可写 EventBus；AOP 事件只经 `Publish` 进入唯一 Stream，观察与持久化分别使用 `Observe` 和 `Consume`。
- 连接不能接受通用 Extension 工厂；具体 namespace 直接注册到该连接的 Mux。
- 活跃 Registry 不热替换、不 shadow registration、不保留兼容 fallback。
- `tools/*` 和 `agent/*` 不直接实现或导入 Extension 宿主生命周期。
- 原始实现不关闭由 Profile 拥有的 Registry；业务能力对象不提供资源关闭入口。

架构测试固定以上边界；默认/full 编译和 lifecycle/race 测试是交付门禁。

## IOA 客户端与服务端

IOA 只有两个扩展：`pkg/exts/ioa/client` 与 `pkg/exts/ioa/server`。
客户端统一拥有注册重试、收信订阅、handoff Consumer 和命令贡献；协议 skills 通过
静态 `skills.Bundle` 由 Profile 注入 App。Agent 和通用 Skills 不导入 IOA SDK。
客户端先于 Command Registry 和 Agent 加载，最后关闭。Agent 的 `Deliver` 是通用业务准入，
Profile 传入检查 `Set.Active()` 的投递函数；它可以在任何时候拒绝调用，
不把 Agent 的资源寿命借给客户端。Agent 关闭后，客户端仍排空已接纳的 handoff 事件。

服务端扩展只拥有 Store、Service、认证以及请求/SSE 的准入、取消和排空。
HTTP listener 由命令入口持有，独立 serve 和 Web 分别挂载根路径和 `/ioa/`。
Web 的服务端扩展保持宿主寿命，配置重载只替换应用 Profile；扩展之间不互相导入。
