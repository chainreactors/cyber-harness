# Inbox 跨节点反馈安全与结构清理

日期：2026-11-02。平台：Windows / PowerShell。范围：Inbox Router、IOA adapter、CLI composition 与控制台。

本轮继续使用真实仓库业务负载发现问题，并按 AGENTS.md 审查无独立职责的类型、字段和兜底分支。新增一个先失败、再修复的跨节点反馈缺陷；没有把所有数据结构都视为应删除的 DTO。Provider 决策仍由测试控制，不能据此声称真实模型自主路由或长期运行已经通过。

## 新发现：跨节点反馈绕过环路边界

入口：`exts/inbox/release_feedback_test.go` 的 `TestReleaseReviewFeedbackSafetyAcrossIOA`。

1. builder 的实际 worker 通过 Bash 执行 `git show HEAD:go.mod | sha256sum`，保存当前提交的真实源码摘要。
2. durable outbox 经实际 IOA SDK、HTTP server 和 SSE，将摘要交给 reviewer。reviewer 的实际 worker 用 Bash 写入 `review-audit.txt`。
3. 关闭完整 reviewer 扩展图，在其离线期间发送携带 builder/reviewer ancestry 的反馈，再使用同一 SQLite 目录重启并补读。
4. 路由 Provider 故意先选择不安全的目标；工具必须拒绝该选择，Provider 才改为 `record`。依次验证 cyclic echo、结果隐式创建新任务、16-hop 链、历史 execution 地址回投、仅靠 `source_task_id` 表示的回投。
5. 相同 feedback ID 和 chain 的普通 `ci_notice` 仍应正常处理。等待 messages、inputs、outbox 静止后，核验实际审计文件和数据库，而非只核对模型是否调用过工具。

修复前的真实失败日志为 `$env:TEMP/aiscan-release-feedback-red-20261102.log`，关键输出：

```text
unsafe release feedback dispatched instead of recorded:
... feedback_id:echoed-review kind:task_result ...
targets=[task:...] err=<nil>
--- FAIL: TestReleaseReviewFeedbackSafetyAcrossIOA
```

根因有三个：IOA adapter 丢弃 `feedback_chain` / `source_task_id`；Router 只检查本地 `subagent_feedback`，peer 的 `task_result` 未受相同约束；CLI 的实际 Notify composition 没有发送完整 ancestry 和执行身份。

修复将这些字段从 CLI 发送端保留到 IOA 接收端；将 peer result 纳入禁止隐式 new_task、反馈环路及 16-hop 上限的同一边界；在历史 execution 解析为 task 后统一检查，删除末端重复查询和递归 fallback。结果无需进一步执行时可 record，普通 peer 消息仍须正常路由。

测试首次曾误取重启后补读的较早有效 envelope；校正为按 `feedback_id` 等待后，重新在缺陷代码上跑出上述红测试，避免将测试取样错误当作产品缺陷。

## 删除审查

删除前检查了构造者、所有仓库内生产调用者、测试及构建标签路径；未通过别名、通用 helper 或新 DTO 替换删除层。

| 候选与原职责 | 生产者 / 消费者 | 修改与契约核对 |
| --- | --- | --- |
| `presentation`、`printBoxTable`：保存现有 View 和重复 Out/Err，转发一次 Table 调用 | `console_bindings.go` 构造；三个 render 方法和 context handler 消费 | 直接使用 `consoleapi.View`，删除类型、字段、转发方法及临时复制变量。保留现有表格与输出行为，无新增 wire/schema 边界 |
| `sessionRoute.deliver/primary/delegated`：复制 `hooks.SessionEvent` 已有字段 | `sessionStart` 写入；`deliver` 选择接收方 | 改读 `route.start.Deliver/Primary/Delegation`，保留 route 的 space/dispatch 生命周期职责。仍先保留身份并置 Deliver=nil，handoff 记录成功后才开放投递，维持并发准入顺序 |
| `isNilIOADependency`：反射检查 typed-nil | 全仓库无生产调用；仅自身测试调用 | 删除 helper 及仅验证该 helper 的测试，不引入新依赖或默认值 |
| 路由上下文 marshal/unmarshal/map/marshal | Router 自己构造并立即消费 | 构造同一 metadata map 后加入 tasks，仅序列化一次；字段、JSON key 与模型投影不变 |
| `executeActive` 末端查 attempts 后递归调用自身 | 同一方法入口已解析 execution ID | 入口统一解析后删除末端重复分支；实际旧 execution 寻址仍由反馈案例覆盖 |
| `Submit` 的 nil context / nil receiver 兜底 | host adapter、CLI、测试均由 New 和正常 context 调用 | 删除隐式 Background 和 nil receiver fallback，明确非 nil 调用契约；保留 Load/Close、消息合法性、容量、持久化错误边界 |
| IOA `ReadOptions/ConfigFromOption(nil)` 自动默认 | CLI/Profile 等生产调用者均提供配置，唯一 nil 调用在测试中 | 删除 nil 特例，测试改传空 `cfg.Option` 以验证正常默认解析；配置默认与显式空值行为仍保留 |
| `/context` handler 的重复参数检查 | Cobra `ExactArgs(2)` 先负责校验 | 删除第二次 len 检查；直接读取已校验参数，错误仍由命令入口返回 |

`TestClientLegacyYAMLAndCLIOverride` 实际使用 canonical `extensions.ioa.client` 配置，因此改名为 `TestClientYAMLAndCLIOverride`；这只是纠正名称，不宣称删除了一种并不存在于此测试中的旧格式。

