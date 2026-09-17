// Package proc adapts the shared work-unit registry to cyber's event bus. The
// registry itself lives in github.com/chainreactors/utils/proc; this package
// only bridges its events. Command parsing and routing live in tools/terminal.
package proc

import (
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/utils/proc"
)

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

// Subscribe registers an event listener owned by the returned subscription.
func (m *Manager) Subscribe(fn func(proc.Event)) *eventbus.Subscription[proc.Event] {
	if fn == nil {
		return nil
	}
	return m.events.Subscribe(fn)
}
