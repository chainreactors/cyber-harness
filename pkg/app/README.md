# App：产品资源所有权

`app.New(ctx, app.Config{...})` 装配 Provider、Commands、Skills、Hooks、共享事件总线、
引擎、IOA 和 Recorder。配置构建继续使用现有 `AppConfig`、`AppConfigFromDistribute`
及 Provider 配置转换函数，没有第二套配置对象或兼容门面。

依赖方向为 `runner/console/node → runtime → app → 既有能力包`。App 不导入 Runtime、Console、Host、runner、TUI、Node 或 Web。
通信仍由 Host 或各入口的既有连接负责；Session、Run、状态命令和终端格式化由 Runtime 与 Console 分别负责。

## 共享事件

App 直接实现既有 `aop.EventEmitter`：Agent 和 Commands 使用 App，观察者订阅 `App.EventBus`。
`App.Emit` 补全时间戳和缺省 ID、分配每个 Session 的序号，并发布原始 AOP Event。
多个 Runtime 借用同一 App 时共用序号状态，关闭或重建 Runtime 不会重置它。

Session/Turn 的生命周期事件由 runner 在对应操作处构造。App 不解释这些事件，
也没有独立 emitter、队列、Sink、DTO 或转发接口。投递保持原有同步语义：
订阅回调在序号锁之外执行，可重入；并发生产者的回调需要自行同步，投递顺序不保证按序号排列。

`New` 总会初始化 EventBus。测试或嵌入代码直接构造 App 并用于 Runtime 时必须显式提供它，
Runtime 不再回填 App 的共享状态。

## Provider 与状态

- `ProviderState()` 返回既有 Provider 与 ProviderConfig 的一致快照；字段由 App 私有持有。
- `SetProvider(provider, config)` 安装已经构建的 Provider，并标记连接探测待完成。
- `ReloadProvider(ctx, config)` 构建、安装并探测 Provider，返回 Provider 与解析后的配置。
  构建失败保留旧状态；探测失败保留有效配置并记录健康状态。迟到的探测结果不会覆盖更新的配置状态。
- `LLMHealth()` 和 `ScannerState()` 读取 App 拥有的资源状态，不返回新的状态 DTO 或暴露 readiness channel。

运行期间需要同时更新会话时，通过 Runtime 的 `SetProvider` / `ReloadProvider` 入口调用。
Runtime 负责更新自己的模板与现有会话，运行中的 Run 保留其快照。
App 不保存 Runtime 列表，也不反向通知其他借用者；多个 Runtime 的配置更新由调用方显式协调。

## 创建与关闭

App 只关闭自己装配的共享资源。关闭时取消 App context，等待 IOA 注册任务和引擎初始化结束，
再释放 Recorder、Commands、引擎及代理资源；重复关闭无额外作用。
IOA 初次设置响应调用方取消，后续注册重试属于 App context。已关闭的 App 不再接受 IOA 设置或录制重启。

Runtime 的 `ExistingApp` 表示借用：调用方先关闭使用它的 Runtime，再关闭 App。
只有 Runtime 自己创建的 App 才随该 Runtime 关闭。Web 继续使用现有的借用计数延迟释放旧 App。

## 阶段边界

App、Runtime、Console 已完成包级拆分。Runtime 的传递依赖不包含展示、Host 或入口包；
Console 拥有终端任务，runner 负责产品入口。详见 [Runtime](../runtime/README.md) 与 [Console](../console/README.md)。
