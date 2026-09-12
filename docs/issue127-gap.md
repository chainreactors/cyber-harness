# Issue 127 实施差距

核对日期：2026-09-13。Issue 127 尚未完成；当前工作区仍有大量既有迁移变更。

| 边界 | 当前证据和状态 |
| --- | --- |
| Executor / Registrar 分离 | bd4db598 已提交；删除 tool.Runtime |
| 唯一 Extension 契约 | aa894697 已提交 core/extension 的 Load(*Context)/Close(context.Context)；生产和测试调用方尚未全部迁移 |
| 显式构造依赖 | 核心 Entry 只有 ID、DependsOn、Extension，无 Factory、Descriptor 或 NewWithContext |
| 最小 Context | 只有 Owner、Init、Lifetime、Track；删除业务层级、ServiceKey、服务定位和第二套事件总线 |
| Set 回收契约 | 创建每实例 Context；撤销、取消、排空、依赖保留；最后一个 Load 取消也回滚；撤销 panic 保守保留依赖 |
| App 内 Registry 所有权 | 28f3e499 通过真实 Set 管理内部 Registry，删除临时 LoadContext 兼容入口；App 仍有其他手工装配和关闭链 |
| Registry lease | 已完成第一版；`Registrar.Register` 返回 `Registration`，`Revoke` 停止准入，`Close` 等待在途调用；仍需迁移全部旧生产入口 |
| 文件机制边界 | 已收敛；`files.FS` 是资源 Extension，`filetools`/`workspacefiles` 只构造不同路径策略的工具，`toolgroup.Extension` 统一发布和回收 |
| Session / Run | 未完成；AgentRuntime 和现有协调结构仍在 |
| 产品 Profile 与外部适配 | 未完成；不能以接口存在或 workspace 单个 profile 的成功推断完成 |

已执行并通过：

```text
go test -mod=readonly -race -timeout=90s ./core/extension ./pkg/profile/workspace
go list -mod=readonly -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./core/extension
```

依赖查询只返回该包本身，表明生产代码没有其他非标准库依赖。测试结果来自当前工作区，未验证独立检出或跨仓消费者。

部分历史调用方编译探测仍失败：

```text
go test -mod=readonly -run '^$' ./pkg/profile/... ./pkg/toolset/... ./pkg/app ./pkg/runtime
```

剩余失败主要是历史测试直接把 `context.Context` 传给具体 Extension，以及少量旧测试构造名。生产构建 `go build -mod=readonly ./...` 已通过；相关文件、工具组和 Profile 测试已迁移并通过。后续必须继续把历史测试按真实 Set 所有权迁移，不能恢复双生命周期接口。当前仍没有全仓测试、full 标签或 Pi/ACP 外部适配器通过的结论。

架构边界应继续以以下语义评估：Session 可以包含多个 Run；Root 可以拥有显式共享的资源；Session 不拥有其他 Session 的集合；连接属于真正创建它的入口，未必属于 Session；具体 Extension 负责领域注册，Profile 负责实例装配。调用元数据路径为 core/tool/context.go。Pi 的 AgentContext 不应被描述为由其类型保证不可变的请求快照。
