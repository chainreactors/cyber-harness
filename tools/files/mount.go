package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

type mount struct {
	source fs.FS
	active int
	closed bool
	done   chan struct{}
}

// Mount installs one immutable read-only virtual filesystem under a URI prefix.
// The mounting extension owns the source and must Unmount before closing it.
func (f *Files) Mount(prefix string, source fs.FS) error {
	m, err := newMount(prefix, source)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.root == nil || f.stopping {
		return ErrUnavailable
	}
	if f.mounts == nil {
		f.mounts = make(map[string]*mount)
	}
	if _, exists := f.mounts[prefix]; exists {
		return fmt.Errorf("mount prefix already reserved: %s", prefix)
	}
	f.mounts[prefix] = m
	return nil
}

func newMount(prefix string, source fs.FS) (*mount, error) {
	scheme, suffix, found := strings.Cut(prefix, "://")
	validScheme := scheme != ""
	for i, c := range scheme {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || !(c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
			validScheme = false
		}
	}
	if source == nil || !found || !validScheme || (suffix != "" && (!strings.HasSuffix(suffix, "/") || !fs.ValidPath(strings.TrimSuffix(suffix, "/")))) {
		return nil, fmt.Errorf("mount requires a URI prefix and filesystem")
	}
	return &mount{source: source, done: make(chan struct{})}, nil
}

func (f *Files) Unmount(ctx context.Context, prefix string) error {
	f.mu.Lock()
	m := f.mounts[prefix]
	if m == nil {
		f.mu.Unlock()
		return fmt.Errorf("unknown mount: %s", prefix)
	}
	if !m.closed {
		m.closed = true
		if m.active == 0 {
			close(m.done)
		}
	}
	f.mu.Unlock()
	select {
	case <-m.done:
	default:
		select {
		case <-m.done:
		case <-ctx.Done():
			return errors.Join(ctx.Err())
		}
	}
	f.mu.Lock()
	m.source = nil
	f.mu.Unlock()
	return nil
}

func (f *Files) readMount(ctx context.Context, location string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme, name, ok := strings.Cut(location, "://")
	if !ok || !fs.ValidPath(name) {
		return nil, fmt.Errorf("invalid virtual path")
	}
	f.mu.Lock()
	// Most specific prefix wins, allowing an embedded tree to be mounted at
	// cyber://skills/ without wrapping every fs.File just to add a directory.
	var m *mount
	matched := ""
	for prefix, candidate := range f.mounts {
		if strings.HasPrefix(location, prefix) && len(prefix) > len(matched) {
			matched, m = prefix, candidate
		}
	}
	if m == nil || m.closed {
		f.mu.Unlock()
		return nil, fmt.Errorf("virtual filesystem is not mounted: %s", scheme)
	}
	name = strings.TrimPrefix(location, matched)
	if !fs.ValidPath(name) {
		f.mu.Unlock()
		return nil, fmt.Errorf("invalid virtual path")
	}
	m.active++
	source := m.source
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		m.active--
		if m.closed && m.active == 0 {
			close(m.done)
		}
		f.mu.Unlock()
	}()
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > f.config.MaxBytes {
		return nil, fmt.Errorf("virtual read exceeds file policy")
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, f.config.MaxBytes+1))
	if err == nil {
		err = ctx.Err()
	}
	if int64(len(data)) > f.config.MaxBytes {
		return nil, fmt.Errorf("virtual read exceeds file limit")
	}
	return data, err
}
