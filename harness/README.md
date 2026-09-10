# Repository harness

`harness/` 只放用户场景：从当前工作区源码构建 `cmd/aiscan` 的 `full` 版本，
启动真实产品子进程，通过公开 HTTP / Connect JSON / stdio 接口操作，验证实际配置文件、
进程重启与资源释放。测试不导入业务实现包，不注入 fake store、Provider 或 Host。

静态守卫位于仓库根目录 `architecture_test.go`，运行 `make check-architecture`。
协议回显测试位于 `pkg/host/process_test.go`，不计入用户场景验收。

运行：

```sh
go test -count=1 -v -timeout 5m ./harness/...
# 或
make harness
```

需要 Go 与完整产品构建、运行所需的原生依赖；缺失时直接失败，不跳过或替换实现。
Web 服务以 `--no-agent` 启动，绑定 `127.0.0.1:0`；IOA 场景额外启动两个独立的
`aiscan agent --transport stdio` 进程。每个场景有独立的配置、数据库与
数据目录。无 LLM 场景仅继承操作系统及动态库加载所需环境变量。真实 LLM 场景
只额外注入明确配置的 `AISCAN_HARNESS_LLM_*`，不读取个人默认模型设置。

当前验收范围：

| 场景 | 验证范围 |
| --- | --- |
| `TestUserConfigurationAcrossCrashAndRestart` | 登录、跨客户端配置可见性、非法修改保持旧文件、错误后的有效保存、空白密钥保留、强制终止后恢复、重新编辑与登出再登录 |
| `TestUserConcurrentProfileChanges` | 随机选择配置，三个独立客户端并发读取；检查每次读取是完整的旧/新状态、写入后全部客户端收敛、重启恢复最后一次提交 |
| `TestUserStartupRecoveryAndConfirmedExit` | 错误 YAML 启动失败、用户修正文件后启动、保存、首次退出提示后二次确认、退出码 130、操作系统释放端口及数据库文件、再次启动恢复配置 |
| `TestLiveLLMRecoveryAcrossRestart`（`live_llm`） | 真实模型响应、产品模型状态、漏填模型后的错误与重试、产品重启后从新客户端再次调用真实模型 |
| `TestLiveLLMConcurrentClients`（`live_llm`） | 两个客户端并发请求真实模型，同时第三个客户端读取配置与状态，完成后登出再登录并再次调用模型 |
| `TestLiveLLMMultiAgentIOAThreadAndIsolation`（`live_llm`） | 两个独立 AI 上下文经两个产品进程实际调用 IOA；随机任务、计算回复、节点定向与原消息引用、回执、完整线程读取、空间切换与隔离 |
| `TestLiveLLMParentDelegatesIOASiblings`（`live_llm`） | 产品主 Agent 实际调用 `subagent` 创建两个异步子会话；子会话经 IOA 交换 offer → reply → ack；主 Agent 收到两份完成通知后读取线程并结束；校验父子事件和自动 handoff 记录 |

随机场景打印种子并写入 `seed.txt`。设置 `AISCAN_HARNESS_SEED=<整数>` 可重放操作序列，
`AISCAN_HARNESS_STEPS` 控制切换次数（默认 12，范围 1–100，CI 使用 48）；
线程调度不保证逐次一致。每次运行都记录各客户端的请求路径、响应、状态和耗时，
以及产品进程日志、实际 YAML、数据库。真实模型密钥只经环境和内存中的 HTTP 请求传递，
不写入 YAML。日志按完整行脱敏，HTTP 响应也在记录和报错前脱敏。默认保存在 `.runlogs/harness/<run>/`；
`AISCAN_HARNESS_ARTIFACTS` 可指定保存父目录。临时产品二进制在运行结束后删除。

当前 CLI 在二次退出确认后调用 `os.Exit(130)`；退出测试不能证明 App 的 defer 收尾执行。
Web 长期服务也不受 `--timeout` 控制。两点按实际用户行为记录，不把强制退出称为优雅关闭。

默认三个场景验证无 LLM 的 Web 配置工作流。两个 LLM 连接场景通过产品的 `TestLLM`
接口向真实 Provider 发送 `ping`，要求成功且回复非空，不匹配固定文本或用假模型替代。
这两个场景有 6 次显式模型请求，另有 3 次产品启动健康检查；每次请求的
输出上限由产品探测接口限制为 16 tokens，无测试级自动重试。服务失败和超时直接失败。

