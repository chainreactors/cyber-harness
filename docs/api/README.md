# 生成式 API 参考（protoc-gen-doc）

字段级参考以 proto 源码为准。需要逐字段文档时，用下面的命令从 proto 源生成 `aop.md` 和 `rpc.md`：

| 文档 | 来源 | 内容 |
|------|------|------|
| `aop.md` | `web/frontend/cyber-ui/packages/aop/proto/aop/**` | AOP 实时平面：Envelope、Session/Turn、Event、Tool/File/Exec/PTY/SCO 全部 message 与 enum |
| `rpc.md` | `proto/rpc/*.proto` + `proto/types/*.proto` | 管理平面：SessionService / ScanService / AgentService / ConfigService / SCOService / SystemService 的方法与请求响应 |

接入教程（chat 输入/输出）见 [../api.md](../api.md)；概念与拓扑见 [../integration.md](../integration.md)。

## 生成

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

proto 变更后按需重新生成；生成的 `aop.md`、`rpc.md` 不入库。
