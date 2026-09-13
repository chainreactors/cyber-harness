package app

import (
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
)

// Emit stamps and publishes an event on the application's shared bus.
// Each App owns one sequence per session across all of its runtimes and tools.
// Subscribers receive the original event and may be called concurrently.
func (a *App) Emit(event *aop.Event) {
	if a == nil || a.events == nil || event == nil {
		return
	}
	a.events.Emit(event)
}

// SubscribeEvents observes the canonical application event stream. Events can
// only be published through Emit, so every producer shares one stamping
// authority.
func (a *App) SubscribeEvents(fn func(*aop.Event)) *eventbus.Subscription[*aop.Event] {
	if a == nil || a.events == nil || fn == nil {
		return nil
	}
	return a.events.Subscribe(fn)
}
