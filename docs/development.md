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

需要独立进程工具能力时，使用 `pkg/profile/files`：它组合具体
`pkg/exts/files.Extension`（拥有 `tools/files.Files`）和 `toolset.Registry`，完整 Load 后返回 `tool.Executor`。AOP ToolNode 只负责协议
入口；它不创建 Agent、Runtime、App 或第二套执行循环。

```go
profile, err := filesprofile.New(files.Config{Directory: workDir})
if err != nil { return err }
defer profile.Close(context.Background())
if err := profile.Load(ctx); err != nil { return err }
executor, err := profile.Executor()
if err != nil { return err }
// 将 executor 交给入口。此处省略关闭错误处理；实际入口须检查
// profile.Close 的结果，遇到 ErrCloseIncomplete 时保留实例并重试。
```

完整 AIScan 产品使用 `pkg/profile/aiscan`。profile 在唯一 Set 中组合 App 能力和可选
Session Manager；Manager 构造时接收已创建的 App，只使用工具、Provider、Commands、Hooks 和
只读事件订阅。事件发布统一经 `App.Emit`。需要 Agent 时入口显式选择
`agent.StandardLoop{}`，Profile 将选定的 Loop 装入 `pkg/exts/agent`，再通过 `agent.Loop`
接口注入 Session。该扩展负责运行准入、寿命取消和排空；没有 Loop 时工具和命令仍可用，
Agent Run 会明确返回未配置错误，不隐藏回退到默认 Loop。

拥有独立资源或注册的适配器实现 `core/extension.Extension` 的 `Load`/`Close`；底层资源和
Agent Loop 保留普通实现。Profile 的固定 Entry 集合按依赖顺序装载、逆序关闭。
App 的 `Entries` 也必须并入该集合，不能在 App 或
其他 Extension 内创建第二个 Set。依赖通过构造函数传递，`DependsOn` 只表达生命周期
顺序，不用于运行时查找。不要增加全局容器、通用输出接口、线协议镜像类型或仅转接用的接口。

测试至少覆盖：工具定义和结构化结果、取消与超时、profile Load/Close、在途调用排空、
失败回滚，以及 `go list -deps` 的无 Agent 工具闭包。
