# PR #56 后端新增机制

本文档记录 `feat/agent-console-aligned` 分支引入的所有后端新机制、协议变更和行为契约。

---

## 1. Agent 池稳定身份

**问题**: hub 原来每次 WS 连接都 `generateID()` 生成随机 key。Chat Session 在创建时绑定 `node_id`；如果连接 key 不稳定，节点重连后 Session 会解析到空并拒绝新消息。

**机制**: `agentKey()` 从生成的 `aop.AgentHello` 中提取稳定标识，作为 pool 的唯一 key。重连的 agent 覆盖旧 slot 而非新建。

**守卫**:
- `register()` 检测旧连接并 Close，触发旧 read loop 退出
- `unregister()` 只在 slot 仍属于当前实例时才删除，防止旧 defer 误删新连接
- SQLite v2 migration 将历史 `chat_sessions.agent_id` 列原位重命名为 `node_id`

**文件**: `pkg/web/agents.go`

---

## 2. Typed broker 可靠性分级

**问题**: live buffer 满时若所有事件同等丢弃，终结性事件被丢弃后 UI 会停在 streaming indicator。

**机制**: `Hub` 只传递 typed `AOPDelivery` 和 `scan.ScanEvent`。广播方显式标记可靠性；buffer 满时：
- 非 reliable（token delta、scan progress）：直接丢弃
- reliable（完整 message、turn ended、scan terminal）：驱逐最旧 queued 事件后入队

持久化重放由 `chat_aop_events` 和 Scan snapshot 负责，live protobuf 不经过 JSON envelope。

**文件**: `pkg/web/broker.go`, `pkg/web/api/envelope.go`, `pkg/web/service.go`

---

## 3. 配置热重载链路

**完整链路**:

```
Settings UI 保存
  → Service.SaveConfig() 串行化保存请求
  → PrepareDistributeConfig() 同目录写 0600 临时文件并 fsync
  → AppFactory 从临时文件完整构建候选 App
  → CommitDistributeConfig() 原子替换正式配置
  → swapApp() 将新请求切到候选 App
      └─ 旧 App 标记 retired，最后一个活动租约释放后才 Close
  → BroadcastConfigReload() 向所有 agent 推 "config" 消息 (非阻塞)
  → agent 收到后异步:
      FetchRemoteConfig(hubURL) 拉取最新配置
      → chatRuntimeManager.reloadProvider() 加锁重建 provider
        ├─ rt.App.Provider = new
        ├─ rt.Config.Provider = new
        ├─ 遍历所有 live session: ag.SetProvider(new)
        └─ 发 "agent.identity" {provider, model} 回报 hub
  → hub 合并 identity → UI 徽章实时更新
```

**失败隔离**: 候选 App 构建失败时删除临时文件，正式配置和旧 App 都不变；原子提交失败时同时关闭候选 App。只有配置落盘成功后才交换 App 和通知 agent。agent 重建 provider 失败时保留旧 provider；reload 已排队或正等待控制 channel 空间时，后续请求会合并，agent 拉取的仍是最新正式配置。

**并发模型**: hub 的 `saveMu` 防止多个配置事务交错；本地扫描通过 managed App 租约继续使用旧运行时，不会被保存设置中断。agent 侧 `Agent.SetProvider()` / `SetMaxTurns()` 在 `mu.Lock` 下修改 `Cfg`，`Run`/`Continue` 开始时 `configSnapshot()` 在锁下拷贝，已在飞的 run 不受影响。

**文件**: `pkg/web/service.go`, `cmd/aiscan/web_full.go`, `pkg/web/agents.go`, `pkg/node/agent.go`, `pkg/runner/runner.go`, `agent/agent.go`

---

## 4. Goal 模式 AOP 扩展

Goal 参数不再定义 Chat DTO。`RunTurnRequest` 是唯一输入；AIScan 专属字段编码为
`Any<aiscan.agent.AgentRunOptions>` 并放入 `RunTurnRequest.extensions`，类型身份只由标准
`type.googleapis.com/aiscan.agent.AgentRunOptions` 表达。普通对话和 evaluator 复用同一
Run/Turn 生命周期。

**文件**: `proto/types/agent.proto`, `pkg/runner/runtime_protocol.go`, `pkg/web/service.go`

---

## 5. Eval 事件透传与持久化

agent 在 producer 边缘生成 `aop.Event`；hub 通过 `aop.Envelope` 原样转发。
评估字段使用 `aiscan.agent.EvalDetail` protobuf `Any` 扩展，不做 flatten。

eval/compact 徽章仍可由 hub 从 AOP extension 派生为 Web 平台控制事件，但不会再投影成另一套 agent 事件或 system message。会话正文只持久化到 `chat_aop_events`，刷新后从同一 AOP 源重建。

