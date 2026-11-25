# 进程内 API 中转

`exts/gateway` 在进程内为已有工具选择外部 API、绑定地址和凭据。配置应用时直接更新候选 Profile 的客户端，执行时由原客户端请求选定的上游。Agent 继续调用 `passive`、`proxy`；gateway 不贡献 Tool、Command、Skill 或提示词。

```text
配置中心 / 配置文件 → gateway 在内存中选择并绑定上游
Agent → 原有工具 → 已绑定的客户端 → 外部 API
```

gateway 不监听 HTTP 端口，不注册 Hub HTTP 路由，也不通过 localhost 回环转发。`base_url` 是外部上游地址，可以是官方 API 或已有的远程网关；进程内接管直接复用现有客户端。Clash 同样将订阅配置直接交给现有 proxy 所有者。注册、配置管理和资源句柄服务于这些绑定，不建立独立的网关服务。

配置直接使用顶层块，服务字段直接放在实例下：

```yaml
gateway:
  fofa:
    base_url: https://gateway.example/fofa
    key: "<key>"
  hunter:
    base_url: https://gateway.example/hunter
    key: "<key>"
  shodan:
    base_url: https://gateway.example/shodan
  tavily:
    base_url: https://gateway.example/tavily
    key: "<key>"
  duckduckgo:
    base_url: https://gateway.example/duckduckgo
  office:
    kind: clash
    url: "<subscription-url>"
    refresh: 30m

cyberhub:
  proxy: office

jev:
  base_url: https://gateway.example/typesafe
```

省略 `kind` 时使用实例名称，所以默认的 `fofa` 不必再写 `kind: fofa`。`enabled` 默认 true；false 禁用对应来源，不回退到已有凭据。每种类型默认绑定一个实例；FOFA 通过 `recon.service`、Clash 通过 `cyberhub.proxy` 选择多实例，其他类型出现多个实例时报错。`cyberhub.proxy` 也接受代理 URL。选择仅属于宿主配置，不增加工具参数。

FOFA 的地址和凭据统一配置在 `gateway` 的服务下，接受 `base_url`、`key`、出站 `proxy` 和 `limit`。`base_url` 可以指向自己的 FOFA 兼容网关；默认值为 `https://fofa.info`。查询和连接检查共用该地址，分别追加 `/api/v1/search/all` 与 `/api/v1/info/my`，保留已有路径前缀并处理末尾斜线。例如上面的查询地址为 `https://gateway.example/fofa/api/v1/search/all`。网关使用 FOFA 请求参数和响应结构；请求失败直接返回错误。

## 外部 API 地址

| 服务 | 配置入口 | 行为 |
| --- | --- | --- |
| passive 的 16 个来源 | `gateway.<source>.base_url` | 替换原始 API 的 origin，保留请求路径、查询参数、方法、请求体和鉴权；配置 URL 中的路径作为前缀 |
| Tavily | `gateway.tavily.base_url` | 追加 `/search`，查询与连接检查共用地址 |
| DuckDuckGo HTML 搜索 | `gateway.duckduckgo.base_url` | 追加 `/html/` |
| JEV / TypeSafe | `jev.base_url` | 追加 `/v1/systemone`，加速、guardrail 和连接检查共用地址 |
| LLM 与模型搜索 | `llm.providers[].base_url` | 已有配置，DeepSeek 等模型搜索复用所选 profile 地址 |
| Cyberhub | `cyberhub.url` | 已有可配置资源服务地址 |
| MCP、IOA、OTLP | MCP 服务的 `url`、`ioa.client.url`、`OTEL_EXPORTER_OTLP_ENDPOINT` | 已有服务端点配置，保留各自协议含义 |
| Clash | `gateway.<name>.url` | 已有完整订阅 URL，无需拆成 base URL 与路径 |

passive 来源为 `fofa`、`hunter`、`shodan`、`shodan-idb`、`censys`、`quake`、`zoomeye`、`netlas`、`criminalip`、`publicwww`、`hunterhow`、`odin`、`binaryedge`、`onyphe`、`driftnet`、`greynoise`。除 FOFA 外，`key` 可省略并使用宿主已有凭据；填写时覆盖该来源的主凭据。Censys 的组织 ID 仍由 `CENSYS_ORGANIZATION_ID` 提供。`shodan-idb` 与 DuckDuckGo 无需 key；`shodan-idb` 默认不可用，需写出 `gateway.shodan-idb`（例如 `enabled: true`）才会加入可用来源。没有任何可用来源时，passive 不注册命令，也不挂载 `cyber://skills/passive/` 文档；`enabled: false` 与未配置相同。

