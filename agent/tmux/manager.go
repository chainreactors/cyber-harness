// Package tmux provides a thin event-aware wrapper around the shared
// github.com/chainreactors/utils/pty manager. Command parsing and routing live
// in pkg/commands; this package only owns terminal sessions.
package tmux

import (
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/utils/pty"
)

// ---------------------------------------------------------------------------
// Type aliases — keep all existing callers compiling without changes.
// ---------------------------------------------------------------------------

type State = pty.State

const (
	StateRunning   = pty.StateRunning
	StateCompleted = pty.StateCompleted
	StateKilled    = pty.StateKilled
	StateFailed    = pty.StateFailed
)

type Info = pty.Info

type EventAction = pty.EventAction

const (
	EventSessionCreated = pty.EventSessionCreated
	EventSessionUpdated = pty.EventSessionUpdated
	EventSessionOutput  = pty.EventSessionOutput
	EventSessionClosed  = pty.EventSessionClosed
)

type Event = pty.Event

type OutputBuffer = pty.OutputBuffer

const (
	DefaultTimeout   = pty.DefaultTimeout
	DefaultBufferCap = pty.DefaultBufferCap
)

// Re-export buffer constructors.
var (
	NewOutputBuffer         = pty.NewOutputBuffer
	NewOutputBufferWithFile = pty.NewOutputBufferWithFile
)

// Re-export shell helpers.
var (
	ShellCommand        = pty.ShellCommand
	DefaultShellCommand = pty.DefaultShellCommand
)

// Re-export formatting.
var FormatCompletion = pty.FormatCompletion

// ---------------------------------------------------------------------------
// Manager — embeds pty.Manager and bridges its events
// ---------------------------------------------------------------------------

// Manager wraps pty.Manager and exposes aiscan's event subscription API.
type Manager struct {
	*pty.Manager
	events *eventbus.Bus[Event]
}

// NewManager creates a Manager backed by a fresh pty.Manager.
func NewManager() *Manager {
	m := &Manager{
		Manager: pty.NewManager(),
		events:  eventbus.New[Event](),
	}
	// Bridge pty.Manager events into the aiscan eventbus.
	m.SetOnEvent(func(ev Event) {
		if m.events != nil {
			m.events.Emit(ev)
		}
	})
	return m
}

// Subscribe registers an event listener and returns an unsubscribe function.
func (m *Manager) Subscribe(fn func(Event)) func() {
	if fn == nil {
		return func() {}
	}
	return m.events.Subscribe(fn)
}
