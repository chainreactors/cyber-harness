// Package tmux provides a thin event-aware wrapper around the shared
// github.com/chainreactors/utils/pty manager. Command parsing and routing live
// in pkg/commands; this package only owns terminal sessions.
package tmux

import (
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/utils/pty"
)

// Manager wraps pty.Manager and exposes cyber's event subscription API.
type Manager struct {
	*pty.Manager
	events *eventbus.Bus[pty.Event]
}

// NewManager creates a Manager backed by a fresh pty.Manager.
func NewManager() *Manager {
	m := &Manager{
		Manager: pty.NewManager(),
		events:  eventbus.New[pty.Event](),
	}
	// Bridge pty.Manager events into the cyber eventbus.
	m.SetOnEvent(func(ev pty.Event) {
		if m.events != nil {
			m.events.Emit(ev)
		}
	})
	return m
}

// Subscribe registers an event listener owned by the returned subscription.
func (m *Manager) Subscribe(fn func(pty.Event)) *eventbus.Subscription[pty.Event] {
	if fn == nil {
		return nil
	}
	return m.events.Subscribe(fn)
}
