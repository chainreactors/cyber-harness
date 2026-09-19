package app

import (
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
)

// Publish stamps and publishes an event on the shared stream.
// Each State owns one sequence per session across all of its runtimes and tools.
// Observers receive the owned event as a read-only value and may be called
// concurrently.
func (a *State) Publish(event *aop.Event) {
	if a == nil || a.events == nil || event == nil {
		return
	}
	a.events.Publish(event)
}

// ObserveEvents registers an explicit synchronous observer of the canonical
// state stream. The returned handle owns admission and callback drain.
func (a *State) ObserveEvents(observer coreevents.Observer) *eventbus.Subscription[*aop.Event] {
	if a == nil || a.events == nil || observer == nil {
		return nil
	}
	return a.events.Observe(observer)
}
