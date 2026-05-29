# Quick Start

本文档帮助你在 5 分钟内完成安装并运行第一次扫描。

---

## 安装

从 [GitHub Releases](https://github.com/chainreactors/aiscan/releases/latest) 下载对应平台的二进制文件。

### Linux

```bash
# amd64
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_linux_amd64
chmod +x aiscan
sudo mv aiscan /usr/local/bin/aiscan
aiscan --version
```

```bash
# arm64
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_linux_arm64
chmod +x aiscan
sudo mv aiscan /usr/local/bin/aiscan
```

### macOS

```bash
# Apple Silicon
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_darwin_arm64
chmod +x aiscan
xattr -d com.apple.quarantine aiscan 2>/dev/null || true
sudo mv aiscan /usr/local/bin/aiscan
aiscan --version
```

```bash
# Intel
curl -L -o aiscan https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_darwin_amd64
chmod +x aiscan
sudo mv aiscan /usr/local/bin/aiscan
```

### Windows

```powershell
Invoke-WebRequest "https://github.com/chainreactors/aiscan/releases/latest/download/aiscan_windows_amd64.exe" -OutFile aiscan.exe
.\aiscan.exe --version
```

---

## 第一次扫描（无需 LLM）

`scan` 命令是最常用的入口，自动串联所有扫描阶段，不需要配置 LLM 即可运行。

```bash
aiscan scan -i <target>
```

扫描流程：

```
输入目标 → gogo 端口发现 → spray Web 探测/指纹 → zombie 弱口令 → neutron POC
```

示例：

```bash
# 扫描单个 IP
aiscan scan -i 192.168.1.1

# 扫描网段
aiscan scan -i 192.168.1.0/24

# 扫描 URL
aiscan scan -i http://target.example

# 从文件读取目标
aiscan scan -l targets.txt
```

### quick 和 full 模式

| 模式 | 说明 |
| --- | --- |
| `quick`（默认） | 端口扫描、Web 探测/指纹、常见插件探测、爬取（depth 1）、弱口令、POC |
| `full` | quick 基础上增加路径爆破和更深爬取（depth 2） |

```bash
# 默认 quick 模式
aiscan scan -i 192.168.1.0/24

# 完整模式
aiscan scan -i 192.168.1.0/24 --mode full
```

### 输出格式

```bash
# 终端（默认，实时流式）
aiscan scan -i 192.168.1.0/24

# JSON Lines（机器可读）
aiscan scan -i 192.168.1.0/24 -j

# Markdown 报告
aiscan scan -i 192.168.1.0/24 --report

# 写入文件
aiscan scan -i 192.168.1.0/24 -f result.txt
```

---

## 使用 AI Agent（需要 LLM）

Agent 模式让 LLM 自主选择工具、执行扫描、分析结果。需要先配置 LLM Provider。

### 配置 LLM

最快的方式是设置环境变量：

```bash
# OpenAI
export OPENAI_API_KEY="sk-..."

# 或 DeepSeek
export DEEPSEEK_API_KEY="..."

# 或统一环境变量
export AISCAN_API_KEY="..."
```

也可以通过 CLI 参数：

```bash
aiscan agent --api-key "sk-..." --model gpt-4o -p "检查目标" -i http://target.example
```

详细配置参考 [配置指南](configuration.md)。

### 运行 Agent

```bash
# 自然语言任务（one-shot）
aiscan agent -p "发现 Web 服务并检查高风险漏洞" -i 192.168.1.0/24

# 交互式 REPL
aiscan agent

# 仅提供目标（自动生成扫描任务）
aiscan agent -i http://target.example
```

### AI 增强扫描

给 scanner 加 `--ai` 让 LLM 分析结果：

```bash
aiscan --ai scan -i http://target.example
```

启用 AI 验证减少误报：

```bash
aiscan scan -i http://target.example --verify=high
```

---

## 直接使用扫描器

除了 `scan` 流水线，你也可以直接调用单个扫描器：

```bash
# 端口/服务发现
aiscan gogo -i 192.168.1.0/24 -p top100

# Web 探测和指纹
aiscan spray -u http://target.example --finger

# 弱口令检测
aiscan zombie -i ssh://127.0.0.1:22 --top 3

# POC 检测
aiscan neutron -u http://target.example -s critical,high
```

---

## 下一步

- [配置指南](configuration.md) — LLM Provider 详细配置、配置文件、Cyberhub 资源服务
- [使用指南](usage.md) — 完整命令参考、Agent 高级用法、IOA 协作模式
