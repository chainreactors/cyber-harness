# MCP → CLI 扩展

`exts/mcp` 使用 `mcp-go` 连接外部 MCP 服务，自动将服务别名映射为命令、工具名映射为子命令、inputSchema 属性映射为参数。配置 `ida` 后即可在 harness 的 bash 中调用 `ida decompile --addr 0x401000`。Agent 按需发现工具和参数，无需把整个 MCP 目录加入模型的函数列表。

接入路径：**AI → bash → CommandRegistry → MCP client → MCP server → 上游工具**。支持 stdio 和 Streamable HTTP；它适用于 IDA、REA 及其他提供 MCP tools 的服务，不包含分析引擎安装或数据库生命周期策略。

## AI 如何调用

配置服务别名为 `ida` 后，加载扩展自动注册 `ida`，并把简短用法加入命令发现：

```sh
ida --list
ida decompile --help
ida decompile --addr 0x401000
ida decompile --json '{"addr":"0x401000"}'
ida decompile --file arguments.json > result.json
```

`--list` 输出名称和描述的 JSON 数组；`<tool> --help` 生成参数说明，并附上完整的上游声明，包括 `inputSchema`、`outputSchema`、annotations 和未知字段。子命令调用同名上游工具；工具与参数名保留原有大小写、下划线等拼写。工具名作为独立 argv 传递，不受模型函数名的 64 字节限制。参数和数据库选择以实际 `--help` 为准。

所有工具共享一套 Schema → 参数规则，既接受 `--name value`，也接受 `--name=value`。每个参数消费一个值；以 `--` 开头的值使用等号形式。

| Schema 类型 | 参数值 | 示例 |
| --- | --- | --- |
| string | 原样字符串 | `--addr 0x401000` |
| integer | 十进制整数，保留大整数精度 | `--limit 10` |
| number | JSON 数字，保留原有数字表示 | `--ratio 1.25e-3` |
| boolean | 显式 `true` / `false` | `--recursive true` |
| object / array | 单个 JSON 值 | `--options '{"depth":2}'` / `--addresses '["0x401000"]'` |
| null | `null` | `--value null` |
| 多类型 union、$ref 或未声明类型 | 单个 JSON 值 | `--value '"text"'` |

单一具体类型加 null 的 Schema 使用该类型的参数编码；需要显式 null 时用完整 JSON 输入。省略可选字段时不注入 defaults。命名参数检查顶层 required、类型编码、未知参数和重复参数，上游服务负责完整 Schema 校验。

`--json '<object>'` 和 `--file <path>` 提供通用回退，支持任意嵌套结构和复杂 Schema，不能与命名参数混用。相对文件路径基于调用方当前目录解析。字段名使用字母、数字、`_`、`-` 且不以 `-` 开头时可直接生成参数；名为 `help`、`json`、`file` 的字段或其他字段名使用完整 JSON 输入。工具名为 `--help`、`-h`、`--list` 或 `--` 时可通过 `ida -- <tool>` 调用；`tools`、`schema`、`call`、`help` 都是普通上游工具名。

stdout 是完整 MCP result JSON，保留文本、图片/音频的 base64、资源 URI、`structuredContent`、`_meta` 和未知内容。结果中的 `isError: true` 会在保留 stdout 后让命令失败；协议、连接、参数和 I/O 错误也使命令失败。bash 支持管道、重定向、变量和 `&&` / `||`，例如：

```sh
ida idb_list > databases.json
ida decompile --addr 0x401000 2> error.txt || printf '查看 error.txt\n'
```

命令运行在 harness 的 bash 解释器中，由 CommandRegistry 路由，不生成磁盘上的独立可执行文件。另行启动的系统 shell 只能访问自己的 PATH；可以使用下面的独立示例作为外部 CLI 宿主。

