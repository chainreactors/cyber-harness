# 扩展与装配架构

系统由**扩展**组装。装配只有两个通用机制，都在 `core/resource` 里，都以 Go 类型为键——
没有扩展 ID、没有依赖 DAG、没有服务名字符串、没有 DTO。

```
Define[T] + Add[T]      多 → 一   贡献点：一个扩展拥有存放 T 的点，其他扩展往里加
Provide[T] + Use[T]     一 → 多   共享能力：一个扩展提供唯一的 T，其他扩展借用
```

配置值走构造参数，能力走 `Use`。判断一个东西属于哪边，看它有没有生命周期。

## 为什么是两对而不是一个

这两个方向的数据流、所有权和关闭顺序都相反，合并会让其中一边说谎：

| | 贡献点 | 共享能力 |
|---|---|---|
| 谁读 | 拥有者读自己的数据 | 借用者读别人的值 |
| 键是什么 | 被存放的**值类型**（`commands.Command`） | 描述**行为的接口**（`commands.Executor`） |
| 数量 | 多个贡献批次 | 恰好一个提供者 |
| 关闭 | 贡献先于拥有者的 `Close` 撤销 | 借用在借用者的 `Close` **之后**释放 |

因为键不同，同一个对象可以既是点又是能力——`commands.Registry` 就是：写入方 `Add[commands.Command]`，
调用方 `Use[commands.Executor]`。"一个类型要么是点要么是能力"由构造保证，不靠约定。

## 类型即键的三条约束

1. **键必须是为此声明的具名类型。** 不用内建类型，不用结构化的 `func`/`map`/`slice`——
   那是等着发生的碰撞。
2. **不用第三方模块的类型做键。** 进程注册表的键是仓内的 `agent/proc.Sessions`，不是 `utils/proc.SessionManager`；否则谁都能声称它。
3. **一个被拥有的对象，一个能力键。** 想要更窄的视图，消费者在自己包里声明接口并转换——
   Go 接口是结构化的，窄化是消费者的私事，不是第二个协调点。

`Provide` 一律显式写类型实参：`extension.Provide[commands.Executor](scope, r)`。推导会把
它注册到具体类型上，而消费者按接口找，错误要到加载期才暴露。

## 加载与关闭

`extension.Set` 按切片顺序加载，逆序关闭。每个扩展拿到一个带**加载序数**的 `Scope`，
`resource` 用它强制"提供者必须排在消费者之前"——顺序错了是一条指名道姓的加载错误，
不是一个运行期的 nil。

关闭分四段，顺序是承重的：

```
scope.stop()              取消这个扩展的生命周期 context
scope.close(handles)      撤销它发布的东西（Define / Add / Provide）
extension.Close(ctx)      扩展排空自己的工作
scope.release(borrows)    释放它借来的东西（Use）
```

借用排在最后，因为扩展的 `Close` 正是它排空仍在调用借来的值的地方。提供者在仍有借用时
关闭会返回 `ErrCloseIncomplete`——引用计数是受检的，不是靠纪律。

推论：**同一个扩展不能借用自己提供的东西**，那会让计数永远归不了零。`Use` 直接拒绝。

`Use` 在 `Freeze` 之后也被拒绝，这把它钉死在 `Load` 体内。装配根要读取加载结果，用排在
切片最后的 `extension.Func`——它借得最晚、释放最早。

## 什么是能力

四条合取，四条都要满足：

1. **穿透深度 ≥ 1** —— 该值至少穿过一个自己不使用它的函数。这才是判据，不是消费者数量。
2. **是身份，不是配置** —— 若两个实例能合理共存，它就是配置，归构造参数。
3. **恰好一个扩展拥有它的生命周期** —— 没有拥有者就没有 `Close`，引用计数是空的。
4. **消费者只读** —— 需要拥有者替自己**持有**东西的，要的是贡献点。

**没有 `TryUse`。** 一旦存在可选形式，`if x != nil` 会重新长回每个消费者。可选性由切片
成员资格表达，或者由空对象表达：不带代理的 host 装 `base.NoEgress()`，发布一个
`egress.Disabled()`。这也是"需要表达缺席的能力，键用接口"的由来。

