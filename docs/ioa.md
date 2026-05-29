# IOA：Intelligent Operation Architecture

IOA 是 aiscan 的多 agent 协作架构。它通过一个轻量 HTTP server 和消息空间（Space），让多个 aiscan 实例以自治 agent 的身份协同工作——每个 agent 独立决策，通过消息交换情报、分配任务、汇报结果。

---

## 目录

- [核心概念](#核心概念)
- [架构总览](#架构总览)
- [数据模型](#数据模型)
- [Loop Worker 生命周期](#loop-worker-生命周期)
- [消息路由](#消息路由)
- [Heartbeat 机制](#heartbeat-机制)
- [Peer 消息](#peer-消息)
- [IOA 工具](#ioa-工具)
- [多 Worker 协作模式](#多-worker-协作模式)
- [使用指南](#使用指南)
- [配置参考](#配置参考)

---

## 核心概念

| 概念 | 说明 |
| --- | --- |
| **Space** | 协作空间。一次渗透测试、一个目标网段可以是一个 Space。所有参与的 Node 在同一个 Space 中交换消息 |
| **Node** | 自治工作节点。每个 `aiscan agent --loop` 实例注册为一个 Node，拥有独立的 LLM agent 和工具集 |
| **Message** | Space 中的消息。可以是任务分派、情报共享、结果汇报或协调指令 |
| **Ref** | 消息引用。通过 `refs.nodes` 定向发送给特定节点，通过 `refs.messages` 建立会话线程 |
| **Task** | 标记为任务的消息。Node 收到 Task 后自动启动 agent 执行 |

---

## 架构总览

```
┌──────────────────────────────────────────────────────────┐
│                      IOA Server                          │
│              HTTP API + SSE + SQLite                     │
│  ┌─────────────────────────────────────────────────────┐ │
│  │                   Space: case-1                     │ │
│  │                                                     │ │
│  │  msg1: [scanner-1] joined, skills: scan,gogo       │ │
│  │  msg2: [recon-1] joined, skills: spray,neutron     │ │
│  │  msg3: → scanner-1: "扫描 10.0.0.0/24 端口"        │ │
│  │  msg4: [scanner-1] accepted task                    │ │
│  │  msg5: → recon-1: "对 Web 目标做指纹识别"           │ │
│  │  msg6: [scanner-1] result: 发现 12 个服务...        │ │
│  │  msg7: [recon-1] result: 识别到 nginx, tomcat...    │ │
│  └─────────────────────────────────────────────────────┘ │
└──────────┬───────────────────┬───────────────────────────┘
           │ SSE + Poll        │ SSE + Poll
     ┌─────▼──────┐      ┌────▼───────┐
     │  scanner-1 │      │  recon-1   │
     │  (Node)    │      │  (Node)    │
     │            │      │            │
     │  LLM Agent │      │  LLM Agent │
     │  gogo      │      │  spray     │
     │  zombie    │      │  neutron   │
     └────────────┘      └────────────┘
```

IOA Server 本身不做决策——它只是消息总线。所有智能都在 Node 端的 LLM agent 中。

---

## 数据模型

### Space

Space 是消息的容器，类似聊天室。一个 Space 对应一次协作任务。

```bash
# 创建/获取 Space
aiscan ioa spaces --ioa-url http://127.0.0.1:8765
```

### Node

Node 是注册到 IOA Server 的工作节点。注册时携带元数据：

```json
{
  "client": "aiscan",
  "hostname": "scanner-host",
  "capabilities": ["scan", "gogo", "spray"]
}
```

每个 Node 同一时间只执行一个 Task，其余排队等待。

### Message

Message 是 Space 中的通信单元，结构化为 `SwarmMessage`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `content` | string | 消息文本（自然语言任务描述或结果） |
| `targets` | []string | 相关目标（IP、URL 等） |
| `task` | bool | 是否为任务分派 |

### Ref（引用）

Ref 是消息路由和线程化的核心：

| 引用类型 | 说明 |
| --- | --- |
| `refs.nodes` | 定向发送。只有指定的 Node 会处理此消息 |
| `refs.messages` | 线程引用。建立消息之间的上下文关联 |
| `refs.spaces` | 跨 Space 引用 |

---

## Loop Worker 生命周期

`aiscan agent --loop` 启动一个持久运行的 Loop Worker：

### 1. 启动

```
注册 Node → 加入 Space → 发布 Profile → SSE 订阅
```

Node 启动时向 Space 发布自己的 Profile（名称、意图、技能、主机名），让其他 Node 了解自己的能力。

### 2. 消息监听

Loop Worker 通过两个通道获取消息：

- **SSE（Server-Sent Events）**：实时推送，低延迟
- **定期轮询（Poll）**：每 2 秒补偿 SSE 可能的消息丢失

使用 watermark（`lastSeenID`）+ dispatched set 双重去重，保证消息不丢失也不重复处理。

### 3. 任务执行

收到 Task 后：

```
routeIncoming() → startTask() → 发送 Accept 确认 → 启动 Agent 执行 → completeTask() → 发布结果
```

- Agent 执行期间拥有完整的 LLM 工具链（扫描器、文件操作、IOA 工具等）
- 执行结果通过 `refs.messages` 引用原始 Task 消息，形成线程
- 同一时间只执行一个 Task，新 Task 自动排队

### 4. 关闭

- 第一次 Ctrl+C：等待当前 Task 完成后退出
- 第二次 Ctrl+C：立即退出

---

## 消息路由

Node 收到消息后，按以下规则路由：

```
消息到达
  │
  ├─ 是自己发的？ → 跳过
  │
  ├─ refs.nodes 指定了其他节点？ → 跳过
  │
  ├─ 是历史消息（启动前已存在）？ → 跳过
  │
  ├─ task=true 或 refs.nodes 包含自己？
  │   ├─ 当前空闲 → 立即执行
  │   └─ 当前忙碌 → 加入待办队列
  │
  └─ 普通消息 + 当前正在执行 Task？
      → 转为 Peer 消息注入 Agent 上下文
```

**定向消息**通过 `refs.nodes` 只发给指定 Node。**广播消息**（无 `refs.nodes`）所有空闲 Node 都可以接收。

---

## Heartbeat 机制

Heartbeat 让 Worker 在没有任务时也能主动审视协作上下文并采取行动。

```bash
aiscan agent --loop --heartbeat 5 --space case-1 \
  -p "持续观察上下文，协调各节点扫描进度"
```

### 工作方式

每隔 N 分钟：

1. 读取 Space 中最近的消息（默认最近 50 条）
2. 构造结构化 prompt，包含：Space 信息、Node 自身能力、近期消息上下文
3. 交给 Agent 判断下一步行动
4. Agent 可以：执行本地工具、发送 IOA 消息协调其他 Node、或决定无需行动

### 适用场景

- **协调者角色**：一个 Node 专门负责审视全局进度，分配任务
- **持续监控**：定期检查是否有新情报需要处理
- **自愈**：发现某个 Node 长时间无响应时主动接管

---

## Peer 消息

当 Worker 正在执行 Task 时，其他 Node 发来的非 Task 消息会作为 Peer 消息注入当前 Agent 的上下文：

```xml
<swarm_peer sender="recon-1" message_id="msg-007">
发现 10.0.0.5 运行 Apache Tomcat 9.0.50，建议优先检查 CVE-2021-42013
</swarm_peer>
```

这意味着 Agent 在执行任务过程中可以实时接收来自其他 Node 的情报，并据此调整自己的行为。

---

## IOA 工具

当 Agent 连接 IOA 时，以下工具自动注册到 Agent 的工具集中：

| 工具 | 说明 |
| --- | --- |
| `ioa_send` | 向 Space 发送消息（任务分派、情报共享、结果汇报） |
| `ioa_read` | 读取 Space 中的消息（支持过滤） |
| `ioa_space` | 获取或创建 Space |
| `ioa_node` | 注册 Node 或查询 Node 信息 |

### ioa_send 示例

Agent 可以通过 `ioa_send` 给其他 Node 分配任务：

```json
{
  "space_id": "space-001",
  "content": "对 10.0.0.5:8080 的 Tomcat 执行 critical 级别 POC 检测",
  "targets": ["10.0.0.5:8080"],
  "refs": {
    "nodes": ["scanner-node-id"]
  }
}
```

---

## 多 Worker 协作模式

### 模式一：手动分工

人工为每个 Node 设定明确的职责分工：

```bash
# 终端 1：IOA Server
aiscan ioa serve

# 终端 2：端口扫描 Worker
aiscan agent --loop --space pentest-001 \
  --ioa-node-name port-scanner \
  -s gogo -p "负责端口和服务发现"

# 终端 3：Web 探测 Worker
aiscan agent --loop --space pentest-001 \
  --ioa-node-name web-recon \
  -s spray -s neutron -p "负责 Web 指纹识别和漏洞检测"

# 终端 4：弱口令 Worker
aiscan agent --loop --space pentest-001 \
  --ioa-node-name cred-tester \
  -s zombie -p "负责弱口令检测"
```

然后通过 IOA 消息发起任务：

```bash
# 通过交互式 agent 发送任务
aiscan agent --ioa-url http://127.0.0.1:8765
> 在 pentest-001 中给 port-scanner 分配任务：扫描 10.0.0.0/24 全端口
```

### 模式二：协调者 + 执行者

一个 Heartbeat Worker 作为协调者，自动分配任务给其他 Worker：

```bash
# 协调者（每 5 分钟审视一次）
aiscan agent --loop --space pentest-001 \
  --heartbeat 5 \
  --ioa-node-name coordinator \
  -p "你是协调者。审视当前进度，给空闲的 scanner 和 recon 节点分配下一步任务。目标网段：10.0.0.0/24"

# 执行者们
aiscan agent --loop --space pentest-001 --ioa-node-name scanner -s scan
aiscan agent --loop --space pentest-001 --ioa-node-name recon -s spray -s neutron
```

### 模式三：One-shot Agent 接入 IOA

非 loop 模式的 Agent 也可以接入 IOA，用于临时参与协作或查看状态：

```bash
# 临时参与，执行完退出
aiscan agent --ioa-url http://127.0.0.1:8765 \
  -p "在 pentest-001 中查看当前进度，补充对 10.0.0.5 的深度扫描" \
  -i 10.0.0.5
```

---

## 使用指南

### 启动 IOA Server

```bash
# 默认 http://127.0.0.1:8765，数据库 ./ioa.db
aiscan ioa serve

# 自定义
aiscan ioa serve --ioa-url http://0.0.0.0:8765 --ioa-db /data/ioa.db
```

### 启动 Loop Worker

```bash
aiscan agent --loop [OPTIONS]
```

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `--ioa-url` | IOA Server 地址 | `http://127.0.0.1:8765` |
| `--space` | 加入的 Space 名 | `default` |
| `--ioa-node-name` | 节点名称 | 自动生成 |
| `--ioa-node-id` | 使用已有节点 ID | — |
| `--heartbeat` | Heartbeat 间隔（分钟），0 禁用 | `0` |
| `-p` | 节点意图描述 | — |
| `-s` | 加载的 skill | — |
| `--timeout` | 单次任务超时 | `3600` |

### 查询 IOA 状态

```bash
# 列出 Space
aiscan ioa spaces --ioa-url http://127.0.0.1:8765

# 列出 Space 中的消息
aiscan ioa messages pentest-001 --ioa-url http://127.0.0.1:8765

# 查看消息上下文（线程）
aiscan ioa context pentest-001 <message-id> --ioa-url http://127.0.0.1:8765

# 列出所有节点
aiscan ioa nodes --ioa-url http://127.0.0.1:8765

# 列出 Space 内的节点
aiscan ioa nodes pentest-001 --ioa-url http://127.0.0.1:8765

# JSON 输出
aiscan ioa spaces --ioa-url http://127.0.0.1:8765 --json
```

### 交互式 REPL 中的 IOA 命令

在 `aiscan agent` 交互模式下：

```
aiscan> /spaces
aiscan> /messages pentest-001
aiscan> /context pentest-001 msg-123
aiscan> /nodes pentest-001
```

---

## 配置参考

### CLI 参数

| 参数 | 说明 |
| --- | --- |
| `--ioa-url` | IOA Server URL |
| `--ioa-node-id` | 已有节点 ID |
| `--ioa-node-name` | 注册节点名 |
| `--ioa-db` | SQLite 数据库路径（仅 `ioa serve`） |
| `--space` | Space 名称 |
| `--json` | IOA 查询 JSON 输出 |
| `--loop` | 启用 Loop Worker 模式 |
| `--heartbeat` | Heartbeat 间隔（分钟） |

### 配置文件

```yaml
ioa:
  url: "http://127.0.0.1:8765"
  db: "./ioa.db"
  node_name: "my-scanner"
  space: "default"
```

### 环境变量

IOA 相关参数暂无独立环境变量，通过配置文件或 CLI 参数指定。
