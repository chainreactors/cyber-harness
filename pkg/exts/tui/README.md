# TUI Bindings Point

TUI Extension 在 Load 时定义 `*console/api.Bindings` typed resource。Session 和 IOA 的展示
适配 Extension 随后通过自己的 Scope 贡献命令、补全和状态；它们不接收 Registrar、不携带
插件 Source ID，也不查找运行中服务。

```text
tui.Load                    -> Define[*console.Bindings]
session.NewConsole().Load        -> Add session bindings
ioa/client.NewConsole(...).Load     -> Add IOA bindings
Profile.ConsoleBindings     -> Seal and publish snapshot
```

命令名和 alias 是 Console 领域的真实标识，冲突直接报错。贡献批次随其 Scope 自动撤销，
注册时只保存 inert factories，不执行命令。终端挂接时通过 `View.Command` 绑定当前 Session；
Session 的协议命令在没有 TUI 时仍可使用。

`pkg/cli` 管理进程参数，Command Registry 管理 bash pseudo-command，本 Point 只管理 REPL 展示。
