# 内嵌 Arsenal 工具

Arsenal 可以在构建时下载指定工具，把可执行文件压缩并嵌入应用。分发时只需携带应用二进制；加载 arsenal 扩展时，工具自动释放到数据目录下的 `arsenal/bin`，随后通过现有 PATH 使用。释放过程不访问网络。

## 构建

Arsenal 在本仓库的 [arsenal.yaml](../pkg/exts/arsenal/arsenal.yaml) 统一维护工具的仓库、默认版本、平台文件名和使用提示。命令安装和 bundle 构建共用这个目录。新增场景需要的工具直接补充到这里，例如：

```yaml
- name: example
  version: "1.2.3"
  repo: owner/example
  asset_pattern: "{name}_{version}_{os}_{arch}.zip"
  platforms:
    linux/amd64: {}
    windows/amd64: {}
```

空的平台条目使用 `asset_pattern`；特殊平台可以用 `asset` 指定文件名。声明了 `platforms` 的工具会在下载前拒绝未列出的平台。

发行入口只选择工具名称：[aiscan](../cmd/aiscan/bundle.yaml) 选择 `rg`，[audit](../audit/cmd/cyber-audit/bundle.yaml) 按平台选择工具。audit 的完整清单为：

```yaml
id: cyber-audit
catalog: ../../../pkg/exts/arsenal/arsenal.yaml
tools: [rg, ast-grep, osv-scanner]
platforms:
  windows/amd64: [radare2, capa, floss]
  linux/amd64: [capa, floss]
```

无需复制工具定义或维护另一份版本表。工具目录随 cyber-harness 版本维护，增删工具或更新版本无需修改 CRTM。生成器按名称取出默认版本与平台规则；`platforms` 是各平台在公共 `tools` 之上的增量选择。`id` 标识发行版，应跨应用版本保持不变。需要单独覆盖版本时，`tools` 也支持 `{rg: "15.2.0"}` 映射；未知名称在构建时失败。`catalog` 路径相对 bundle 清单。运行时的 Arsenal 扩展嵌入同一份完整目录，所以未打包的工具仍可按需下载；生成元数据仅保留选中工具定义。CRTM 自带目录保留为独立使用时的默认值，本仓库定义优先。audit 的版本预检需要固定版本。

```sh
make ARSENAL_EMBED=1
make full ARSENAL_EMBED=1
make audit ARSENAL_EMBED=1
make ARSENAL_EMBED=1 ARSENAL_CONFIG=path/to/tools.yaml
```

aiscan 默认只包含 ripgrep；audit 使用 `AUDIT_ARSENAL_CONFIG` 覆盖选择清单。`ARSENAL_EMBED` 与扫描模板的 `EMBED` 开关独立，也可同时设置。普通构建只生成工具元数据，不下载二进制。最小 `agent` 发行版没有 arsenal 扩展。

两个构建模式都从同一选择清单生成 `bundle_spec_generated.go`，供运行时预检与安装使用。该小文件纳入版本控制，普通 `go build` 无需先下载工具。修改 YAML 后，`make` 会自动更新；直接调用 `go build` 时，先运行 `make arsenal-spec audit-arsenal-spec`。无需在 Go 中重复维护工具版本或解析构建 YAML。

构建分为资源准备和编译两步，也可以分别执行。在 PowerShell 中：

```powershell
go run github.com/chainreactors/crtm/cmd/crtm-bundle -config cmd/aiscan/bundle.yaml -target windows/amd64 -output cmd/aiscan -package main
go build -tags "forceposix emptytemplates noembed osusergo netgo arsenal_embed" -o bin/aiscan.exe ./cmd/aiscan
```

交叉构建时，生成器在宿主平台运行，`-target` 指定资源平台，然后为同一目标编译应用：

