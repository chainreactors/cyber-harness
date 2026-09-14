# AIScan 扩展开发手册

新增结构化能力时，先阅读 [`tools/README.md`](../tools/README.md)。工具实现
`core/tool.Tool`，由 profile 构造实际插件，在插件 Load 中显式注册；工具执行不依赖 Agent、Runtime
或模型。

## 工具与命令的边界

原生 Tool 适合模型或外部框架直接调用：它提供名称、描述、AOP 定义和
`Execute(context.Context, string)`。需要文件、代理、扫描引擎、IOA 或工作目录的
工具通过构造参数接收这些依赖，资源由拥有它的模块关闭。

Pseudo-command 仍适合通过 `bash` 暴露已有命令行语义。命令实现
`pkg/commands.Command` 的 `Run`，从 `commands.Execution` 读取参数并写入该调用的
输出。命令的注册也由 profile 或应用装配入口显式完成；不再通过 `init` 工厂列表、
`Deps`/`Bag` 或空导入隐式激活。

```go
func (e *Extension) Load(scope *extension.Scope) error {
    return e.commands.Register(scope, "scanner", commands.Command{
        Name: "whatweb", Usage: "whatweb <url>",
        Run: func(ctx context.Context, execution *commands.Execution) (any, error) {
            return runWhatweb(ctx, execution)
        },
    })
}
```

需要独立进程工具能力时，使用 `cmd/runner` 的显式文件组合：它组合具体
`pkg/exts/files.Extension`（拥有 `tools/files.Files`）和 `toolset.Registry`，完整 Load 后返回 `tool.Executor`。AOP ToolNode 只负责协议
入口；它不创建 Agent、Runtime、App 或第二套执行循环。

```go
// cmd/runner 中的产品装配入口；共享包不提供具体 Profile。
profile, err := newFileProfile(files.Config{Directory: workDir})
if err != nil { return err }
defer profile.Close(context.Background())
if err := profile.Load(ctx); err != nil { return err }
executor, err := profile.Executor()
if err != nil { return err }
// 将 executor 交给入口。此处省略关闭错误处理；实际入口须检查
// profile.Close 的结果，遇到 ErrCloseIncomplete 时保留实例并重试。
```

完整 AIScan 产品图声明在 `cmd/aiscan`，并构造一个具体 `pkg/profile.Profile`。Profile
只持有唯一 Set、App、可选 Session Runtime 与资源 namespace 发布函数。需要 Agent 时，
入口显式选择 `agent.StandardLoop{}`，由 `pkg/exts/agent.Extension` 发布带准入、寿命取消
和 drain 的 Loop。`pkg/exts/session.Extension` 接收该 Loop 与已创建的 App，独立管理
Session、Run、Inbox、历史和协议；Set 依赖保证 Session 先关闭。没有 Loop 时仍可构造
只提供历史和控制能力的 Session 宿主，但推理 Run 会明确返回未配置错误。

拥有独立资源或注册的适配器实现 `core/extension.Extension` 的 `Load`/`Close`；底层资源和
Agent Loop 保留普通实现。Profile 的固定 Entry 集合按依赖顺序装载、逆序关闭。
App 的能力贡献者也必须并入该集合，不能在 App 或
其他 Extension 内创建第二个 Set。依赖通过构造函数传递，`DependsOn` 只表达生命周期
顺序，不用于运行时查找。不要增加全局容器、通用输出接口、线协议镜像类型或仅转接用的接口。

测试至少覆盖：工具定义和结构化结果、取消与超时、profile Load/Close、在途调用排空、
失败回滚，以及 `go list -deps` 的无 Agent 工具闭包。
