package files

import (
	"context"
	"path/filepath"
	"strings"

	filepb "github.com/chainreactors/aiscan/aop/file"
	corehooks "github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/operation"
	toolhooks "github.com/chainreactors/aiscan/core/tool/hooks"
)

// observe emits at the actual file-operation boundary, including failures.
// Data remains borrowed for the synchronous dispatch. With no installed hook,
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
	if f.hooks.Has(toolhooks.FileAccessControl.Kind) {
		response, hookErr := toolhooks.FileAccessControl.Emit(ctx, f.hooks, event)
		cancellation = toolhooks.CancellationCause(response, hookErr)
		if cancellation != nil {
			operation.RequestCancel(ctx, cancellation)
		}
	}
	if f.hooks.Has(toolhooks.FileAccessObserved.Kind) {
		corehooks.Notify(ctx, f.hooks, toolhooks.FileAccessObserved, event)
	}
	return cancellation
}
