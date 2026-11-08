# PR193：移除命名空间包装后的验证

日期：2026-11-05。本报告对应直接 meta 格式，取代此前嵌套格式的设计结论和验证结果。

## 最终格式

    {
      "meta": {
        "version": 1,
        "event": "inbox.route",
        "id": "stable-route-event",
        "source": {"node_id": "producer", "session_id": "review"},
        "target": {"task_id": "audit"}
      },
      "refs": {"messages": ["original-transport-message-id"]}
    }

字段直接进入 IOA 的动态 meta，不增加 cyber.harness、protocol 等包装或识别字段。meta.id 是稳定的应用事件 ID；IOA 消息本身的 id 仍标识一次传输。

- 删除 Go MetaKey 与前端 HARNESS_META_KEY；生产者、解析器、CLI、WebUI 和提示说明使用同一组直接字段。
- 保留固定的 typed Envelope；它定义字段类型、序列化标签和事件约束，避免把 meta 当作任意运行时状态容器。
- event 表示采用该应用协议；缺少 event 的普通记录不会执行。解析器投影已声明的协议字段，其他顶层动态 key 保留并保持不透明。
- CLI 拥有全部协议字段；用户 --meta 不能伪造 version、event、id、source、target、interrupt、chain、agent、status 或 call_id；不相关的动态字段仍保留。测试覆盖全字段伪造输入。
- 旧嵌套格式没有 fallback、没有双写；接收端和浏览器均有拒绝旧格式获得执行/图语义的测试。
- source/target、refs.messages、稳定事件身份和跨节点图身份规则保持一致。没有修改 IOA transport 或 Ref。
- 本次只改变 IOA wire meta；本地 typed Record 的存储结构没有改变，active schema 仍为 2。schema 1 仍须使用新状态目录。

## 五个真实模型复杂场景

DeepSeek 官方 API / deepseek-flash，全部通过，总计 362.349 秒。模型真实调用工具和处理业务文件；业务结果由独立 oracle、数据库状态和实际文件验证。

| 场景 | 秒数 | 关键结果 |
| --- | ---: | --- |
| 版本化对账、迟到修订、重启/离线回放 | 159.36 | 25 行、21 事件、4 重复；三稳定任务；CNY 63000/56000/-7000、USD 7200/7300/+100 |
| 24 作业 DAG、单执行槽、wait/tmux 唤醒 | 48.73 | makespan 59；并发峰值 1；三个消费者以原 execution ID 继续完成 |
| 已提交初稿后的紧急容量修订 | 44.34 | 同执行更新；最终 cost 74、weight 22、value 173 |
| 9 批乱序库存、旧版本晚到、背压和重放 | 49.60 | 48 唯一事件、10 输入、13 次背压；重复回放模型调用 0 |
| 12 文件独立发布审计、ACK 丢失与重启 | 60.17 | 2725 字节；2 次发送、1 次审计写入、1 条来源路由；生产者重新执行调用 0 |

仓库保留 [运行日志](pr193-direct-meta-20261105/live.log) 和 [独立 oracle 汇总](pr193-direct-meta-20261105/evidence-summary.json)。完整原始证据保留在本地 exts/inbox/reports/pr193-direct-meta-20261105/，包括真实模型 trace、业务产物和状态库，不纳入源码提交。

库存与发布共 31 条 IOA transcript 已逐条检查：meta.event 直接存在，cyber.harness 字段不存在。随后通过 IOA SDK 回放到真实 Hub，保留稳定事件 ID，重映射传输引用与来源节点。

## WebUI 与代码回归

- 56 项 UI 回归通过；默认跳过的外部 Hub 用例，分别以本轮库存和发布数据独立运行并通过。
- 实际登录主 WebUI，读取真实 /ioa/nodes、/ioa/spaces、/ioa/messages；验证远端来源连边、原输入引用、最终 return、事件 ID、引用跳转入口、桌面和 390px 手机布局。
- 发布数据加五项图测试最终 6 项通过，库存实际 Hub 测试另行通过。
- 完整浏览器证据保留本地 web/test-results/direct-meta-ui-20261105/、direct-meta-inventory-web-20261105/、direct-meta-release-web-20261105/；仓库附 [桌面截图](pr193-direct-meta-20261105/web-desktop.png) 和 [手机截图](pr193-direct-meta-20261105/web-mobile.png)。
- Hub 回放数据保留本地 reports/pr193-direct-meta-20261105/web-inventory.json 和 web-release.json。这是实际模型历史到实际 Hub/WebUI 的回放验证，不声称模型任务全部由 WebUI 同时启动。
- race：core/collaboration、tools/ioa、exts/ioa/client、exts/inbox、cmd/aiscan 通过。
- full sqlite tags：cmd/aiscan、agent/subagent/sessionexec、exts/inbox、exts/ioa/client 通过。
- cmd/harness 离线行为测试、live_llm 标记编译、前端 tsc、受影响包 go vet 通过。
- 全仓编译检查 go test ./... -run '^$' 通过；它不代表全仓所有行为测试。

Router 和 task.result 继续使用持久化 outbox。subagent delegate/return 仍使用同步生命周期记录，发送失败显式上报，没有新增持久化离线补发语义。此次变更不扩大交付保证。

详细协议见 [协议定义](../docs/developer/ioa-harness.md)。通用图组件由 [cyber-ui PR #28](https://github.com/chainreactors/cyber-ui/pull/28) 提供，主仓引用对应提交；原始运行证据和无关工作区产物保留本地。