设计参考了 [hengyunabc/mcp2cli](https://github.com/hengyunabc/mcp2cli) 的 Go / mcp-go 动态子命令、[knowsuchagency/mcp2cli](https://github.com/knowsuchagency/mcp2cli) 的服务命名与按需发现，以及 [mcporter 的 CLI 生成](https://github.com/openclaw/mcporter/blob/main/docs/cli-generator.md)。这里直接复用 harness 的 CommandRegistry，通过一套运行时映射接入已有 MCP 服务。

## 配置和独立试用

扩展配置使用 `mcpServers`，每个服务选择 `command` 或 `url`。别名使用字母、数字、`-`、`_`，不能与 bash 内建命令、关键字或已注册的 harness 命令冲突。stdio 的 command 是已安装的可执行文件，args 是参数数组，直接启动子进程；env 覆盖指定环境变量，cwd 设置子进程工作目录。

现代 `ida-pro-mcp` headless supervisor 示例：

```json
{
  "mcpServers": {
    "ida": {
      "command": "idalib-mcp",
      "args": ["--stdio", "--max-workers", "1"],
      "startupTimeoutSeconds": 30,
      "timeoutSeconds": 300
    }
  }
}
```

保存为 `mcp.json` 后，在仓库根目录运行，无需模型密钥：

```sh
go run ./examples/mcp -config mcp.json
go run ./examples/mcp -config mcp.json ida --list
go run ./examples/mcp -config mcp.json ida idb_open --help
go run ./examples/mcp -config mcp.json ida decompile --addr 0x401000
```

[示例](../examples/mcp/main.go)复用相同的 Extension Set 和 CommandRegistry，直接输出 JSON，失败返回非零退出码。每次运行会创建和关闭连接；需要连续维护 stdio 服务状态时，把扩展装进长期运行的 Profile。

Streamable HTTP 服务配置：

```json
{
  "mcpServers": {
    "ida": {
      "url": "http://127.0.0.1:8745/mcp",
      "headers": {"Authorization": "Bearer YOUR_TOKEN"},
      "tools": ["idb_list", "decompile"]
    }
  }
}
```

配置中的 `tools` 使用上游名称；省略或 null 表示全部，`[]` 表示空目录。缺失的白名单工具、重复工具和循环分页使加载失败。调用和工具帮助只允许访问白名单内的目录。

HTTP 重定向不自动跟随；请求与协议会话 DELETE 使用同一组 headers，诊断文本遮蔽配置中的 header 值。扩展不会持久化继承环境，stdio 的 stderr 持续排空但当前不保存上游日志。启动和发现各默认 30 秒，调用默认 300 秒；两个 timeout 字段可分别设置为正整数秒，0 使用默认值。

## 加入 cyber-harness Profile

在发行版组合根的 Extensions 中加入 MCP ext，基础 CommandRegistry 先加载，模型 loop 和 Session 后加载：

```go
import (
    "github.com/chainreactors/cyber/core/extension"
    mcpext "github.com/chainreactors/cyber/exts/mcp"
    "github.com/chainreactors/cyber/pkg/harness"
    mcptools "github.com/chainreactors/cyber/tools/mcp"
)

mcpConfig := mcpext.Config{
    MCPServers: map[string]mcptools.ServerConfig{
        "ida": {Command: "idalib-mcp", Args: []string{"--stdio", "--max-workers", "1"}},
    },
}
h, err := harness.New(harness.Config{
    Base: baseConfig,
    Extensions: []extension.Extension{mcpext.New(mcpConfig)},
    Session: &sessionConfig,
})
```

`baseConfig` 和 `sessionConfig` 由发行版提供。按常规路径调用 `h.Load` / `h.Close`，加载失败也关闭。Load 验证整个配置、初始化连接、完成分页发现后，一次性贡献所有服务命令；失败时 Set 回滚已建立的连接。命令调用经过现有 command hooks，AI 的 bash 调用仍经过 tool hooks。

扩展目前是可装配的 Go API，未加入 aiscan 的 CLI 配置声明。完整的基础接线与生命周期说明见[扩展开发](developer/extensions.md)。[examples/rmcp](../examples/rmcp/main.go)是 AOP 远程工具节点，与此处的标准 MCP 客户端不同。

## 连接已有的逆向基础设施

IDA、Python、许可证与已有 `ida-pro-mcp` 服务需要按[上游说明](https://github.com/mrexodia/ida-pro-mcp#headless-idalib-session-model)单独准备。headless 示例需要支持 `idb_open` / `idb_list` / `idb_close` supervisor 的版本；旧版以初始文件名启动并提供 legacy SSE 的服务不适用此配置。具体工具名称和参数以 `--list` / `<tool> --help` 的实际发现为准。

连接已打开的 IDA GUI 时，复用工作正常的上游 stdio proxy 注册。legacy 1.4.0 的示例：

```json
{
  "mcpServers": {
    "ida": {
      "command": "python",
      "args": ["/absolute/path/to/ida_pro_mcp/server.py", "--ida-rpc", "http://127.0.0.1:13337"]
    }
  }
}
```

legacy GUI 的 `/mcp` 是内部 RPC 接口，需要通过 stdio proxy，不能直接当作 Streamable HTTP URL。

也可以连接已安装的 REA MCP 服务，复用其 Hopper / Ghidra / IDA provider：配置 command 为 `npx`、args 为 `["rea-agents", "mcp"]`，按 [REA provider 文档](https://github.com/morluto/rea#choosing-a-deep-analysis-provider)配置分析引擎。Windows 上填写可直接执行的入口；npm 的 `.cmd` shim 可改为 `node` 加已安装包的入口路径。

## 边界和验证

目录是加载时的快照；上游工具变化后需重建 Profile。扩展处理 tools，不暴露 resources/prompts、sampling、elicitation 或 legacy HTTP+SSE，也不自动把服务 instructions 变成系统提示词。完整 schema 提供给调用方，命名参数转换不是完整 JSON Schema 验证器；完整 JSON 输入只检查 object，上游负责业务参数校验。

取消结束本地等待并尽力发送 `notifications/cancelled`，不会自动重连或重放调用，也不证明上游任务已停止。卸载先撤销命令、取消并排空调用，再关闭连接。stdio 先关闭输入，200 毫秒后终止仍未退出的直接子进程。关闭 deadline 到达时保留清理任务并返回 `ErrCloseIncomplete`，允许重试 Close。HTTP 只释放客户端与协议会话；SDK 的 DELETE 为尽力释放，不验证服务器端已清理。

一个 Profile 的服务配置拥有一条连接，不同 Agent Session 共用它。扩展不隔离上游“当前数据库”，不自动打开、保存、关闭 IDA 数据库，不管理上游分离的 workers。cyber-re 应显式选择数据库句柄或使用独立 Profile，并显式释放自己创建的分析资源。

测试使用真实 Go stdio 子进程和 SDK Streamable HTTP 服务，验证 AI bash → CommandRegistry → MCP 的完整路径、复杂工具名、Schema 参数和 JSON 回退、管道和重定向、失败状态、准入 hooks、超时、卸载排空和加载回滚。原始协议测试覆盖完整 schema、数字精度与表示、可选字段省略、nullable / union / $ref、控制名称冲突、混合内容与 metadata、分页和认证 headers。真实 IDA、Hopper、Ghidra 尚未验证。
