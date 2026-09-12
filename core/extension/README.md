# 统一 Extension 体系

项目只有一套插件生命周期：`extension.Extension`、`extension.Entry`、`extension.Set`。
Extension 是插件的执行契约；Entry 是带 ID 和依赖的安装声明；Set 管理一组插件。
profile 是装配代码，不是第二个插件框架。文件工具注册、操作观察和协议处理各自使用
领域 API，但安装它们的插件都交由同一 Set 装载和回收。

包路径为 `core/extension`，接口与具体安装单元统一称为 `Extension`，构造函数使用
`New` 或 `NewExtension`。安装字段为 `Entry.Extension`，配置字段与 App 集合均为
`Extensions` / `extensions`，CLI 使用 `--extensions`。旧的 Module/Plugin API
及 `core/plugin` 路径已直接迁移，不保留兼容别名。
AOP 的 `Event.Extension` 是协议中的扩展载荷，文件 extension 是文件后缀，两者都不是
插件类型，因此不做协议或文件语义的重命名。

生命周期库被 Cairn/AIScan profile、工具注册和 AOP ToolNode 直接使用。它只负责
固定组合的依赖顺序、回滚和整体回收，不创建业务资源，也不提供服务查找或执行入口。

入口先通过构造函数传入具体依赖，再将真实实例交给 `New(entries...)`。`Entry.DependsOn` 只声明生命周期顺序，不查找、创建或注入服务。构造参数与依赖边的一致性由入口负责。

| API | 行为 |
| --- | --- |
| `New(entries...)` | 无副作用地检查空 ID、空接口、重复 ID、缺失依赖和环；复制依赖列表，按声明顺序遍历生成确定的拓扑顺序 |
| `Load(ctx)` | 按固定 Entry 集合和依赖顺序装载；成功后幂等，失败后封存该 Set |
| `Close(ctx)` | 禁止后续装载，逆拓扑关闭；未完成实例保留依赖，无关模块继续关闭；可重复调用完成回收 |

`Extension` 只有 `Load` 和 `Close` 两个方法，这是统一管理不同资源生命周期所需的唯一执行契约。`Entry` 是装配声明；内部 `item` 保存真实实例、依赖和状态，没有业务结果镜像。`Set` 不提供服务容器、反射注入、Sink、DTO 或清理回调列表。

`Close` 返回值区分资源状态与操作错误：

- `nil`：回收完成。
- 普通错误：回收完成，但处理、刷新或最终提交失败。Set 报告错误并继续释放依赖，后续 Close 不重复调用该实例。
- 包含 `ErrCloseIncomplete` 的错误：仍有工作或资源，必须保留实例及其依赖。使用 `errors.Join(extension.ErrCloseIncomplete, cause)` 保留原始原因；重试只继续未完成部分。

这是关闭契约的直接迁移。所有 Extension 必须显式标记未完成状态；Set 不再将任意错误推断为仍持有资源，也不根据错误文本猜测。父 Set 保留子 Set 返回的标记，包括等待生命周期锁被取消的情况。未能取得锁不证明资源已释放。

Subscription 的 Close 只报告等待失败，历史处理错误通过已有 Err 获取。拥有订阅的实际模块先排空，再收集 Err 和释放资源；等待失败须转换为 ErrCloseIncomplete。不能直接将业务错误当作回收未完成。

模块构造必须无副作用，每个有效实例只能归属一个 Entry/Set；调用者不得重复交付同一实例或传入带类型的 nil。库不引入全局所有权表或反射来追踪调用者对象。装载后的资源只能由所属模块释放，借用者不关闭依赖资源。

所有拓扑操作串行，等待中的调用可以取消。模块方法不得反向调用同一个 Set，以免自锁。模块必须遵守 context；库不会把无法停止的方法放到后台 goroutine 中假装完成关闭。

`Load(ctx)` 的 context 限制初始化操作，不自动拥有模块的整个存活期；持续工作的取消由模块自身管理。`Close(ctx)` 发起停止并限制本次等待时间，超时后由调用者使用新 context 重试。取得串行锁后会再次检查取消状态，已取消的等待者不启动生命周期事务。

Load 失败会先关闭部分初始化的失败模块，再逆序关闭本次新启动的模块，保留此前 active 的实例。回滚沿用调用者的 context，合并原始错误和回收错误；若超时或取消导致回收失败，实例保持 stopping，调用者须使用新的 context 重试 Close。依赖在回收完成前不会释放。

Set 的 Entry 集合固定。卸载结束不代表可把新实例写回同一 ID；当前 API 没有 Add/Replace，不承诺动态更新拓扑或无中断替换。重新装配使用新的实例和 Set。

注册撤销、工作准入和在途任务等待由真实模块实现；`pkg/toolset/registry` 提供了
带 owner 的工具注册与调用排空，本库只控制模块生命周期顺序。

`resource_test.go` 使用实际打开的临时文件，以及通过构造函数借用文件的写入模块，验证资源所有权和在途工作。文件只由其所属模块关闭；消费者停止准入、取消并等待工作后才能完成回收。测试模块均留在测试包，不增加生产 API 或模块包装器。

测试覆盖：构造与空选择无副作用、初始化/单次请求取消与模块存活期分离、写入与读回、消费者卸载后资源仍有效、在途工作与拒绝新工作、关闭超时保持依赖、部分初始化回滚、合并原始错误与回收错误、并发装载/关闭只操作资源一次，以及不同 Set 的资源隔离。

并发测试通过 channel 屏障控制阶段，`testing/synctest` 调度实际的 context 截止时间定时器，使用虚拟时间而非 wall-clock sleep。屏障有失败清理，测试包设置总超时；不将手动返回 DeadlineExceeded 当作真实超时验收。

已验证：

- `go test -mod=readonly -timeout=60s ./core/extension ./core/capability`
- `go test -mod=readonly -race -timeout=90s ./core/extension ./core/capability`
- `go list -mod=readonly -deps ./core/extension`：生产代码仅依赖标准库；生命周期图不依赖产品 capability 目录。

上述结果来自 Windows/amd64、Go 1.26.1 当前工作区。Ubuntu/WSL 本地 Go 为 1.22.2，运行同一测试命令时下载 Go 1.26 工具链失败（proxy.golang.org 连接超时），Linux 测试未能启动。profile 的库级装配测试已在 Windows 运行；Cairn 外部 runner 已使用临时 `-modfile` 指向当前 AIScan 与 AOP 源码，standard/full 的 runtime 与 HTTP evidence 集成测试均通过。验证未使用 `go.work`，临时 modfile 未保留。

2026-09-12：Entry 已改为 ID string、DependsOn []string、Extension 三个字段；旧 Descriptor 字段已删除，调用方需直接迁移。以上 Cairn 验证属于此前版本，当前 API 尚未重新进行跨仓验证。
