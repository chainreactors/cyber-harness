# Extension 生命周期

`core/extension` 管理一个固定、线性的 Extension 列表：

```go
type Extension interface {
    Load(*Scope) error
}

set, err := extension.New(first, second, third)
```

没有 Entry、Extension ID、DependsOn、DAG、Service 表或运行时实例替换。构造参数表达业务
依赖，声明顺序就是加载顺序，关闭顺序固定相反。只有真正持有后台工作或打开资源的插件才
额外实现 `Close(context.Context) error`；纯资源贡献插件不需要空的 Close 方法。

`Scope` 提供初始化 context、生命周期 context 和 typed resource Registry。
`extension.Define[T]` 定义一个资源 Point，`extension.Add[T]` 注册一个原子批次；两者产生的
handle 都归 Scope 所有，并在 `Extension.Close` 前逆序撤销。

`Set.Load` 仅在全部 Extension 成功后发布 Active。加载失败会逆序回滚，包括失败 Extension
已经创建的 Scope handles。成功加载后冻结新资源类型，但已有 Point 仍可接受运行时贡献。

`Set.Close` 开始即取消发布，然后对每个 Extension 执行：取消 Lifetime、撤销 handles、调用
Close。普通清理错误会被收集并继续；`ErrCloseIncomplete` 会保留当前 Extension 及其更早的
依赖，以便调用方使用新 context 重试。取消或超时统一视为未完成关闭。

Load/Close panic 会转成错误，不越过 Set 边界。带类型的 nil Extension 在构造时拒绝。

完整资源与插件约定见 [系统架构](../../docs/architecture.md)。典型验证：

```text
go test -race ./core/resource ./core/extension ./core/registry
```
