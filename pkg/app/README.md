# App：内置 Agent 产品访问面

`app.New(config, dependencies)` 无副作用，返回生命周期 `Resource`；Profile 仅发布其中不含
Load/Close 的 `App`。`cmd/aiscan` 构造 App、能力贡献者、
Command Registry 和 Tool Registry，并将它们直接加入 Profile 拥有的唯一
`core/extension.Set`。App 不生成 Entry、不选择插件、不创建子 Set，也不维护资源关闭链。

App 向入口暴露 Provider、Tool Executor、Command Registry、Bash、Skills、Hooks，以及
类型化只读事件观察和统一的 `Publish` 入口。实际资源由 Profile 图中的 Extension 拥有：Terminal 拥有 Bash/PTY，
Scanner 拥有引擎并只向 App 提供只读状态，Proxy 和 IOA 的业务类型从定义上就不含 Close，Registry 拥有调用准入与 drain，
EventOutput 拥有输出文件。`Resource.Close` 只关闭 App 自身状态。

```text
EventOutput → Observe → Proxy / IOA ─┐
Agent Loop ─────────────────────────┼→ App + capability contributors
                                   └→ Command Registry → Tool Registry → Session Runtime
```

关闭逆序执行。App.Publish 始终委托同一 `core/events.Stream` 补全 Event ID、时间和 session
内序号；Session Runtime 只使用该流和已加载能力。Provider 更新按 Run 快照隔离，迟到的
健康探测不会覆盖更新后的 Provider。
