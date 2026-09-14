# TUI 提供注册点，功能扩展提供贡献

当前实现是 TUI 的注册层，终端实现仍在 `pkg/console`，终端的启动和停止
仍由宿主负责。加载 TUI 注册层不会打开终端，因而可以用于 headless 测试。

```text
tui.Load → 开放 console/api.Registrar
  ├─ session + session/console.Load → 注册会话命令
  └─ ioa.client + ioa/client/console.Load → 注册查询、补全、状态行
全部 Set.Load 成功 → Profile.ConsoleBindings → 冻结注册 → 宿主挂接终端
```

展示适配的 `Entry.DependsOn` 同时声明 TUI 与业务资源；构造参数显式注入
Registrar 和业务 Catalog/Reader。不通过字符串查找运行中的服务。
展示适配是 Session/IOA 扩展的可选部分，不是新的业务能力。

- TUI 未加载时注册失败；同一来源重复注册和命令名称/别名冲突会报错。
- 冻结后拒绝新贡献；切换扩展集合需重建 Profile，不支持运行中热卸载。
- Session 命令实现仍在 `agent/session`，无 TUI 时仍可使用协议调用。
- 注册时只保存工厂，不执行命令。挂接终端时通过 `View.Command` 绑定该
  终端当前 Session，不能在注册时捕获某一个全局 Session。
- 宿主先停止并排空终端，再关闭 Profile；注册表不拥有命令业务的执行队列。
- 当前 `/help`、`/resume`、`/stop` 等终端内置交互，以及 provider/skill
  菜单仍由 Console 实现，尚未迁移为独立展示贡献。

与原生命令执行区分：`pkg/cli` 是进程级 CLI；Harness 的 Command Registry
执行 native/pseudo commands；这里的 Registrar 管理 REPL 展示命令。
