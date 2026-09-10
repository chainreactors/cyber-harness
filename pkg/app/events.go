package app

import (
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Emit stamps and publishes an event on the application's shared bus.
// Each App owns one sequence per session across all of its runtimes and tools.
// Subscribers receive the original event and may be called concurrently.
func (a *App) Emit(event *aop.Event) {
	if event == nil {
		return
	}
	if event.EmittedAt == nil {
		event.EmittedAt = timestamppb.Now()
	}
	a.eventMu.Lock()
	if a.eventSeq == nil {
		a.eventSeq = make(map[string]uint64)
	}
	a.eventSeq[event.SessionId]++
	event.Seq = a.eventSeq[event.SessionId]
	if event.Id == "" {
		event.Id = fmt.Sprintf("runtime-%d", event.Seq)
	}
	a.eventMu.Unlock()
	a.EventBus.Emit(event)
}
