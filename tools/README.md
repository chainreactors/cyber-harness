# Extending AIScan

Agent 工具实现 `core/tool.Tool`；Bash 原生命令使用 `pkg/commands.Command`。二者是不同
调用协议，不应通过适配器伪装成一个万能抽象。

Tool Extension 在 Load 时调用 `pkg/toolset.Registry.Register(scope, tools...)`，Command
Extension 调用 `pkg/commands.Registry.Register(scope, group, commands...)`。注册批次由
Registry 原子完成，Scope 只记录失败回滚所需的撤销动作。Profile 让 Registry 依赖所有
贡献者，因此 Registry 先关闭并 drain，贡献者随后释放其资源。

```go
profile, err := filesprofile.New(files.Config{Directory: workDir})
if err != nil { return err }
if err := profile.Load(ctx); err != nil { return err }
defer profile.Close(context.Background())
executor, err := profile.Executor()
```

工具使用 `tool.TextResult` 返回文本，使用 `tool.Result.Details` 返回结构化数据；真实执行
失败返回 Go error，并遵守 context 取消。构造函数显式接收 scanner、proxy、IOA 或工作
目录等依赖。不要增加全局 Registry、factory 清单、依赖 bag、兼容 wrapper 或第二套 DTO。

`tools/*` 保存原始领域实现与 Tool/Command 声明，`pkg/exts/*` 适配 Profile 生命周期和
Registry 贡献，`pkg/profile/*` 是组合根。
