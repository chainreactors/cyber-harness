# Extension 生命周期

`core/extension` 只依赖 Go 标准库。依赖通过构造参数传递；`Entry.DependsOn` 只表达启动和关闭顺序。

```go
type Extension interface {
    Load(*Scope) error
    Close(context.Context) error
}

type Entry struct {
    ID string
    DependsOn []string
    Extension Extension
}
```

`Set.New` 校验固定依赖图，无业务副作用。`Set.Load(context.Context)` 是装配入口，为每个启动的 Entry 创建独立 Scope，再按拓扑调用 Extension.Load；它不是第二套 Extension 接口。构造和 Entry 不支持 Factory、服务定位或运行时替换。实例不得重复归属不同 Entry/Set，也不得传入带类型的 nil。跨 Set 的实例占用在 Load 时原子取得，完全关闭后释放，因此只构造但未加载的候选图不会污染进程状态。

`Scope` 只有三种操作：

- `Init()`：仅限制初始化。初始化返回后取消它不会结束 Extension 寿命。
- `Lifetime()`：关闭该 Extension 时取消，不从初始化 context 继承业务值。
- `Track(func())`：接管同步、无等待的注册撤销。登记失败时仍由调用者立即撤销注册。

Scope 没有 ID/Ref、Root/Runtime/Session 枚举、父子树、Provide/Require、通用事件总线或公开 Close。注册批次的唯一性由固定 Registry 自身保证，不再生成 owner token。业务资源的归属由装配决定。

Set 串行执行生命周期，等待锁可以取消。初始化 context 就是调用方传给 Set.Load 的
context，只能在 Load 内使用；若它在加载期间取消，Set 会封存并逆序回滚本次开始初始化的
实例（包括失败实例）。回滚沿用该 context；未完成清理须使用新的 Close context 重试。

关闭顺序：封存 Track → 逆序撤销注册 → 取消寿命 → Extension.Close 排空并释放资源 → 关闭依赖。撤销回调不能等待在途工作或调用所属 Set；需等待或报告业务错误的清理由 Extension.Close 处理。Scope 不会自动接管未交给 Track 的领域注册。

Set 会把 Extension.Close 返回的 `context.Canceled` 或 `context.DeadlineExceeded` 统一标记为
`ErrCloseIncomplete`；适配器只返回原始 context 错误，不重复编码宿主策略。Close 的其他返回值区分资源状态：

- nil：回收完成。
- 普通错误：回收完成但刷新等操作失败；报告错误，继续释放依赖，不重复调用该实例。
- 包含 `ErrCloseIncomplete`：仍有资源或工作；保留实例及其依赖，无关实例继续关闭，之后可以重试。

撤销 panic 会被记录；其他撤销、寿命取消及资源 Close 仍继续。由于撤销状态未知，Set 持续报告 ErrCloseIncomplete 并保留依赖，重试不会重新执行该回调。它是需要修复的回调缺陷，不是可以通过反复 Close 自动恢复的临时错误。已经完成的资源 Close 不会因此重复执行。

Extension 的 Load/Close panic 不会越过 Set：Load panic 转为启动失败并触发逆序回滚；Close
panic 转为可重试的未完成关闭，依赖继续受保护。

典型验证：

```text
go test -mod=readonly -race ./core/extension ./core/registry ./pkg/profile ./cmd/runner
```

覆盖依赖校验、启动回滚、关闭重试、资源排空、初始化与寿命取消分离和撤销 panic。完整边界与全仓验收见 [Issue 127](../../docs/issue127-extension-boundary.md)。
