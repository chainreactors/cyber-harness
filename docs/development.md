# 开发者指南

[文档首页](README.md) · 前置：[基本概念](concepts.md)

本指南面向把 cyber-harness 嵌入应用的开发者。你可以给现有 Agent 增加工具，也可以自己组合模型、会话和宿主界面。下面从一个无需模型的 Go 程序开始，逐步把工具变成可对话的应用。

## 第一个组合

准备好 [快速开始](getting-started.md)中的源码与 Go 环境，在仓库根目录执行：

```sh
go run ./examples/custom
```

程序输出 `Hello, Cyber!`，然后释放资源退出。[完整源码](../examples/custom/main.go)把一个 hello 工具注册到基础组合，通过执行接口真正调用它。这里还没有模型循环，因此输出是确定的。

先看工具本身。`helloTool` 提供名称、描述、参数定义和 `Execute`。它只处理业务输入并返回结果，不需要知道应用怎样启动。扩展负责把这个对象交给框架：

```go
extension.Func{LoadFunc: func(scope *extension.Scope) error {
    return extension.Add[tool.Tool](scope, helloTool{})
}}
```

`Add[tool.Tool]` 将工具加入基础组合已经定义的贡献点。显式写接口类型很重要：按具体类型注册不会落到同一个贡献点。参数 schema 是模型和执行器理解输入的契约，业务实现仍应处理无效值和执行错误。

## 组合根与宿主

示例先用 `harness.BaseExtensions` 建立基础能力，传入绝对工作目录，并关闭启动时的 Provider 初始化。它随后追加工具扩展和一个最终消费者；消费者用 `Use[tool.Executor]` 借到执行接口。最后，宿主创建 Set、Load、调用工具并 Close。

这个顺序也是资源依赖顺序。工具注册表必须先存在，工具才能加入；宿主必须等 Load 完全成功后才能使用借出的接口。示例把 Close 放在 Load 之前注册的 defer 中，因此部分初始化失败也有清理入口，关闭错误会与业务错误一起返回。

基础组合提供工具、命令、知识、提示词、执行环境和应用状态；它并不自动开始推理或创建会话。要让模型调用这个 hello 工具，在同一组合中加入运行循环和会话运行时即可。

## 从工具到会话

```sh
go run ./examples/session
```

[会话示例](../examples/session/main.go)在基础组合之后加入 `StandardLoop` 和 Session 扩展，借出 `*session.Runtime`。宿主打开一个会话，连续提交两次输入，等待结果，观察结束事件，最后关闭会话和整个组合。

例子使用本地演示 Provider：第一次报告收到 1 条用户消息，第二次报告 2 条。这验证了宿主接入、历史连续性和结束事件；它不调用远程服务，也不模拟模型的工具选择能力。换成实际模型配置后，循环才会依据模型响应调用已注册的工具。

接下来阅读[扩展开发](developer/extensions.md)，将工具扩展为命令、知识和共享服务；再阅读[会话与宿主集成](developer/hosting.md)，把这套组合接入自己的 UI、服务或协议客户端。

## 构建自己的应用

通用应用可以直接使用 `pkg/harness`：`harness.New` 总是安装 `harness.BaseConfig`，把 `Session` 留空就是工具宿主，提供 `Session` 就会安装 `StandardLoop` 和会话运行时；`Session.Loop` 可以替换为自己的循环，`Extensions` 可以加入场景专属工具或服务。这样可以在同一个 harness 包中组合工具型、对话型和领域型应用，同时由 `Harness.Load`、`Harness.Close` 统一管理生命周期。需要更细的生命周期控制时，仍可沿用示例的 `harness.BaseExtensions → append → extension.New`。

```go
h, err := harness.New(harness.Config{
    Base: harness.BaseConfig{Directory: workDir, Provider: provider.StartupConfig{Mode: provider.StartupRequired}},
    Extensions: []extension.Extension{myScannerExtension},
    Session: &agentsession.Config{Option: option},
})
if err != nil { return err }
defer h.Close(context.Background())
if err := h.Load(ctx); err != nil { return err }
runtime, err := h.Runtime()
```

aiscan 的完整安全工具组合由 [cmd/aiscan](../cmd/aiscan/profile.go) 内部构造，不再提供独立的公开发行版构造包。嵌入方可使用 [pkg/harness](../pkg/harness) 构造通用宿主，或从 `harness.BaseExtensions` 取得基础扩展后显式组合自己的能力；这不等价于完整 aiscan 发行版。产品入口先完成配置声明与解析，再构造运行时。最小 Agent 宿主的完整实现可读 [cmd/agent](../cmd/agent)。

构建源码发行版时，`make agent` 生成最小本地 Agent，`make` 生成标准版，`make full` 生成包含前端的完整版。标签来自 [editions.env](../editions.env)，实际步骤来自 [Makefile](../Makefile)。full 需要前端工具链；standard 与 full 均使用 CGO_ENABLED=0。原生录屏需要 CGO 工具链，另见 [record](record.md)。

自定义应用应明确分发哪些模型配置、知识文件和外部运行依赖。二进制中注册了浏览器或外部命令入口，并不意味着目标机器已经安装对应程序。固定源码版本后验证实际部署平台上的启动、一个完整任务和关闭；按 AGPL-3.0 的要求处理分发与源代码提供。

## 继续深入

[架构概览](architecture.md)解释宿主、运行时、工具和协议之间的关系；[扩展装配](architecture/composition.md)进一步解释贡献、借用、回滚与关闭。这些规则在开发有后台工作或共享资源的扩展时尤其重要。

为项目贡献代码时，业务实现保持独立，生命周期适配放在扩展中；行为变化同步更新对应的使用章节。文档归属和验证方式见[文档维护](maintaining-docs.md)。
