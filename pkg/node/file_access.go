package node

import (
	"context"
	"errors"

	filepb "github.com/chainreactors/cyber/aop/file"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

func observeControlAccess(registry *corehooks.Registry, ctx context.Context, op filepb.AccessOp, base, path string, value *fileResultValue) {
	if registry == nil || path == "" || value == nil {
		return
	}
	var data []byte
	var size int64
	if value.result != nil {
		data = value.result.GetData()
		size = value.result.GetSize()
	}
	event := toolhooks.FileEvent{
		Operation: operation.Correlation(ctx), Op: op, Source: filepb.AccessSource_ACCESS_SOURCE_CONTROL,
		Path: resolveFileRPCPath(base, path), Directory: base, Data: data, Size: size, Err: value.err,
	}
	if toolhooks.FileAccessControl.Has(registry) {
		response, hookErr := toolhooks.FileAccessControl.Emit(ctx, registry, event)
		if cause := toolhooks.CancellationCause(response, hookErr); cause != nil {
			operation.RequestCancel(ctx, cause)
			value.err = errors.Join(value.err, cause)
		}
	}
	if toolhooks.FileAccessObserved.Has(registry) {
		corehooks.Notify(ctx, registry, toolhooks.FileAccessObserved, event)
	}
}