`Use` 返回的 error 只表示装配故障——没提供、顺序错了、正在关闭。加载成功即蕴含存在。

## 当前的能力

| 键 | 提供者 | 借用者 |
|---|---|---|
| `*hooks.Registry` | `base` | 命令/工具注册表、files、terminal、proxy、session、observe |
| `*events.Stream` | `base` | telemetry、observe、IOA |
| `egress.Endpoint` | proxy ext，或 `base.NoEgress()` | terminal、scanner、search |
| `commands.Executor` | 命令注册表 | terminal、proxy、scanner |
| `tool.Executor` | 工具注册表 | scanner、session |
| `*skills.Store` | 技能库 | scanner、session |
| `*terminaltool.BashTool` | terminal ext | tmux、scanner、session |
| `agent/proc.Sessions` | terminal ext | pty |
| `agent.Loop` | loop ext，profile 不选推理时是 `agent.NoLoop()` | scanner、session |
| `prompt.Resolver` | prompt ext | scanner、session；经 Agent 传给 evaluator、compact |
| `*app.App` | app ext | provider、scanner、search、session |
| `*console/api.Registry` | tui ext | 装配根（加载后封存并读取贡献） |
| `*agentsession.Runtime` | session ext | 装配根 |

借用者一列不是给运行时查的，它是加载顺序的说明书：提供者必须排在每一个借用者之前。

## Prompt 贡献点

`pkg/exts/prompt` 拥有 `prompt.Contribution` Point，并提供只读的 `prompt.Resolver` 能力。
默认 agent、evaluator 与 compact prompt 都是普通 contribution；scanner 等功能扩展拥有自己的
typed target、payload 和默认 prompt contribution。所有功能扩展通过
`extension.Add` 追加、替换、删除或重置具名 section，不再向 session 传 `PromptConfig` 或拼接
preamble。内置 section ID 是稳定 API，target 区分每一种模型请求。

Contribution 严格按扩展注册顺序执行，不另设 priority 或依赖图。每次修改在 `Document` 副本上
完成，失败或 panic 只产生 diagnostic，不泄漏部分修改；renderer 同样隔离诊断。一次 Agent Run
在 `BeforeRun` hook 之前解析一次 system prompt，随后整个 Run 保持不变。贡献句柄撤销会取消并
排空正在执行的 apply/render。

OKF 是这种功能扩展的实例：完整 `aiscan` 安装 `pkg/exts/okf`，由它贡献 Markdown 策略、`okf`
校验命令和虚拟参考文档；最小 `cmd/agent` 只安装通用 prompt ext，不自动启用 OKF。

## `tools/` 与 `pkg/exts/`

Extension 是**生命周期**的边界，`tools/` 不是。`tools/<name>` 是普通业务类型——引擎、客户端、
渲染器——由某个 Extension 构造并持有：没有 `Load`/`Close`，没有 Scope，不进 Profile 的 Set。
`pkg/exts/<feature>` 是适配层，把需要启动时装、关闭时停的东西包成 Extension，再登记它贡献的
Point 与它提供、借用的能力。

判据因此是生命周期而不是代码量。守卫测试把这条钉在目录上：每个 `pkg/exts/<feature>` 子树至少
要声明一个 `Load(*extension.Scope)`；没有 Extension 的子树要么是胶水、属于拥有该资源的
`tools/` 包，要么是绕过了适配层、让宿主直接学会了一个领域概念。

## 装配根

`pkg/base.New(Config) ([]extension.Extension, error)` 返回每个 host 都需要的核心扩展，
按必须加载的顺序。

> **它的返回值只能是这两个。** 如果它需要回传一个 `*hooks.Registry`、一个 `*BashTool`
> 或一个 `*app.App`，说明还有能力没迁完。这条签名是读侧是否真的生效的测试。

同一条测试适用于每个装配根：构造一个扩展、再把它的内部读回来交给下一个扩展，说明那个值
是能力而不是构造参数。`agent.Loop` 曾经这样穿过两个 profile，`*association.Index` 曾经这样
从 scanner 穿到 search。前者成了能力；后者不是——`cyberhub` 搜的就是 scanner 自己的索引，
命令搬回了索引的拥有者。空索引和没有索引在这里语义不同（后者要告诉用户怎么配置），所以
它不能用空对象表达，也就不该是能力。

