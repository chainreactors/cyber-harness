# Issue 127：统一 Extension、Registry 与观察边界

状态：核心机制、产品装配与生命周期边界已收敛。本文记录当前架构约定；
旧 Issue 中的 Context 服务容器、Registrar/Registration 和 Bundle/Patch 方案不再作为实施要求。

## 单一生命周期图

每个产品 Profile 只拥有一张 `core/extension.Set` 固定图。构造函数注入真实依赖，
`Entry.DependsOn` 只决定 Load 与逆序 Close；`extension.Scope` 只提供初始化 context、
寿命 context 和同步撤销跟踪，不是服务容器，也不生成 owner ID 或资源 Ref。

构造必须无副作用。实例所有权在 Load 时取得，完全关闭后释放。Load/Close panic 由 Set
转换为错误；Load 失败会逆序回滚；Close 先关闭依赖者。Extension.Close 返回 context
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
session 内序号。`pkg/exts/observe` 将已选择的 typed hook 转为 AOP 事实；operation 关联使用
Event typed extension 中的 `aop.operation.Ref`，不在每种 payload 中重复 `tool_id`。
`pkg/exts/eventoutput` 是唯一通用 JSONL 输出扩展。它只订阅 AOP Stream，不导入 Tool、File 或
Traffic 领域。无 session 的根观察同样是合法事件。

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
| `pkg/profile/*` | 唯一 composition root：构造具体实例、生成固定图并拥有唯一 Set |

AIScan 的主要加载顺序是 EventOutput、Observe、Proxy/IOA、App 与能力贡献者、Command
Registry、Tool Registry、可选 Agent 扩展、Session Manager；关闭严格逆序。Output 可独立记录 Agent 事件，
Observe 只在明确选择观察种类时安装。

`pkg/exts/session.Resource` 是会话管理器的生命周期所有者，Profile 只借出没有 Load/Close
的 `Manager`。`pkg/exts/agent.Extension` 同样只拥有生命周期，通过 `Loop()` 借出实现
`agent.Loop` 的 Runtime，负责选定 Loop 的运行准入、寿命取消和排空。两者在同一张图中
平级装配，不另建 Agent 状态或 Session 图。会话级 Loop 覆盖入口已移除。

此处的所有权统一不代表旧状态实现已全部迁移：Session 内部仍调用 `agent.Agent`，
`agent/subagent.go` 仍有独立的派生会话起止路径。这些是尚未收敛的实现边界，
不能以目录迁移、私有化方法或删除兼容入口代替其调用链迁移。

Files、Proxy、IOA 在原始实现中拆分为生命周期 `Resource` 与业务对象；各自 Extension
只持有 Resource，消费者直接取得本身没有 Open/Start/Close 的 Files、ProxyHub 或 Runtime。
不存在 Borrow、Handle、私有 seal 或 owner token。插件之间不相互导入。ACP/Pi 互操作由 Issue 124 独立跟踪，
不属于本边界的兼容层。

## 禁止回归

- 不恢复 `commands.Catalog`、`toolset.Catalog`、Registrar/Registration 或第二套 Registry。
- 不恢复 `filetools`、`workspacefiles`、`toolgroup`、Journal 或独立 FileAccess 事件管线。
- App 不生成 Entries；App 和 Extension 不创建子 Set，不维护通用 cleanup bag 或服务定位器。
- App 不暴露可写 EventBus；事件只经 `Emit` 进入唯一 Stream。
- 连接不能接受通用 Extension 工厂；具体 namespace 直接注册到该连接的 Mux。
- 活跃 Registry 不热替换、不 shadow registration、不保留兼容 fallback。
- `tools/*` 和 `agent/*` 不直接实现或导入 Extension 宿主生命周期。
- 原始实现不关闭由 Profile 拥有的 Registry；业务能力对象不提供资源关闭入口。

架构测试固定以上边界；默认/full 编译和 lifecycle/race 测试是交付门禁。
