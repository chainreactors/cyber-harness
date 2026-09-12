package files

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/core/eventbus"
)

// observation is private transport for the synchronous callback arguments.
// It is neither a persisted record nor a public operation DTO.
type observation struct {
	ctx   context.Context
	op    filepb.AccessOp
	path  string
	data  []byte
	size  int64
	err   error
	edits uint32
}

// Subscription uses the shared admission and drain implementation. The
// observer owns it and must Close it before releasing its other resources.
type Subscription = eventbus.Subscription[observation]

// Subscribe observes admitted operations, including failed operations, before
// their in-flight accounting is released. Rejected admission has no callback.
// The file service must be loaded first. Handlers run synchronously and may
// run concurrently; data is immutable and borrowed only until handler return.
// Read data includes bytes consumed even if the read ultimately failed. Write
// data contains the committed content, or nil on failure. Size is the file size
// observed during a read or committed by a write; zero means unknown otherwise.
// Callbacks must not wait for filesystem.Close or their own subscription to drain.
// Paths are lexical absolute locations for local requests, not symlink targets;
// invalid paths are reported verbatim along with the operation's error.
func (f *Files) Subscribe(handler func(context.Context, filepb.AccessOp, string, []byte, int64, error, uint32)) (*Subscription, error) {
	if handler == nil {
		return nil, fmt.Errorf("file observer is required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.root == nil || f.stopping {
		return nil, ErrUnavailable
	}
	return f.observations.Subscribe(func(o observation) {
		handler(o.ctx, o.op, o.path, o.data, o.size, o.err, o.edits)
	}), nil
}

func (f *Files) observe(ctx context.Context, op filepb.AccessOp, path string, data []byte, size int64, err error, edits uint32) {
	if !strings.Contains(path, "://") && filepath.IsLocal(path) {
		path = filepath.Join(f.config.Directory, path)
	}
	if f.config.Observe != nil {
		f.config.Observe(ctx, op, path, data, size, err, edits)
	}
	f.observations.Emit(observation{ctx: ctx, op: op, path: path, data: data, size: size, err: err, edits: edits})
}
