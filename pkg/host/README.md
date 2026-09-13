# 嵌入通信 Host

`pkg/host` 负责 inline 进程内调用与 stdio 进程间通信。两种入口共用
`Host.Handle` 和现有 `aop.NamespaceMux`，直接使用 `*aop.Envelope`。
该包仅依赖 AOP、protobuf 和标准库，不构造 Agent、工具或产品 App。

## 使用

应用在开始接收请求前，注册实际业务处理函数。现有产品提供
`session.Manager.RegisterNamespaces(mux)`；嵌入者也可直接调用 `mux.Register`。

```go
mux := aop.NewNamespaceMux(ctx)
if err := runtime.RegisterNamespaces(mux); err != nil {
    return err
}
h := host.New(mux)
defer h.Close()

// inline：send 是现有 aop.SendFunc，可接收异步响应。
err := h.Handle(request, send)

// stdio：根据入口选择此路径；Stdio 只负责 protobuf JSONL 编解码。
stream := host.NewStdio(input, output)
err = h.Serve(stream)
```

可编译的最小 inline 示例见 [example_test.go](example_test.go)，真实子进程往返
验证见 [process_test.go](process_test.go)。这两个示例均不调用模型或工具；产品进程的用户验收见 [harness](../../harness/README.md)。

## 唯一职责与状态所有权

| 对象 | 状态与职责 |
| --- | --- |
| `Host` | 通信 context、请求准入、在途分发、发送互斥、首个写入错误 |
| `Stdio` | 行读取器和输出 writer；只编解码，不保存连接状态 |
| `aop.NamespaceMux` | 每连接的协议路由表；注册带 owner，拥有 namespace context、执行准入和排空 |
| 产品 Runtime | Session、Run、Inbox、业务 goroutine、事件订阅 |

Host 是一个实际需要维护连接状态的具体对象。它不持有 Sender/Sink 对象，不提供
新接口、DTO、传输适配器、依赖容器或另一套业务状态机。`Send` 接收现有的
`aop.SendFunc`，用于让请求响应和主动事件经过同一个关闭检查与发送互斥。
所有写入错误只由 Host 保存，Stdio 不重复保存错误或给写入加锁。

Web 使用已有 `pkg/web.Connection`，Node 使用已有连接循环和发送队列；它们已经
拥有连接生命周期，因此不再套 Host。Node 直接调用产品公开的 core/command
处理函数，复用同一份业务实现；不再靠可选接口断言选择控制入口，也不重复解码。
Web/Node 的握手、错误码及 EOF 策略保持各自原有行为。

## 关闭与错误语义

- `Handle` 返回仅表示本次分发返回，异步处理函数仍可通过传入的 `send` 响应。
- `Serve` 遇到正常 EOF 返回 nil，停止本次读取循环但不关闭 Host。调用者随后等待
  自己的业务工作、发出最后事件、注销订阅，再关闭 Host 并检查 `h.Err()`。
- `Close` 停止后续请求与发送，取消 Host context，等待已进入的分发和写入。
  它可重复调用，并关闭本连接的 mux；它不关闭借用的 Runtime、输入输出流。
- 异步处理函数自行创建的 goroutine 由业务拥有者等待。Host 会传递取消，并拒绝
  关闭后才提交的响应；不会假装已经等待全部业务工作。
- 首个写入错误会取消 Host 并保留到 `Err()`，后续写入不会重试。编码错误、短写、
  EOF 后异步响应的写入错误都走这条路径。

每条连接构造独立 mux。直接注册使用 `mux.Register(owner, prototype, handler)`，
分发使用 `mux.Dispatch(envelope, send)`。处理函数收到 namespace 的连接级 context，
返回后仍有效，直到所属 owner、mux 或连接关闭；不能在重连时复用已经关闭的 mux。
- 任意 `io.Reader`/`io.Writer` 无法仅靠 context 取消中断。流拥有者必须关闭或设置
  自己的 IO deadline 来解除阻塞；Host 不为此创建可能泄漏的读 goroutine。
- send 回调中不可同步重入同一 Host 的 `Handle`、`Send` 或 `Close`；业务处理函数
  内也不调用 `Close`。连接拥有者负责从外部关闭。

## 边界守卫

公开 inline 接入、产品 stdio 接线、真实子进程通信、异步响应、连接取消和
关闭隔离、并发发送与错误保留、Node 具体运行时接线回归，以及协议依赖边界检查。
`queuedEnvelopeStream`、嵌套 Host、可选控制接口和 Stdio 重复状态均不保留。

Session Manager 位于 `pkg/exts/session`，Console 位于 `pkg/console`；Host 不持有二者。
协议辅助函数使用 `aop.EnvelopeID`、`aop.Reply` 和 `aop.NewProtocolError`，不再由 Host 提供。
