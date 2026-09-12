# Extension 生命周期

`core/extension` 只依赖 Go 标准库。依赖通过构造参数传递；`Entry.DependsOn` 只表达启动和关闭顺序。

```go
type Extension interface {
    Load(*Context) error
    Close(context.Context) error
}

type Entry struct {
    ID string
    DependsOn []string
    Extension Extension
}
```

`Set.New` 校验固定依赖图，无业务副作用。`Set.Load(context.Context)` 是装配入口，为每个启动的 Entry 创建唯一 Context，再按拓扑调用 Extension.Load；它不是第二套 Extension 接口。构造和 Entry 不支持 Factory、服务定位或运行时替换。实例不得重复归属不同 Entry/Set，也不得传入带类型的 nil。

`Context` 只有四种操作：

- `Owner()`：当前安装实例的唯一注册身份，不是持久化业务 ID。
- `Init()`：仅限制初始化。初始化返回后取消它不会结束 Extension 寿命。
- `Lifetime()`：关闭该 Extension 时取消，不从初始化 context 继承业务值。
- `Track(Dispose)`：接管同步、无等待的注册撤销。返回的 Dispose 可提前撤销，并发调用也只执行一次。登记失败时仍由调用者撤销注册。

Context 没有 Root/Runtime/Session 枚举、父子树、Provide/Require、通用事件总线或公开 Close。业务资源的归属由装配决定。

Set 串行执行生命周期，等待锁可以取消。Load 失败或最后一个 Load 返回时初始化已取消，都会封存 Set，并逆序回滚本次开始初始化的实例（包括失败实例）。回滚沿用初始化 context；未完成清理须使用新的 Close context 重试。

关闭顺序：封存 Track → 逆序撤销注册 → 取消寿命 → Extension.Close 排空并释放资源 → 关闭依赖。Dispose 不能等待在途工作、调用所属 Set 或递归调用自身；需等待或报告业务错误的清理由 Extension.Close 处理。Context 不会自动接管未交给 Track 的领域注册。

Close 的返回值区分资源状态：

- nil：回收完成。
- 普通错误：回收完成但刷新等操作失败；报告错误，继续释放依赖，不重复调用该实例。
- 包含 `ErrCloseIncomplete`：仍有资源或工作；保留实例及其依赖，无关实例继续关闭，之后可以重试。

Dispose panic 会被记录；其他撤销、寿命取消及资源 Close 仍继续。由于撤销状态未知，Set 持续报告 ErrCloseIncomplete 并保留依赖，重试不会重新执行该 Dispose。它是需要修复的回调缺陷，不是可以通过反复 Close 自动恢复的临时错误。已经完成的资源 Close 不会因此重复执行。

本次验证（Windows/amd64，Go 1.26.1，当前工作区）：

```text
go test -mod=readonly -race -timeout=90s ./core/extension ./pkg/profile/workspace
```

通过。覆盖依赖校验、启动回滚、关闭重试、资源排空、owner 隔离、初始化与寿命取消分离、并发撤销和撤销 panic。`go list -deps` 确认生产依赖只有标准库。

这些结果不代表全仓、独立检出或外部适配器已验收。当前调用方迁移仍不完整，详见 ../../docs/issue127-gap.md。
