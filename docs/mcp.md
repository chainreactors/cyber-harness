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

## 真实开源引擎验证：JADX

可用 REA 已集成的 [jadx-headless-mcp v0.7.1](https://github.com/1013503897/jadx-headless-mcp/releases/tag/v0.7.1) 验证此扩展。它用 jadx-core 实际反编译 APK，直接提供 stdio MCP；需要现有 Java 17+。固定输入为开源 [Appium ApiDemos v6.0.18](https://github.com/appium/android-apidemos/releases/tag/v6.0.18) 的 debug APK，仅执行静态分析。

配置服务别名 `jadx`，command 为现有 Java，args 为 `["-Xmx512m", "-jar", "/absolute/path/jadx-headless-mcp-0.7.1-all.jar"]`。在同一个长期运行的 harness Profile 的 bash 中执行：

```sh
jadx --list
jadx get_class_source --help
jadx load_apk --path '/absolute/path/ApiDemos-debug.apk' --threads 1 --resources lite
jadx list_classes --prefix io.appium.android.apis --offset 0 --limit 3
jadx get_class_source --class_name io.appium.android.apis.ApiDemos --max_bytes 32768 --smali_fallback false
jadx get_method_by_name --class_name io.appium.android.apis.ApiDemos --method_name onCreate --smali_fallback false
jadx unload_apk
```

可重复运行的真实引擎 lane 是 [TestJADXRealBashWorkflow](../exts/mcp/jadx_integration_test.go)。调用链为 **AI bash 工具边界 → CommandRegistry → MCP ext → 上游 JADX MCP → jadx-core**。测试检查发布包与 APK 的固定 SHA-256，再验证工具发现、动态帮助、无 APK 时的失败状态、含空格路径、会话状态、整型分页参数、布尔参数、实际 Java 类与方法、命名参数和完整 JSON 的结果一致、变量/管道/重定向、卸载和连接清理。它不会下载引擎、安装 Java 或执行 APK；默认不配置环境时跳过。

准备文件后，PowerShell 中运行：

```powershell
$env:CYBER_MCP_JAVA = 'D:\tools\existing-jdk\bin\java.exe'
$env:CYBER_MCP_JADX_JAR = 'D:\tools\jadx-headless-mcp-0.7.1-all.jar'
$env:CYBER_MCP_JADX_APK = 'D:\targets\ApiDemos-debug.apk'
go test -v ./exts/mcp -run '^TestJADXRealBashWorkflow$' -count=1
```

JAR SHA-256 为 `6e5eacf500b64292bfb73c49797c1958f6ee44646e43e868039ae7feb573ff75`，与 [REA 的发布身份](https://github.com/morluto/rea/blob/main/src/android/JadxRelease.ts)一致；APK SHA-256 为 `a9eecf37b26cd084855c530db81c2bb1b91f4c1b095a04f47aa7c20e2791f686`，与 Appium 发布资产摘要一致。环境配置不完整或摘要不匹配会失败。

2026-10-11 在 Windows x64、JBR 21.0.7 上通过此 lane：发现 26 个工具，索引 2,876 个顶层类，实际返回 `ApiDemos` 的 Java 类与 `onCreate` 方法。普通运行耗时 13.67 秒。此结果验证 stdio 接入的真实 JADX；JADX HTTP 模式和其他分析引擎不由此 lane 建立覆盖。

## 真实 LLM 驱动的分析

[examples/mcp-agent](../examples/mcp-agent/main.go) 把 MCP ext、现有 `StandardLoop` 和 Session 装入同一个长期运行的 harness。模型通过普通 bash 工具发现命令、加载文件、查询和卸载；入口支持任意 MCP 配置和用户任务文件。没有预写的分析调用序列，也不需要新的 JADX/IDA adapter。

先准备隔离的任务目录、目标文件、UTF-8 任务文件和上述 `mcp.json`。在已有环境中设置 `CYBER_MCP_API_KEY` 后运行：

```sh
go run ./examples/mcp-agent \
  -config /absolute/path/mcp.json \
  -dir /absolute/path/task-workspace \
  -task /absolute/path/task.txt \
  -base-url https://your-provider.example/v1 \
  -model your-tool-calling-model \
  -trace /absolute/path/new-events.jsonl \
  -max-turns 40 -timeout 15m
```

任务应指定输入文件、分析问题、证据要求，以及显式卸载本次打开的分析资源。入口把最终回答写到 stdout，工具调用、用量、耗时和 harness 关闭状态写到 stderr。可选 trace 使用新的文件，保存完成后的 AOP 消息、工具参数/结果和生命周期事件，略去逐 token delta。最大模型决策数和总 deadline 由入口限制。

入口在创建工具或子进程之前取出并移除专用密钥环境变量，provider 在内存中持有凭据；不开启 HTTP/provider 原始帧捕获。trace 和最终输出遮蔽已知密钥的明文值。trace 包含本地任务和分析证据，应按实际输入管理。任务目录不是安全沙箱；示例提供真实 bash 能力，模型的权限由宿主环境决定。它不安装引擎或运行 APK。

真实模型驱动的 UnCrackable / ApiDemos 分析、独立核验、失败恢复和模型结论遗漏见[验证记录](verification/mcp-jadx-llm.md)。

## 边界和验证

目录是加载时的快照；上游工具变化后需重建 Profile。扩展处理 tools，不暴露 resources/prompts、sampling、elicitation 或 legacy HTTP+SSE，也不自动把服务 instructions 变成系统提示词。完整 schema 提供给调用方，命名参数转换不是完整 JSON Schema 验证器；完整 JSON 输入只检查 object，上游负责业务参数校验。

取消结束本地等待并尽力发送 `notifications/cancelled`，不会自动重连或重放调用，也不证明上游任务已停止。卸载先撤销命令、取消并排空调用，再关闭连接。stdio 先关闭输入，200 毫秒后终止仍未退出的直接子进程。关闭 deadline 到达时保留清理任务并返回 `ErrCloseIncomplete`，允许重试 Close。HTTP 只释放客户端与协议会话；SDK 的 DELETE 为尽力释放，不验证服务器端已清理。

一个 Profile 的服务配置拥有一条连接，不同 Agent Session 共用它。扩展不隔离上游“当前数据库”，不自动打开、保存、关闭 IDA 数据库，不管理上游分离的 workers。cyber-re 应显式选择数据库句柄或使用独立 Profile，并显式释放自己创建的分析资源。

测试使用真实 Go stdio 子进程和 SDK Streamable HTTP 服务，验证 AI bash → CommandRegistry → MCP 的完整路径、复杂工具名、Schema 参数和 JSON 回退、管道和重定向、失败状态、准入 hooks、超时、卸载排空和加载回滚。原始协议测试覆盖完整 schema、数字精度与表示、可选字段省略、nullable / union / $ref、控制名称冲突、混合内容与 metadata、分页和认证 headers。真实 IDA、Hopper、Ghidra 尚未验证。
