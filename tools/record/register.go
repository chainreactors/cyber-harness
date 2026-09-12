//go:build full && record_ffmpeg && cgo && (windows || linux)

package record

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	coreconfig "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	coretool "github.com/chainreactors/aiscan/core/tool"
	toolregistry "github.com/chainreactors/aiscan/pkg/toolset/registry"
)

type Extension struct {
	mu         sync.Mutex
	registry   coretool.Registrar
	workDir    string
	tool       *Tool
	registered bool
	lease      coretool.Registration
	closed     bool
}

var _ extension.Extension = (*Extension)(nil)

func NewExtension(registry coretool.Registrar, workDir string) (*Extension, error) {
	if registry == nil || strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("record extension requires a tool registry and working directory")
	}
	return &Extension{registry: registry, workDir: workDir}, nil
}

func (m *Extension) Load(scope *extension.Context) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return toolregistry.ErrUnavailable
	}
	if m.registered {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	maxConcurrent, err := maxConcurrentFromEnvironment(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("record config: %w", err)
	}
	recorder := New(m.workDir, coreconfig.DataSubDir("record"), maxConcurrent, newPlatformBackend())
	lease, err := m.registry.Register(scope.Owner(), recorder)
	if err != nil {
		recorder.Close()
		return fmt.Errorf("register record tool: %w", err)
	}
	m.tool = recorder
	m.registered = true
	m.lease = lease
	if _, err := scope.Track(lease.Revoke); err != nil {
		return err
	}
	return nil
}

func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if m.registered {
		if err := m.lease.Close(ctx); err != nil {
			return errors.Join(extension.ErrCloseIncomplete, err)
		}
		m.registered = false
	}
	if m.tool != nil {
		m.tool.Close()
		m.tool = nil
	}
	m.closed = true
	return nil
}
