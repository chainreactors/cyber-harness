# 生成式 API 参考（protoc-gen-doc）

本目录文档由 `protoc-gen-doc` 从 proto 源**自动生成**，是字段级的唯一权威参考：

| 文档 | 来源 | 内容 |
|------|------|------|
| [aop.md](aop.md) | `web/frontend/cyber-ui/packages/aop/proto/aop/**` | AOP 实时平面：Envelope、Session/Turn、Event、Tool/File/Exec/PTY/SCO 全部 message 与 enum |
| [rpc.md](rpc.md) | `proto/rpc/*.proto` + `proto/types/*.proto` | 管理平面：SessionService / ScanService / AgentService / ConfigService / SCOService / SystemService 的方法与请求响应 |

接入教程（chat 输入/输出）见 [../api.md](../api.md)；概念与拓扑见 [../integration.md](../integration.md)。

## 重新生成

```bash
# 安装一次
go install github.com/pseudomuto/protoc-gen-doc/cmd/protoc-gen-doc@latest

# AOP
protoc -I web/frontend/cyber-ui/packages/aop/proto \
  --doc_out=docs/api --doc_opt=markdown,aop.md \
  web/frontend/cyber-ui/packages/aop/proto/aop/*.proto \
  web/frontend/cyber-ui/packages/aop/proto/aop/*/*.proto

# 管理平面
protoc -I proto -I web/frontend/cyber-ui/packages/aop/proto \
  --doc_out=docs/api --doc_opt=markdown,rpc.md \
  proto/rpc/*.proto proto/types/*.proto
```

proto 变更后请重新生成并随 PR 一起提交，保持文档与 schema 一致。
