# 复杂场景：持续支付流水对账

这是业务链路场景记录。后续故障深挖发现并修复了 8 项消息处理与恢复缺陷；可靠性边界和失败证据见 [Inbox 深度故障测试](inbox-deep-faults-20261101.md)。

日期：2026-11-01。数据为专门构造的测试数据。场景定义与输入位于 `exts/inbox/testdata/reconciliation/`。

## 要解决的问题

12 笔订单的付款系统与订单系统数据不一致。支付来源有重复、乱序、延迟通知，包含部分付款、退款、撤销、未知订单和币种错误。要求给出逐订单的差异和证据，随后在新消息进入及宿主重启后修正结论。

这次不是让三个 worker 各回复一句文本：分析与质量 worker 必须实际读取 CSV、计算并写文件；汇总 worker 必须等待完整证据，重新核算后输出 JSON 和 Markdown。

## 任务及消息安排

1. 创建 **reporter** 汇总任务。证据尚未齐备时返回 waiting，结束当前执行，保留任务。
2. 并行准入 **ledger** 分析任务和 **quality** 质量任务。Router 根据各 worker 的反馈，将证据消息继续投给原 reporter。
3. 第一版报告生成后关闭宿主，保存三个任务的历史和来源游标。
4. 加入迟到流水 `late.csv`，故意把旧质量报告放到 `quality-v2.json` 路径下，模拟名字正确但内容过期的产物。
5. 宿主离线时，外部 IOA producer 通知原 ledger 任务重算；重启相同状态目录后，通过真实 HTTP / SSE 和历史补读恢复该通知。
6. ledger 提交 v2 证据后，reporter 发现质量报告的版本 / SHA-256 不一致，必须返回 waiting，不能生成最终 v2 报告。
7. 再次提交同一来源消息，验证不重新执行 ledger。
8. 外部监控在线通知原 quality 任务重算。quality 反馈到原 reporter；完整证据通过后，生成最终报告。

整个过程保持 **3 个稳定任务**。保留的成功运行有 **8 个执行实例、13 条已路由消息、0 个路由错误**。这体现了任务可以长期存在，而每次执行实例可以结束或被重启替换。

## 得到的业务结果

金额在程序里全部使用整数最小货币单位，以下为了阅读方便转成元 / 美元；币种分别展示。

| 币种 | 应收净额 | 有效实收 | 差额 |
| --- | ---: | ---: | ---: |
| CNY | 630.00 | 560.00 | -70.00 |
| USD | 72.00 | 73.00 | +1.00 |

最终未平订单只有三笔：

| 订单 | 证据与解释 | 后续处理 |
| --- | --- | --- |
| A008 | CNY 收款 90.00，退款 10.00，但订单批准退款为 0，实收比应收少 10.00 | 核对退款批准记录与退款流水 e12 |
| A009 | USD 批准退款 5.00，实际退款 4.00，所以净实收比应收多 1.00 | 核对剩余 1.00 的退款进度 |
| A010 | 订单应收 CNY 60.00，却关联到 USD 59.00 的流水 e15 | 隔离错误币种流水，核对关联关系；禁止直接抵扣人民币应收 |

另有未知订单 X999 的 CNY 7.00 流水 e16，同样隔离。两个隔离金额不计入有效实收，不互相换算或混合求和。

初版存在的 A002、A005、A007 差异在新数据到达后被消除：A002 的第二笔收款被有效 void；A005 的 CNY 50.00 到账被补齐；A007 的 USD 3.00 尾款补齐。退款乱序不影响 A003、A004、A012 的正确核算。

来源总计 **25 行、21 个唯一事件、4 行重复**。去重同时覆盖单个文件内部的重复和跨文件回放；不能把两个不同 event_id 的真实付款误当成一笔。

最终来源指纹：

```text
1e36e40ab89e86d60f4a9ba2f0252fa3c0626ae27fdd613df8a5ec87853df5bd
```

## 产物与验收

成功运行目录：`outputs/inbox-reconciliation-20261101-230235/`。

- `report-v2.md`：可阅读的最终报告。
- `report-v2.json`：结构化结果和质量信息。
- `ledger-v1.json` / `ledger-v2.json`：逐订单金额及分币种汇总。
- `quality-v1.json` / `quality-v2.json`：重复、未知订单、币种错误、void 和来源指纹。
- `run-trace.json`：稳定任务、执行实例和消息路由的持久化记录摘要。
- `state/`：本次运行的真实 SQLite 持久状态。

Go 测试逐笔核对全部 12 笔订单，独立检查正确的业务金额、剩余差异集合、来源指纹、质量统计、任务数量、恢复后的历史及请求者关联。仅靠模型声称完成不会通过。

场景机制测试开启 `-race` 重复 10 轮全部通过；最后的 `exts/inbox`、`exts/ioa/client`、`cmd/aiscan` 三包回归为 145 个顶层测试通过、9 个按条件跳过、0 个失败。`go vet ./exts/inbox` 及 `git diff --check` 通过。

## 如何复现

```powershell
# 真实基础设施 + 脚本化模型决策 + Python 实际计算
go test -race ./exts/inbox -run '^TestReconciliationScenario$' -count=1 -v -timeout 90s

# 调度重复验证；这次 10 轮全部通过
go test -race ./exts/inbox -run '^TestReconciliationScenario$' -shuffle=on -count=10 -v -timeout 120s

# 可选：保存一次运行的产物，目标目录必须为空
$env:CYBER_INBOX_SCENARIO_OUTPUT='D:\path\to\new-empty-scenario-directory'
go test -race ./exts/inbox -run '^TestReconciliationScenario$' -count=1 -v -timeout 90s
Remove-Item Env:CYBER_INBOX_SCENARIO_OUTPUT

# 网络恢复后，预先从本机安全环境设置 CYBER_API_KEY
$env:CYBER_INBOX_LIVE='1'
$env:CYBER_MODEL='deepseek-flash'
$env:CYBER_BASE_URL='https://sub.chainreactors.cn/v1'
go test ./exts/inbox -run '^TestLiveReconciliationScenario$' -count=1 -v -timeout 10m
```

## 验证边界

本次成功运行使用脚本化 Provider 决定路由和工具调用；CSV 计算确实通过 worker 的 Bash 工具执行参考程序，IOA / SSE、Session、反馈、SQLite 与重启都是真实路径。它证明这个业务场景的机制链路和参考解答可以完成，不证明真实模型已经自主完成问题分解与程序实现。

live 版本复用同一验收器，只提供 CSV 和任务 / 输出约束，不复制参考程序，也不将 Go 中的验收答案提供给 worker。真实模型需要自行实现解答。目前指定端点的连接仍超时，live 场景尚未验证通过。此时应保留这一边界，不能用离线成功代替真实模型能力结论。
