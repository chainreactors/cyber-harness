# 内嵌 Arsenal 工具

Arsenal 可以在构建时下载指定工具，把可执行文件压缩并嵌入应用。分发时只需携带应用二进制；加载 arsenal 扩展时，工具自动释放到数据目录下的 `arsenal/bin`，随后通过现有 PATH 使用。释放过程不访问网络。

## 构建

Arsenal 在本仓库的 [arsenal.yaml](../tools/arsenal/arsenal.yaml) 统一维护工具的仓库、默认版本、平台文件名和使用提示。命令安装和 bundle 构建共用这个目录。新增场景需要的工具直接补充到这里，例如：

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
catalog: ../../../tools/arsenal/arsenal.yaml
tools: [rg, ast-grep, osv-scanner]
platforms:
  windows/amd64: [radare2, capa, floss]
  linux/amd64: [capa, floss]
```

无需复制工具定义或维护另一份版本表。工具目录随 cyber-harness 版本维护，增删工具或更新版本无需修改 CRTM。生成器按名称取出默认版本与平台规则；`platforms` 是各平台在公共 `tools` 之上的增量选择。`id` 标识发行版，应跨应用版本保持不变。需要单独覆盖版本时，`tools` 也支持 `{rg: "15.2.0"}` 映射；未知名称在构建时失败。`catalog` 路径相对 bundle 清单。运行时由 `tools/arsenal` 嵌入同一份完整目录，所以未打包的工具仍可按需下载；生成元数据只保留选中工具定义。显式目录是完整目录，不隐式混入 CRTM 默认工具；省略目录时 CRTM 才使用自己的默认值。audit 的版本预检需要固定版本。

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

Makefile 会自动为生成器使用宿主平台、为资源使用编译目标。aiscan 的生成文件位于 `cmd/aiscan`，audit 的位于 `audit/internal/toolchain`；共享命令包 `tools/arsenal` 只嵌入目录，不携带任何发行版的工具载荷。压缩资源位于生成目录的 `assets` 子目录，资源和平台嵌入声明均忽略 Git 跟踪。两种发行版统一使用 `arsenal_embed`，每个目标只编译自己的嵌入声明；未生成相应平台资源时，编译失败。

资源包目录按清单摘要命名，生成器完成全部工具的下载与校验后才发布新目录和嵌入声明。失败不会覆盖之前的完整资源包。资源更新后会留下旧目录，可以在没有构建进行时清理整个 `assets` 目录和生成文件，再重新生成。

## 运行与更新

初始化时，缺失工具从内嵌包释放。已由同一内嵌包管理的工具，在文件摘要未被修改的情况下跟随新应用携带的版本，包括回退到旧应用版本。重复初始化不重写相同文件。

用户通过 `arsenal update` 或指定版本主动替换工具后，该安装转为用户管理。手动修改文件也会停止自动跟随；旧安装记录没有来源字段时同样保留。新包不再携带某工具时，不会自动删除磁盘上的安装。

`arsenal install` 优先使用匹配的内嵌版本，找不到时才尝试远程来源；`arsenal update` 默认查询远程最新版本。指定版本必须精确匹配。内嵌文件损坏或校验失败直接报错，不会悄悄改为下载其他版本。

`arsenal remove` 删除磁盘安装。由于工具仍属于应用携带的集合，下次初始化会重新释放；要永久移除，应从构建清单中删除并重新构建。

内嵌的是 Registry 所选择的可执行文件，不包含工具自行下载的模板、数据库或额外运行库。例如 nuclei 的模板仍由 nuclei 自身管理。

## 运行时结构

- `tools/arsenal/arsenal.yaml`：工具定义、默认版本、平台文件名和使用提示。
- 发行入口的 `bundle.yaml`：公共选择、平台增量和可选版本覆盖。
- `tools/arsenal.NewManager`：统一加载完整目录、发行定义和用户配置，打开同一份安装状态。
- `pkg/exts/arsenal`：在扩展加载时调用 `Manager.Prepare(ctx)`，注册现有 `arsenal` 命令。
- audit 的 toolchain：检查当前平台要求的版本和 CLI 能力，预检与会话复用同一个 Manager。

CRTM 的 `Source.Resolve` 统一远程下载与内嵌来源，`Artifact` 表达单个可执行文件。安装器负责校验、暂存、原子替换、安装记录和文件锁。调用方不遍历来源、不猜测 bundle 在来源列表中的位置，也不重复维护安装状态。

```go
bundle, err := EmbeddedBundle()
if err != nil {
    return err
}
manager, err := arsenal.NewManager(directory, ToolSpec.ManagerOption(bundle))
if err != nil {
    return err
}
// 扩展只使用已经创建的 Manager。
arsenalExtension := arsenalext.New(manager)
```

`NewManager` 只读取配置和状态，`Prepare` 才释放已配置来源中的 bundle，且不访问远程来源。因此 doctor 可以复用同一个构造入口而保持只读。安装和更新共用 `Manager.Install(ctx, name, version, validate)`：空版本使用来源默认值，具体版本安装该版本，`latest` 请求远程更新；取消信号传递到下载和安装。

`ManagerOption.Catalog` 表达完整目录，nil 才表示 CRTM 默认目录。用户的 `custom_tools` 覆盖该目录；与内嵌二进制身份冲突时拒绝初始化。显式目录中不存在的工具在选择阶段失败，YAML 的未知字段和重复名称也会报错。

`BundleSpec.Definitions` 是生成器解析后的选中定义，供非内嵌构建和内嵌构建共用，不是另一份手写工具清单。新增工具或更新版本时修改统一 YAML，再运行生成命令即可。

## 独立使用 CRTM

`LoadBundleSpec` 解析目录与选择，`BuildBundle` 构建资源包，`OpenBundle` 接受 `fs.FS`。Bundle 本身实现 Source，磁盘目录和 `embed.FS` 使用相同安装流程。`ToolSpec.ManagerOption(bundle)` 生成选中目录和来源链；不需要 harness 的完整目录时可直接传给 `crtm.NewManager`。

不需要 Go 嵌入声明时，省略生成器的 `-package`，即可得到由 `os.DirFS` 读取的资源包。仅配置 Bundle Source 即可构造离线安装器；默认来源是 GitHub。
