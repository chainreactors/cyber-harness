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

`Scope` 只有两个上下文访问器：

- `Init()`：仅限制初始化。初始化返回后取消它不会结束 Extension 寿命。
- `Lifetime()`：关闭该 Extension 时取消，不从初始化 context 继承业务值。

Scope 没有 ID/Ref、Root/Runtime/Session 枚举、父子树、Provide/Require、通用事件总线或公开 Close。注册批次的唯一性由固定 Registry 自身保证，不再生成 owner token。业务资源的归属由装配决定。

Set 串行执行生命周期，等待锁可以取消。`Set.Active()` 是完整图唯一的发布门：仅在全部
Entry 加载成功后为 true，Close 请求一开始即变为 false，并发 Close 不会让尚在 Load 的图
重新发布。初始化 context 就是调用方传给 Set.Load 的
context，只能在 Load 内使用；若它在加载期间取消，Set 会封存并逆序回滚本次开始初始化的
实例（包括失败实例）。回滚沿用该 context；未完成清理须使用新的 Close context 重试。

关闭顺序：取消当前节点寿命 → Extension.Close 排空并释放资源 → 关闭依赖。Scope 不接管资源或注册。固定声明由 Profile 独占的 Registry 整体持有；订阅和后台工作由实际所有者在 Close 中清理。

Set 会把 Extension.Close 返回的 `context.Canceled` 或 `context.DeadlineExceeded` 统一标记为
`ErrCloseIncomplete`；适配器只返回原始 context 错误，不重复编码宿主策略。Close 的其他返回值区分资源状态：

- nil：回收完成。
- 普通错误：回收完成但刷新等操作失败；报告错误，继续释放依赖，不重复调用该实例。
- 包含 `ErrCloseIncomplete`：仍有资源或工作；保留实例及其依赖，无关实例继续关闭，之后可以重试。

Extension 的 Load/Close panic 不会越过 Set：Load panic 转为启动失败并触发逆序回滚；Close
panic 转为可重试的未完成关闭，依赖继续受保护。

典型验证：

```text
go test -mod=readonly -race ./core/extension ./core/registry ./pkg/profile ./cmd/runner
```

覆盖依赖校验、启动回滚、关闭重试、资源排空、初始化与寿命取消分离和生命周期 panic。完整装配约定见 [静态扩展设计](../../docs/extension-minimal-design.md)。
