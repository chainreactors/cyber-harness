// Package events owns stamping and publication of the canonical AOP stream.
package events

import (
	"errors"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
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

func (s *Stream) Subscribe(handler func(*aop.Event)) *eventbus.Subscription[*aop.Event] {
	if s == nil {
		return nil
	}
	return s.bus.Subscribe(handler)
}

func (s *Stream) SubscribeAsync(options eventbus.SubscribeOptions[*aop.Event], handler func(*aop.Event) error) (*eventbus.Subscription[*aop.Event], error) {
	if s == nil {
		return nil, errors.New("event stream is required")
	}
	return s.bus.SubscribeAsync(options, handler)
}

func (s *Stream) Emit(event *aop.Event) {
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
