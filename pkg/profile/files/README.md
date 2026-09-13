# Minimal file profile

`pkg/profile/files` 是无 Agent 的最小文件组合：一个 `pkg/exts/files.Extension`、一个
`pkg/toolset.Registry` 和一张 `core/extension.Set`。

```text
files extension → tool registry
```

`New` 直接接收 `tools/files.Config` 且不触碰文件系统。`Load` 打开 root 并原子发布工具；
整图加载完成后 `Executor` 才可借用。`Close` 先关闭 Registry 的调用准入并 drain，再释放
文件 root；调用方即使保留旧 Executor，也无法在关闭后执行。

需要事件观察、JSONL 输出或 Skills mount 的 runner 使用 `pkg/profile/workspace`，显式选择
`files,observe,skills`。这些可选能力不进入最小 Profile 的依赖闭包。

```powershell
go test -race -count=1 ./core/registry ./pkg/toolset ./tools/files ./pkg/exts/files ./pkg/profile/files
```
