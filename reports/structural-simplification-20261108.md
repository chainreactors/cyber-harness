# 结构简化复核（2026-11-08）

本轮从扫描适配器扩展至仓库内已跟踪的 Go 源码，并检查前端视图转换。Go AST 索引排除了测试、protobuf 和 Connect 生成文件，得到 684 个结构声明、151 个含至少三个字段复制的复合字面量，以及 164 个单 return 调用候选。这些是筛选数量，不代表冗余数量；是否删除仍取决于调用关系、字段类型、标签、默认值和所有权。

## 删除与复用

| 候选 | 原职责及生产消费者 | 处理与契约 |
| --- | --- | --- |
| applyWebStrategyOptions | 向 Spray 选项复制扫描标志；只有 Web 探测适配器调用 | 合并到 probeWeb 的选项组装处；保留切片复制、能力默认值与 CLI 值的 OR 合并、Recon 开启规则 |
| scanCapability | 将四个参数原样填入 pipeline.Capability；扫描和 Katana 能力组装使用 | 直接使用现有结构字面量，保留路由、并发和回调签名 |
| derivePerInvocationThreads | 仅在 buildCapabilities 中设置单次调用线程上限 | 在容量分配处直接设置，保留低容量和默认值分支 |
| emitAcceptedArtifact | 将内部产物的字段转发给 EmitArtifactJSONCtx；接受事件观察器调用 | 直接调用已有发布 API；保留取消后的证据发布上下文、错误收集、数据裁剪与操作关联 |
| artifactLootEvent / errorEventOf | 仅组装 event；POC、弱口令、错误路径使用 | 使用现有 event 字面量。产物在生产者中已完成组装，后续不再修改；不再复制一次产物结构 |
| webConfigStore | 保存三个参数，延迟构造 FileConfigStore，并转发其四个方法；仅 AIScan Web 使用 | 直接构造共享存储；删除重复状态、sync.Once 和转发方法。configCodec 保留产品的扩展 schema、校验和秘密值处理契约；并发仍由共享存储的锁负责 |
| machineUsage | 将 TokenUsage 的五个字段复制到同类型、同 JSON 标签的私有 DTO；JSON 结果输出使用 | 直接使用已有 TokenUsage，保留数值编码、snake_case 键和 omitempty；聚合器内部仍持有自己的 usage 快照 |
| truncateToolResultLine | 仅转发 ClipRunes；终端工具结果预览使用 | 直接调用现有截断函数 |
| selectorArgs | 仅转发 ParseSelector；浏览器录制使用 | 直接调用现有选择器解析器 |
| parseCLI 包装入口 | 仅供测试调用，给真实解析器补 stdout | 删除包装，真实解析器统一命名 parseCLI；测试显式传入输出目标，包含 full 构建测试 |
| hubLocalURL | 已无任何生产或测试调用的旧监听地址转换 | 删除失去消费者的实现；保留实际使用的 embeddedAgentOption 行为测试 |
| preserveLLMProfileSecrets 包装 | 仅供测试转发共享实现 | 测试直接调用共享实现 |
| toExtensionItem | 在已确定为 scan_complete 的分支中重复判断类型并组装视图项 | 在现有视图投影分支直接组装，保留输出字段和缺省 scanID |

扫描执行函数改为 scanPorts、probeWeb、checkCredentials、checkPOCs 和 checkBasicAuth，去掉重复的 Capability 后缀。

## 保留的边界

| 类别或实例 | 保留原因 |
| --- | --- |
| scan.artifactResult → tool.Artifact | 前者保留完整原始 JSON 和稳定结果 ID；发布 API 才执行传输大小裁剪、时间戳、媒体类型及操作关联。直接混用会模糊产物生成与传输的契约 |
| machineFailure → ProtocolError | 错误结果的 message 必须输出；protobuf 结构的 JSON 标签会省略空消息，不能视作相同序列化契约 |
| proton.jsonFinding | 对外保留 template-id/template-name 等 nuclei 格式，原生发现结构的字段和标签不同 |
| provider 请求、响应与 usage 类型 | 各供应商的协议、默认值、工具消息和用量含义不同，需要转换到 AOP |
| CLI options、运行配置、SDK runner options | 分别拥有解析标签、配置优先级、运行默认值和第三方接口契约；buildGogoRunnerOption/buildSprayOption 等转换保留 |
| Web SQL model 与 protobuf 文档 | 关系索引列、外键、数据库时间和完整 protobuf 存储承担不同职责 |
| Inbox storedMessage、流量索引与 Playwright storageState | 分别解决 protobuf oneof 持久化、流量恢复元数据和外部浏览器文件格式，不能删除其独立 schema |
| 连接配置、运行状态、缓存条目及任务边界 | 生命周期、取消、并发或依赖方向不同；没有因为字段相似而合并 |
| build-tag 实现与依赖回调适配器 | Windows/Unix 行为、可选功能和依赖签名需要独立入口；公开兼容 API 未作无依据删除 |

## 真实边界测试发现

旧的字段映射测试不能确认字典实际执行。替换为本地 HTTP 测试后发现，SDK BruteTask 中占位的根路径会覆盖 runner 已加载的自定义字典。现在仅使用 SDK 的统一执行路径，由 runner 选项决定模式；无字典爬取仍通过根路径启用所需的池。回归测试验证自定义字典路径、无字典链接爬取与 Host 都实际到达服务端。

JSON usage 测试检查实际序列化键和数值类型；配置保存测试直接经过共享 FileConfigStore，继续验证分层配置、秘密值保留、预提交与原子提交。其余验证结果记录在 PR。