## IOA 多 AI 任务

IOA 场景中，每个 AI 保有独立模型历史，调用真实模型的 function calling。模型输出只能
选择加入预设空间、查看节点、读取消息/线程、发送纯文本及消息/节点引用；测试驱动
把这些操作交给各自产品进程的公开命令协议，不接受任意 shell 命令。

1. A 发布包含随机 nonce 与任务词的消息，并读取确认。
2. B 从 IOA 读取任务，保留 nonce、将任务词转为大写，向 A 定向回复并引用原消息。
3. A 读取回复并校验内容，向 B 发送引用该回复的回执。
4. B 从原线程读取回执，切换到另一空间，读取并发送独立标记。
5. harness 独立读取两个空间，逐项核对实际内容、节点身份、消息引用、完整线程及标记隔离。

模型不能相互读取本地历史；B 的初始提示中没有随机任务内容，也没有消息 ID。
线程消息由模型实际发送，harness 不代发或纠正。每个 AI 在整个场景中最多 24 次
模型请求，每次最多 512 输出 tokens；两者共享 140 秒模型任务期限。另有两次产品
启动探测。接口错误、预算耗尽、错误内容、重复消息或缺少证据均失败，不自动重跑。
`*-model.jsonl` 保存各 AI 的任务、调用、真实响应与 token 用量；每个进程保存脱敏的
`protocol.jsonl` 和 `stderr.log`，验收成功时额外生成 `ioa-evidence.json`。

**覆盖边界：这是两个 AI 操作真实产品 IOA 的主动读取/回复场景。** 产品启动时各自
订阅独立空 inbox，随后模型用 IOA 命令加入工作空间；命令空间切换不会替换启动时的
inbox 订阅。这样不会同时启动另一条产品 Agent 推理循环；如果出现内部 `turnStarted`，
测试直接失败。它不证明 SSE 自动唤醒、并发协作、掉线重连或 Agent 自主调度正确。
空间隔离验证的是当前空间的消息选择，不是空间访问控制权限。

## 主 Agent → subagent → IOA 闭环

`TestLiveLLMParentDelegatesIOASiblings` 只向一个真实产品进程提交一次根任务。
主 Agent 自己加入工作空间并调用内置 `subagent` 工具，创建 `worker-a` 和 `worker-b`
两个 `async` 子会话；harness 不创建子会话，也不代发消息。

- A 发送随机 `offer:nonce`，B 从 IOA 发现 nonce 并发送引用 offer 的 `reply:nonce`。
- A 读取 reply，发送引用 reply 的 `ack:nonce`；B 读取 ack 后完成。
- 主 Agent 的真实 system inbox 收到两份 `subagent_completion`，随后读取最终 IOA 线程并返回结果。
- harness 将每个 IOA 消息 ID 与对应子会话的 `bash` 工具结果关联，检查父会话 ID、派发 tool call ID、异步会话重叠、子会话完成及主会话最终结束。
- 同时验证产品自动写入的两条 delegate、两条 return handoff，以及 return 对 delegate 的引用。

当前产品的子 Agent 是**独立会话，共享工具注册表和 IOA 节点**。因此消息 sender 相同，
由 AOP 子会话证明消息来自哪个子 Agent；这个场景不声称子 Agent 有独立的 IOA 身份、
权限或进程。两个独立节点的通信由上一节的场景覆盖。

真实模型由本机测试网关转发，网关只暴露产品已有的 `bash` 和父会话的 `subagent`，
并在交给产品执行前校验每个响应的完整工具参数。仅允许本次预设空间的 IOA 读写，
禁止额外派发、直接 `subagent.message` 转发和任意 shell 命令。它不生成、修正或回放
模型回答。SSE 响应完整缓冲后原样交付，因此这里不测 token 流的实时延迟。

整个父子任务最多 40 次真实模型请求（含启动探测），每次最多 1024 输出 tokens，
根任务限 16 个 turn，150 秒内必须完成。任意越界调用、超时、遗漏消息或生命周期
证据均失败；不以重试或 skip 变绿。真实 API key 只存在于测试网关，产品收到的是
本机网关的测试 token。`subagent-model.jsonl` 保存脱敏请求/响应，产品保存完整协议日志，
`subagent-evidence.json` 汇总父子关系、IOA 消息、handoff 和每个角色的模型请求数。

