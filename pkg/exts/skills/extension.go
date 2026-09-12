// Package skillmount owns an explicitly selected, read-only skills directory.
package skills

import (
	"context"
	"fmt"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	files "github.com/chainreactors/aiscan/tools/files"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	coreextension "github.com/chainreactors/aiscan/core/extension"
)

type Extension struct {
	mu              sync.Mutex
	files           *fileext.Extension
	directory       string
	root            *os.Root
	mounted, closed bool
	attempted       bool
	names           []string
}

func New(filesystem *fileext.Extension, directory string) (*Extension, error) {
	if filesystem == nil || !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("skills require a file service and absolute directory")
	}
	return &Extension{files: filesystem, directory: directory}, nil
}
func (m *Extension) Load(scope *coreextension.Context) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.attempted && !m.mounted {
		return files.ErrUnavailable
	}
	if m.mounted {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.attempted = true
	root, err := os.OpenRoot(m.directory)
	if err != nil {
		return err
	}
	m.root = root
	var names []string
	remaining := 10000
	var discover func(string) error
	discover = func(name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		directory, err := root.Open(filepath.FromSlash(name))
		if err != nil {
			return err
		}
		defer directory.Close()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, err := directory.ReadDir(128)
			if err != nil && err != io.EOF {
				return err
			}
			remaining -= len(entries)
			if remaining < 0 {
				return fmt.Errorf("skills directory exceeds entry limit")
			}
			for _, entry := range entries {
				location := path.Join(name, entry.Name())
				if entry.IsDir() {
					if err := discover(location); err != nil {
						return err
					}
				} else if entry.Type().IsRegular() && strings.EqualFold(entry.Name(), "SKILL.md") {
					names = append(names, "skill://"+location)
				}
			}
			if err == io.EOF {
				return nil
			}
		}
	}
	err = discover(".")
	if err != nil {
		return err
	}
	if err = m.files.Mount("skill://", root.FS()); err != nil {
		return err
	}
	m.mounted = true
	slices.Sort(names)
	m.names = names
	return nil
}
func (m *Extension) Locations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.names...)
}
func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.names = nil
	if m.mounted {
		if err := m.files.Unmount(ctx, "skill://"); err != nil {
			return err
		}
		m.mounted = false
	}
	if m.root != nil {
		root := m.root
		m.root = nil
		return root.Close()
	}
	return nil
}
