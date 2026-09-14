package eventbus

import "context"

// Publisher is the minimal, protocol-agnostic event transport contract.
// Concrete payload types are selected by the owning Extension; the core bus
// never interprets envelopes or persistence formats.
type Publisher[T any] interface {
	Emit(T)
}

// Subscriber describes the synchronous and asynchronous subscription surface
// exposed by a Bus. Subscription lifetime, buffering and draining remain
// owned by the returned Subscription; no additional Sink abstraction is
// required for durable consumers.
type Subscriber[T any] interface {
	Subscribe(func(T)) *Subscription[T]
	SubscribeAsync(SubscribeOptions[T], func(T) error) (*Subscription[T], error)
}

// Consumer is implemented by asynchronous consumers such as persistence or
// transport extensions. Flush observes admitted work; it does not imply any
// storage-specific durability guarantee.
type Consumer[T any] func(context.Context, T) error

var _ Publisher[any] = (*Bus[any])(nil)
var _ Subscriber[any] = (*Bus[any])(nil)
