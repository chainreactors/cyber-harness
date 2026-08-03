# Scan 模式详解

`scan` 是 aiscan 最常用的入口命令。它将多个扫描引擎编排为一条事件驱动的流水线，自动完成从端口发现到漏洞检测的全流程，无需 LLM 即可运行。本文档详细说明其内部流程、参数、AI 增强能力和输出格式。

---

## 目录

- [扫描流程](#扫描流程)
- [扫描模式](#扫描模式)
- [scan 参数参考](#scan-参数参考)
- [AI 增强扫描](#ai-增强扫描)
- [输出格式](#输出格式)
- [示例](#示例)

---

## 扫描流程

scan 采用 capability 驱动的事件流水线架构。各扫描阶段并非简单的顺序调用，而是通过事件队列连接——上游产生的发现自动触发下游对应的处理逻辑。

```
输入目标
  │
  ▼
gogo 端口发现          ← 主机存活 + 端口 + 服务 + banner + 指纹
  │
  ├─ 发现 HTTP 服务 ──▶ spray Web 探测/指纹/插件/爬取
  ├─ 发现可认证服务 ──▶ zombie 弱口令检测
  │
  ▼
spray 识别指纹 ────────▶ neutron 按指纹选择 POC 模板
  │
  ▼
可选：AI 验证 / sniper 漏洞搜索 / deep 动态测试
  │
  ▼
输出结果（流式终端 / JSON Lines / Markdown 报告）
```

### 事件驱动机制

各阶段之间的关键衔接逻辑：

| 上游事件 | 下游动作 |
| --- | --- |
| gogo 发现 HTTP 端口 | 目标进入 spray 进行 Web 探测和指纹识别 |
| gogo 发现 SSH/FTP/MySQL 等可认证服务 | 目标进入 zombie 进行弱口令检测 |
| spray 识别到 Web 指纹（如 nginx、tomcat） | neutron 根据指纹自动筛选并执行匹配的 POC 模板 |
| neutron/spray 产生漏洞发现 | 若启用 `--verify`，触发 AI 验证 |
| spray 识别到指纹 | 若启用 `--sniper`，触发公开 CVE/Exploit 搜索 |
| spray 发现 Web 资产 | 若启用 `--deep`，触发 AI 动态测试 |

这种事件驱动设计意味着 scan 的行为会随目标的实际情况自适应——只有发现了 HTTP 服务才会做 Web 探测，只有识别到指纹才会做对应 POC 检测。

---

## 扫描模式

scan 提供 `quick` 和 `full` 两种预设模式，通过 `--mode` 参数选择。

### quick 模式（默认）

面向快速暴露面发现，覆盖主要风险点，适合日常巡检和初步评估。

| Capability | 说明 |
| --- | --- |
| `gogo_portscan` | 端口扫描，默认 ports=all |
| `spray_check` | Web 基础探测和 HTTP 指纹识别 |
| `core_web` | Web 结果关联分析 |
| `spray_plugins` | 合并执行 common、bak、active 插件探测 |
| `spray_crawl` | 网页爬取（depth 2） |
| `zombie_weakpass` | 弱口令检测 |
| `neutron_poc` | 基于指纹的 POC 检测 |

### full 模式

在 quick 基础上增加路径爆破和更深层的探测，适合需要更全面覆盖的场景。

| Capability | 说明 |
| --- | --- |
| （包含 quick 的全部 capability） | |
| `spray_brute` | 默认字典路径爆破 |

### 对比

| 维度 | quick | full |
| --- | --- | --- |
| 端口范围 | all | all（`-` 全端口） |
| spray 插件 | common, bak, active | common, bak, active |
| 路径爆破 | 不执行 | 默认字典 |
| 爬取深度 | depth 2 | depth 2 |
| 弱口令 | 执行 | 执行 |
| POC | 基于指纹 | 基于指纹 |
| 耗时 | 较快 | 较长（增加路径爆破） |

---

## scan 参数参考

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `-i, --input` | 目标（IP/IP:port/CIDR/URL），可重复 | |
| `-l, --list` | 从文件读取目标，每行一个 | |
| `--mode` | 扫描模式：`quick` 或 `full` | `quick` |
| `--thread` | 总并发预算，自动按比例分配给各引擎 | `1000` |
| `--timeout` | 每个探测的超时秒数 | `5` |
| `--ports` | gogo 端口集合（`top100`/`all`/`-`/自定义） | quick: `all` |
| `--dict` | spray 字典文件，可重复 | |
| `--rule` | spray 变形规则文件，可重复 | |
| `--word` | spray 词汇生成 DSL 表达式 | |
| `--default-dict` | 使用 spray 内置默认字典 | |
| `--advance` | 启用 spray advance 插件 | |
| `--user` | 弱口令用户名，可重复 | |
| `--pwd` | 弱口令密码，可重复 | |
| `--zombie-top` | 使用 top N 默认弱口令组合 | |
| `--max-neutron-per-finger` | 每个指纹最大 neutron 模板数 | `20` |
| `--broad-poc` | 无指纹匹配时也运行 POC 模板 | |
| `--verify` | AI 验证模式（详见 [AI 增强扫描](#ai-增强扫描)） | `auto` |
| `--sniper` | 对发现的指纹搜索公开 CVE/Exploit | |
| `--deep` | 对发现的 Web 资产进行 AI 动态测试 | |
| `-j, --json` | JSON Lines 输出 | |
| `--report` | Markdown 报告输出 | |
| `-f, --file` | 将 scan、agent 与结构化 tool artifact 事件流式追加为 AOP ProtoJSONL | |
| `-F, --view` | 按需格式化同一份 AOP ProtoJSONL（`-o terminal\|markdown`，可用 `-f` 指定展示输出文件） | |
| `--trace` | 显示内部 pipeline 事件流（调试用） | |
| `--no-color` | 禁用终端颜色 | |
| `--debug` | 启用 trace + 底层扫描器 debug 日志 | |

### 并发分配策略

`--thread` 设置的是总预算上限，各引擎按比例获得自己的并发额度：

| 引擎 | 分配比例 | 默认并发 |
| --- | --- | --- |
| gogo | 80% | 500/次 |
| spray | 10% | 20/次 |
| zombie | 10% | 100/次 |
| neutron | 10% | - |

---

## AI 增强扫描

scan 提供三种 AI 增强能力，均需要配置 LLM Provider（参考 [参考手册](reference.md)）。这些能力可以单独使用，也可以组合使用。

### --verify：AI 验证

对扫描发现的漏洞和风险进行 LLM 主动验证，减少误报。验证级别控制哪些优先级的发现需要被验证。

| 值 | 说明 |
| --- | --- |
| `off` | 关闭验证（CLI 显式传 `--verify` 时的默认值） |
| `low` | 验证所有优先级的发现 |
| `medium` | 验证 medium 及以上优先级 |
| `high` | 验证 high 及以上优先级 |
| `critical` | 仅验证 critical 优先级 |
| `auto` | 编译时默认值；等效于 `high`，但 LLM 不可用时自动跳过 |

**auto 行为说明：**

当命令行未显式传入 `--verify` 时，aiscan 使用编译时固化的 `auto` 策略。`auto` 等效于 `high` 级别验证，但如果 LLM Provider 未配置或不可用，验证阶段会被静默跳过，不影响扫描主体流程的正常运行。这意味着即使没有 LLM，scan 依然可以完整运行。

### --sniper：漏洞情报搜索

对扫描过程中识别到的指纹（如 nginx、tomcat、spring 等），通过 web search 搜索已知的公开 CVE 和 Exploit 信息。搜索结果会作为补充情报输出，帮助评估目标的潜在风险。

### --deep：AI 动态测试

对发现的 Web 资产和指纹进行更深层的 AI 驱动动态测试。与 `--verify`（验证已有发现）不同，`--deep` 会主动尝试发现新的安全问题。

### 组合使用

三种 AI 能力可以自由组合。更多组合示例见 [示例](#示例) 一节。

---

## 输出格式

### 终端流式输出（默认）

默认输出为带颜色的结构化文本，边扫描边实时输出。每行是一个事件，前缀 `[capability.子类型]` 标识来源，指纹统一为 `[name]` 短格式：

```
[gogo_portscan.web] http://192.168.1.10:80 200 http "Welcome" [nginx]
[gogo_portscan.web] http://192.168.1.10:8080 200 http "Tomcat" [tomcat]
[spray_plugins.word] http://192.168.1.10/admin 200 532 41ms "Admin Panel" [nginx]
[zombie_weakpass] ssh://192.168.1.10:22 admin:admin123
[neutron_poc] http://192.168.1.10:8080 [CVE-2021-42013] critical
[scan.summary] completed inputs 1 services 3 web 2 probes 15 fingerprints 2 weakpass 1 vulns 1 ...
```

### JSON Lines（-j）

每行一个 JSON 对象，适合管道处理和程序化分析。使用 `-j` 时关闭流式输出，等待扫描完成后一次性输出。

### Markdown 报告（--report）

生成结构化的 Markdown 报告，包含扫描摘要、发现列表和风险评估。同样等待扫描完成后一次性输出。

### 文件输出（-f）

将输出写入文件，自动去除 ANSI 颜色转义符。

### 回放扫描记录（-F/--view）

使用 `-f` 保存的 JSONL 扫描记录可以通过 `-F` 回放：

```bash
aiscan scan -i 192.168.1.0/24 -f scan_result.jsonl   # 保存
aiscan -F scan_result.jsonl                            # 终端回放
aiscan -F scan_result.jsonl -o markdown                # 转 Markdown
aiscan -F scan_result.jsonl -o markdown -f report.md   # 输出到文件
```

---

## 示例

以下示例展示 scan 的进阶用法。基础用法参见 [README](../README.md)。

```bash
# 自定义端口范围
aiscan scan -i 10.0.0.0/24 --ports top100
aiscan scan -i 10.0.0.0/24 --ports 80,443,8080,8443,9090
aiscan scan -i 10.0.0.10 --ports -

# 自定义字典和规则
aiscan scan -i http://target.example --dict /path/to/wordlist.txt
aiscan scan -i http://target.example --dict paths.txt --dict backup.txt --rule rules.txt
aiscan scan -i http://target.example --default-dict

# 自定义弱口令
aiscan scan -i 10.0.0.0/24 --user admin --user root --pwd password --pwd admin123
aiscan scan -i 10.0.0.0/24 --zombie-top 10

# 无指纹时也运行 POC / 增加 POC 上限
aiscan scan -i http://target.example --broad-poc
aiscan scan -i http://target.example --max-neutron-per-finger 50

# 输出与回放
aiscan scan -i 10.0.0.0/24 -j
aiscan scan -i 10.0.0.0/24 -f result.jsonl
aiscan -F result.jsonl
aiscan -F result.jsonl -o markdown -f report.md

# 并发与超时
aiscan scan -i 10.0.0.0/16 --thread 200
aiscan scan -i 10.0.0.0/24 --timeout 10

# 调试
aiscan scan -i 192.168.1.1 --trace
aiscan scan -i 192.168.1.1 --debug
aiscan scan -i 192.168.1.0/24 --no-color -f scan.log

# AI 增强组合
aiscan scan -i http://target.example --mode full --verify=high --sniper --deep --report
aiscan scan -i http://target.example --verify=critical
aiscan scan -i http://target.example --verify=off
```
