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

// Observer participates synchronously in publication. It is appropriate for
// ordering-sensitive transport and projection boundaries that must observe an
// event before Publish returns. Implementations must report their own failures;
// observer failure never changes the operation that produced the event.
type Observer interface {
	ObserveEvent(*aop.Event)
}

// ObserverFunc is the standard function implementation for short-lived
// transport observers, analogous to net/http.HandlerFunc. Long-lived resource
// consumers should implement Observer directly so ownership remains visible.
type ObserverFunc func(*aop.Event)

func (f ObserverFunc) ObserveEvent(event *aop.Event) { f(event) }

// Consumer processes owned event copies on a bounded serial worker. Durable
// outputs implement this interface directly; their Subscription is the sole
// source of backpressure, processing and drain status.
type Consumer interface {
	ConsumeEvent(*aop.Event) error
}

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

func (s *Stream) Observe(observer Observer) *eventbus.Subscription[*aop.Event] {
	if s == nil || observer == nil {
		return nil
	}
	return s.bus.Subscribe(func(event *aop.Event) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("AOP observer panicked", "error", recovered, "stack", string(debug.Stack()))
			}
		}()
		observer.ObserveEvent(event)
	})
}

func (s *Stream) Consume(options eventbus.SubscribeOptions[*aop.Event], consumer Consumer) (*eventbus.Subscription[*aop.Event], error) {
	if s == nil || consumer == nil {
		return nil, errors.New("event stream and consumer are required")
	}
	return s.bus.SubscribeAsync(options, consumer.ConsumeEvent)
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
