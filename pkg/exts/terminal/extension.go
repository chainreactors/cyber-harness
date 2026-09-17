// Package terminaltools owns a Bash/tmux tool installation and all its sessions.
package terminal

import (
	"context"
	"fmt"
	"sync"
	"time"

	procbus "github.com/chainreactors/cyber/agent/proc"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

type Config struct {
	Environment    map[string]string
	Directory      string
	Timeout        int
	Containment    terminaltool.ProcessContainment
	MaximumTimeout time.Duration
	// Tmux constructs the terminal command published by this extension. Nil
	// selects the native command; Profiles may supply their own session
	// ownership policy without replacing an already published registration.
	Tmux func(*terminaltool.BashTool) commands.Command
	// HiddenCommands are control-only registry commands omitted from the Bash
	// description and shell aliases.
	HiddenCommands []string
}
type Extension struct {
	mu                 sync.Mutex
	config             Config
	bash               *terminaltool.BashTool
	registered, closed bool
	done               chan struct{}
}

func New(config Config) *Extension { return &Extension{config: config} }

// Bash is the tool this extension owns. It is nil until the extension has
// loaded; consumers borrow it as a capability rather than reading it here.
func (m *Extension) Bash() *terminaltool.BashTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bash
}

// Load builds the tool here rather than in New because everything it needs --
// the hook registry, the command executor, the egress endpoint -- is a
// capability, and capabilities only exist once the graph is loading.
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
	if m.config.Directory == "" {
		return fmt.Errorf("terminal requires a working directory")
	}
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	executor, err := extension.Use[commands.Executor](scope)
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

	tmux := terminaltool.NewTmuxCommand(bash)
	if m.config.Tmux != nil {
		tmux = m.config.Tmux(bash)
	}
	if tmux.Name != "tmux" || tmux.Run == nil {
		return fmt.Errorf("terminal tmux command must be named tmux and executable")
	}
	m.bash = bash

	if err := extension.Add(scope, tmux); err != nil {
		return err
	}
	if err := extension.Add[tool.Tool](scope, bash); err != nil {
		return err
	}
	if err := extension.Provide[*terminaltool.BashTool](scope, bash); err != nil {
		return err
	}
	if err := extension.Provide[procbus.Sessions](scope, bash.Manager()); err != nil {
		return err
	}
	m.registered = true
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