这个测试已经包含在 live CI 的 `^TestLiveLLM` 选择器中。单独运行：

```sh
make harness-llm-subagent
```

## 尚待补齐的任务

| 机制 | 模型需要实际完成的任务 | harness 独立检查的证据 |
| --- | --- | --- |
| tmux / PTY | 启动交互程序，读取随机挑战，提交错误输入后在同一会话恢复，读取确认结果，终止会话并查看状态 | 子进程生成的随机挑战与操作回执、确实发生过错误与恢复、终止前进程仍活跃、终止后进程及其端口释放 |
| proxy | 经本机代理访问本机 HTTP 服务，观察故障代理导致的失败，恢复请求，验证临时代理设置结束后的默认路径 | 上游代理的真实连接记录、目标服务的请求记录及随机响应、失败请求未绕过代理、恢复后的路由与响应正确 |
| IOA 自动协作 | 并发节点通过订阅自动接收任务，断线恢复并继续处理 | SSE 投递、自动唤醒、去重、任务完成和节点退出的独立证据 |


模型参与任务执行，但不能成为唯一裁判。模型自述成功、日志出现命令名、会话创建
返回成功，都不能替代上述证据。固定回放、缺少配置后的 skip、没有匹配到测试的
`no tests to run` 也不能算真实任务通过。

实现时需明确区分两种覆盖：模型作为外部用户操作公开协议，验证的是产品机制；
模型运行在产品自身 Agent 循环中，才覆盖产品的工具选择、上下文与异步消息处理。
前者不能宣称覆盖后者。每个场景使用临时目录和回环地址，对可执行动作、模型请求数、
输出 tokens、总时长和资源收尾设置硬限制；`--tools` 是可选工具组设置，并非执行白名单。

## CI 与真实 LLM 配置

| 路径 | 触发与验收 |
| --- | --- |
| `harness-offline` | 每个 PR、master push、手动触发；Linux / Windows × 两个种子，race 检查驱动、随机用例顺序、48 次并发配置切换 |
| `harness-live` | master push 和手动触发；不在 PR 代码上注入密钥；缺少配置直接失败 |
| `harness-gate` | 要求所有被选中的 suite 成功，失败或取消会阻断 release-verify；PR 的 live job 明确标记为未执行 |

普通单元覆盖率任务排除 harness 包，由独立 job 验收产品进程，避免重复运行。
两条路径均保存 JSON 测试结果和运行产物 14 天。race 检查当前覆盖测试驱动，
产品子进程仍由普通 `go build -tags full` 构建。

在 GitHub 仓库设置中配置：

| 配置 | 类型 | 要求 |
| --- | --- | --- |
| `AISCAN_HARNESS_LLM_API_KEY` | Secret | 必填，使用独立测试密钥 |
| `AISCAN_HARNESS_LLM_BASE_URL` | Actions Variable | 必填，HTTP(S) API 根地址，不含 URL 凭据或查询参数 |
| `AISCAN_HARNESS_LLM_MODEL` | Actions Variable | 必填，支持 function calling 的模型；本地验证使用 `deepseek-chat` |
| `AISCAN_HARNESS_LLM_PROVIDER` | Actions Variable | 可选，默认 `openai`；完整 live suite 的 IOA 操作器当前支持 `openai`、`deepseek`（OpenAI 兼容接口）；连接场景单独运行时仍支持产品其他 Provider |

本地使用同名环境变量后执行 `make harness-llm`，或：

```sh
go test -tags live_llm -run '^TestLiveLLM' -count=1 -v -timeout 8m ./harness/...
# 只运行多 AI IOA 场景
make harness-llm-ioa
```

`live_llm` 是显式测试选择，不以 `t.Skip` 隐藏缺少模型配置。默认无 LLM 测试不编入
live 场景，即使机器存在个人模型密钥也不会自动调用。HTTP 客户端模拟用户操作接口；
当前尚未验证浏览器渲染、Agent 会话任务的推理质量或整仓库所有功能。新增场景应明确
实际入口、前置条件、可观察结果与退出条件，避免用固定快照数量代替覆盖范围。
