# 快速上手

[使用者指南](user/README.md) · 前置：[基本概念](concepts.md) · 下一篇：[Agent](agent.md)

目标是完成一次可观察、可保存的运行。先验证本地 Agent，再按需进入扫描和 Web。扫描示例只用于你有权测试的本地靶场。

## 1. 选择安装方式

从 [Releases](https://github.com/chainreactors/cyber-harness/releases/latest) 下载与你的系统和架构匹配的压缩包，解压后将可执行文件加入 PATH。

| 发行方式 | 包含什么 | 适用场景 |
| --- | --- | --- |
| `aiscan` | Agent、核心扫描器、代理、Skills、IOA | CLI 使用和自动化 |
| `aiscan-full` | 标准能力，加 Web、浏览器、被动测绘和深度爬取 | 浏览器工作台和完整工具集 |
| `make agent` | 通用本地 Agent，不装安全扫描与 Web 等扩展 | 学习框架、最小嵌入 |
| `make record` | 带原生录屏能力的构建 | 需要桌面捕获的开发者，见 [record](record.md) |

发布文件名形如 `aiscan_linux_amd64.zip`、`aiscan_darwin_arm64.zip`、`aiscan_windows_amd64.zip`，full 使用 `aiscan-full_` 前缀。实际可用平台以该 release 的附件为准。

```sh
aiscan --version
aiscan -h
```

Windows 下若没有加入 PATH，将下文的 `aiscan` 换为 `.\aiscan.exe`。命令中的模型名和端点必须替换为你的服务实际支持的值。

源码构建需要 [go.mod](../go.mod) 指定的 Go 版本（当前为 1.26）和 Git。下面的 Makefile 命令使用 POSIX shell；Windows 可用相应的 MSYS2 环境。

```sh
git clone --recurse-submodules https://github.com/chainreactors/cyber-harness.git
cd cyber-harness
make
# 可选：make agent / make full
```

`make full` 还需要 Node.js/npm，先构建前端再嵌入；standard 与 full 均使用 CGO_ENABLED=0，原生录屏构建才需要 CGO 工具链。构建标签统一由 [editions.env](../editions.env) 管理；模板资源的内嵌策略见 [Makefile](../Makefile)，不要把“单二进制”理解为所有模型、浏览器和资源都无需外部依赖。

## 2. 配置模型

在工作目录创建 `cyber.yaml`：

```yaml
llm:
  provider: openai
  base_url: https://api.deepseek.com/v1
  model: deepseek-chat
```

通过环境变量设置密钥。Linux/macOS：

```sh
export OPENAI_API_KEY="你的密钥"
```

PowerShell：

```powershell
$env:OPENAI_API_KEY = "你的密钥"
```

OpenAI-compatible 服务使用 `provider: openai`；Anthropic-compatible 服务使用 `anthropic` 和对应凭据。配置文件与环境变量的优先级见 [参考手册](reference.md#配置优先级)。

## 3. 完成一个本地任务

```sh
aiscan agent -p "只读取当前目录，列出顶层文件并说明判断依据，不修改文件" -o first-run.jsonl
```

你应看到模型输出和可能的工具调用，任务结束后命令退出；`first-run.jsonl` 保存 AOP 事件。模型选择哪些工具不是固定的，不能用某段回答是否逐字匹配判断安装成功。

`-o` 创建新文件，重复运行时换一个文件名。查看记录：

```sh
aiscan -F first-run.jsonl
```

从记录恢复上下文并开始新任务：

```sh
aiscan agent --resume first-run.jsonl -p "根据刚才的结果总结目录用途" -o second-run.jsonl
```

恢复会话不恢复旧的子进程、网络连接或后台任务，也不会修改输入记录。详细边界见[会话与上下文](user/sessions.md#回看与恢复)。

## 4. 选择接下来的工作方式

不传任务输入，进入交互模式：

```sh
aiscan agent
```

输入 `/help` 查看当前安装实际提供的命令，输入 `/status` 查看模型与工具状态。工具集合取决于发行版；[Agent 指南](agent.md)介绍继续、停止、压缩和目标评估。

对本地靶场直接扫描，不使用模型验证：

```sh
aiscan scan -i http://127.0.0.1:3000 --verify=off -o lab-scan.jsonl
```

先确保该端口运行着你要测试的服务。扫描可能包含主动探测和认证检测；无发现不代表服务安全。`scan` 的规则流水线与 Agent 的自主决策不同，见 [扫描指南](scan.md)。

使用完整发行版的浏览器工作台：

```sh
aiscan-full web
```

访问 `http://127.0.0.1:8080`，输入启动时打印的 access key；默认同时启动本地 Agent。Web 数据默认保存到 `cyber-web.db`，可用 `--db` 指定路径。远程节点见 [Web 与协作](user/web.md)。

## 安装验证

| 现象 | 先检查 |
| --- | --- |
| 找不到命令或某个工具 | PATH、发行版，以及该二进制的 `-h` |
| Provider 未配置或鉴权失败 | `provider`、端点、密钥、显式模型名；交互模式用 `/provider` 查看 |
| 工具无法执行 shell 命令 | 宿主 shell、PATH 与平台依赖；模型 API 可用不等于本地执行环境齐全 |
| 没有 JSONL 文件 | 是否传了 `-o`，目标路径是否可写，是否因同名文件被拒绝 |
| 扫描没有 AI 验证 | 本例显式关闭验证；启用条件见 [scan --verify](scan.md#ai-增强扫描) |

到这里，安装、模型、工具、记录四个环节已经可以分别检查。下一篇[Agent](agent.md)继续介绍交互、任务控制和目标评估。
