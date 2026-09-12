# Issue 127：插件边界收敛

## 目标

所有可装配能力遵循一条生命周期路径：`extension.Set` 创建固定依赖图，
`Load` 阶段由插件通过 `Context.RegisterTools` 声明工具，整个图成功后才
发布一个工具目录；关闭时先停止工具准入并排空调用，再按逆序关闭资源。

## 包边界

| 目录 | 责任 |
| --- | --- |
| `core/extension` | `Extension`、`Context`、`Set`、工具目录和关闭契约 |
| `pkg/exts` | 产品插件适配器与装配边界；按能力拆分子包（`files`、`tools`、`agent`） |
| `tools` | 工具和底层资源的原始实现；不保存注册器或 lease |
| `agent` | agent loop、消息状态和推理实现；不管理插件发布 |
| `pkg/files`（已迁移到 `tools/files` 实现层） | 一个文件服务实现；由 `pkg/exts/files` 暴露为唯一 files 插件 |

`pkg/exts/files` 是文件能力的唯一插件入口，提供 `read`、`write`、`ls`、
`glob` 四个基础工具。审计、skills mount 等插件只借用这个文件服务，不再
复制文件工具或创建第二个文件根目录。

## 明确禁止

- 插件保存 `tool.Registrar`、`tool.Registration` 或自行创建注册中心。
- 通过 Runtime/service locator 查找依赖。
- `filetools`、`workspacefiles`、`toolgroup` 等重复文件或发布机制。
- 在 `tools` 或 `agent` 原始实现中混入宿主生命周期。

## 验证

已覆盖：全图加载后发布、重复工具原子回滚、关闭准入、调用排空、关闭
超时后资源保留与重试、panic 计数释放，以及 files 插件的读写审计和挂载
生命周期。
