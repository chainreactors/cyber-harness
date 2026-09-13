# 订阅所有权

Bus 只持有订阅集合。同步和异步订阅都返回已有的 `*Subscription[T]`，
插件直接持有该对象；事件仍使用调用方原有类型。

| API | 语义 |
| --- | --- |
| `Subscribe(handler)` | 在生产者上同步调用；并发 Emit 可以同时调用同一 handler |
| `SubscribeFiltered(filter, handler)` | 同步过滤和调用，过滤器也计入在途回调 |
| `SubscribeAsync(options, handler)` | 有界队列和单个串行 worker；沿用容量、复制、错误和丢弃策略 |
| `Cancel()` | 停止接纳、移出 Bus、丢弃异步待处理队列；不等待已接纳的回调 |
| `Flush(ctx)` | 保持接纳，等待调用前已接纳的同步/异步工作完成 |
| `Close(ctx)` | 停止接纳、移出 Bus、等待已接纳的回调；完成返回 nil，等待超时返回 context 错误 |
| `Stopped()` | 停止接纳的信号 |
| `Done()` | 回调全部结束的信号；异步模式包含 OnDrop/OnError |
| `Err()` | 异步处理终止错误；Close 不清除、不代为返回此错误 |

旧分发快照持有真实订阅的指针，每次调用前检查准入。已经停止的订阅不会因为旧快照
再次进入回调。同步回调内可调用 Cancel；不能同步等待自己的 Close/Done。
同步 panic 向生产者传播，但仍释放在途计数；异步 panic 继续按原有订阅错误策略处理。

插件释放回调使用的资源前必须等待 Close 成功。Close 超时后仍持有同一订阅，用新的
context 重试；不能把 Cancel 返回当作资源可释放的证明。Close 不会中断用户代码，
阻塞回调需要自己的取消机制。异步 Filter/Size/Clone 在准入锁内执行，必须快速且不可
重入订阅；Close 的 context 不会强行中断这些函数。

无资源的 Bus 不需要 Extension 包装。实际插件在 Load 中订阅、在 Close 中排空，
profile 通过 `DependsOn` 保留它借用的资源。`core/extension/subscription_test.go`
使用真实文件验证关闭超时保留依赖、回调写入完成后关闭文件，以及拒绝后续事件。

处理失败、队列溢出或异步 panic 后，订阅仍可完成 Close。资源所有者在 Close 成功后
显式收集 Err，不能将历史错误误认为订阅仍在运行。实现 extension.Extension 的所有者将
等待失败包装为 extension.ErrCloseIncomplete，排空后的处理错误则作为普通终结错误报告。
eventbus 自身不依赖 plugin，不增加生命周期适配器。

nil Subscription 的 Cancel/Close 可用于未装配的可选订阅；其他方法需要有效实例。

```sh
go test -race -timeout 60s ./core/eventbus ./core/output ./core/extension
```
