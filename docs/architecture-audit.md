# Issue 127 架构审计

审计日期：2026-09-14。

## 结果

- 每个组合根只有一个 `extension.Set`；具体 `profile.Profile` 直接持有该 Set 和少量已构造
  能力，不存在 `Application`、`Assembly`、`aiscanProfile` 或第二套发布/关闭状态。App 不生成 Entry，也不拥有子图。
- `pkg/toolset.Registry` 与 `pkg/commands.Registry` 共享 `core/registry.Store[T]`，互不依赖。
- 同名批次原子失败；激活后不可变；Close 拒绝、取消、drain，超时可重试。
- `core/hooks.Registry` 覆盖 Tool、Command、Process、File、HTTP 的真实执行边界。
- `core/operation` 是唯一进程内执行身份和协作取消机制。
- `core/events.Stream` 是唯一 AOP stamping 入口；Observe 生成类型化观测，EventOutput 单独落盘。
- 文件能力只剩 `pkg/exts/files` + `tools/files`；旧文件工具、审计和双观察管线已删除。
- `tools/*` 不依赖 Extension 宿主；`pkg/exts/proxy` 只适配 Proxy Hub，Traffic handler 由连接 Mux 直接拥有。
- App 只发布 `Publish` 和类型化只读观察，不暴露第二个可写 EventBus。
- ToolNode 没有通用连接 Extension 工厂；只有实际支持的 core/tool 协议。
- Agent 只依赖 `tool.Executor`；无 Agent 的文件 Profile 保持 headless 依赖闭包。
- `extension.Scope` 不含 owner ID、资源 Ref 或服务定位；只表达初始化、寿命和注册撤销。
- Files、Proxy、IOA 与 App 均分离生命周期所有者和业务访问面；Agent Extension 只发布
  受控 Loop，Session Extension 独立发布会话 Runtime。两者由能力注入和 Set 依赖关联，
  不互相导入或关闭。不存在 Borrow/Handle/seal 适配层，业务对象不提供 Load/Open/Start/Close。

## 防回归

根目录 `architecture_test.go` 检查单向依赖、App 无嵌套 Set、Tool/Command 只共享生命周期
内核、已删除入口不返回、纯工具依赖闭包无产品层，以及生成协议文件的归属。

生命周期测试覆盖 Extension 回滚/重试、Registry 冲突和 drain、Hook 撤销与回调排空、
panic 稳定错误、Observe operation 关联、EventOutput 排空和 Profile 关闭顺序。

验证命令：

```powershell
go test -count=1 ./...
go test -race -count=1 ./core/extension ./core/registry ./core/hooks ./core/events ./core/eventbus ./core/tool/hooks ./pkg/commands ./pkg/toolset ./pkg/exts/observe ./pkg/exts/eventoutput ./pkg/exts/proxy ./pkg/exts/agent ./pkg/exts/session ./pkg/profile ./pkg/node ./pkg/toolnode ./tools/proxy ./cmd/aiscan ./cmd/runner
go test -tags full -run '^$' ./...
go build -mod=readonly ./...
```

浏览器 E2E 若被本机浏览器状态或安全软件阻断，必须作为环境失败单独报告；不能用跳过
测试或恢复旧架构来掩盖。

## 本轮收敛验证（2026-09-14）

- 默认/full 全仓 build、vet；默认架构测试、core/agent/extensions 与各 host 包测试。
- core/agent/extensions、Console、Node、Profile、命令入口的 race；full Web 与入口的 race。
- 联合取消与排空、Loop panic、命令声明及异常隔离、flags 默认值等测试重复 race 运行 10 次。
- `harness` 的三个 `TestUser*` 真实进程场景：配置与崩溃恢复、并发配置切换、启动恢复与确认退出。
  场景使用隔离目录、loopback 服务及无模型配置，不调用真实 LLM。

没有执行依赖真实模型的 `TestLiveLLM*`；本机浏览器复用 E2E 仍按环境测试单独报告，
因此不将环境失败描述成默认全仓测试通过。Console/Web/Node 是 Session Runtime 的并列
入口，不拥有独立资源或注册，无需为了形式一致再包装成 Extension。
