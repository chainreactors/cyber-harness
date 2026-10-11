# Cyber Harness 接入 SigNoz / Phoenix

正式 Cyber Agent 发布现有 AOP，`exts/otel` 将其转换为 traces、结构化日志和指标。Collector 将三种信号写入 SigNoz，并把相同 traces 写入 Phoenix。Agent / 工具无需新增 OTel hooks 或协议定义。映射及边界见 [Agent 可观测性](../../docs/observability.md)。

## 启动本地后端

```powershell
docker compose -f examples/observability/docker/docker-compose.yaml up -d --wait --wait-timeout 240
```

| 服务 | 地址 |
| --- | --- |
| SigNoz | http://127.0.0.1:24880 |
| Phoenix | http://127.0.0.1:24006 |
| OTLP/HTTP 基地址 | http://127.0.0.1:24318 |
| OTLP/gRPC Collector | 127.0.0.1:24317 |
| Collector 健康检查 | http://127.0.0.1:23133 |

固定版本为 SigNoz v0.129.0、Collector v0.144.5、Phoenix 20.20.0、ClickHouse 25.5.6。SigNoz 配置来自 [官方 v0.129.0 Compose](https://github.com/SigNoz/signoz/tree/v0.129.0/deploy/docker)，附带的授权文本见 [SIGNOZ-LICENSE](common/SIGNOZ-LICENSE)。服务绑定本机，数据使用命名卷保存。首次访问 SigNoz 创建本地管理员；Phoenix 本地实例关闭身份验证。这组配置用于本地开发。

## 执行正式 Agent

在仓库根目录执行，模型密钥与配置沿用正常 Cyber 配置：

```powershell
$env:OTEL_EXPORTER_OTLP_ENDPOINT = 'http://127.0.0.1:24318'
$env:OTEL_SERVICE_NAME = 'cyber-harness'
$env:CYBER_OTEL_PROJECT = 'cyber-harness'
go run ./cmd/aiscan agent --prompt '用 Bash 执行 pwd，然后解释结果'
```

OTel 开启且未显式选择 `--observe` 时，自动启用现有 tools / commands / processes 观测。需要文件/HTTP 事实时使用 `--observe tools,commands,processes,files,http`；显式选择保持原样。OTLP 客户端使用 HTTP，gRPC 端口供其他客户端使用。

复杂任务可用正常 `--task-file`，例如审查代码、生成统计程序、执行已有测试并输出报告。`--output <路径>` 保存 canonical AOP JSONL，可与后端记录对照；`--output-format json` 输出最终结果及 usage。JSONL 中 protobuf uint64 是数字字符串，统计只累计顶层 usage，不能再次累计 TurnEnded 的 usage 摘要或 assistant 消息内的工具调用。

默认不导出消息正文。`CYBER_OTEL_CAPTURE_CONTENT=true` 开启最多 4096 字节的输入输出和事件正文。此开关不采集原始 provider frames 或流式 delta；AOP 日志不等于自动采集任意 stdout。

## 查看数据

SigNoz 按 `service.name` 筛选；每个进程中的扩展实例还有独立 `service.instance.id`，可关联同一次执行的三个信号。Traces 中根 `agent.turn` 展开模型、工具、命令和进程；Logs 中可见 Session/Turn、operation、usage、outcome 和 trace/span ID。Phoenix 的对应项目使用 AGENT / LLM / TOOL / CHAIN 展示同一 trace。

| 指标 | 用途 |
| --- | --- |
| `cyber.agent.sessions` / `.active` / `cyber.agent.session.duration` | Session 开始/结束转换、活跃数与耗时 |
| `cyber.agent.turns` / `.active` / `cyber.agent.turn.duration` | Turn outcome、活跃数与耗时 |
| `cyber.scope.completed` / `.active` / `.duration` | tool / command / process 和其他机制的状态及耗时 |
| `cyber.model.requests` / `cyber.model.usage.missing` | 模型请求数与缺失 usage 覆盖 |
| `cyber.model.tokens` | input / output / total；总量只筛 `gen_ai.token.type=total` |
| `gen_ai.client.operation.duration` / `gen_ai.client.token.usage` | 模型耗时和 token histogram |
| `cyber.aop.events` / `cyber.otel.events.dropped` | 消费的事实和已观察到的订阅丢弃 |

指标不以 Session/Turn/operation ID 为标签。`cyber.agent.sessions` 是状态转换计数，筛 `cyber.lifecycle.state=started` 才表示启动的 Session 数。累计 input/output/total 不可三者相加。SigNoz 将 histogram 保存为 `.sum` / `.count` / `.bucket` 序列。

任务进度按生命周期和正在执行的工具展示，不估算百分比。tokens 在 AOP usage 到达后更新；当前 StandardLoop 在本轮工具结果收集后发布 usage，长工具会延后该响应计量。模型 active 表示请求投影尚未收尾，不保证此时网络请求仍在进行。startup / recap 等辅助请求没有完整 AOP usage，因此已报告 tokens 不等于整个进程或网关账单。

短任务可以对每条指标序列取累计末值。查询 ClickHouse 时按 fingerprint + metric_name 去重，不能用 Map 属性的物理顺序区分序列：

```sql
SELECT sum(v) AS reported_tokens
FROM (
  SELECT argMax(s.value, s.unix_milli) AS v
  FROM signoz_metrics.distributed_samples_v4 s
  INNER JOIN (
    SELECT fingerprint, metric_name, any(attrs) AS attrs
    FROM signoz_metrics.distributed_time_series_v4
    WHERE resource_attrs['service.instance.id'] = '<本次实例ID>'
    GROUP BY fingerprint, metric_name
  ) t ON s.fingerprint = t.fingerprint AND s.metric_name = t.metric_name
  WHERE t.metric_name = 'cyber.model.tokens'
    AND t.attrs['gen_ai.token.type'] = 'total'
  GROUP BY t.fingerprint
)
```

该查询用于累计末值核对，不表示当前时间窗口内的速率。长期服务可在 SigNoz 创建按时间范围计算的看板和告警。

2026-10-11 的工作区验收执行了真实模型销售/SLO/审计任务，获得 138,297 tokens、22 模型请求、28 工具、361 logs / 69 spans；随后正式 CLI 执行仓库审查、Go 测试和统计脚本修复，含一次超时取消，共 211,744 tokens、21 模型请求、29 工具、374 logs / 63 spans。JSONL、CLI usage、SigNoz trace/log/metric 与 Phoenix traces 核对一致，活跃数最终归零。Agent 生成的报表另行验收，CLI completed 不替代业务正确性。测试代码、验收夹具、运行脚本、截图、凭据和临时产物保留在本地，未纳入此变更。

## 嵌入式宿主与关闭

在事件流提供者之后、执行者之前安装 `otelext.New(Options{Endpoint: ...})`；需要工具/命令/进程生命周期时安装现有 `observe`。三个 SDK 独立，Resource 一致，不修改全局 provider。MetricInterval 默认 10 秒，Flush / Close 也导出指标。

先结束生产者、Session 与后台工作，再关闭 OTel。Flush 不强制结束运行中的任务；Close 将未完成 scope/Turn/Session 标记 incomplete 并归零活跃数。导出失败和已观察到的 AOP 订阅丢弃由 Flush/Close 返回；Log SDK 内部丢弃尚未计量。其他事实缺口见设计文档与 [Issue #175](https://github.com/chainreactors/cyber-harness/issues/175)。

```powershell
docker compose -f examples/observability/docker/docker-compose.yaml stop
```

停止服务保留历史数据；再次启动继续使用命名卷。
