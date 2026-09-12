# Extending AIScan with a tool

Native tools implement `core/tool.Tool` and are registered explicitly by the
profile that owns them. A tool does not need an Agent, Runtime, Console, or
model provider.

```go
type Args struct { Text string `json:"text"` }

type Echo struct{}
func (Echo) Name() string { return "echo" }
func (Echo) Description() string { return "Return text unchanged." }
func (Echo) Definition() *aop.ToolDefinition {
    return tool.Def("echo", "Return text unchanged.", Args{})
}
func (Echo) Execute(ctx context.Context, raw string) (*tool.Result, error) {
    args, err := tool.ParseArgs[Args](raw)
    if err != nil { return nil, err }
    if err := ctx.Err(); err != nil { return nil, err }
    return tool.TextResult(args.Text), nil
}
```

The profile constructs the owning module; the module registers its tools during Load. For a standalone tool profile use `pkg/toolset/registry`; for the
minimal file runner use `pkg/profile/files`:

```go
profile, err := filesprofile.New(files.Config{Directory: workDir})
if err != nil { return err }
if err := profile.Load(ctx); err != nil { return err }
defer profile.Close(context.Background())
executor, err := profile.Executor()
if err != nil { return err }
```

`Registry.Register(owner, tools...)` publishes a complete owner group.
`ExecuteTool` preserves the caller context and Invocation, rejects new calls
after owner removal, waits for accepted calls during `UnregisterOwner`, and
returns structured tool results. `Close` is the final registry lifecycle
operation. Modules unregister their owner before closing resources borrowed by
their tools.

Use `tool.TextResult` for normal text, `tool.ErrorResult` for a model-visible
tool failure, and `Result.Details` for structured domain data. Return a Go
error when execution itself failed, and honor cancellation and deadlines.

Tools with scanner, proxy, IOA, or working-directory dependencies receive those
objects in their constructors. Product profiles own the composition; do not
add a global registry, factory list, dependency bag, Sink, or DTO to avoid an
explicit constructor.

Pseudo-commands exposed through `bash` retain their command-specific
registration APIs and are documented in [`docs/development.md`](../docs/development.md).


## 插件边界

`tools` 只提供工具和资源的原始实现。生命周期、工具发布和依赖顺序由 `pkg/exts` 中的适配器交给 `core/extension.Set` 管理。新工具应实现 `tool.Tool`，由对应的 `pkg/exts/<name>` 插件在 Load 时通过 `Context.RegisterTools` 声明。