保留的边界：

- `storedMessage`：protobuf oneof 的持久编码与运行时 envelope 不同。
- `taskRecord/taskInput`：SQLite 索引状态、输入顺序与执行内容有不同所有权。
- Router `target`：模型仅需安全目录投影，不能暴露完整 SessionEvent。
- IOA Options 到 service.Config：默认、校验、凭证和 identity 的构造语义不同。
- `Router.Deliver`：依赖的回调返回 error，Submit 返回 Receipt，签名适配有真实职责。
- 持久 Router 与内存 Router：生命周期和持久契约不同，不作为多余兼容分支删除。
- 取消、外部输入校验、事务回滚、生命周期和反馈循环检查：这些是实际不变量，不属于无契约的防御式兜底。

## 实际 CLI 验证

`cmd/aiscan/TestInboxCLIProcessWorkflow` 使用独立 CLI 子进程、真实 Profile 和本地 IOA server。四个隔离任务分别来自 stdin、tmux 输入和外部 IOA；检查实际文件各写一行、目录独占锁、EOF 后保活，以及重启无额外模型请求。

外部请求的结果还必须携带与数据库 execution 一致的 `source_session_id`、`source_task_id`、稳定 `feedback_id` 和 ancestry；target、node 和原始请求引用也逐项核验。最终 tasks=4、attempts=4。模型 HTTP 服务使用可控响应。

## 产物与独立复核

保留产物：`outputs/inbox-release-feedback-20261102/release-feedback-2907289386/`，包含 `source.sha256`、`review-audit.txt`、`case-summary.json` 和两端 SQLite。运行数据库及 outputs 不提交到仓库。

- `case-summary.json`：reviewer tasks=1、attempts=3、inputs=3、audit_rows=2、unsafe_feedback_recorded=5。
- builder 数据库：completed task=1、completed attempt=1、done input=1、done message=2、sent outbox=1、journal=0。
- reviewer 数据库：completed task=1、completed attempts=3、done inputs=3、done messages=11、sent outbox=3、record routes=5、journal=0。
- 审计文件恰有两行真实摘要，分别对应有效 builder 结果与独立普通通知；五种不安全反馈均不增加执行。

上述计数另用 Python sqlite3 读取落盘数据库核对。源码摘要来自运行当时的 committed go.mod；不将该摘要当成整个未提交工作树的 fingerprint。

## 回归

| 范围 | 顶层通过 | 跳过 | 失败 |
| --- | ---: | ---: | ---: |
| 11 个受影响包，`-race -shuffle=on` | 531 | 18 | 0 |
| 三项真实案例随机重复 10 轮，`-race` | 30 | 0 | 0 |
| `cmd/aiscan`，`-race -tags 'full sqlite'` | 105 | 4 | 0 |
| 最后删除 presentation 后的整个 IOA client，`-race` | 30 | 0 | 0 |

以上行存在重叠，不合并为独立测试总数。主矩阵与带标签回归在最后一处控制台清理前完成，控制台变更随后由整个 IOA client 包及 `go vet ./exts/ioa/client` 覆盖。18 个跳过项包括显式 opt-in 的 live model、平台相关交互、父测试启动的 subprocess helper 和当前构建未链接能力；跳过不计为通过。

受影响 11 包的 go vet 已通过；架构/模块边界的六包检查 `pkg/internal/architecture`、`core/resource`、`core/extension`、`core/registry`、`exts`、`cmd/agent` 已通过。

本机日志均位于 `$env:TEMP`：

- `aiscan-inbox-cleanup-final-regression-20261102.jsonl`；Inbox shuffle seed `1793555232386938600`。
- `aiscan-inbox-cleanup-stress-20261102.jsonl`；seed `1793555228756180400`。
- `aiscan-inbox-cleanup-full-20261102.jsonl`。
- `aiscan-inbox-console-cleanup-20261102.jsonl`。

复现：

```powershell
go test -race ./agent ./agent/inbox ./agent/session ./agent/subagent/... ./exts/inbox ./exts/ioa/client ./exts/subagent ./pkg/config ./cmd/aiscan ./cmd/harness -count=1 -shuffle=on -timeout 180s
go test -race ./exts/inbox -run '^Test(ReleaseReviewFeedbackSafetyAcrossIOA|RepositoryCI)' -count=10 -shuffle=1793555228756180400 -timeout 180s
go test -race -tags 'full sqlite' ./cmd/aiscan -count=1 -timeout 180s
go test -race ./exts/ioa/client -count=1 -timeout 120s
```

## 仍未验证的边界

- 真实模型端点本轮经环境代理出现 TLS EOF，直连超时；没有取得 live 模型自主路由/业务解题的通过证据。未记录或提交密钥。
- 当前验证覆盖真实工具、副作用、SQLite、IOA HTTP/SSE、CLI 子进程和故障时序；不等于数小时/跨日 soak、真实第三方监控源或持续模型成本验证。
- 外部副作用完成但 ToolResult 尚未落盘的崩溃窗口仍需核对现实状态；稳定 feedback 准入去重不提供任意副作用 exactly-once。
- 内部 tmux 管理宿主中的工作单元；宿主重启依靠持久任务恢复，不保留旧 OS 进程。

本轮代码、回归测试和内部报告随分支提交并发起 PR；不新建 issue，不发布测试数据库或本机日志。
