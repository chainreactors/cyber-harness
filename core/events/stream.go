// Package events owns publication and observation of the canonical AOP stream.
package events

import (
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Stream is shared by every producer in one Profile. It is the single sequence
// authority; independent observations legitimately use the empty session key.
type Stream struct {
	bus *eventbus.Bus[*aop.Event]
	mu  sync.Mutex
	seq map[string]uint64
}

func New() *Stream {
	return &Stream{bus: eventbus.New[*aop.Event](), seq: make(map[string]uint64)}
}

func (s *Stream) Observe(observer func(*aop.Event)) *eventbus.Subscription[*aop.Event] {
	if s == nil || observer == nil {
		return nil
	}
	return s.bus.Subscribe(func(event *aop.Event) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("AOP observer panicked", "error", recovered, "stack", string(debug.Stack()))
			}
		}()
		observer(event)
	})
}

func (s *Stream) Consume(options eventbus.SubscribeOptions[*aop.Event], consumer func(*aop.Event) error) (*eventbus.Subscription[*aop.Event], error) {
	if s == nil || consumer == nil {
		return nil, errors.New("event stream and consumer are required")
	}
	return s.bus.SubscribeAsync(options, consumer)
}

// Publish is the only envelope-stamping authority. Producers transfer event
// ownership with correlation and payload populated; this method assigns event
// identity, time and the session sequence immediately before synchronous
// publication. Observers must treat the published value as read-only.
func (s *Stream) Publish(event *aop.Event) {
	if s == nil || event == nil {
		return
	}
	if event.EmittedAt == nil {
		event.EmittedAt = timestamppb.Now()
	}
	if event.Id == "" {
		event.Id = aop.EnvelopeID()
	}
	s.mu.Lock()
	s.seq[event.SessionId]++
	event.Seq = s.seq[event.SessionId]
	s.mu.Unlock()
	s.bus.Emit(event)
}
