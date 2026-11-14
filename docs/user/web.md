# Web 与协作

[使用者指南](../README.md#使用者指南) · 前一篇：[安全扫描](../scan.md)

Web 提供会话、扫描结果与节点的统一操作界面。子 Agent 将一项任务拆成多个本地推理过程，IOA 则让独立 Agent 通过消息空间协作。这几种能力可以组合使用，但各自保留独立的会话和身份边界。

## 通用 Web Hub

`cyber-harness` 是包含 Web 的主程序，通过 AOP 接入扫描、RE、审计或自定义 profile 的同级节点。当前发布扫描和审计节点。下载主程序后运行：

```sh
cyber-harness --token replace-me
```

源码构建使用 `make full`。Hub 管理节点、会话、事件和共享配置，工具在执行节点运行。

访问 `http://127.0.0.1:8080` 并登录后，选择执行节点创建会话并提交任务。文件路径和工具能力来自该节点；scan、audit 和自定义 profile 可以同时连接。快速连接面板提供 scan 与 audit 下载和接入命令。

Web 宿主通过独立 IOA 服务端扩展挂载 `/ioa`，顶部 IOA 控制台在没有执行节点时也可使用。Web 节点未显式配置 IOA URL 时从 `--server-url` 推导 `/ioa` 地址；也可用 `--ioa-url http://replace-me@192.0.2.10:8080/ioa` 连接独立服务。显式空 URL 禁用外部连接。内置 IOA 当前使用内存存储，重启 Hub 后消息空间会重建。

生成的安装命令只在执行节点所在机器上运行。Linux/macOS 命令使用临时目录下载并清理压缩包，Windows 命令使用 PowerShell 临时目录；节点进程退出后临时文件会被删除。已经安装过节点时，复制“仅上线”命令即可复用现有二进制。

会话、事件、扫描与配置等管理数据默认保存到 `cyber-harness.db`，可用 `--db` 指定路径。安全工具的原始产物在服务端归档，浏览器将它们解析成资产视图。因此聊天回答、执行记录和资产面板分别表达不同层面的结果。

## 执行记录与资产

选择会话后，从 Web 顶栏打开可观测面板，查看该会话的工具、命令、进程、文件、HTTP 流量与录制事件。可以按类别筛选或搜索，运行中的记录会实时追加，刷新后可从历史继续查看。窄屏下使用列表与详情切换，查看完毕后返回列表或清除筛选。

### 会话活动

活动视图将同一次操作的开始与完成合并，工具卡片内展示关联的文件访问、HTTP 请求与响应、截图或视频。失败记录保留；缺少可靠调用关联的观察事件单独显示。需要完整事件信息时切换到“原始事件”，在详情中查看调用标识与关联信息。

切换会话后，活动视图只展示所选会话及其归档的子会话数据。录制、浏览器和扫描信息取决于执行节点已安装的扩展与实际产生的事件，聊天回答本身不替代执行证据。

### HTTP 流量

流量详情保留请求与响应的 header 和正文，包括重复 header。文本正文按 UTF-8 显示，二进制正文提供有限长度的十六进制预览；大文本预览限制为 128 KiB。捕获错误和已收到的响应可以同时显示，应一起核对。

带会话运行时的扫描节点默认观察 `tools,commands,processes,files,http`；显式设置 `--observe` 时按所选种类工作。HTTP 捕获遵循节点的代理与捕获配置，纯 relay 模式不会产生捕获结果。代理、HTTPS 信任与捕获限制见[工具指南](tools.md#网络与代理)。

### 资产库与工具目录

“资产库”汇总历史会话和导入的资产，可以筛选、搜索、导入、导出、比较执行结果或将资产发送到对话。它跨会话保存资产；单条工具卡片仍展示该次调用的实际产物。资产解析与归档的关系见[事件、记录与资产](../architecture.md#事件与数据)。

Agent 管理中的工具页与终端共用 Agent 选择。选择执行节点后，查看其公布的原生工具、参数定义与 Bash 命令目录；不同节点的可用工具可能不同。

## 远程节点

节点通过 `--server-url` 连接。模型配置由 Hub 分发，包含环境变量和 CLI 的有效覆盖值；保存配置只写入编辑过的文件值。产品配置由各节点解析，Hub 保留文件中未注册的扩展：

```sh
# Web 所在主机；替换 access key
cyber-harness --addr 0.0.0.0:8080 --token replace-me

# 扫描节点
cyber-scan agent --server-url http://replace-me@192.0.2.10:8080 --node-name scan-1

# 审计节点
cyber-audit --workdir /path/to/repository --server-url http://replace-me@192.0.2.10:8080 --node-name audit-1
```

节点在 Web 中使用 node ID 路由会话与终端操作。Web 连接传输请求和事件，实际工具仍在节点上执行。连接恢复不等于所有旧进程都能恢复；使用前确认节点和会话状态。

扫描服务按节点公布的命令能力选择执行位置；没有扫描节点时立即返回 `FAILED_PRECONDITION`，不会在 Hub 执行扫描。如果节点在扫描排队或执行期间断开，扫描会标记为失败并说明原因。

自定义宿主用 `node.RunWebSocket` 和自己的 `profile.Profile` 接入，无需在 Hub 注册产品名单。`make full` 从 `cmd/cyber-harness` 构建主程序；发布矩阵包含 `cyber-harness`、`cyber-scan` 和 `cyber-audit`。

## 子 Agent

主 Agent 可以调用 `subagent`，将独立的分析或执行工作交给子 Agent。`sync` 等待结果后返回，`async` 使用新对话在后台执行，`fork` 继承父对话中最后一个完整工具调用批次边界之前的历史。显式 mode 优先；匿名调用默认 async，具名调用使用定义的默认模式（未填为 sync）。timeout 仅支持 sync。

```text
subagent(prompt="分析日志")
subagent(name="verify", prompt="验证这个发现")
subagent(name="sniper", label="检查 nginx", prompt="分析指纹", mode="async")
subagent(action="catalog")
subagent(action="list")
subagent(action="kill", session_id="执行 ID")
```

`name` 是可选注册名，未知名称报错；`label` 是本次运行的可读标签，可重复。唯一执行身份是 `session_id`，`catalog` 列出定义，`list` 列出运行实例。旧 `type` 参数已移除，旧实例名称参数改为 `label`。

异步完成后，结果会回到父会话；主 Agent 可以据此继续总结或执行。`list` 查询运行实例，`kill` 停止子任务。已安装 IOA 协作扩展时，通过下节的 `ioa send` 向仍存活的子任务发送追加信息。

async 和 fork 脱离当前工具调用的取消范围，仍受父会话和扩展生命周期约束。停止或关闭父会话会取消所属子任务。

子 Agent 的上下文独立，不意味着执行环境隔离。它们可能访问同一目录和同一组工具；同时修改相同文件、浏览器会话或远程对象时，任务安排需要避免冲突。类型化 Skill 的定义见[Skills 与知识](knowledge.md)。

## IOA 消息空间

IOA 通过 Space 组织多个 Agent 的消息。Agent 注册身份并加入 Space 后，可以发送任务、情报和结果；接收到的消息被交给已有会话继续处理。

产品安装客户端后，未配置 URL 时使用进程内的内存服务，不监听 HTTP 端口；跨进程协作需要外部 IOA 服务。配置外部 URL 后，连接故障不会切换到另一个内存空间；Web 节点的 URL 推导规则见[协作连接配置](../configuration.md#协作连接配置)。

先在一个终端启动服务：

```sh
cyber-scan ioa serve
```

再在另一个已配置模型的终端启动交互 Agent：

```sh
cyber-scan agent --ioa-url http://127.0.0.1:8765 --space lab
```

`--ioa-url` 增加协作连接，不改变本地任务模式。若同时传入 `-p`，仍执行一次性任务；持续协作需要持续存在的会话。凭据与客户端、服务端配置见[协作连接配置](../configuration.md#协作连接配置)。

### 发送、回复与等待

以下是 Agent 通过 bash 调用的 IOA 命令；交互终端可在命令前加 `!` 直接执行：

```text
ioa send <session-id-or-name> "消息"
ioa send <session-id-or-name> "回复" --ref-messages <message-id>
ioa send <session-id-or-name> "跨节点消息" --ref-nodes <node-id>
ioa read --all --after <message-id>
ioa send <session-id-or-name> "更新后的要求" --interrupt
```

Session ID 精确匹配；名称只有在目标节点的活跃会话中唯一时可用，重名会拒绝投递。`--ref-messages` 建立回复关联，仅在正文提及 ID 不会建立回复链。高级消息可用 `--content JSON` 或 `ioa send SESSION PROTOCOL [options]`；将协议名作为普通文本时使用 JSON 消息，避免命令歧义。

发送成功返回已保存的消息 ID，目标是否处理需要其回复确认。子任务结束后不再接收消息；继续工作需重新派发并引用已有记录。IOA 协作扩展自动记录派发和返回，返回消息关联原派发消息。

新消息自动进入会话 Inbox。Agent 可以调用 `inbox_wait` 工具等待新消息，无需轮询历史；可选 timeout 指定秒数，省略或为零时等待消息到达或任务取消。后来者需显式 `ioa read` 补齐历史，当前没有可靠离线 outbox 或消费回执。

`--interrupt` 使当前工作处理新要求，不撤销已完成的操作。正在运行的命令继续执行，tmux 前台等待让出后同一命令转入后台，仍会通知完成；已结束的任务不会被重新唤醒。

`ioa space NAME "description"` 切换命令空间和自动收信订阅，切换失败保留旧空间。已有派发的返回仍写入原派发空间。没有会话或无法确定接收者时，自动投递会被拒绝。

Web Node ID 与 IOA Node ID 属于各自系统。`--server-url` 决定执行节点连接哪个 Web，`--ioa-url` 决定协作客户端连接哪个消息服务；设置其中一个不表示另一种关系也已经建立。

## 应用集成

外部程序可以用 AOP 接入会话与工具，通过管理 API 查询配置和历史；Go 应用可以直接嵌入框架。具体集成方式属于[开发者指南](../developer/hosting.md)，使用者无需了解线协议即可通过 CLI 或 Web 工作。
