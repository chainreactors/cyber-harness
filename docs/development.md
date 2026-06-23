# Aiscan 扩展开发手册

Aiscan 提供两种对 AI 零侵入的扩展机制，开发者无需修改 agent 核心代码即可为 AI 增加新能力：

| 扩展方式 | 实现方式 | 侵入程度 | 适用场景 |
|----------|----------|----------|----------|
| **Bash Tool** | 编译内置（Pseudo-Command）或运行时下载（Arsenal） | 代码/零代码 | 为 AI 增加可执行的工具能力 |
| **Skill** | Markdown 文件 | 零代码 | 指导 AI 使用工具的策略和流程 |

两者的关系：

```
Skill（行为策略层）
  │  告诉 AI 何时、如何使用工具
  │
  └── 引用 → Bash Tool（能力层）
                │
                ├── Pseudo-Command：编译到二进制，进程内执行
                │     例：gogo, spray, scan, zombie, neutron
                │
                └── Arsenal 工具：运行时下载，PTY 子进程执行
                      例：nuclei, httpx, subfinder, ffuf
```

## 1. 架构概览

### 调用流程

AI 的所有工具调用最终都通过 `bash` 工具执行。`tmux.Manager` 根据命令名决定执行路径：

```
AI 调用 bash(command="gogo -i 10.0.0.1 -p 80")
  → BashTool.Execute()
    → tmux.Manager.RunCommand("gogo -i 10.0.0.1 -p 80")
      → firstCommandToken() 提取 "gogo"
      → 是否注册了名为 "gogo" 的 Command？
        → 是（Pseudo-Command）: goroutine 内执行 cmd.Execute(ctx, args)
        → 否（Arsenal 或普通命令）: PTY 子进程执行 shell 命令
      → 输出捕获并返回给 AI
```

无论哪种路径，AI 看到的都是统一的 bash 调用接口。长时间运行的命令（超过 15 秒）会自动后台化，返回 session id，增量输出通过 inbox 自动推送。

---

## 2. Bash Tool 扩展

### 2.1 统一调用模型

所有 Bash Tool 扩展对 AI 呈现统一的调用方式：

```
bash(command="<tool_name> <args>")
```

两种实现路径：

| | Pseudo-Command | Arsenal |
|---|---|---|
| **本质** | Go 代码编译进二进制 | 独立 CLI 二进制，运行时下载 |
| **执行方式** | 进程内 goroutine | PTY 子进程 |
| **注册方式** | `Command` 接口 + `RegisterFactory` | `arsenal add <repo>` + `arsenal install` |
| **需要编译** | 是 | 否 |
| **适用场景** | 深度引擎集成、需要访问内部资源 | 已有 CLI 工具的快速接入 |

### 2.2 方式一：Pseudo-Command（编译内置）

#### Command 接口

```go
// pkg/commands/command.go
type Command interface {
    Name() string                                      // 命令名，AI 用此名称调用
    Usage() string                                     // 用法说明，注入到 system prompt
    Execute(ctx context.Context, args []string) error  // 执行逻辑，args 已按 shell 规则解析
}
```

#### 输出方式

伪命令通过 `commands.Output` 全局 writer 输出结果。tmux.Manager 在执行前/后自动设置 Output 指向会话缓冲区：

```go
func (c *MyCommand) Execute(ctx context.Context, args []string) error {
    fmt.Fprint(commands.Output, "scan result here\n")
    return nil
}
```

#### 可选接口

```go
// 工作目录感知 — 初始化和 SetWorkDir 时自动调用
type WorkDirAware interface {
    SetWorkDir(dir string)
}

// 代理更新 — proxy 命令切换代理时自动调用
interface { SetProxy(proxy string) }
```

#### 工厂注册机制

所有伪命令通过 **工厂模式** 在 `init()` 中注册：

```go
// pkg/commands/factory.go
type Factory struct {
    Group string                                    // 工具组名
    Build func(deps *Deps, reg *CommandRegistry)    // 构建函数
}

func RegisterFactory(f Factory)                                    // 全局注册
func BuildAll(deps *Deps, reg *CommandRegistry)                    // 构建所有组
func BuildGroup(group string, deps *Deps, reg *CommandRegistry)    // 构建指定组
```

**工具组（Group）分类：**

| Group | 加载时机 | 包含的工具 |
|-------|---------|-----------|
| `core` | 始终加载 | read, write, glob, bash, tmux |
| `arsenal` | 始终加载 | arsenal |
| `scanner` | 引擎就绪后加载 | scan, gogo, spray, zombie, neutron |
| `search` | 可选（默认加载） | web_search, fetch, cyberhub |
| `browser` | 可选 + `full` 构建标签 | playwright |
| `ioa` | IOA 连接后加载 | ioa_space, ioa_send, ioa_read |
| `proxy` | 始终加载 | proxy |

