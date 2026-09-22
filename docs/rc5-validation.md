# rc5 issue 复测记录

日期：2026-09-22。基线为 master `540ee09a`，复测包含本次 rc5 修复及 cyber-ui `5e023b6`。

## Open issue 结论

| Issue | 复测证据 | 处理 |
| --- | --- | --- |
| #121 非多模态模型 400 后无法继续 | 以原始 `dsv4s is not a multimodal model` 响应通过本地 HTTP 服务复现；修复后流式和非流式均只拒绝一次图片请求，随后文字重试成功，下一轮对话成功 | 修复完成 |
| #143 工具错误及 Goal 评估后时间线不连贯 | 默认 Harness 挂载内置 Skill；真实工具执行验证 Skill 读取及 Shell 组合命令；1280px/520px 浏览器测试验证两轮 Goal、评估、实际压缩及刷新恢复，工具结果无错误 | 修复完成 |
| #145 思考缺失、重复内容及页面过长 | 混合 reasoning/content 帧、多个流式步骤、部分输出后重试和 WebSocket 重连测试；浏览器检查思考滚动区高度、正文不重复、运行状态与终态一致 | 修复完成 |
| #113 scan CSV 导出 | 本地发布二进制 `scan --help` 及参数解析仍无 CSV 输出入口；`scan --view-format csv` 返回 unknown flag。当前 JSONL/资产展示不等同于所请求的 CLI CSV 转换 | 保留 open |
| #124 ACP/Pi 集成 | `examples/acp` 仍为 Application WebSocket/ConnectRPC 示例；没有 Pi 扩展包或标准 ACP Agent 适配入口 | 保留 open |
| #126 定向 Inbox 通信 | memory/HTTP 定向路由、同 Node 投递、去重、歧义目标、关闭拒绝、sync/async/fork 及 Inbox 中断测试通过。真实父子/兄弟模型验收需要 `CYBER_HARNESS_LLM_*`，本次环境未配置；不将包级测试视为完整验收 | 保留 open |
| #131 领域注入契约 | Prompt、Inbox、Provider 回滚测试通过；`compactHistory` 仍固定，Expander 仍为 file/skill switch 且无 context，默认 Provider 装配仍调用固定 Initialize 路径 | 保留 open |

## 本地验证

环境：Windows amd64，Go 1.26.1，Chromium；模型错误和 Goal 展示使用确定性的本地 HTTP/SSE fixture，不依赖真实模型或外部扫描目标。

以下验证通过：

- `go test -count=1 -timeout 5m ./agent/... ./core/... ./pkg/... ./tools/files/... ./tools/terminal/... ./cmd/aiscan/...`
- `go test -count=1 ./agent ./agent/provider ./pkg/console`，包含新增 #121 HTTP 恢复回归。
- `go test -count=1 -tags "full sqlite" -timeout 5m ./cmd/aiscan ./pkg/web/api ./pkg/web/service ./agent/skills ./tools/passive ./tools/scan/engine`
- `golangci-lint run --timeout=8m`：0 issues；`go mod tidy -diff`；`goreleaser check`；`git diff --check`。
- cyber-ui `pnpm --filter @cyber/viewer test`：8 项通过。
- 前端 `npm run test:e2e`：18 项通过，1 项真实模型 Goal 测试按条件跳过；启动过程包含 TypeScript/Vite 构建和 full Go 二进制构建。
- 按 `editions.env` 构建 Windows standard/full，`--version` 均为 `aiscan v1.0.0-rc5`，验证 `scan --help` 和 full `web --help`。
- WSL Ubuntu / Go 1.26.0：`go vet ./...`、根模块/AOP tidy 及 AOP race 测试通过；tmux 多轮交互用例改为用单引号向内层 Shell 发送变量，`go test -race -count=3 ./agent -run '^TestAgentTmuxMultiRoundInteraction$'` 通过。

发布仍要求 release commit 的 GitHub CI 成功，包括 Linux race、Windows、scanner、浏览器及发布包验证；本地 fixture 结果不代表任意模型都不会自行生成重复内容。
