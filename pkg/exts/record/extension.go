//go:build record && cgo && (windows || linux)

package record

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"

	"github.com/chainreactors/cyber/tools/record"
)

type Extension struct {
	mu         sync.Mutex
	workDir    string
	directory  string
	maximum    int
	backend    *nativeBackend
	tool       *record.Tool
	registered bool
	closed     bool
}

var _ extension.Extension = (*Extension)(nil)

func New(workDir, directory string, maximum int) (*Extension, error) {
	if strings.TrimSpace(workDir) == "" || !filepath.IsAbs(directory) || maximum < 1 || maximum > record.MaxConcurrentLimit {
		return nil, fmt.Errorf("record extension requires directories and maximum between 1 and 16")
	}
	return &Extension{workDir: workDir, directory: directory, maximum: maximum}, nil
}

func (m *Extension) Load(scope *extension.Scope) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return coretool.ErrUnavailable
	}
	if m.registered {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backend, err := newNativeBackend()
	if err != nil {
		return err
	}
	recorder := record.New(m.workDir, m.directory, m.maximum, backend)
	if err := extension.Add[coretool.Tool](scope, recorder); err != nil {
		recorder.Close()
		_ = backend.Close()
		return fmt.Errorf("register record tool: %w", err)
	}
	m.backend = backend
	m.tool = recorder
	m.registered = true
	return nil
}

func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	value := m.tool
	backend := m.backend
	m.mu.Unlock()
	if value != nil {
		if err := value.CloseContext(ctx); err != nil {
			return err
		}
	}
	if backend != nil {
		if err := backend.Close(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.tool = nil
	m.backend = nil
	m.closed = true
	m.mu.Unlock()
	return nil
}
