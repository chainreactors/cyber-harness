# aiscan as cairn runner

## 核心思路

aiscan `cmd/runner/` 编译精简二进制（工具链，不带 agent/web/TUI），作为 **tool-only 节点**复用 aiscan 中立的 `pkg/webagent`/`pkg/webproto` 协议接入 cairn。cairn 服务端（`server/internal/runner/`）做信封适配。

aiscan 侧不再维护任何 cairn 专属协议包（原 `pkg/cairnrunner` 已删除）。所有 aiscan 工具（scan/gogo/spray/neutron/zombie/proton）通过 webproto 的 `exec` 消息暴露，runner 在 exec handler 中拦截已注册的命令名走进程内执行（BashTool 统一策略边界）。

## 协议：复用 webproto

runner 与 cairn 之间使用 aiscan 的 webproto 信封 `{type, task_id, data, data_b64, payload}`：

| 消息 | 方向 | 用途 |
|---|---|---|
| `register` → `connected` | R→S / S→R | 握手（payload 携带 name/node/runtime/commands） |
| `exec` → `complete`/`error` | S→R / R→S | 命令执行（payload: ExecPayload / ExecResult） |
| `output` | R→S | 流式 stdout/stderr（payload.stream 区分流） |
| `file.read` / `file.write` → `complete` | S→R / R→S | 文件读写（base64 `data_b64`，JSON-only，无 binary frame） |
| `pty` | 双向 | PTY 帧（payload 结构不变） |
| `cancel` | S→R | 按 task_id 取消 |
| `tool.data` / `tool.sco` | R→S | 扫描器遥测 / 归一化 SCO 节点（task_id = call_id） |
| WS ping/pong | 双向 | 心跳（原生帧） |

与早期 bespoke 设计（hello/welcome、数字 id req/res、binary frame 文件块）的差异全部由 **cairn 服务端适配层**吸收：数字 id 映射为字符串 task_id（`exec-N`），文件传输改为单发 base64，握手改为 register/connected。

## aiscan runner 侧

### cmd/runner/main.go

入口只做三件事：

1. 解析 flags（`--server` / `--token` / `--name` / `--ws-path`，ws-path 默认 `/ws/runner`）
2. `initTools()` 构建 `*commands.CommandRegistry`（core/scanner/arsenal 组）+ dataBus + SCO sidecar
3. 调 `webagent.RunToolNode(ctx, webagent.ToolNodeConfig{...})`

`RunToolNode`（`pkg/webagent/toolnode.go`）复用 webagent 的连接循环（register 握手、断线重连、exec/file/pty/cancel 分发、tool.data/tool.sco 事件转发），但不挂 LLM provider、agent loop 与 IOA 依赖——NodeRef 由 ServerURL 直接合成。

### exec handler — 统一进入 BashTool

`pkg/webagent/exec.go` 的 `ExecCommand`：所有 exec 经注册的 BashTool `RunForeground` 执行，流式输出走 `output` 消息（`payload:{"stream":"stdout"}`），终态走 `complete` 消息（payload 为 `webproto.ExecResult{exit_code, state, kill_cause, duration, details?}`）。

## cairn 服务端适配

适配全部集中在 `server/internal/runner/`，TS 侧零改动（只走 Go 内部 HTTP API）：

- `protocol.go` — 信封类型换成 webproto（`{type, task_id, ...}`）
- `bridge.go` — 握手 register→connected；读循环按 `type` 分发；tool.data/tool.sco 的 call_id 取 `task_id`
- `rpc.go` — pending map 键为字符串 task_id；exec 结果从 `complete.payload` 组装；文件读写单发 base64

## PTY 转发 + cyber-ui 复用

cairn 的浏览器终端经 `/ws/runners/:id/exec` 把 `pty` 消息透传到 runner 连接；runner 侧由 webagent 连接循环里的 `PTYRouter` 处理（frame 结构与 aiscan WebAgent 完全一致，仅信封字段名为 `type` 而非 `t`）。

## Explore agent 使用

全部通过 exec，和普通 shell 命令一样：

```bash
scan -i 192.168.1.0/24 --mode quick
gogo -i 10.0.0.1 -p top1000
neutron -t cve-2024-xxxx.yaml 192.168.1.10
spray -u http://target.com --crawl
zombie -i 192.168.1.10:3306 --top 100
```
