// Package harness provides small, reusable composition roots for Cyber.
//
// A Harness owns one extension set.  It can be used as a tool host by leaving
// Config.Session nil, or as a conversational Agent by selecting a session.
// Product distributions can add their own extensions between the base
// capabilities and the optional session runtime without depending on a
// reference distribution.
package harness

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/proc"
	coretool "github.com/chainreactors/cyber/core/tool"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

// Config selects a harness scenario.
//
// Base is always installed first. Extensions are then loaded in the order
// supplied, so a product can publish additional capabilities before the
// optional session consumes them. Session enables the Agent runtime; its Loop
// defaults to StandardLoop when Session.Loop is nil.
type Config struct {
	Base       BaseConfig
	Extensions []extension.Extension
	Session    *agentsession.Config
}

// Harness owns the assembled extension graph and the capabilities it
// publishes. Constructing a Harness does not start resources; call Load
// before using any accessor and Close it even when Load fails.
type Harness struct {
	set       *extension.Set
	providers *provider.State
	events    *events.Stream
	progress  *eventbus.Bus[*toolpb.Progress]
	processes *proc.Manager
	runtime   *agentsession.Runtime
	tools     coretool.Executor
	commands  coretool.CommandExecutor
	bash      *terminaltool.BashTool
}

// New builds a harness without loading it.
func New(config Config) (*Harness, error) {
	entries, err := BaseExtensions(config.Base)
	if err != nil {
		return nil, err
	}
	entries = append(entries, config.Extensions...)

	h := &Harness{}
	if config.Session != nil {
		session := *config.Session
		if session.Loop == nil {
			session.Loop = agent.StandardLoop{}
		}
		entries = append(entries, loopext.New(session.Loop), sessionext.New(session))
	}
	// The final consumer reads the capabilities assembled by the graph. This
	// keeps ownership in the extensions while giving an embedded host a small,
	// stable surface to run against.
	entries = append(entries, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		if h.providers, err = extension.Use[*provider.State](scope); err != nil {
			return err
		}
		if h.events, err = extension.Use[*events.Stream](scope); err != nil {
			return err
		}
		if h.progress, err = extension.Use[*eventbus.Bus[*toolpb.Progress]](scope); err != nil {
			return err
		}
		if h.processes, err = extension.Use[*proc.Manager](scope); err != nil {
			return err
		}

		if h.tools, err = extension.Use[coretool.Executor](scope); err != nil {
			return err
		}
		if h.commands, err = extension.Use[coretool.CommandExecutor](scope); err != nil {
			return err
		}
		if h.bash, err = extension.Use[*terminaltool.BashTool](scope); err != nil {
			return err
		}
		if config.Session != nil {
			h.runtime, err = extension.Use[*agentsession.Runtime](scope)
			return err
		}
		return nil
	}})

	set, err := extension.New(entries...)
	if err != nil {
		return nil, err
	}
	h.set = set
	return h, nil
}

// Load starts the graph and publishes its capabilities.
func (h *Harness) Load(ctx context.Context) error {
	if h == nil || h.set == nil {
		return fmt.Errorf("harness is unavailable")
	}
	return h.set.Load(ctx)
}

// Close stops the graph. It is safe to call after a failed Load.
func (h *Harness) Close(ctx context.Context) error {
	if h == nil || h.set == nil {
		return nil
	}
	return h.set.Close(ctx)
}

// Active reports whether the graph is loaded and accepting work.
func (h *Harness) Active() bool { return h != nil && h.set != nil && h.set.Active() }

// Runtime returns the session runtime for a conversational harness.
func (h *Harness) Runtime() (*agentsession.Runtime, error) {
	if !h.Active() || h.runtime == nil {
		return nil, fmt.Errorf("harness has no active session runtime")
	}
	return h.runtime, nil
}

// Tools returns the shared tool executor after Load.
func (h *Harness) Tools() (coretool.Executor, error) {
	if !h.Active() || h.tools == nil {
		return nil, fmt.Errorf("harness is not active")
	}
	return h.tools, nil
}

// Commands returns the shared command executor after Load.
func (h *Harness) Commands() (coretool.CommandExecutor, error) {
	if !h.Active() || h.commands == nil {
		return nil, fmt.Errorf("harness is not active")
	}
	return h.commands, nil
}

// Bash returns the base shell tool after Load.
func (h *Harness) Bash() (*terminaltool.BashTool, error) {
	if !h.Active() || h.bash == nil {
		return nil, fmt.Errorf("harness is not active")
	}
	return h.bash, nil
}

// Providers borrows a capability from the active installation.
func (h *Harness) Providers() (*provider.State, error) {
	if !h.Active() {
		return nil, fmt.Errorf("harness is not active")
	}
	return h.providers, nil
}

// Events borrows a capability from the active installation.
func (h *Harness) Events() (*events.Stream, error) {
	if !h.Active() {
		return nil, fmt.Errorf("harness is not active")
	}
	return h.events, nil
}

// Progress borrows a capability from the active installation.
func (h *Harness) Progress() (*eventbus.Bus[*toolpb.Progress], error) {
	if !h.Active() {
		return nil, fmt.Errorf("harness is not active")
	}
	return h.progress, nil
}

// Processes borrows a capability from the active installation.
func (h *Harness) Processes() (*proc.Manager, error) {
	if !h.Active() {
		return nil, fmt.Errorf("harness is not active")
	}
	return h.processes, nil
}
