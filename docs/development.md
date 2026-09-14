# AIScan 扩展开发手册

新增结构化能力时，先阅读 [`tools/README.md`](../tools/README.md)。工具实现
`core/tool.Tool`，静态声明可由 Profile 直接注册，需要资源就绪的声明在 Extension.Load 中注册；工具执行不依赖 Agent、Runtime
或模型。

## 工具与命令的边界

Flags、Config、静态 Skill、协议定义和宿主绑定同样是扩展点，不受 Tool/Command 分类限制。
Flags 复用 `config.FlagGroup` 和普通 Options；入口先声明选项，再解析配置并选择运行扩展。
Config 复用现有 struct/tag、加载、优先级和默认值机制，Profile 将结果注入具体功能。
help、配置模板生成和参数校验不要求 Extension.Load。完整插件接入约定见
[系统架构](architecture.md)。

原生 Tool 适合模型或外部框架直接调用：它提供名称、描述、AOP 定义和
`Execute(context.Context, string)`。需要文件、代理、扫描引擎、IOA 或工作目录的
工具通过构造参数接收这些依赖，资源由拥有它的模块关闭。

Pseudo-command 仍适合通过 `bash` 暴露已有命令行语义。命令实现
`pkg/commands.Command` 的 `Run`，从 `commands.Execution` 读取参数并写入该调用的
输出。命令的注册也由 profile 或应用装配入口显式完成；不再通过 `init` 工厂列表、
`Deps`/`Bag` 或空导入隐式激活。

```go
func (e *Extension) Load(scope *extension.Scope) error {
    return e.commands.Register("scanner", commands.Command{
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

完整 AIScan 产品图和具体 `aiscanProfile` 都声明在 `cmd/aiscan`。`pkg/profile.Application`
是 host 契约，具体 Profile 直接持有 Set；命令入口在唯一 Set 中组合 App 能力和可选
Agent Runtime。Runtime 构造时接收已创建的 App，只使用工具、Provider、Commands、Hooks 和
类型化事件观察；事件发布统一经 `App.Publish`。需要 Agent 时入口显式选择
`agent.StandardLoop{}`，`pkg/exts/agent.Extension` 将受控 Loop 与 Session 宿主统一发布为一个
Runtime。该扩展负责运行准入、寿命取消和排空；没有 Loop 时工具和命令仍可用，
Agent Run 会明确返回未配置错误，不隐藏回退到默认 Loop。

需要初始化或清理的适配器实现 `core/extension.Extension` 的 `Load`/`Close`；底层资源和
Agent Loop 保留普通实现。Profile 的固定 Entry 集合按依赖顺序装载、逆序关闭。
App 的能力贡献者也必须并入该集合，不能在 App 或
其他 Extension 内创建第二个 Set。依赖通过构造函数传递，`DependsOn` 只表达生命周期
顺序，不用于运行时查找。不要增加全局容器、通用输出接口、线协议镜像类型或仅转接用的接口。

测试至少覆盖：工具定义和结构化结果、取消与超时、profile Load/Close、在途调用排空、
失败回滚，以及 `go list -deps` 的无 Agent 工具闭包。
