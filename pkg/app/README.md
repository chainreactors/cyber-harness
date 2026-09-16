# App

`app.New(logger, dependencies)` 通过直接构造注入接收 Hooks、Events、Tool/Command Registry、
Skills 和 Bash。它不持有产品配置或 Scanner 状态，不读取 Service 表，不按 capability 选择
插件，也不创建子 Set。

构造直接返回不含 Load/Close 的 `App`。Profile 只在唯一的线性 `extension.Set` 完整激活后
向入口发布 App。Terminal、Scanner、Provider 等 Extension 保留各自资源所有权；App 只借用
业务接口，不伪装成没有实际资源的生命周期对象。

App 统一暴露 Provider 状态、Tool Executor、Command Registry、Skills、Hooks 和 AOP Event
Stream。Provider 更新按 Run 快照隔离，迟到的健康探测不会覆盖后续配置。

产品配置留在 `cmd/aiscan` / `cmd/agent` 组合根，Scanner 配置归 Scanner Extension；扫描能力
直接由其注册的 Command/Tool 资源表达，不维护第二份状态。
