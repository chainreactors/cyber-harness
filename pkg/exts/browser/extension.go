//go:build full

package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/playwright"
)

// Extension owns the browser command registration and the browser processes
// opened by that command. The command registry is borrowed from the profile.
type Extension struct {
	mu             sync.Mutex
	registry       *commands.Registry
	workDir        string
	defaultSession string
	command        *playwright.Command
	registered     bool
	closed         bool
}

var _ extension.Extension = (*Extension)(nil)

func New(registry *commands.Registry, workDir, defaultSession string) (*Extension, error) {
	if registry == nil || strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("browser extension requires a command registry and working directory")
	}
	return &Extension{registry: registry, workDir: workDir, defaultSession: defaultSession}, nil
}

func (m *Extension) Load(scope *extension.Context) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return commands.ErrUnavailable
	}
	if m.registered {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	command := playwright.New(m.workDir).WithDefaultSession(m.defaultSession)
	if err := m.registry.Register("browser", "browser", commands.Command{
		Name: command.Name(), Usage: command.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/playwright.md",
		Run:             command.Run,
	}); err != nil {
		command.Close()
		return err
	}
	m.command = command
	m.registered = true
	return nil
}

func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if m.registered {
		if err := m.registry.UnregisterOwner(ctx, "browser"); err != nil {
			return errors.Join(extension.ErrCloseIncomplete, err)
		}
		m.registered = false
	}
	if m.command != nil {
		m.command.Close()
		m.command = nil
	}
	m.closed = true
	return nil
}
