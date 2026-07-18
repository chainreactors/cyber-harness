# PR #56 后端新增机制

本文档记录 `feat/agent-console-aligned` 分支引入的所有后端新机制、协议变更和行为契约。

---

## 1. Agent 池稳定身份

**问题**: hub 原来每次 WS 连接都 `generateID()` 生成随机 key。chat session 在创建时冻结 `agent_id`，agent 断连重连后 id 变化，session 绑定的旧 id 解析到空，消息被拒为 "not connected"。

**机制**: `agentKey()` 从 agent 的 `RegisterPayload` 中提取稳定标识（`NodeName` → `Name` → fallback random），作为 pool 的唯一 key。重连的 agent 覆盖旧 slot 而非新建。

**守卫**:
- `register()` 检测旧连接并 Close，触发旧 read loop 退出
- `unregister()` 只在 slot 仍属于当前实例时才删除，防止旧 defer 误删新连接
- SQLite migration 将历史 session 的 `agent_id` 对齐到 `agent_name`

**文件**: `pkg/web/agents.go`

---

## 2. SSE 可靠性分级

**问题**: SSE buffer 满时所有事件同等丢弃。终结性事件（message_end、error）被丢弃后 UI 永远停在 streaming indicator。

**机制**: `HubEvent` 新增 `Reliable bool`。`Hub.Broadcast` 在 buffer 满时:
- 非 Reliable（token delta）: 直接丢弃，下一个 delta 会补
- Reliable（终结性事件）: 驱逐最旧的 queued 事件腾出空间，保证送达

**Reliable 事件**: message, message_end, error, scan_complete, scan_error, eval

**文件**: `pkg/web/sse.go`, `pkg/web/service.go`

---

## 3. 配置热重载链路

**完整链路**:

