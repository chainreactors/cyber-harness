//go:build full && record_ffmpeg && cgo && (windows || linux)

package record

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/tools/record"
)

type Extension struct {
	mu         sync.Mutex
	workDir    string
	tool       *record.Tool
	registered bool
	closed     bool
}

var _ extension.Extension = (*Extension)(nil)

func New(workDir string) (*Extension, error) {
	if strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("record extension requires a working directory")
	}
	return &Extension{workDir: workDir}, nil
}

func (m *Extension) Load(scope *extension.Context) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return extension.ErrToolsUnavailable
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
	if err := scope.RegisterTools(recorder); err != nil {
		recorder.Close()
		return fmt.Errorf("register record tool: %w", err)
	}
	m.tool = recorder
	m.registered = true
	return nil
}

func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if m.tool != nil {
		m.tool.Close()
		m.tool = nil
	}
	m.closed = true
	return nil
}
