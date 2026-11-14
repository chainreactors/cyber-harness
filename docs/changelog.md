# Changelog

本页保留当前开发版本的用户可见变化。已发布版本的说明见 [GitHub Releases](https://github.com/chainreactors/cyber-harness/releases) 和对应 Git tag。

## 未发布

- 发布产物使用 `cyber-harness`（Web 主程序）、`cyber-scan`（扫描节点）和 `cyber-audit`（审计节点）；Release 压缩包、校验文件、CLI 帮助和快速连接命令使用同一名称，源码入口为 `cmd/cyber-scan`。
- 清理一次性验收报告、实验分析稿和过期发布说明；回归测试所需的合成事件 fixture 继续保留。
- 扫描、Agent 和回放统一使用 `-o <格式>`、`-f <文件>`；支持 `text`、`json`、`jsonl`、`markdown` 和 `csv`，省略文件时写入 stdout。
- 原历史记录命令 `-o run.jsonl` 改为 `-o jsonl -f run.jsonl`。移除回放专用格式参数，`stream-json`、`md` 分别保留为 `jsonl`、`markdown` 的格式别名。
- 原生 scanner 子命令之后的参数继续遵循其自身契约。
