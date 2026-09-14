// Package terminaltools owns a Bash/tmux tool installation and all its sessions.
package terminal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

type Config struct {
	Directory      string
	Timeout        int
	Proxy          string
	ProxyCA        string
	Egress         func(context.Context) (string, string, func())
	Containment    commands.ProcessContainment
	MaximumTimeout time.Duration
	// Tmux constructs the terminal command published by this extension. Nil
	// selects the native command; product profiles may supply their own session
	// ownership policy without replacing an already published registration.
	Tmux func(*commands.BashTool) commands.Command
	// HiddenCommands are control-only registry commands omitted from the Bash
	// description and shell aliases.
	HiddenCommands []string
}
type Extension struct {
	mu                 sync.Mutex
	tools              *toolset.Registry
	commands           *commands.Registry
	bash               *commands.BashTool
	tmux               commands.Command
	registered, closed bool
	done               chan struct{}
}

func New(registry *hooks.Registry, tools *toolset.Registry, c *commands.Registry, config Config) (*Extension, error) {
	if tools == nil || c == nil || config.Directory == "" {
		return nil, fmt.Errorf("terminal requires commands and a working directory")
	}
	bash := commands.NewBashTool(config.Directory, config.Timeout, registry).
		WithScannerProxy(config.Proxy).
		WithScannerProxyCA(config.ProxyCA).
		WithProcessContainment(config.Containment).
		WithForegroundTimeoutCeiling(config.MaximumTimeout)
	bash.SetEgressResolver(config.Egress)
	bash.EnableShellCommands(c)
	bash.HideCommands(config.HiddenCommands...)
	tmux := commands.NewTmuxCommand(bash)
	if config.Tmux != nil {
		tmux = config.Tmux(bash)
	}
	if tmux.Name != "tmux" || tmux.Run == nil {
		return nil, fmt.Errorf("terminal tmux command must be named tmux and executable")
	}
	return &Extension{tools: tools, commands: c, bash: bash, tmux: tmux}, nil
}
func (m *Extension) Bash() *commands.BashTool { return m.bash }
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
	if err := m.commands.Register("terminal", "terminal", m.tmux); err != nil {
		return err
	}
	if err := m.tools.Register("terminal", m.bash); err != nil {
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
