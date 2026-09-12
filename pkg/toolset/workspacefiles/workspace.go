package workspacefiles

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

type WorkspaceSource interface {
	VirtualFileReader
	VirtualGlobber
}

// Workspace owns the complete host-workspace tool registration used by the
// AIScan product. Its path behavior intentionally includes absolute paths and
// per-invocation working directories; the bounded files.FS instance is used by
// isolated runner profiles instead.
type Workspace struct {
	mu         sync.Mutex
	registry   tool.Registrar
	owner      string
	tools      []tool.Tool
	registered bool
	closed     bool
}

var _ extension.Extension = (*Workspace)(nil)

func NewWorkspace(registry tool.Registrar, owner, workDir string, source WorkspaceSource, audit *fileaudit.Audit, includeList bool) (*Workspace, error) {
	if registry == nil || strings.TrimSpace(owner) == "" || strings.TrimSpace(owner) != owner || strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("workspace file tools require a registry, owner and working directory")
	}
	var readers []VirtualFileReader
	var globbers []VirtualGlobber
	if source != nil {
		readers = append(readers, source)
		globbers = append(globbers, source)
	}
	tools := []tool.Tool{
		newWorkspaceReadTool(workDir, audit, readers...),
		newWorkspaceWriteTool(workDir, audit),
	}
	if includeList {
		tools = append(tools, newWorkspaceListTool(workDir))
	}
	tools = append(tools, newWorkspaceGlobTool(workDir, globbers...))
	return &Workspace{registry: registry, owner: owner, tools: tools}, nil
}

func (m *Workspace) Load(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return registry.ErrUnavailable
	}
	if m.registered {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.registry.Register(m.owner, m.tools...); err != nil {
		return err
	}
	m.registered = true
	return nil
}

func (m *Workspace) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if m.registered {
		if err := m.registry.UnregisterOwner(ctx, m.owner); err != nil {
			return errors.Join(extension.ErrCloseIncomplete, err)
		}
		m.registered = false
	}
	m.tools = nil
	m.closed = true
	return nil
}
