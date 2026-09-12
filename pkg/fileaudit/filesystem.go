package fileaudit

import (
	"context"
	"fmt"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"

	filepb "github.com/chainreactors/aiscan/aop/file"
)

// NewWithFiles constructs an inert audit extension borrowing a file service.
// Load owns the observation subscription; Close revokes and drains it before
// draining publication. Profiles must load FS before Audit and close all file
// producers before Audit, then close FS. Removing Audit leaves FS usable.
// New remains available for explicit reporting and shell snapshot consumers.
func NewWithFiles(filesystem *fileext.Extension) (*Audit, error) {
	if filesystem == nil {
		return nil, fmt.Errorf("file audit requires a file service")
	}
	a := New()
	a.files = filesystem
	return a, nil
}

func (a *Audit) ObserveFile(ctx context.Context, op filepb.AccessOp, path string, data []byte, size int64, err error, edits uint32) {
	if !a.Enabled() {
		return
	}
	access := &filepb.Access{
		Op: op, Source: filepb.AccessSource_ACCESS_SOURCE_TOOL,
		Path: path, Bytes: int64(len(data)), Size: size, Edits: edits,
	}
	if err != nil {
		access.Error = err.Error()
	} else if op == filepb.AccessOp_ACCESS_OP_WRITE || op == filepb.AccessOp_ACCESS_OP_CREATE || op == filepb.AccessOp_ACCESS_OP_EDIT {
		// Hash borrowed committed bytes now, never re-read a possibly newer
		// file or keep its content in the publication queue.
		access.Digest = Digest(data)
	}
	a.Record(ctx, access)
}