测绘源使用每次查询独立的 transport 在内存中改写地址，同时覆盖 Uncover 的备用客户端与 Censys SDK；不修改进程全局客户端或依赖常量。配置地址可带路径前缀和末尾斜线。显式网关不跟随 HTTP 重定向；Tavily 自定义上游失败直接返回错误，不访问默认 DuckDuckGo 作为替代。

当前仓库没有独立的 company API 客户端。扫描、浏览器、fetch、curl 的目标 URL 是调用输入；代理健康检查 URL 和工具资源下载属于各自功能，不作为业务 API 配置重写。

Clash 默认 `refresh: 30m`、`strategy: adaptive`，也支持 `type`、`name`、`country` 过滤和独立的下载 `proxy`。`refresh: 0s` 仅拉取一次，空 `strategy` 仅加载节点。拉取成功后原子发布，失败保留旧快照；手动选定的节点不会被定时刷新切回自动模式。选定节点被移除或改变时保留旧快照并记录错误。已有 `proxy subscribe/auto` 和 traffic 协议也调用同一订阅实现。

订阅下载使用独立 HTTP 客户端和配置的下载出口，默认遵循宿主代理环境，不依赖订阅产生的节点。刷新串行执行；停止或替换配置后旧结果不能继续发布，关闭会取消并等待刷新结束。部分依赖的健康检查和地理筛选在取消后可能仍需完成；关闭超时会保留资源供重试。

## 宿主接入

服务类型复用 `config.Section`；只有地址和主凭据的 API 复用 `config.API`，FOFA 的限额、代理和 JEV 的模型等独立设置保留自己的 schema。声明时调用 `gateway.Declare(resources, schemas...)`，配置读取使用 `gateway.Read(option)`，实例集合直接复用 `config.Values`。

运行时的 `gateway.Kind` 只有三个字段：`Config` 是 schema，`Use` 选择实例，`Load` 将解析结果交给现有所有者并返回资源句柄。未配置的类型不调用 Load；显式禁用传入 nil；多个实例未选择时拒绝加载。所有实例和选择先通过校验，再调用绑定，加载失败交给原有 Extension Set 回滚。

cyber-scan 在 proxy 加载后、passive/search 加载前安装 gateway。`passive.Config.BaseURLs` 为不同测绘源保存独立上游；搜索绑定各自的 Tavily 与 DuckDuckGo 客户端，Clash 向 proxy 发布节点和路由。gateway 不反向依赖具体服务；增加服务只需提供 schema、绑定与组合声明。

配置中心可以使用不链接具体类型的 `gateway.Section()` 转发配置。默认 Web Hub 已声明这个轻量 section：只公开实例名称、kind 和 enabled，其余字段视为敏感数据；执行节点用实际注册的 schema 严格校验。检查、配置写入和状态管理仍走宿主程序接口。

## 配置更新

外部配置中心通过现有配置加载/API/分发路径提供配置，协议仍使用 `DistributeConfig.extensions.gateway`，文件写成顶层 `gateway`。不增加新的传输协议或配置中心 SDK。

局部编辑保留仍存在、类型未变实例的空缺密钥；删除实例不会恢复它的密钥。完整下发快照以输入为准，缺失实例被移除。敏感字段在配置查看和日志中遮蔽，在受控分发中保留。

变更沿用宿主的配置预备、提交、回滚与 Profile 替换。相同快照由现有保存路径去重；本扩展不增加另一套运行时热替换机制。候选配置加载失败时继续使用原 Profile，首次拉取订阅失败也不会提交候选路由。

## 其他配置

所有已注册的扩展 section 都以自身名称作为顶层块。配置解析、展示、保存只使用这一种格式，未知配置和字段直接报错，不做格式转换或别名映射。文件文档与传输消息是独立 schema：文件直接使用配置块，传输消息使用现有 typed 字段。

嵌入的配置字段直接展开，不增加结构体名称层。Guardrail 的规则直接写在 `guardrail.level`、`guardrail.on_error`、`guardrail.criteria`；MCP 直接使用服务名映射。模型 profiles、HTTP headers、规则 criteria 等有独立含义的集合保留自己的分组。

Go 名称沿用包的上下文：`passive.Config`、`passive.Engine`、`passive.Uncover`、`search.Client`、`proxy.Hub`、`proxy.Filter`、`mcp.Config`、`terminal.Options`。搜索客户端通过 `search.New(config)` 构造，来源相关的请求与解析分别放在 Tavily、DuckDuckGo、FOFA、Hunter 文件中；连接检查的结果和耗时采集复用 `config.Check`。配置与每次执行选项、协议响应与命令输出、运行资源与借用接口仍保留不同类型；简化命名不合并这些合同。
