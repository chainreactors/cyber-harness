// Package files owns bounded filesystem operations under one directory.
// Construction is inert. Resource owns the root lifetime; Files exposes only
// filesystem behavior.
package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	filepb "github.com/chainreactors/cyber/aop/file"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
)

var ErrUnavailable = errors.New("file service is not active")

// slashPath accepts an OS path or a slash path and returns one root-relative
// slash path. path.Match treats '\' as an escape on every OS, so the files
// namespace stays slash-separated. Clean drops "." and duplicate separators first.
func slashPath(path string) (string, error) {
	if !filepath.IsLocal(path) {
		return "", fmt.Errorf("path is not local")
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if !filepath.IsLocal(filepath.FromSlash(cleaned)) {
		return "", fmt.Errorf("path is not local")
	}
	return cleaned, nil
}

type Config struct {
	Directory string
	// MaxBytes bounds each read and write; zero means 1 MiB.
	MaxBytes int64
	ReadOnly bool
	// Mounts maps virtual URI prefixes to immutable read-only filesystems.
	Mounts map[string]fs.FS
}

// Files exposes bounded filesystem behavior. Its Config is immutable after
// construction. Lifecycle is intentionally absent; Resource is the only owner
// that can open or close the underlying root.
type Files struct {
	config    Config
	mu        sync.Mutex
	mutation  sync.Mutex
	root      *os.Root
	attempted bool
	stopping  bool
	active    int
	done      chan struct{}
	lifetime  context.Context
	cancel    context.CancelFunc
	hooks     *corehooks.Registry
	mounts    map[string]*mount
}

// Resource is the construction result held by the files extension. Its named
// Files field prevents lifecycle and business methods from sharing a method set.
type Resource struct {
	Files *Files
}

func New(config Config, registry *corehooks.Registry) (*Resource, error) {
	if !filepath.IsAbs(config.Directory) {
		return nil, fmt.Errorf("file service requires an absolute directory")
	}
	if config.MaxBytes < 0 || config.MaxBytes == math.MaxInt64 {
		return nil, fmt.Errorf("invalid file size limit")
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = 1 << 20
	}
	mounts := make(map[string]fs.FS, len(config.Mounts))
	for prefix, source := range config.Mounts {
		mounts[prefix] = source
	}
	config.Mounts = mounts
	return &Resource{Files: &Files{config: config, hooks: registry, done: make(chan struct{})}}, nil
}

func (r *Resource) Open(ctx context.Context) error {
	if r == nil || r.Files == nil {
		return ErrUnavailable
	}
	f := r.Files
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopping || f.attempted && f.root == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.root != nil {
		return nil
	}
	f.attempted = true
	f.mounts = make(map[string]*mount, len(f.config.Mounts))
	for prefix, source := range f.config.Mounts {
		m, err := newMount(prefix, source)
		if err != nil {
			return err
		}
		f.mounts[prefix] = m
	}
	root, err := os.OpenRoot(f.config.Directory)
	if err != nil {
		return err
	}
	f.root = root
	f.lifetime, f.cancel = context.WithCancel(context.Background())
	if err := ctx.Err(); err != nil {
		f.stopping = true
		f.cancel()
		close(f.done)
		return err
	}
	return nil
}

func (f *Files) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.root != nil && !f.stopping
}

func (f *Files) ReadOnly() bool { return f.config.ReadOnly }

func (f *Files) acquire(ctx context.Context) (*os.Root, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.root == nil || f.stopping {
		return nil, ErrUnavailable
	}
	f.active++
	return f.root, nil
}

func (f *Files) release() {
	f.mu.Lock()
	f.active--
	if f.stopping && f.active == 0 {
		close(f.done)
	}
	f.mu.Unlock()
}

// Read returns owned bytes from a regular file. Filesystem policy lives here;
// text encoding and presentation belong to the consumer.
func (f *Files) Read(ctx context.Context, path string) (result []byte, err error) {
	ctx, cancel := operation.Begin(ctx, "file", "read")
	defer cancel(nil)
	return f.read(ctx, path, true)
}

