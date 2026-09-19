# Shared state

`app.State` 是一个 Profile 内供扩展共享的非生命周期状态：provider 状态、progress 通知、
规范 AOP 事件流以及可替换 logger。

`State` 不拥有 `extension.Set`、配置、scanner、tool、command、skill 或 session。这些资源留在
各自的 Extension 中，并通过类型化 capability 使用。`pkg/exts/app` 创建并发布 `*app.State`；
Profile 唯一的 `extension.Set` 负责启动和关闭。

Provider 更新使用快照，迟到的健康检查不会覆盖更新后的配置。事件发布者和观察者共享同一条
事件流和序号。
