# IOA 客户端与服务端扩展

IOA 提供基于 HTTP、SSE 和消息空间的多 Agent 协作。Cyber 通过两个独立扩展集成：

| 扩展 | 所有权 | 对外业务能力 |
| --- | --- | --- |
| `pkg/exts/ioa/client` | 身份注册、重试、收信、handoff 消费、命令贡献 | `Runtime()`：查询、状态，无 SDK 句柄和 Start/Close |
| `pkg/exts/ioa/server` | Store、Service、认证、HTTP/SSE 请求排空 | `Server()`，无 Start/Close |

客户端与服务端使用 IOA HTTP 协议通信。原始实现在 `tools/ioa` 与 `tools/ioa/server`，
不依赖 Extension 宿主。连接目标仍由 `--ioa-url` 决定；Web Agent 未指定时使用
`<server-url>/ioa`。Web/AOP 的节点路由与 IOA 身份仍是两个独立协议边界。

## 客户端装配

```text
Command Registry ── ioa 命令 ──→ IOA Client
AOP Event Stream ── handoff ───→ IOA Client
Agent Inbox      ←─ peer 消息 ─ IOA Client
Console / CLI    ── Reader ───→ IOA Client
```

Profile 构造客户端并注入 Command Registry、共享的 Event Stream 和可选的投递函数。
客户端不导入 Agent Extension，Agent 不导入 IOA SDK。
没有投递函数时仍可使用命令和 skills，不启动自动收信。

加载顺序为 IOA Client → App/贡献者 → Command Registry → Tool Registry → Agent；
关闭顺序相反。只有整张 Profile 图发布后，投递函数才允许调用 Agent 的 `Deliver`。
该入口选择主会话，否则选择唯一会话；无会话、多会话歧义、关闭或队列满时返回错误。
不会自动创建会话。忙碌会话接收追加输入，空闲会话通过既有 Inbox 机制自动执行。

注册、加入配置 Space 失败会重试，随后建立 SSE；订阅断开也会重连。
注册和订阅寿命不受初始化 context 结束影响。自身消息按当前 Node ID 过滤。
`ioa space` 切换的是命令当前 Space；自动收信和 handoff 继续使用启动配置中的 Space。

handoff 通过同一个 AOP Stream 的有界 Consumer 生成，保留 delegate/return 内容和引用关系。
队列上限为 256 个事件、16 MiB。发送失败被记录，不终止后续事件消费；队列溢出会停止
该订阅并记录错误和丢弃数量。`Runtime.Status()` 提供 Bound、Space、LastError 和 Dropped。
关闭时先取消并等待收信，再排空 handoff，最后释放客户端资源；超时可通过 Set.Close 重试。
已完成资源回收但输出失败时返回普通错误，不再报告资源未关闭。

当前没有离线补投或可靠 outbox。没有会话时拒绝输入，网络发送失败不自动重发。

本地与远程 Console 只接收 `pkg/console/api.Bindings`。客户端的 `console` 子包持有 Reader，
提供 `/spaces`、`/nodes`、`/messages`、`/context`、补全和状态行，复用同一个已注册身份。
Reader 只有查询能力，不能注册、切换命令 Space、订阅或关闭客户端；查询随扩展关闭而取消并排空。

独立查询 CLI 在 `cmd/aiscan` 装配仅含 client ext 的图；连通性探测使用一次性的只读 client ext，
支持已有 bearer token，且不自动注册节点。Console、Node、Runner 和 Web 不直接构造 IOA SDK。


## 启动声明与配置

只有 client/server 两个生命周期扩展。`client/cli`、`client/console`、`client/probe` 和
`server/cli` 是静态适配代码，不创建第三个 Extension，也不加入另一套生命周期。
`cmd/aiscan/ioa_composition.go` 在解析参数前注册它们；运行时选择仍由具体 Profile 决定。

- `core/config.Sections` 保存类型工厂、别名、校验和密钥路径；`Option.Extensions` 只保存数据。
- `pkg/cli.Registry` 收集子命令与 flag groups，解析不执行 Action。每个命令作用域内拒绝重名参数。
- `pkg/probe.Registry` 只执行显式注册的探测；普通 Web API 不包含 IOA 分支。
- `pkg/profile.Application` 只发布通用 ConsoleBindings、Capabilities 和完整 AgentStatus。
- `pkg/web.Route` 是 typed resource；Web 扩展定义目录，IOA server 扩展在启用浏览器桥接时自行贡献 `/ioa/`。

新配置使用独立命名空间：