func (f *Files) read(ctx context.Context, path string, observed bool) (result []byte, err error) {
	root, err := f.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer f.release()
	var data []byte
	var size int64
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(f.lifetime, cancel)
	defer stop()
	if f.lifetime.Err() != nil {
		cancel()
	}
	if strings.Contains(path, "://") {
		result, err := f.readMount(ctx, path)
		if err == nil {
			err = f.lifetime.Err()
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	defer func() {
		if observed {
			err = errors.Join(err, f.observe(ctx, filepb.AccessOp_ACCESS_OP_READ, path, data, size, err, 0))
		}
	}()
	if f.lifetime.Err() != nil {
		cancel()
	}
	local, nerr := slashPath(path)
	if nerr != nil {
		return nil, fmt.Errorf("read requires a local path")
	}
	path = local
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size = info.Size()
	if !info.Mode().IsRegular() || info.Size() > f.config.MaxBytes {
		return nil, fmt.Errorf("read requires a regular file within %d bytes", f.config.MaxBytes)
	}
	data, err = io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, f.config.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(data)) > f.config.MaxBytes {
		return nil, fmt.Errorf("read exceeds %d bytes", f.config.MaxBytes)
	}
	return data, nil
}

// Write reads data only until return. It preserves the destination on errors
// before rename. Context cancellation cannot preempt a filesystem syscall or
// undo a successful rename. Concurrent writes use last successful rename.
func (f *Files) Write(ctx context.Context, path string, data []byte) (err error) {
	ctx, cancel := operation.Begin(ctx, "file", "write")
	defer cancel(nil)
	f.mutation.Lock()
	defer f.mutation.Unlock()
	return f.writeBytes(ctx, path, data, filepb.AccessOp_ACCESS_OP_WRITE, 0)
}

func (f *Files) writeBytes(ctx context.Context, path string, data []byte, op filepb.AccessOp, edits uint32) (err error) {
	root, err := f.acquire(ctx)
	if err != nil {
		return err
	}
	defer f.release()
	created := false
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(f.lifetime, cancel)
	defer stop()
	defer func() {
		// Temporary writes are implementation details. Only a successful
		// commit reports destination bytes and exposes the committed content.
		if err != nil {
			err = errors.Join(err, f.observe(ctx, op, path, nil, 0, err, edits))
		} else {
			if created && op == filepb.AccessOp_ACCESS_OP_WRITE {
				op = filepb.AccessOp_ACCESS_OP_CREATE
			}
			err = errors.Join(err, f.observe(ctx, op, path, data, int64(len(data)), nil, edits))
		}
	}()
	if f.lifetime.Err() != nil {
		cancel()
	}
	if f.config.ReadOnly {
		return &os.PathError{Op: "write", Path: path, Err: os.ErrPermission}
	}
	local, nerr := slashPath(path)
	if strings.Contains(path, "://") || nerr != nil || int64(len(data)) > f.config.MaxBytes {
		return fmt.Errorf("write requires a local path within %d bytes", f.config.MaxBytes)
	}
	path = local
	if err := ctx.Err(); err != nil {
		return err
	}
	created, err = write(ctx, root, path, bytes.NewReader(data))
	return err
}

// Close retains the root on timeout. A later Close with a fresh context waits
// for the same operations and releases the same resource. Closed filesystem instances
// cannot be loaded again.
func (r *Resource) Close(ctx context.Context) error {
	if r == nil || r.Files == nil {
		return nil
	}
	f := r.Files
	f.mu.Lock()
	if !f.stopping {
		f.stopping = true
		if f.cancel != nil {
			f.cancel()
		}
		if f.active == 0 {
			close(f.done)
		}
	}
	f.mu.Unlock()
	select {
	case <-f.done:
	default:
		select {
		case <-f.done:
		case <-ctx.Done():
			return errors.Join(ctx.Err())
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.root != nil {
		root := f.root
		f.root = nil
		return root.Close()
	}
	return nil
}
