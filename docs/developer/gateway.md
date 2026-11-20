# 自动配置

`exts/gateway` 将外部服务配置绑定到现有能力。Agent 继续调用 `passive`、`proxy`，无需了解 gateway；它不贡献 Tool、Command、Skill 或提示词。

配置直接使用顶层块，服务字段直接放在实例下：

```yaml
gateway:
  fofa:
    key: "<key>"
  office:
    kind: clash
    url: "<subscription-url>"
    refresh: 30m

cyberhub:
  proxy: office
```

省略 `kind` 时使用实例名称，所以默认的 `fofa` 不必再写 `kind: fofa`。`enabled` 默认 true；false 表示关闭该绑定。相同类型有多个实例时必须明确选择：FOFA 使用 `recon.service`，Clash 使用 `cyberhub.proxy` 引用实例名。`cyberhub.proxy` 也继续接受原有代理 URL。选择仅属于宿主配置，不增加工具参数。

FOFA 接受 `key`、可选的完整搜索接口 `url`、出站 `proxy` 和 `limit`。旧 `recon.fofa_key`、`FOFA_KEY`、CLI flag 仍可独立使用；CLI 和环境覆盖保持原有优先级，同一层的冲突凭证会报错。

Clash 默认 `refresh: 30m`、`strategy: adaptive`，也支持 `type`、`name`、`country` 过滤和独立的下载 `proxy`。`refresh: 0s` 仅拉取一次，空 `strategy` 仅加载节点。拉取成功后原子发布，失败保留旧快照；手动选定的节点不会被定时刷新切回自动模式。选定节点被移除或改变时保留旧快照并记录错误。已有 `proxy subscribe/auto` 和 traffic 协议也调用同一订阅实现。

订阅下载使用独立 HTTP 客户端和配置的下载出口，默认遵循宿主代理环境，不依赖订阅产生的节点。刷新串行执行；停止或替换配置后旧结果不能继续发布，关闭会取消并等待刷新结束。部分依赖的健康检查和地理筛选在取消后可能仍需完成；关闭超时会保留资源供重试。

## 宿主接入

服务类型复用 `config.Section`，不另建配置 DTO。声明时调用 `gateway.Declare(resources, schemas...)`，配置读取使用 `gateway.Read(option)`，实例集合直接复用 `config.Values`。

运行时的 `gateway.Kind` 只有三个字段：`Config` 是 schema，`Use` 选择实例，`Load` 将解析结果交给现有所有者并返回资源句柄。未配置的类型不调用 Load；显式禁用传入 nil；多个实例未选择时拒绝加载。所有实例和选择先通过校验，再调用绑定，加载失败交给原有 Extension Set 回滚。

cyber-scan 在 proxy 加载后、passive 加载前安装 gateway。FOFA 绑定配置 passive 的查询客户端，Clash 绑定向 proxy 发布节点和路由。gateway 不反向依赖具体服务；增加服务只需提供 schema、绑定与组合声明。

配置中心可以使用不链接具体类型的 `gateway.Section()` 转发配置。默认 Web Hub 已声明这个轻量 section：只公开实例名称、kind 和 enabled，其余字段视为敏感数据；执行节点用实际注册的 schema 严格校验。检查、配置写入和状态管理仍走宿主程序接口。

## 配置更新

外部配置中心通过现有配置加载/API/分发路径提供配置，协议仍使用 `DistributeConfig.extensions.gateway`，文件写成顶层 `gateway`。不增加新的传输协议或配置中心 SDK。

局部编辑保留仍存在、类型未变实例的空缺密钥；删除实例不会恢复它的密钥。完整下发快照以输入为准，缺失实例被移除。敏感字段在配置查看和日志中遮蔽，在受控分发中保留。

变更沿用宿主的配置预备、提交、回滚与 Profile 替换。相同快照由现有保存路径去重；本扩展不增加另一套运行时热替换机制。候选配置加载失败时继续使用原 Profile，首次拉取订阅失败也不会提交候选路由。

## 其他配置

所有已注册的扩展 section 都自动支持以自身名称作为顶层块，不必重复声明同名 alias。旧 `extensions.<section>` 和其他明确的 alias 仍能读取，混用时冲突会报错。配置展示和保存采用顶层块；未链接的扩展保留原 envelope，避免破坏其他宿主的数据。传输协议和运行快照保持现有结构。
