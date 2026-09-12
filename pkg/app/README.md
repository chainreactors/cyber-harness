# App：内置 Agent 应用模块

`app.New(config, fileAudit, proxyInfra)` 只构造 App。它保存 profile 显式传入的
`*fileaudit.Audit` 与 `*proxy.Infra`，但不装载或关闭这两个依赖。Skills、Provider、
Commands、工具注册、引擎和 Recorder 从 `Load(ctx)` 开始初始化；业务入口在 Load
成功前不可用。`Close(ctx)` 停止 App 自己的工作，并允许调用方在等待超时后使用新
context 继续等待。

AIScan 产品由 `pkg/profile/aiscan` 组合具体模块。当前依赖图是：

```text
FileAudit ─┐
           ├─> Application ─> IOA ─┐
Proxy ─────┘                       ├─> Runtime
                  Application ─────┘
```

IOA 是 `tools/ioa.Extension`，Proxy 是 `tools/proxy.Infra`，FileAudit 是
`pkg/fileaudit.Audit`。三者都直接实现 `extension.Extension`；App 与 Runtime 只借用构造
函数中收到的具体对象。`extension.Set` 按依赖顺序装载并逆序关闭，App 不保存通用
service bag、cleanup 列表或兼容所有权。

## 共享事件

App 直接实现既有 `aop.EventEmitter`。Agent 和工具发布到 `App.EventBus`；
`App.Emit` 补全时间戳和缺省 ID，并分配每个 Session 的序号。Session/Turn 生命周期
事件由 Runtime 构造，App 不解释事件，也不增加另一套 Sink、DTO 或转发接口。

多个 Runtime 可以借用同一已装载 App，共享事件序号和 Recorder。关闭 Runtime
不会关闭 App，关闭 App 也不负责关闭 profile 拥有的 Proxy、FileAudit 或 IOA。

## Provider 与状态

- `ProviderState()` 返回既有 Provider 与 ProviderConfig 的一致快照。
- `SetProvider(provider, config)` 安装已经构造的 Provider。
- `ReloadProvider(ctx, config)` 构造、安装并探测 Provider；构造失败保留旧状态，
  迟到的探测结果不会覆盖更晚的配置。
- `LLMHealth()` 与 `ScannerState()` 直接读取实际所有者的状态。

运行期间需要同步会话时，调用 Runtime 的 `SetProvider` 或 `ReloadProvider`。
Runtime 更新自己的模板和已有会话，运行中的 Run 保留已取得的快照。

## 关闭顺序

profile 先关闭 Runtime 和可选 IOA，再关闭 Application，最后关闭 Proxy 与
FileAudit。App 内部先停止 Tool Registry 的发现和调用准入，等待已接受的调用，
随后关闭 Browser/Recorder、Bash、引擎与事件录制资源。Command 不携带 Close
回调；有资源的能力由具体所有者关闭。

依赖方向保持为 `runner/console/node → runtime → app → 能力包`。App 不导入
Runtime、Console、Host、Runner、Node 或 Web。通信由各入口与既有 AOP mux 管理。
