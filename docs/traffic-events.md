# Traffic observation and storage

ProxyHub 始终是稳定的本地转发入口；capture 开启时记录完成的 HTTP Flow，关闭时只转发。
MITM `FlowFinished` 是唯一完成边界。FlowStore 在 body finalization 和 metadata 提交完成后发
typed `http.completed` hook，客户端 EOF 不构成事实发布屏障。

`traffic.body_storage=none` 只保留有界 preview；`disk` 将 body 增量保存到
`.aiscan/mitm/capture/body`，FlowStore 只在查询/发送边界按需 hydrate。单 body 和总保留
预算均在启动前校验，失败或截断会明确标记 Flow 不完整。

FlowStore 的内部 eventbus 只驱动磁盘 metadata index，并由 FlowStore 在关闭时 drain；
它不是公开订阅面。body 文件或索引失败会显式进入 Flow/Store 错误，不静默丢失。

实时 AOP 事实由 `pkg/exts/observe` 将 HTTP hook 投影到统一 Event Stream，并在 Event typed
extensions 中携带 `aop.operation.Ref`。`pkg/exts/eventoutput` 可将它与 Agent、Tool、File、
Process 事件写入同一 JSONL；Traffic 不维护第二份实时日志。Traffic 协议只按请求返回
State 或 `FlowRecord{operation, flow}` 快照，不维护实时流，也不使用合成 session ID。

线协议仍传完整 Flow 而不是 body chunks。Disk 模式按发送者逐条读取保留 body；none 模式
只发送 preview。缺失或已驱逐文件会标记 incomplete。
