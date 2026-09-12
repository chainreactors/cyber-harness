package files

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Write to a sibling temporary file, then rename. Cancellation before the
// rename leaves the destination untouched. Cancellation racing with rename
// cannot undo a completed write. Filesystem syscalls are not preempted by ctx.
func write(ctx context.Context, root *os.Root, path string, text io.Reader) (created bool, err error) {
	mode := os.FileMode(0600)
	info, statErr := root.Lstat(path)
	if statErr == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("write destination must be a regular file")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, statErr
	}
	name := filepath.Join(filepath.Dir(path), ".harness-"+rand.Text()+".tmp")
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	defer func() {
		if f != nil {
			err = errors.Join(err, f.Close())
		}
		if removeErr := root.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if _, err := io.Copy(f, contextReader{ctx: ctx, reader: text}); err != nil {
		return false, err
	}
	if err := f.Chmod(mode); err != nil {
		return false, err
	}
	closeErr := f.Close()
	f = nil
	if closeErr != nil {
		return false, closeErr
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err = root.Rename(name, path)
	return errors.Is(statErr, os.ErrNotExist) && err == nil, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	// Bound the interval between cancellation checks during large copies.
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	return r.reader.Read(p)
}
