# Agent Loop Extension

`agent/` 实现标准 loop、inbox、evaluator 和工具执行。`pkg/exts/agent` 只为一个选定的
`agent.Loop` 提供生命周期准入、取消和 drain；Session 由独立的 `pkg/exts/session` 拥有。

`New(loop)` 无副作用，`Runtime()` 只暴露 Run，不暴露 Load/Close。Load 将调用
绑定到 `Scope.Lifetime()`；Close 停止新调用、取消正在执行的调用并等待排空。超时返回原始
context 错误，由 Set 标记为 `ErrCloseIncomplete` 后保留依赖供下次 Close 重试。

Session 构造时借用 Loop Runtime、App 和具体配置。Agent Extension 不查找 Service、不创建
子 Set，也不关闭 App。IOA delivery、Console 展示、历史和 Session 命令仍由各自 Extension
负责。
