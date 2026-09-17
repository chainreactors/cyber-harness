// Package proc adapts the shared work-unit registry to cyber's event bus. The
// registry itself lives in github.com/chainreactors/utils/proc; this package
// only bridges its events. Command parsing and routing live in tools/terminal.
package proc

import (
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/utils/proc"
)

// Sessions is the process registry as the callers that only read and steer
// sessions see it. It is declared here, rather than reused from the runtime
// module, so that the capability key belongs to this repository: a key any
// module could name is a key two owners could claim.
type Sessions interface {
	proc.SessionManager
	// Openers are the session kinds this registry can start. They come from
	// the owner because only it knows how to build a session in its own
	// registry; a borrower that could only read would have nothing to attach.
	Openers() map[string]proc.OpenFunc
}

// Manager wraps proc.Manager and exposes cyber's event subscription API.
type Manager struct {
	*proc.Manager
	events *eventbus.Bus[proc.Event]
}

// NewManager creates a Manager backed by a fresh registry.
func NewManager() *Manager {
	m := &Manager{
		Manager: proc.NewManager(),
		events:  eventbus.New[proc.Event](),
	}
	// Bridge registry events into the cyber eventbus.
	m.SetOnEvent(func(ev proc.Event) {
		if m.events != nil {
			m.events.Emit(ev)
		}
	})
	return m
}

// Openers exposes the standard session kinds for this manager.
func (m *Manager) Openers() map[string]proc.OpenFunc {
	return proc.DefaultOpeners(m.Manager, proc.DefaultSessionTimeout, proc.DefaultEnv())
}

// Subscribe registers an event listener owned by the returned subscription.
func (m *Manager) Subscribe(fn func(proc.Event)) *eventbus.Subscription[proc.Event] {
	if fn == nil {
		return nil
	}
	return m.events.Subscribe(fn)
}
