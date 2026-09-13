# Issue 127 架构审计

审计日期：2026-09-13。

## 结果

- Profile 是唯一 `extension.Set` 所有者；App 只把固定 Entries 贡献到同一张图。
- `pkg/toolset.Registry` 与 `pkg/commands.Registry` 共享 `core/registry.Store[T]`，互不依赖。
- 同名批次原子失败；激活后不可变；Close 拒绝、取消、drain，超时可重试。
- `core/hooks.Registry` 覆盖 Tool、Command、Process、File、HTTP 的真实执行边界。
- `core/operation` 是唯一进程内执行身份和协作取消机制。
- `core/events.Stream` 是唯一 AOP stamping 入口；Observe 生成事实，EventOutput 单独落盘。
- 文件能力只剩 `pkg/exts/files` + `tools/files`；旧文件工具、审计和双观察管线已删除。
- `tools/*` 不依赖 Extension 宿主；`pkg/exts/proxy` 只适配 Proxy Hub，Traffic handler 由连接 Mux 直接拥有。
- App 只发布 `Emit` 和只读订阅，不暴露第二个可写 EventBus。
- ToolNode 没有通用连接 Extension 工厂；只有实际支持的 core/tool 协议。
- Agent 只依赖 `tool.Executor`；无 Agent 的文件 Profile 保持 headless 依赖闭包。

## 防回归

根目录 `architecture_test.go` 检查单向依赖、App 无嵌套 Set、Tool/Command 只共享生命周期
内核、已删除入口不返回、纯工具依赖闭包无产品层，以及生成协议文件的归属。

生命周期测试覆盖 Extension 回滚/重试、Registry 冲突和 drain、Hook 撤销与回调排空、
panic 稳定错误、Observe operation 关联、EventOutput 排空和 Profile 关闭顺序。

验证命令：

```powershell
go test -count=1 ./...
go test -race -count=1 ./core/extension ./core/registry ./core/hooks ./core/events ./core/tool/hooks ./pkg/commands ./pkg/toolset ./pkg/exts/observe ./pkg/exts/eventoutput ./pkg/exts/proxy ./pkg/exts/session ./pkg/profile/files ./pkg/profile/workspace ./pkg/profile/aiscan ./pkg/node ./pkg/toolnode ./tools/proxy
go test -tags full -run '^$' ./...
go build -mod=readonly ./...
```

浏览器 E2E 若被本机浏览器状态或安全软件阻断，必须作为环境失败单独报告；不能用跳过
测试或恢复旧架构来掩盖。
