//go:build full

package browser

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/playwright"
)

// Extension owns the browser command registration and the browser processes
// opened by that command. The profile owns the command registry.
type Extension struct {
	mu             sync.Mutex
	registry       commands.Runtime
	workDir        string
	defaultSession string
	command        *playwright.Command
	registered     bool
	closed         bool
	done           chan struct{}
}

var _ extension.Extension = (*Extension)(nil)

func New(registry commands.Runtime, workDir, defaultSession string) (*Extension, error) {
	if registry == nil || strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("browser extension requires a command registry and working directory")
	}
	return &Extension{registry: registry, workDir: workDir, defaultSession: defaultSession}, nil
}

func (m *Extension) Load(scope *extension.Scope) error {
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
		DescriptionPath: "cyber://skills/cyber/okf/easm/playwright.md",
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
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.registered = false
	if m.done == nil {
		m.done = make(chan struct{})
		command := m.command
		go func() {
			if command != nil {
				command.Close()
			}
			close(m.done)
		}()
	}
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.mu.Lock()
	m.command = nil
	m.closed = true
	m.mu.Unlock()
	return nil
}
