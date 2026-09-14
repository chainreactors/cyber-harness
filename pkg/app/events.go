package app

import (
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	coreevents "github.com/chainreactors/aiscan/core/events"
)

// Publish stamps and publishes an event on the application's shared stream.
// Each App owns one sequence per session across all of its runtimes and tools.
// Observers receive the owned event as a read-only value and may be called
// concurrently.
func (a *App) Publish(event *aop.Event) {
	if a == nil || a.events == nil || event == nil {
		return
	}
	a.events.Publish(event)
}

// ObserveEvents registers an explicit synchronous observer of the canonical
// application stream. The returned handle owns admission and callback drain.
func (a *App) ObserveEvents(observer coreevents.Observer) *eventbus.Subscription[*aop.Event] {
	if a == nil || a.events == nil || observer == nil {
		return nil
	}
	return a.events.Observe(observer)
}
