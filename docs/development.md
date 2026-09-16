# Cyber 扩展开发手册

新增能力前先确定它是声明、运行资源还是生命周期所有者，不要为同一个对象重复建立机制。

## Tool、Command 与 Skill

- 模型直接调用的结构化能力实现 `core/tool.Tool`。
- bash 内的 pseudo-command 使用 `commands.Command`。
- prompt/知识使用 `skills.Bundle` 或普通 Skill，不并入执行 Registry。

Tool Registry 和 Command Registry 在 Load 时分别定义自己的 typed Point。贡献 Extension 只需：

```go
func (e *Extension) Load(scope *extension.Scope) error {
    return extension.Add[tool.Tool](scope, e.tool)
}
```

批次必须原子校验。Scope 关闭时先撤销该批次并排空它的在途调用，不影响其他插件。名称、
flag、section key 等由各领域校验；不要再增加 Plugin ID、owner token 或通用 typed ID。

## 新资源类型

资源类型是具体 Go 类型。基础 Extension 实现 `resource.Point[T]` 并调用
`extension.Define[T]`，后续插件调用 `extension.Add[T]`。Point 负责该领域的重复、覆盖、
快照和排空规则。不要在 `core/resource` 中加入这些业务策略。

资源类型定义只在 Profile Load 阶段开放；完整 Load 后冻结。已有 Point 可继续接受运行时
热增删。定义者必须排在贡献者前面，较早 Scope 无法反向使用较晚定义。

## Config、CLI 与连接测试

这些是解析前 Resource，不需要 Extension 生命周期：

```go
resources := resource.New()
sections := config.NewSections()
resource.Define[cli.Contribution](resources, cliRegistry)
resource.Define[config.Section](resources, sections)
resource.Define[config.Connection](resources, sections.ConnectionPoint())
plugin.Declare(resources)
resources.Freeze()
sections.Seal()
cliRegistry.Seal()
```

扩展在自己的包中提供 `Declare` 初始化入口，内部直接 `resource.Add` 自己拥有的
`cli.Contribution`、`config.Section` 和可选 `config.Connection`。Declare 不调用 `Define`，不返回
聚合 DTO，也不建立 declaration 子包或第二套插件接口；没有解析前资源的扩展不需要空 Declare。
贡献在 Seal 前可撤销；CLI 按注册顺序物化，因此后面的扩展可以扩展前面声明的命令。连接测试
属于对应配置扩展，不要恢复独立 Probe Registry、Catalog 或 settings Declaration DTO。

## 依赖与生命周期

构造参数表达业务依赖，`extension.New(a, b, c)` 的顺序表达加载和关闭关系。没有 Entry、
DependsOn、Service table、Descriptor 或 capability catalog。底层 Resource 由 Extension 保留，
消费者只接收不含 Load/Close 的业务对象。

Close 返回普通错误表示回收已经完成；只有仍需重试时返回或包装
`extension.ErrCloseIncomplete`。初始化使用 `scope.Init()`，后台工作绑定
`scope.Lifetime()`。

## 组合与验证

完整产品组合根在 `cmd/aiscan`。最小本地 Agent 在 `cmd/agent`，不得依赖 scanner、search、
proxy、IOA、browser、record 或 Web。`pkg/runner` 保留 aiscan 的共享运行模式逻辑，不是命令。

新增或修改资源至少验证：

- 原子批次、重复拒绝和 handle 撤销。
- Load 失败逆序回滚。
- 热删除只取消并排空自己的在途调用。
- Close context 超时后可重试。
- `go test -race ./core/resource ./core/extension ./core/registry`。
- `go list -deps ./cmd/agent` 仍满足最小依赖边界。