组合仍然属于可执行文件：`base` 只交回一个切片，host 拥有 `Set`、决定追加什么、决定顺序。

- **`cmd/aiscan`** —— 完整产品。按 build tag 选装 scanner、search、proxy、browser、record、Web、IOA。
- **`cmd/agent`** —— 最小本地 Agent。它不装代理，于是发布 `base.NoEgress()`；Go 的 import
  图因此免费给了它隔离性——`base` 不 import `pkg/exts/scanner`，它也就永远链接不到。
  `cmd/agent/main_test.go` 的依赖闭包守卫把这条钉死。
- **`pkg/runner`** —— 共享的运行模式逻辑，不是可执行命令。

一个 Profile 只有一个线性 Set。host 可以为不同生命周期建立并列的 Set（Web listener 的附属
资源、一次性的 IOA 连接检查），这些 Set 不嵌套、不共享 Scope 或 Resource Registry。

**顺序不做推导。** 拓扑排序排的是加载序，而真正需要的是关闭序，后者不是依赖图的逆——
"遥测最后 flush""TUI 先于它渲染的东西停"这类约束，依赖关系表达不了。显式切片让启动序列
在一个地方读得完；代价是顺序错误从编译期挪到了启动期，补偿是每个 profile 形态的
加载/关闭测试（`cmd/aiscan/composition_test.go`、`cmd/agent/composition_test.go`）。

## `app.App`

App **借用字段为零**。它只拥有自己的东西：provider 状态、progress bus、事件流、logger。

每个扩展拥有的东西，由需要它的扩展按能力取，而不是停在 App 上供人读取。这条不变式是
二元的——读一眼结构体就能查——也是 `pkg/app` 不再随每个新依赖增加 import 的原因。

非扩展消费者（`pkg/node`、`pkg/console`、`pkg/runner`）没有 `Scope`，它们从构造自己的那个
东西拿窄参数：会话相关的经 `agentsession.Runtime` 的访问器，命令表面经 host 可选实现的
`Shell()`。

## 进程与工作单元

`github.com/chainreactors/utils/proc` 是工作单元注册表，不只管进程。一个单元由
**attachment** 描述，四种形态共用同一套身份、状态、诊断缓冲、超时、停止阶梯和事件：

| 形态 | 是什么 |
|---|---|
| `tty` | 伪终端上的外部进程：合流输出、可 resize、可送键 |
| `pipe` | 管道上的外部进程：stdout/stderr 分流，stdout 可作字节精确的协议流 |
| `func` | 进程内函数，没有 pid 也没有退出码 |
| `extern` | 由别的子系统驱动的工作，注册表只持有身份与取消句柄 |

`Attached` 是一个可空字段的能力结构体：只有 `Wait` 必填，nil 字段表示这个形态没有该能力——
全包没有一个 no-op 实现。`Info.Proc` 当且仅当有 OS 进程时非 nil，所以"没有退出码"和
"退出码为 0"在协议上是两回事。

停止是一条阶梯：interrupt → terminate → kill。tty 的 interrupt 是往 pty 写 `0x03`，因为
行规程会把它交给前台作业；被追踪进程退出后阶梯会扫一遍进程组——非交互 shell 让后台作业
忽略礼貌信号，它们经常会活下来，而**留下幸存者的停止不算停止**。

就绪与存活是 attachment 携带的两个探针。注册表对就绪的全部认知是"一个最终返回 nil 的
函数"，它永远不知道某个单元的就绪意味着什么。

## 事件、hooks 与 operation

三者分工不重叠，互相不能替代：

- **`core/events.Stream`** —— AOP 事实流，负责事件 ID、时间与序号。telemetry、console、
  web、node 只消费实际事件。
- **`core/hooks`** —— 控制与观察。`process.before` 这类点是 `FailClosed` 的准入边界。
- **`core/operation`** —— 调用关联：tool / command / process 共用一套相关性与取消。

关闭时入口先停止接收工作，Set 再逆序撤销。Registry handle 只影响自己的批次，不会取消
别的插件的调用。资源替换靠 `Add` 新批次 + `Close` 旧 handle；整个 Profile 的组合变化则
构造新 Profile。
