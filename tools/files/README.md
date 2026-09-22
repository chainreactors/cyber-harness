# Files

`tools/files.Resource` 是唯一文件生命周期所有者；其 `Files` 业务对象实现有界文件访问、
路径/只读/大小策略、挂载和在途操作。构造只校验配置；Resource.Open 打开已存在的绝对目录；Resource.Close 停止准入、取消并等待
操作后关闭 root。超时返回不完整关闭，使用新 context 重试。

`pkg/exts/files.Extension` 是唯一文件插件。它在 Load 时打开 Files，并把 read、write、ls、
glob 原子贡献给 `core/tool.ToolRegistry`；只读配置不发布 write。Profile 让 Tool Registry
依赖 files，因此 Registry 先 drain，再关闭文件资源。消费者直接得到 `*files.Files`；
该类型从定义上不含 Open/Close，不需要 Access/Mounts、密封接口或 Borrow 包装。

Read 返回自有字节；Write 在返回前借用输入。所有本地路径必须位于 root。调用方可以带当前系统的分隔符；`.` 和重复分隔符先收成同一路径，再统一成 `/`：
`path.Match` 在每个系统上都把 `\` 当作转义。读写只接受普通
文件，并受单次 `MaxBytes` 限制。Write 使用同目录临时文件和 rename，失败时保留原文件并
清理临时文件；不会隐式创建父目录，也不承诺 fsync 或跨平台 rename 原子性。

`Config.Mounts` 可直接将 `embed.FS` 等只读 `fs.FS` 挂到 URI 前缀；基础组合将内嵌技能树
挂到 `cyber://skills/`，因此 `read` 可以读取提示词中引用的技能文档。前缀允许目录路径，
按最长匹配选择；虚拟读取同样检查路径、文件类型和大小，挂载不开放写入。

真实 IO 完成时直接发 `core/tool/hooks.FileEvent`。数据只在同步 hook dispatch 期间借用；
未安装 hook 时不计算 digest、不序列化、不复制。`pkg/exts/observe` 在被选择时计算成功写入
的 digest，并生成带 `aop.operation.Ref` 的 AOP file Access 事实。没有独立文件日志、
外围补记器或另一条文件事件路径。

| Package | Role |
| --- | --- |
| `tools/files` | 文件实现与 Tool 声明 |
| `pkg/exts/files` | 生命周期与 Registry 贡献 |
| `cmd/agent` | 最小本地 Agent 的 files/bash 显式组合 |
