package files

import (
	"context"
	"errors"
	"fmt"
	filepb "github.com/chainreactors/cyber/aop/file"
	"github.com/chainreactors/cyber/core/operation"
	"io"
	"path/filepath"
)

// ReadRange bounds a binary transfer independently of the total artifact size.
func (f *Files) ReadRange(ctx context.Context, path string, offset int64, limit int) (data []byte, size int64, err error) {
	if offset < 0 || limit <= 0 || int64(limit) > f.config.MaxBytes {
		return nil, 0, fmt.Errorf("invalid bounded file range")
	}
	ctx, cancel := operation.Begin(ctx, "file", "read")
	defer cancel(nil)
	root, err := f.acquire(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer f.release()
	stop := context.AfterFunc(f.lifetime, func() { cancel(nil) })
	defer stop()
	if !filepath.IsLocal(path) {
		return nil, 0, fmt.Errorf("read requires a local path")
	}
	defer func() {
		err = errors.Join(err, f.observe(ctx, filepb.AccessOp_ACCESS_OP_READ, path, data, size, err, 0))
	}()
	file, err := root.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	size = info.Size()
	if !info.Mode().IsRegular() {
		return nil, size, fmt.Errorf("read requires a regular file")
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return nil, size, err
	}
	data, err = io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, int64(limit)))
	if err == nil {
		err = ctx.Err()
	}
	return
}
func (f *Files) Mkdir(ctx context.Context, path string) error {
	if f.ReadOnly() {
		return fmt.Errorf("file service is read-only")
	}
	root, err := f.acquire(ctx)
	if err != nil {
		return err
	}
	defer f.release()
	if !filepath.IsLocal(path) {
		return fmt.Errorf("mkdir requires a local path")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.MkdirAll(path, 0755)
}