**Deps 依赖注入：**

```go
type Deps struct {
    WorkDir      string          // 工作目录
    BashTimeout  int             // bash 超时（秒）
    SkillStore   any             // Skill 存储
    EngineSet    any             // 扫描引擎集合
    Resources    any             // 指纹/POC 资源
    IOAClient    any             // IOA 协作客户端
    Provider     any             // LLM Provider
    Model        string          // 模型名称
    ScannerProxy string          // 代理地址
    ScanOpts     []any           // scan 命令选项
    Logger       any             // 日志记录器
    NodeName     string          // IOA 节点名
    NodeMeta     map[string]any  // IOA 节点元数据
    TavilyKeys   string          // Tavily API Key
}
```

**激活方式 — 空导入（blank import）：**

工厂通过 `init()` 自注册，只需在入口文件中空导入即可激活：

```go
// cmd/aiscan/imports.go
import (
    _ "github.com/chainreactors/aiscan/pkg/tools"           // scanner 组
    _ "github.com/chainreactors/aiscan/pkg/tools/arsenal"    // arsenal 组
    _ "github.com/chainreactors/aiscan/pkg/tools/ioa"        // ioa 组
    _ "github.com/chainreactors/aiscan/pkg/tools/proxy"      // proxy 组
    _ "github.com/chainreactors/aiscan/pkg/tools/search"     // search 组
)
```

#### 完整示例：开发一个伪命令

以开发一个 `whatweb` 指纹识别伪命令为例：

**步骤 1：创建包目录**

```
pkg/tools/whatweb/
├── whatweb.go       # 命令实现
└── register.go      # 工厂注册
```

**步骤 2：实现 Command 接口** — `pkg/tools/whatweb/whatweb.go`

```go
package whatweb

import (
    "context"
    "fmt"

    "github.com/chainreactors/aiscan/pkg/commands"
    "github.com/chainreactors/aiscan/pkg/telemetry"
)

type Command struct {
    logger  telemetry.Logger
    proxy   string
    workDir string
}

func New() *Command {
    return &Command{logger: telemetry.NopLogger()}
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
    if logger != nil {
        c.logger = logger
    }
    return c
}

func (c *Command) WithProxy(proxy string) *Command {
    c.proxy = proxy
    return c
}

func (c *Command) SetWorkDir(dir string) { c.workDir = dir }
func (c *Command) SetProxy(proxy string) { c.proxy = proxy }
func (c *Command) Name() string          { return "whatweb" }

func (c *Command) Usage() string {
    return `whatweb — web 指纹识别

Usage:
  whatweb -u <url>              识别单个目标
  whatweb -l <file>             从文件读取目标列表
  whatweb -u <url> -j           JSON 输出`
}

func (c *Command) Execute(ctx context.Context, args []string) error {
    var target, listFile string
    var jsonOutput bool
    for i := 0; i < len(args); i++ {
        switch args[i] {
        case "-u", "--url":
            if i+1 < len(args) {
                target = args[i+1]
                i++
            }
        case "-l", "--list":
            if i+1 < len(args) {
                listFile = args[i+1]
                i++
            }
        case "-j", "--json":
            jsonOutput = true
        }
    }

    if target == "" && listFile == "" {
        return fmt.Errorf("usage: whatweb -u <url> or whatweb -l <file>")
    }

    // 执行指纹识别逻辑 ...
    result := doFingerprint(ctx, target, c.proxy)

    if jsonOutput {
        fmt.Fprintf(commands.Output, "%s\n", result.JSON())
    } else {
        fmt.Fprintf(commands.Output, "%s\n", result.String())
    }
    return nil
}
```

**步骤 3：注册工厂** — `pkg/tools/whatweb/register.go`

```go
package whatweb

import (
    "github.com/chainreactors/aiscan/pkg/commands"
    "github.com/chainreactors/aiscan/pkg/telemetry"
)

func init() {
    commands.RegisterFactory(commands.Factory{
        Group: "scanner",
        Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
            logger, _ := deps.Logger.(telemetry.Logger)
            if logger == nil {
                logger = telemetry.NopLogger()
            }
            cmd := New().WithLogger(logger).WithProxy(deps.ScannerProxy)
            reg.Register(cmd, "scanner")
        },
    })
}
```

