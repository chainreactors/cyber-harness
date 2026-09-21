// Package terminaltools owns a Bash/tmux tool installation and all its sessions.
package terminal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	procbus "github.com/chainreactors/cyber/core/proc"
	coretool "github.com/chainreactors/cyber/core/tool"

	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

type Config struct {
	// Tool optionally selects the host protocol facade over the owned Bash runtime.
	Tool           func(*terminaltool.BashTool) coretool.Tool
	Environment    map[string]string
	Directory      string
	Timeout        int
	Containment    terminaltool.ProcessContainment
	MaximumTimeout time.Duration
	// HiddenCommands are control-only registry commands omitted from the Bash
	// description and shell aliases.
	HiddenCommands     []string
	StandaloneCommands []string
}
type Extension struct {
	mu     sync.Mutex
	config Config
	bash   *terminaltool.BashTool
	closed bool
	done   chan struct{}
}

func New(config Config) *Extension { return &Extension{config: config} }

// Load builds the tool here rather than in New because everything it needs --
// the hook registry, the command executor, the egress endpoint -- is a
// capability, and capabilities only exist once the graph is loading.
func (m *Extension) Load(scope *extension.Scope) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return coretool.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.config.Directory == "" {
		return fmt.Errorf("terminal requires a working directory")
	}
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	executor, err := extension.Use[coretool.CommandExecutor](scope)
	if err != nil {
		return err
	}
	endpoint, err := extension.Use[egress.Endpoint](scope)
	if err != nil {
		return err
	}

	bash := terminaltool.NewBashTool(m.config.Directory, m.config.Timeout, registry).
		WithEnvironment(m.config.Environment).
		WithScannerProxy(endpoint.ProxyURL()).
		WithScannerProxyCA(endpoint.CAPath()).
		WithProcessContainment(m.config.Containment).
		WithForegroundTimeoutCeiling(m.config.MaximumTimeout)
	bash.SetEgressResolver(endpoint.Egress)
	bash.EnableShellCommands(executor)
	bash.HideCommands(m.config.HiddenCommands...)
	bash.StandaloneCommands(m.config.StandaloneCommands...)

	m.bash = bash

	var tool coretool.Tool = bash
	if m.config.Tool != nil {
		tool = m.config.Tool(bash)
	}
	if err := extension.Add[coretool.Tool](scope, tool); err != nil {
		return err
	}
	if err := extension.Provide[*terminaltool.BashTool](scope, bash); err != nil {
		return err
	}
	if err := extension.Provide[*procbus.Manager](scope, bash.Manager()); err != nil {
		return err
	}
	if err := extension.Provide[procbus.Sessions](scope, bash.Manager()); err != nil {
		return err
	}
	return nil
}
func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	if m.bash == nil {
		// Load never got far enough to build the tool.
		m.closed = true
		m.mu.Unlock()
		return nil
	}
	if m.done == nil {
		m.done = make(chan struct{})
		go func() {
			m.bash.Close()
			close(m.done)
		}()
	}
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
	default:
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}
