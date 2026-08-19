package node

import (
	"context"

	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/pkg/commands"
	protobuf "google.golang.org/protobuf/proto"
)

func attachFileAccess(audit *commands.FileAudit, send func(string, protobuf.Message)) func() {
	if audit == nil {
		return nil
	}
	return audit.Subscribe(func(access *filepb.Access) {
		if access == nil {
			return
		}
		send(access.GetToolId(), &filepb.ProtocolMessage{
			Message: &filepb.ProtocolMessage_Access{Access: protobuf.CloneOf(access)},
		})
	})
}

func auditControlAccess(audit *commands.FileAudit, op filepb.AccessOp, base, path string, value fileResultValue) {
	if audit == nil || value.err != nil || path == "" {
		return
	}
	audit.RecordFile(context.Background(), op, resolveFileRPCPath(base, path), &filepb.Access{
		Source:  filepb.AccessSource_ACCESS_SOURCE_CONTROL,
		WorkDir: base,
		Bytes:   int64(len(value.result.GetData())),
	})
}
