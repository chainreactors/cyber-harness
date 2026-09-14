//go:build full && record_ffmpeg && cgo && (windows || linux)

package record

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/chainreactors/aiscan/tools/record"
)

type Extension struct {
	mu         sync.Mutex
	workDir    string
	registry   *toolset.Registry
	tool       *record.Tool
	registered bool
	closed     bool
}

var _ extension.Extension = (*Extension)(nil)

func New(registry *toolset.Registry, workDir string) (*Extension, error) {
	if registry == nil || strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("record extension requires a working directory")
	}
	return &Extension{registry: registry, workDir: workDir}, nil
}

func (m *Extension) Load(scope *extension.Scope) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return toolset.ErrUnavailable
	}
	if m.registered {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	recorder, err := record.NewConfigured(m.workDir)
	if err != nil {
		return err
	}
	if err := m.registry.Register("record", recorder); err != nil {
		recorder.Close()
		return fmt.Errorf("register record tool: %w", err)
	}
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
	m.mu.Unlock()
	if value != nil {
		if err := value.CloseContext(ctx); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.tool = nil
	m.closed = true
	m.mu.Unlock()
	return nil
}
