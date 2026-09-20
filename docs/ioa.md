# IOA 客户端与服务端扩展

[文档首页](README.md) · 前置：[Web 与协作](user/web.md) · 开发前置：[扩展装配](architecture/composition.md)

先在两个终端分别运行 `aiscan ioa serve` 和配置好模型的 `aiscan agent --ioa-url http://127.0.0.1:8765 --space lab`。后者在没有任务输入时保持交互会话；带 `-p` 时仍是一次性任务。本章继续解释身份、投递、配置与资源所有权。

IOA 是可插拔的 Agent 协作扩展。产品默认安装客户端扩展：未配置 URL 时使用 SDK Service、SQLite `:memory:` 和 Hub，在进程内调用，不监听 HTTP 端口；配置 URL 时连接外部 IOA。外部故障不会降级成隔离的内存空间。Web Agent 的默认地址仍为 `<server-url>/ioa`。

| 扩展 | 所有权 | 对外能力 |
| --- | --- | --- |
| `pkg/exts/ioa/client` | 本地或外部客户端、收信、Session 路由、同步 handoff、命令 | `Service()` / Reader 与状态 |
| `pkg/exts/ioa/server` | 独立服务的 Store、Service、认证、HTTP/SSE | `Server()` |

## 客户端装配

```text
Agent / Session ── 同步 SessionStart / SessionEnd Hooks ──→ IOA ext
Agent Inbox     ←─ 借用的投递函数 ←─ ext 私有 Session 路由
ioa send/read   ── SDK ClientAPI / StreamAPI ──→ 内存 Service 或外部服务器
```

Core 不依赖 IOA，不提供通讯 Binder、Recorder 或第二套 Session 注册中心。普通 Session 的投递复用 admission、mailbox 和自动调度；派生任务只接收运行期间的输入。借用的投递函数在生命周期结束后失效，旧 Session ID 不会映射到替代会话。

装配顺序是基础能力、IOA ext、native/session 等执行扩展；逆序关闭时先取消并等待执行收尾，再释放 IOA。后台子任务跨越单次工具调用，但受父执行生命周期和 SubAgentTool 所有者约束。

## 派发、返回与通讯

sync、async、fork 派发都先同步写入 handoff，再启动子任务。记录包括标题、任务、实际输入、父子 Session ID 和派发 tool-call ID；fork 继承的模型历史不复制到 IOA。派发记录失败则任务不启动。

结束时关闭输入入口，同步写入引用派发消息的结果，再通过原调用机制返回或通知父任务。成功、失败、取消均记录终态；记录失败会向父任务明确报告。收尾使用有期限且不随任务取消的上下文，不依赖异步 AOP 观察流。

父子、兄弟任务共享 Node ID，以已有 Session ID 区分：

```text
ioa send --target-session <session_id> --content '{"text":"follow-up"}'
ioa send --ref-nodes <node_id> --target-session <session_id> --content '{"text":"remote follow-up"}'
ioa read --message <dispatch_message_id>
```

Session ID 可从派发返回值和 `subagent list` 获取。源 Session 从执行上下文注入，目标 Session 放入 IOA metadata；继续复用 Sender、Refs.Nodes 和 Refs.Messages。同 Node 定向消息不会被自身发送者过滤。没有目标 Session 的外部消息只进入主会话或唯一普通会话，歧义时拒绝投递。

`subagent.message` 已移除。未安装 IOA ext 时仍可派发并返回一次结果，但不存在持续通讯通道。子任务结束后不再接收消息；需要继续工作时重新派发，并引用既有记录。

`ioa space` 切换命令空间及自动订阅，派发结果仍写入对应派发发生的空间。成功发送只表示 IOA 已保存消息，不表示目标已消费。后来者显式 `ioa read`，不自动回放历史。当前不提供可靠 outbox、消费回执或离线补投。

Console 只借用 Reader，复用扩展身份；Reader 不拥有注册、订阅和关闭能力。独立查询 CLI 与连接测试直接拥有 IOA Resource，无需创建 Agent 或安装生命周期 Hooks。连接测试通过注册交换访问密钥，再执行查询。

## 启动声明与配置

只有 client/server 两个生命周期扩展。`client/cli`、`client/console` 和 `server/cli` 是静态
适配代码，不创建第三个长期 Extension，也不加入另一套生命周期或 declaration 包。
`cmd/aiscan/ioa_composition.go` 在解析参数前选择 client/server 声明；运行时选择仍由具体
Profile 决定。

- `pkg/cli.Registry` 直接实现 `Point[cli.Contribution]`。
- `pkg/config.Sections` 直接实现 `Point[config.Section]`，并提供 `config.Connection` Point。
- client/server 的 `Declare` 函数直接贡献资源，不返回 Provider DTO，也不经过 Catalog。
- `pkg/config.Sections` 保存类型工厂、别名、校验和密钥路径；`Option.Extensions` 只保存数据。
- `pkg/cli.Registry` 收集子命令与 flag groups，解析不执行 Action。每个命令作用域内拒绝重名参数。
- IOA client 声明拥有 `ioa` section 的连接测试；Web Config API 通过 Config Sections 分发。
- `pkg/profile.Profile` 只发布通用 ConsoleBindings、共享 State 和完整 AgentStatus。
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
`ioa.server`；服务端忽略旧节中的客户端 space/node_name。旧 `ioa.node_name` 在配置边界兼容到
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

通用 core、Agent、Profile、Console、Node、Web 与 skills 的生产依赖闭包不包含 IOA SDK、
`tools/ioa` 或 IOA 扩展。架构测试同时检查直接 import 与传递依赖，兼容协议 DTO 不携带运行时行为。

## Skills

客户端提供静态 `skills.Bundle`，由 Profile 在构造发行版图时选择。
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
go test ./pkg/config ./pkg/cli ./agent/skills ./pkg/exts/... ./tools/ioa/... ./pkg/profile ./pkg/node ./pkg/console ./cmd/aiscan ./pkg/web/service
go test -race ./core/extension ./core/events ./core/eventbus ./pkg/exts/... ./tools/ioa/... ./pkg/profile ./pkg/node ./pkg/console ./agent/skills
go test -tags full ./cmd/aiscan ./pkg/web/service
go test github.com/chainreactors/ioa/server
```