**评估器门控修正**: 旧逻辑只对 Terminated/Completed 执行评估，turn-capped（Stopped）或 token-capped（Budget）的 agent 被静默跳过。新逻辑只在 Error/Canceled 时跳过。

**文件**: `agent/aop_emit.go`, `agent/evaluator/loop.go`, `pkg/web/agents.go`, `pkg/web/service.go`

---

## 6. 探活框架 (pkg/probe)

新包，为 Settings UI 的 "Test Connection" 按钮提供后端。

### 连接探活

`TestConn(ctx, section, config, storedConfig)` 按 section 路由:

| section | 探活方式 |
|---------|---------|
| cyberhub | Provider.Fingers() 采样 |
| recon | FOFA account-info + Hunter minimal search (分别返回) |
| search | Tavily "ping" search |
| ioa | Client.ListSpaces() |

统一模式: probe 失败写入 protobuf `ConnectionCheck.error`，不返回传输 error。返回的 error 仅表示 section 不可测。

### LLM 探活

- `TestLLM`: 发 `maxTokens=16` 的 "ping" completion 验证连通性
- `ListLLMModels`: 调用 provider 的 `GET /models` 返回 model picklist；404 作为“不支持目录”正常降级为手动输入

### 安全

- `redactURLError`: 从 `*url.Error` 中剥离 query string（FOFA/Hunter API key 在 query 中）
- 空 APIKey 按请求携带的 `profile_id` 回退到对应 stored config；缺省 ID 才使用 active profile

**文件**: `pkg/probe/conn.go`, `pkg/probe/llm.go`, `pkg/web/probe.go`, `pkg/web/handler.go`

---

## 7. Provider 能力扩展

### ListModels

两个协议 provider 都实现 `ListModels(ctx) ([]string, error)`，通过 `GET {base}/models` 返回 model ID 列表。编译期 `capability_parity_test.go` 守卫能力对齐。

### Provider 协议

运行时只接受 `openai` 和 `anthropic`。两者分别提供官方默认 Base URL；其他模型服务必须显式使用 `openai` 协议并填写 `base_url`。不识别品牌名称，也不做别名映射。

### hint404 协议提示

chat endpoint 返回 404 时包裹 actionable 建议（如"设置 `llm.provider=anthropic`"）。用 `%w` 保留原始 `*APIError` 链，不破坏 retry 分类。

### InferFromBaseURL

这里只推断传输协议：检测 `anthropic.com` 域名选择 `anthropic`，其他自定义地址默认使用 `openai` 兼容协议。

**文件**: `agent/provider/anthropic.go`, `agent/provider/openai.go`, `agent/provider/http.go`, `agent/provider/provider.go`

---

## 8. 内嵌 Agent (Embedded Agent)

`aiscan web` 默认在同一进程内同时启动 hub 和一个 agent：agent 通过 loopback WebSocket 以标准 node 身份注册进 AgentPool（hello → agent_accepted → 配置推送），与外部 `aiscan agent` 节点没有任何区别——pool 里不存在 "local"/"in-process" 特殊种类。`aiscan web --no-agent` 只启动 web 控制台。

**文件**: `cmd/aiscan/web_full.go`（内嵌 agent 启动）, `pkg/node/agent.go`（node 侧入口）

---

## 9. Web 命令路由

### 分层执行

`dispatchUserMessage` 对 `/verb` 消息分三层路由:

1. `/clear` — 前端调用 `SessionService.ResetSession`，原 session 关闭并创建 clean session
2. hub 命令 (`/scan`, `/agents`, `/help`) — 本地执行
3. 其余 — 透传给 agent 的 `runChatREPLLine`，由 agent 的完整 TUI console 执行

agent 端的 skill 命令和 `!bash` 从浏览器也能用。

### 命令菜单

`aiscan.chat.SessionService/ListCommands` 返回 `SessionMenu()` — hub 命令 + agent 注册时上报的命令元数据（从 `tui.Command` 提取，含 skill）。前端 "/" 弹出菜单通过生成的 Connect client 拉取；Scan 不属于 Chat 命令协议。

**文件**: `pkg/web/service.go`, `pkg/web/handler.go`

---

## 10. System Message i18n

`broadcastSystemMessage(sessionID, code, fallback, params)` 直接生成并持久化 AOP message event：

- `code`: 稳定翻译 key（如 `file_uploaded`）
- `params`: 插值变量（如 `{"filename": "note.txt", "path": "/tmp/..."}`)
- `fallback`: 英文文本，供非 i18n 消费者 / 日志 / 测试使用

