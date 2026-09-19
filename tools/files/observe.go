package files

import (
	"context"
	"path/filepath"
	"strings"

	filepb "github.com/chainreactors/cyber/aop/file"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

// observe emits at the actual file-operation boundary, including failures.
// Data is valid only for the synchronous dispatch. With no installed hook,
// no digest, serialization or copy is performed.
func (f *Files) observe(ctx context.Context, op filepb.AccessOp, path string, data []byte, size int64, operationErr error, edits uint32) error {
	if !strings.Contains(path, "://") && filepath.IsLocal(path) {
		path = filepath.Join(f.config.Directory, path)
	}
	event := toolhooks.FileEvent{
		Operation: operation.Correlation(ctx),
		Op:        op,
		Source:    filepb.AccessSource_ACCESS_SOURCE_TOOL,
		Path:      path,
		Directory: operation.WorkDirFromContext(ctx, f.config.Directory),
		Data:      data,
		Size:      size,
		Edits:     edits,
		Err:       operationErr,
	}
	var cancellation error
	if toolhooks.FileAccessControl.Has(f.hooks) {
		response, hookErr := toolhooks.FileAccessControl.Emit(ctx, f.hooks, event)
		cancellation = toolhooks.CancellationCause(response, hookErr)
		if cancellation != nil {
			operation.RequestCancel(ctx, cancellation)
		}
	}
	if toolhooks.FileAccessObserved.Has(f.hooks) {
		corehooks.Notify(ctx, f.hooks, toolhooks.FileAccessObserved, event)
	}
	return cancellation
}