```
Settings UI 保存
  → Service.SaveConfig() 重建 hub 的 App (同步)
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

**失败隔离**: 重建 provider 失败时旧 provider 不变，日志记录原因。channel 满则跳过，agent 下次重连自然拉取。

**并发模型**: `Agent.SetProvider()` / `SetMaxTurns()` 在 `mu.Lock` 下修改 `Cfg`。`Run`/`Continue` 开始时 `configSnapshot()` 在锁下拷贝，已在飞的 run 不受影响。

**文件**: `pkg/web/agents.go`, `pkg/webagent/agent.go`, `core/runner/runner.go`, `pkg/agent/agent.go`

---

## 4. ChatPayload — Goal 模式协议

**旧协议**: `"chat"` 消息的 Payload 只有 `{"session_id":"..."}`。

**新协议**: 扩展为 `webproto.ChatPayload`:

```go
type ChatPayload struct {
    SessionID       string  // web session 隔离
    Persist         bool    // 多轮保持
    EvalCriteria    string  // Goal 评判标准 (非空触发 evaluator loop)
    EvalMaxRounds   int     // 评估轮次上限
    PersistMaxTurns int     // 单轮 turn 上限
}
```

从前端 Goal 面板 → hub `SendMessageRequest` → `DispatchChatSession` → agent WS 透传。agent 端 `runChatWithAgent` 据此决定执行普通对话还是进入 evaluator 循环。

**文件**: `pkg/webproto/message.go`, `pkg/web/types.go`, `pkg/webagent/agent.go`

---

## 5. Eval 事件透传与持久化

**序列化修复**: `Event.MarshalJSON` 是 allowlist 模式，之前缺少 `EvalRound`/`EvalPass`/`EvalReason`/`EvalError` 四个字段，verdict 从未离开 agent 进程。

**hub 转发**: `forwardAgentEvent` 新增 `"agent.eval_end"` / `"agent.eval_error"` case，转为 `ChatEventEval` 广播到 session SSE。`eval_start` 是瞬态标记，不转发。

**持久化**: `persistRuntimeChatEvent` 新增 `ChatEventEval` case，将 round/pass/reason 存为 system message + metadata。页面刷新后从 metadata 重建 eval 徽章。

**评估器门控修正**: 旧逻辑只对 Terminated/Completed 执行评估，turn-capped（Stopped）或 token-capped（Budget）的 agent 被静默跳过。新逻辑只在 Error/Canceled 时跳过。

**文件**: `pkg/agent/event_json.go`, `pkg/agent/evaluator/loop.go`, `pkg/web/agents.go`, `pkg/web/service.go`

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

统一模式: probe 失败写入 `ConnCheck.Error`，不返回 error。返回的 error 仅表示 section 不可测。

### LLM 探活

- `TestLLM`: 发 `maxTokens=16` 的 "ping" completion 验证连通性
- `ListLLMModels`: 调用 provider 的 `GET /models` 返回 model picklist

### 安全

- `redactURLError`: 从 `*url.Error` 中剥离 query string（FOFA/Hunter API key 在 query 中）
- 空 APIKey 回退到 stored config 中的值（Settings UI 留空表示保持不变）

**文件**: `pkg/probe/conn.go`, `pkg/probe/llm.go`, `pkg/web/probe.go`, `pkg/web/handler.go`

---

## 7. Provider 能力扩展

### ListModels

两个 provider 都实现 `ListModels(ctx) ([]string, error)`，通过 `GET {base}/models` 返回 model ID 列表。编译期 `capability_parity_test.go` 守卫能力对齐。

### hint404 协议提示

chat endpoint 返回 404 时包裹 actionable 建议（如"设置 `llm.provider=anthropic`"）。用 `%w` 保留原始 `*APIError` 链，不破坏 retry 分类。

### InferFromBaseURL

检测 `anthropic.com` 域名自动推断 provider，其他默认 `openai`。

**文件**: `pkg/agent/provider/anthropic.go`, `pkg/agent/provider/openai.go`, `pkg/agent/provider/http.go`, `pkg/agent/provider/provider.go`

---

## 8. 本地 Agent 管理 (LocalAgents)

hub 可通过 API 在本机 fork `aiscan agent` 子进程:

```
POST /api/deploy/local     — Launch (fork aiscan agent, 自动拨入 hub)
GET  /api/deploy/local     — List (cross-reference pool 判断连接/忙碌状态)
DELETE /api/deploy/local/{id} — Stop (kill 子进程)
```

子进程通过 `--web-url`/`--ioa-url` 连接 hub 的回环端口，IOA token 嵌入 URL userinfo。退出自动从 roster 移除，hub shutdown 时 `StopAll()` kill 全部。

**文件**: `pkg/web/localagent.go`, `cmd/aiscan/web_full.go`

---

## 9. Web 命令路由

### 分层执行

`dispatchUserMessage` 对 `/verb` 消息分三层路由:

1. `/clear` — hub 全权处理（清 store → 信号 UI → 转发 agent 清 context）
2. hub 命令 (`/scan`, `/agents`, `/help`) — 本地执行
3. 其余 — 透传给 agent 的 `runChatREPLLine`，由 agent 的完整 TUI console 执行

agent 端的 skill 命令和 `!bash` 从浏览器也能用。

### 命令菜单

`GET /api/chat/sessions/{id}/commands` 返回 `SessionMenu()` — hub 3 个命令 + agent 注册时上报的命令元数据（从 `tui.Command` 提取，含 skill）。前端 "/" 弹出菜单从这里拉取。

**文件**: `pkg/web/service.go`, `pkg/web/handler.go`

---

## 10. System Message i18n

`broadcastSystemMessage(sessionID, code, fallback, params)`:

- `code`: 稳定翻译 key（如 `file_uploaded`）
- `params`: 插值变量（如 `{"filename": "note.txt", "path": "/tmp/..."}`)
- `fallback`: 英文文本，供非 i18n 消费者 / 日志 / 测试使用

持久化时 code+params 存入 `ChatMessage.Metadata` JSON，前端从中渲染本地化文本。

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

**文件**: `pkg/webagent/agent.go`

---

## 12. completeAssistantRun 始终广播

**问题**: 旧 `persistAssistantMessage` 在 content 为空时跳过广播和持久化。tool-only turn 或 eval 命中轮次上限时 UI 卡在 streaming indicator。

**机制**: 新 `completeAssistantRun` **始终广播** terminal message event，但只在有文本时持久化。空回复不留空行，UI 正常释放 composer。

**文件**: `pkg/web/service.go`

---

## 13. message_end 中间轮持久化

**问题**: 多轮对话中只有最终聚合回复被持久化（`completeAssistantRun`），中间每轮的 assistant 文本只在 SSE 流中出现，页面刷新后消失。

**机制**: `persistRuntimeChatEvent` 新增 `ChatEventMessageEnd` case。每轮非空的 finalized text 存为 assistant message，带 turn 元数据。`buildTimelineFromMessages` 按 turn 归到正确的气泡。streaming partials (message_start/message_delta) 不持久化。

**文件**: `pkg/web/service.go`

---

## 14. TUI 渲染改进

### CJK 感知宽度

`visibleWidth` 使用 `go-runewidth` 计算终端列宽（CJK 字符占 2 列，ANSI 转义零宽度）。`clipVisible` 在列宽边界截断并保留 ANSI 序列。`renderFixedBox` 改为固定宽度裁剪而非被最长行撑宽。

### 中间截断

`truncMiddle(s, max)` 保留头尾（如 `/var/lib/...agent_history`），用于 /status 中的 history 路径。

### IOA boxed 输出

`/spaces`、`/nodes`、`/messages` 改为 `renderBoxTable` boxed panel 渲染，与 `/status` 和 `/provider` 风格一致。

### IOA URL 脱敏

`redactIOAURL` 剥离 `http://<token>@host/ioa` 中的 userinfo，防止 token 泄露到终端/截图。

### fenceTerminalOutput

REPL 多行输出（box-drawing panel）在 web chat 中包裹 code fence，让前端以 monospace `<pre>` 渲染。单行输出保持 prose。fence 长度自适应避免与内容中的 backtick 冲突。

**文件**: `pkg/tui/banner.go`, `pkg/tui/commands.go`, `pkg/tui/ioa.go`, `pkg/webagent/agent.go`

---

## 15. 环境变量优先级修正

旧逻辑中 provider-scoped env（如 `ANTHROPIC_MODEL`）和 aiscan 自有 env（`AISCAN_MODEL`）在 `else if` 链中平级。hub 启动的 agent 继承 hub 环境后，Settings UI 配置的 model 被环境变量覆盖。

新逻辑拆为两个独立 `if`:
1. 先看 aiscan 自有 env（`AISCAN_MODEL`）
2. 再检查 `option.Model` 是否仍为空，才 fallback 到 provider env

对 `BaseURL`、`APIKey` 同理。

**文件**: `core/config/env.go`