**步骤 4：激活** — 在 `cmd/aiscan/imports.go` 中添加空导入：

```go
import (
    // ...existing imports...
    _ "github.com/chainreactors/aiscan/pkg/tools/whatweb"
)
```

**效果：**
- AI 的 system prompt 中 bash 工具描述会自动包含 `whatweb` 伪命令
- AI 通过 `bash(command="whatweb -u https://example.com")` 调用
- 输出自动通过 tmux 会话管理，支持超时自动后台化

### 2.3 方式二：Arsenal（运行时下载）

Arsenal 是 aiscan 内置的安全工具包管理器，基于 [crtm](https://github.com/chainreactors/crtm)。它允许在运行时安装和使用任何 GitHub 上发布 release 的 CLI 工具，无需编写代码。

#### 使用内置工具

Arsenal 预置了 22+ 安全工具，涵盖 chainreactors 和 projectdiscovery 生态：

```bash
arsenal list                    # 查看所有可用工具及安装状态
arsenal search subdomain        # 按关键词搜索
arsenal info nuclei             # 查看工具详情
arsenal install httpx           # 安装（幂等操作）
httpx -u https://example.com    # 安装后直接使用
```

安装后的工具二进制放在 `~/.aiscan/arsenal/bin/`，自动加入 PATH，AI 可立即通过 bash 调用。

#### 注册第三方工具

```bash
# 注册一个 GitHub 仓库
arsenal add ffuf/ffuf --pattern "{name}_{version}_{os}_{arch}.tar.gz"

# 安装并使用
arsenal install ffuf
ffuf -u https://target.com/FUZZ -w wordlist.txt
```

**asset pattern 占位符：**

| 占位符 | 说明 | 示例值 |
|--------|------|--------|
| `{name}` | 工具名 | `ffuf` |
| `{version}` | 版本号 | `2.1.0` |
| `{os}` | 操作系统 | `linux`, `darwin`, `windows` |
| `{arch}` | 架构 | `amd64`, `arm64` |

**常见 pattern 模板：**

```
{name}_{version}_{os}_{arch}.tar.gz     # 最常见
{name}_{version}_{os}_{arch}.zip        # Windows 工具
{name}_{os}_{arch}                       # 无版本号的裸二进制
{name}-{version}-{os}-{arch}.tar.gz     # 连字符分隔
```

#### 工作原理

Arsenal 本身也是一个伪命令（实现 `Command` 接口），注册在 `arsenal` 工具组中。但通过 Arsenal 安装的工具以普通 shell 命令方式执行：

```
AI 调用 bash(command="arsenal install nuclei")
  → tmux.Manager 路由到 ArsenalCommand（伪命令）
    → crtm.Manager.InstallTool("nuclei")
      → 从 GitHub Release 下载 → 放入 ~/.aiscan/arsenal/bin/ → 加入 PATH

后续 AI 调用 bash(command="nuclei -u target -t cves/")
  → tmux.Manager 未匹配到伪命令
    → 作为 shell 命令在 PTY 中执行 nuclei 二进制
```

### 2.4 两种方式的对比

| 特性 | Pseudo-Command（gogo 等） | Arsenal 工具（nuclei 等） |
|------|--------------------------|--------------------------|
| 执行方式 | 进程内 goroutine | PTY 子进程 |
| 需要编写代码 | 是（Go） | 否 |
| 需要编译 | 是 | 否 |
| 需要安装 | 否（编译内置） | 是（`arsenal install`） |
| 可访问内部资源 | 是（引擎、指纹库等） | 否（独立进程） |
| 输出捕获 | 直接捕获 | 通过 PTY 缓冲 |
| 超时/后台化 | 自动（tmux 管理） | 自动（tmux 管理） |
| 代理支持 | 自动注入 | 通过环境变量 |

**选择建议：**

```
工具需要访问 aiscan 内部引擎/资源？
  ├── 是 → Pseudo-Command
  └── 否 → 工具已有独立 CLI 二进制？
              ├── 是 → Arsenal（零代码接入）
              └── 否 → Pseudo-Command
```

---

## 3. Skill 开发

Skill 是 Markdown 文件，通过 YAML frontmatter 定义元数据，正文部分作为指令注入 AI 的 system prompt。Skill 告诉 AI **何时**、**如何**使用工具，而不是实现工具本身。

### 3.1 Skill 结构

每个 Skill 是一个目录，包含一个 `SKILL.md` 文件和可选的参考文档：

```
skills/
└── my_skill/
    ├── SKILL.md              # 必须，Skill 定义
    └── reference/            # 可选，参考文档
        ├── guide.md
        └── examples.md
```

**SKILL.md 格式：**

```markdown
---
name: my_skill
description: 一句话描述，AI 据此判断何时加载此 Skill
internal: false
---

# Skill 标题

正文内容，作为指令注入 AI 的 system prompt。
可以包含：使用指南、命令示例、工作流程、判断规则等。
```

**Frontmatter 字段：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | Skill 标识符，需唯一 |
| `description` | string | 是 | AI 用于判断何时加载此 Skill |
| `internal` | bool | 否 | `true` 时不在 `<available_skills>` 列表中显示，但仍可通过 `-s` 或代码加载 |
| `agent` | bool | 否 | `true` 时作为子 agent 类型注册 |
| `agent_max_turns` | int | 否 | 作为 agent 时的最大轮次 |
| `agent_model` | string | 否 | 作为 agent 时使用的模型 |
| `agent_background` | bool | 否 | 作为 agent 时是否后台执行 |

### 3.2 加载优先级

Skill 从四个来源加载，后者覆盖前者（同名覆盖）：

```
1. 嵌入 Skill（编译到二进制中）    ← 最低优先级
2. 项目 Skill（.aiscan/skills/）
3. Agent Skill（.agent/skills/）
4. CLI Skill（-s 参数指定）         ← 最高优先级
```

这意味着：
- 开发者可以在 `.aiscan/skills/` 中放置项目级 Skill，覆盖内置行为
- 用户可以通过 `-s` 参数临时加载或覆盖 Skill
- 同名 Skill 后加载的覆盖先加载的

**加载代码路径：** `skills/embed.go` → `LoadAll()`

```go
func LoadAll(cliPaths []string) (*Store, []Diagnostic) {
    // 1. LoadEmbedded()        — 编译时嵌入的 skills/
    // 2. LoadFromDir(".aiscan/skills", SourceProject)
    // 3. LoadFromDir(".agent/skills", SourceAgent)
    // 4. LoadFromFile/Dir(cliPaths, SourceCLI)
    return newStoreWithOverride(allSkills), allDiags
}
```

### 3.3 完整示例：开发一个 Skill

以开发一个「API 安全测试」Skill 为例：

**步骤 1：创建目录结构**

在项目的 `.aiscan/skills/` 目录下（或 `skills/` 嵌入目录中）：

```
.aiscan/skills/
└── api_security/
    ├── SKILL.md
    └── reference/
        └── owasp_api_top10.md
```

**步骤 2：编写 SKILL.md**

```markdown
---
name: api_security
description: Use this skill when testing REST/GraphQL APIs for authentication, authorization, injection, and data exposure vulnerabilities.
---

# API Security Testing

API 安全测试的专项指导。

## 适用场景

当目标包含 REST API、GraphQL 端点、Swagger/OpenAPI 文档时自动适用。

## 测试流程

1. **信息收集**：识别 API 端点和文档
   ```bash
   spray -u https://target.com --crawl
   katana -u https://target.com -d 3 -jc
```

2. **认证测试**：检查认证机制
   - 无认证访问敏感端点
   - JWT 弱密钥 / 算法混淆
   - API Key 泄露

3. **授权测试**：IDOR 和越权
   - 水平越权：替换用户 ID
   - 垂直越权：普通用户访问管理端点

4. **注入测试**：
   ```bash
   neutron -u https://target.com/api/users -t sqli
   ```

## 判定规则

- 未认证可访问用户数据 → P1 高危
- IDOR 可跨用户操作 → P1 高危
- SQL 注入 → P1 严重
- 信息泄露（版本号、堆栈信息）→ P3 低危

## 参考

详细的 OWASP API Top 10 检查清单见：`reference/owasp_api_top10.md`
```

**步骤 3：验证**

Skill 创建后即生效（零代码修改），AI 的 system prompt 中会出现：

```xml
<available_skills>
  <skill>
    <name>api_security</name>
    <description>Use this skill when testing REST/GraphQL APIs...</description>
    <location>.aiscan/skills/api_security/SKILL.md</location>
  </skill>
</available_skills>
```

AI 遇到 API 测试任务时会自动通过 `read` 工具加载该 Skill 的完整内容。

### 3.4 Agent 类型 Skill

设置 `agent: true` 的 Skill 可以作为子 agent 类型注册，支持多 agent 协作场景：

```yaml
---
name: recon_agent
description: Reconnaissance sub-agent for domain and infrastructure discovery.
agent: true
agent_max_turns: 30
agent_model: ""
agent_background: true
---

# Recon Agent

你是专项信息收集 agent。执行以下任务后报告结果：

1. 子域名枚举
2. 端口扫描
3. 服务识别
4. 指纹匹配

完成后调用 finish 工具报告发现。
```

Agent 类型 Skill 通过 `skills.Store.AgentTypes()` 收集，注入到 `SubAgentTool` 中：

```go
// core/runner/runner.go
subAgentTool := agent.NewSubAgentTool(parentAgent, ib, func(name string) (agent.AgentType, error) {
    s, ok := rt.App.Skills.ByName(name)
    if !ok || !s.Agent { return error }
    return agent.AgentType{
        FormattedPrompt: rt.App.Skills.FormatInvocation(s, ""),
        Model:           s.AgentModel,
        Background:      s.AgentBackground,
    }
})
```

### 3.5 Skill 引用机制

**虚拟文件 URI：**

嵌入的 Skill 文件通过 `aiscan://` URI 引用：

```
aiscan://skills/aiscan/SKILL.md
aiscan://skills/aiscan/reference/arsenal.md
aiscan://skills/gogo/SKILL.md
```

AI 使用 `read` 工具加载这些 URI，由 `Store.ReadVirtual()` 处理。

**Skill 间引用：**

Skill 正文中可以引用其他 Skill 的参考文档：

```markdown
详细用法参见 `aiscan://skills/aiscan/reference/tmux.md`。
```

**VirtualFileReader / VirtualGlobber：**

`SkillStore` 实现了这两个接口，使 `read` 和 `glob` 工具能够透明地访问 Skill 虚拟文件：

```go
// pkg/commands/register.go
if r, ok := deps.SkillStore.(VirtualFileReader); ok {
    readers = append(readers, r)
}
if g, ok := deps.SkillStore.(VirtualGlobber); ok {
    globbers = append(globbers, g)
}
reg.RegisterTool(NewReadTool(workDir, readers...))
reg.RegisterTool(NewGlobTool(workDir, globbers...))
```

### 3.6 构建标签控制

部分 Skill 仅在特定构建标签下可用：

```go
// skills/availability.go — 默认阻止列表
var blocked = map[string]bool{
    "katana":  true,
    "passive": true,
}

// skills/availability_full.go — full 构建标签解除阻止
//go:build full
func init() {
    enableSkill("katana")
    enableSkill("passive")
}
```

构建命令：

```bash
# 社区版（不含 katana/passive/playwright）
go build ./cmd/aiscan

# 完整版
go build -tags full ./cmd/aiscan
```

---

## 4. 协作模式

### 场景：接入一个新的扫描工具 `xray`

**方案 A：轻量接入（Arsenal + Skill）— 推荐**

无需编写 Go 代码，适合已有独立 CLI 二进制的工具：

1. **Arsenal 注册**

   ```bash
   arsenal add chaitin/xray --name xray --pattern "{name}_{version}_{os}_{arch}.zip"
   arsenal install xray
   ```

2. **Skill 编写** — `.aiscan/skills/xray/SKILL.md`

   ```markdown
   ---
   name: xray
   description: Use this skill when running xray for passive/active web vulnerability scanning.
   ---
   
   # Xray
   
   Xray 是一款 Web 漏洞扫描器。通过 arsenal 安装后可用。
   
   ## 安装
   
   首先确认已安装：`arsenal list`。若未安装：`arsenal install xray`。
   
   ## 常用命令
   
   ```bash
   xray webscan --url https://target.com --html-output report.html
   xray webscan --listen 127.0.0.1:7777 --html-output report.html
   ```

   ## 结果解读

   扫描完成后使用 read 工具读取 report.html 获取结果。
   ```

**方案 B：深度集成（Pseudo-Command + Skill）**

需要编写 Go 代码，适合需要引擎级集成的工具：

1. **Pseudo-Command** — 将 xray 引擎编译进 aiscan
   - 实现 `Command` 接口
   - 注册到 `scanner` 工具组
   - 支持 proxy、workdir 等标准能力

2. **Skill** — 编写详细的使用指南
   - 放在 `skills/xray/SKILL.md` 嵌入编译
   - 设置 `internal: true`（由 scan 命令自动加载）

### 选择决策

```
工具需要访问 aiscan 内部引擎/资源？
  ├── 是 → Pseudo-Command + Skill
  └── 否 → 工具有 CLI 二进制？
              ├── 是 → Arsenal + Skill（零代码，推荐）
              └── 否 → 只是指导 AI 行为？
                        ├── 是 → 仅 Skill
                        └── 否 → Pseudo-Command + Skill
```