```powershell
go run github.com/chainreactors/crtm/cmd/crtm-bundle -config cmd/aiscan/bundle.yaml -target linux/arm64 -output cmd/aiscan -package main
$env:GOOS = "linux"
$env:GOARCH = "arm64"
$env:CGO_ENABLED = "0"
go build -tags "forceposix emptytemplates noembed osusergo netgo arsenal_embed" -o bin/aiscan-linux-arm64 ./cmd/aiscan
```

Makefile 会自动为生成器使用宿主平台、为资源使用编译目标。aiscan 的生成文件位于 `cmd/aiscan`，audit 的位于 `audit/internal/toolchain`；共享扩展 `pkg/exts/arsenal` 不携带任何发行版的资源。压缩资源位于生成目录的 `assets` 子目录，资源和平台嵌入声明均忽略 Git 跟踪。两种发行版统一使用 `arsenal_embed`，每个目标只编译自己的嵌入声明；未生成相应平台资源时，编译失败。

资源包目录按清单摘要命名，生成器完成全部工具的下载与校验后才发布新目录和嵌入声明。失败不会覆盖之前的完整资源包。资源更新后会留下旧目录，可以在没有构建进行时清理整个 `assets` 目录和生成文件，再重新生成。

## 运行与更新

初始化时，缺失工具从内嵌包释放。已由同一内嵌包管理的工具，在文件摘要未被修改的情况下跟随新应用携带的版本，包括回退到旧应用版本。重复初始化不重写相同文件。

用户通过 `arsenal update` 或指定版本主动替换工具后，该安装转为用户管理。手动修改文件也会停止自动跟随；旧安装记录没有来源字段时同样保留。新包不再携带某工具时，不会自动删除磁盘上的安装。

`arsenal install` 优先使用匹配的内嵌版本，找不到时才尝试远程来源；`arsenal update` 默认查询远程最新版本。指定版本必须精确匹配。内嵌文件损坏或校验失败直接报错，不会悄悄改为下载其他版本。

`arsenal remove` 删除磁盘安装。由于工具仍属于应用携带的集合，下次初始化会重新释放；要永久移除，应从构建清单中删除并重新构建。

内嵌的是 Registry 所选择的可执行文件，不包含工具自行下载的模板、数据库或额外运行库。例如 nuclei 的模板仍由 nuclei 自身管理。

## 复用 CRTM

CRTM 将远程和本地来源统一为 `Source.Resolve`，产物用 `Artifact` 表达。安装器统一处理流式校验、可执行权限、暂存、原子替换及安装记录；并发安装使用操作系统文件锁。安装记录增加可选来源、管理标识和摘要字段，兼容旧记录。

`LoadBundleSpec` 将共享目录与发行选择解析为原有 `BundleSpec`，不引入另一套配置模型。`BuildBundle` 使用同一来源接口构建资源包。`OpenBundle` 接受 `fs.FS`，因此磁盘目录和 `embed.FS` 使用相同实现。Bundle 自身实现 Source，应用只需注入它并调用准备方法：

```go
bundle, err := crtm.OpenBundle(files) // files 可以是 embed.FS 的子目录
if err != nil {
    return err
}
options := ToolSpec.ManagerOption(bundle) // 生成的版本及选中工具定义
options.BinPath, options.ConfigPath = binDir, configPath
manager, err := crtm.NewManager(options)
if err != nil {
    return err
}
return manager.Prepare(ctx, bundle)
```

`NewManager` 只加载配置和状态；`Prepare` 才执行释放。选中的第三方工具定义在普通构建和内嵌构建中都可用，只加入内存目录，不改写用户配置。用户配置可以覆盖发行版定义；与内嵌二进制身份冲突时会报错。仅配置 Bundle Source 可构造完全离线的安装器；不配置 Sources 则保留默认 GitHub 来源。

不需要 Go 嵌入声明时，省略生成器的 `-package`，即可得到可供 `os.DirFS` 读取的独立资源包。这套能力属于 CRTM，aiscan 的命令层只依赖 Manager。