```yaml
node:
  name: worker
extensions:
  ioa.client:
    url: http://access-key@localhost:8765
    space: team
  ioa.server:
    url: http://127.0.0.1:8765
    token: server-access-key
```

继续接受 `--ioa-url`、`--server-token`、`--space`、`--node-name`、`ioa ...`、`ioa serve`、
`serve --addr/--token`。旧 YAML `ioa:` 在客户端命令映射为 `ioa.client`，独立服务端命令映射为
`ioa.server`；服务端忽略旧节中的客户端 space/node_name。旧 `ioa.node_name` 在产品边界兼容到
通用节点名称。客户端、服务端声明均可独立安装。

扩展字段按显式 CLI > 文件 > 类型默认值解析，通用配置原有环境变量优先级保持不变。
IOA 端点与通用节点名称不再有构建期注入，未显式配置时使用代码内置默认值（端点为空，space 为 `default`）。
字段缺失与显式空字符串、false、0 不等价；`url: ""` 禁用自动推导的客户端连接。
同一字段的旧别名与新路径值冲突、未注册的扩展键、未知字段和非法类型在启动前报错。

Web protobuf 增加 `extensions` 数据及脱敏视图；旧 IOA protobuf 字段只作兼容输入/输出。
保存时保留未提交的扩展节及空白密钥，保留相同端点 URL 中被视图隐藏的凭据；换主机不复制凭据。
保存的客户端配置继续使用 `ioa:` YAML 拼写，并保留显式空值。配置校验与候选 Profile 加载成功后
才提交；应用 Profile 可替换，宿主 IOA Server 持续存在。远端现有 Provider 重载仍保持原语义，
扩展连接变化需要重建该节点的 Profile。没有自动生成扩展表单或运行时热注册。

通用 core、Agent、Profile、Console、Node、Probe、Web 与 skills 的生产依赖闭包不包含 IOA SDK、
`tools/ioa` 或 IOA 扩展。架构测试同时检查直接 import 与传递依赖，兼容协议 DTO 不携带运行时行为。

## Skills

客户端提供静态 `skills.Bundle`，由 Profile 在构造 App 时选择。
未安装客户端时，不加载 IOA 使用说明或协议 skills。

- 使用说明：`cyber://skills/ioa/SKILL.md`
- 协议定义：`ioa://skills/<checkpoint|handoff|swarm|team>/SKILL.md`
- 协议 schema：`ioa://skills/<name>/schema.json`

覆盖顺序为内置 → 扩展 Bundle → `.cyber/skills` → `.agent/skills` → CLI 路径。
Bundle 不提供热注册或另一套生命周期。

## 服务端托管

独立 `aiscan ioa serve` 和 Web 都安装同一个服务端扩展，分别挂载根路径和 `/ioa/`。
服务端默认使用内存 SQLite；业务 Store 与 Web 管理数据库不合并。
构造时不打开数据库。扩展加载后才发布业务能力；未加载和关闭后的 handler 返回 503。
关闭先拒绝新请求、取消 SSE、等待请求退出，最后关闭 Store；等待超时保留 Store 供重试。

HTTP listener 和 `http.Server` 由命令入口持有。server 扩展的 `BrowserHandler` 注册浏览器身份并桥接认证；原生 bearer 身份仍保留。上游 `NewHTTPHandler` 统一装配认证、REST/SSE 和
`/mcp`；独立 `ioa serve` 显式启用 MCP，Web 维持原有 REST/SSE 路由。

Web 的 IOA Server 保持宿主寿命，应用配置重载只替换应用 Profile，不清空空间、消息和身份。
扩展不创建子 Set，各运行时之间不共享 Extension 实例。

## 开发验证

上游 `internet-of-agent` 发布 `server.NewHTTPHandler` 后，`go.mod` 已把 IOA 依赖固定到对应版本，
根目录 `go.work` 联调 workspace 随之移除，构建不再依赖相邻仓库的本地路径。

```text
go test . ./core/config ./pkg/cli ./skills ./pkg/exts/ioa/... ./tools/ioa/... ./pkg/exts/agent ./pkg/profile ./pkg/node ./pkg/console ./pkg/probe ./cmd/aiscan ./pkg/web/service
go test -race ./core/extension ./core/events ./core/eventbus ./pkg/exts/ioa/... ./tools/ioa/... ./pkg/exts/agent ./pkg/profile ./pkg/node ./pkg/console ./pkg/probe ./skills
go test -tags full ./cmd/aiscan ./pkg/web/service
go test github.com/chainreactors/ioa/server
```