AOP error 事件把 code 保存在 `ProtocolError.code`，params 使用
`Any<aiscan.agent.WebMessageMetadata>` 放入 Event extension。通用 reducer
保留该扩展，因此实时流和重放使用同一参数来源。

已定义的 code:

| code | 含义 | params |
|------|------|--------|
| `no_running_task` | 无运行中任务 | — |
| `paused` | 已暂停 | — |
| `file_uploaded` | 文件上传完成 | filename, path |
| `no_agents_connected` | 无 agent 连接 | — |
| `agents_list` | agent 列表 | count, agents[] |
| `agent_not_connected` | agent 未连接 | — |

**文件**: `pkg/web/types.go`, `pkg/web/service.go`

---

## 11. 文件上传路径传播

**问题**: hub 上传文件到 agent 后，`SysFileUploaded` 通知只到达 UI，LLM 从未看到磁盘路径。用户让 agent "读取上传的文件"，agent 只能猜测 cwd 下的文件名。

**机制**:

1. `handleFileUpload` 写入磁盘后调用 `notePendingUpload(sessionID, note)` 记录绝对路径
2. 下次该 session 的自然语言消息到达时，`takePendingUploads` 一次性 drain 所有 note，拼接到 prompt 前面
3. REPL 命令（`/` 或 `!` 开头）不触发 drain，防止污染命令语法，note 保留到下一条自然语言消息

**文件**: `pkg/node/agent.go`

---

## 12. Agent 生命周期统一由 AOP 驱动

**问题**: 旧 Web 路径通过 `completeAssistantRun` 合成终止消息，并另外持久化中间轮次。它与 Runtime 已产生的 AOP message/turn 生命周期重复，tool-only turn 还需要额外的空消息规则才能释放 UI 状态。

**机制**: Runtime 产生的 typed AOP event 是 Agent 消息、工具调用和 turn 状态的唯一语义来源。Web 层直接转发和持久化这些事件，不再合成第二套 assistant 完成事件，也不再为中间轮次维护独立的聊天事件协议。

AIScan 产品事件使用 AOP core 的 typed Any 插槽；例如 scan 完成通过
`Event.extension = Any<aiscan.scan.SessionScanEvent>` 表达。`Any.type_url` 是唯一类型身份，不再维护 `ExtensionEvent`、namespace 字符串或 `DomainEvent`。

**文件**: `pkg/runner/`, `aop/`, `pkg/web/service.go`

---

## 13. TUI 渲染改进

### CJK 感知宽度

`visibleWidth` 使用 `go-runewidth` 计算终端列宽（CJK 字符占 2 列，ANSI 转义零宽度）。`clipVisible` 在列宽边界截断并保留 ANSI 序列。`renderFixedBox` 改为固定宽度裁剪而非被最长行撑宽。

### 中间截断

`truncMiddle(s, max)` 保留头尾（如 `/var/lib/...agent_history`），用于 /status 中的 history 路径。

### IOA boxed 输出

`/spaces`、`/nodes`、`/messages` 改为 `renderBoxTable` boxed panel 渲染，与 `/status` 和 `/provider` 风格一致。

### IOA URL 脱敏

`redactIOAURL` 剥离 `http://<token>@host/ioa` 中的 userinfo，防止 token 泄露到终端/截图。

### 命令展示边界

跨界面 Runtime 命令通过 typed AOP command detail 标记 `presentation: preformatted`。Web timeline 在最终展示边界生成自适应 Markdown code fence；Runtime、Session 和 transport 不再处理 Markdown 或终端格式。

**文件**: `pkg/tui/banner.go`, `pkg/tui/commands.go`, `pkg/types/extensions.go`, `core/output/timeline.go`

---

## 14. 环境变量优先级修正

旧逻辑中 provider-scoped env（如 `ANTHROPIC_MODEL`）和 aiscan 自有 env（`AISCAN_MODEL`）在 `else if` 链中平级。hub 启动的 agent 继承 hub 环境后，Settings UI 配置的 model 被环境变量覆盖。

新逻辑拆为两个独立 `if`:
1. 先看 aiscan 自有 env（`AISCAN_MODEL`）
2. 再检查 `option.Model` 是否仍为空，才 fallback 到 provider env

对 `BaseURL`、`APIKey` 同理。

**文件**: `core/config/env.go`

所有 AIScan 运行时业务环境变量都由该入口读取一次。DataDir、TUI、Playwright、Tavily 和 Uncover 只消费解析后的配置，不再自行调用 `os.Getenv`。系统级 `PATH`、Go 标准代理环境变量和 Vite 构建期变量仍按各自平台语义处理。
