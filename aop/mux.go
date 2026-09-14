package aop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// SendFunc writes one Envelope on the connection owned by the caller.
type SendFunc func(*Envelope) error

// NamespaceHandler processes one registered top-level namespace message. Its
// context belongs to the namespace/connection and survives a dispatch return.
// Handlers own and drain any asynchronous work they start; the mux only waits
// for active dispatches when unregistering or closing.
type NamespaceHandler func(context.Context, *Envelope, proto.Message, SendFunc) error

type namespaceEntry struct {
	messageType protoreflect.MessageType
	handler     NamespaceHandler
	owner       *namespaceOwner
}

type namespaceOwner struct {
	ctx      context.Context
	cancel   context.CancelFunc
	stopping bool
	inflight int
	done     chan struct{}
}

var ErrNamespaceUnavailable = errors.New("namespace owner or mux is closed")

// NamespaceMux owns namespace registrations and admission, never the resources
// used by handlers. A connection owns its mux; extensions unregister and wait
// before releasing their resources. Closed owners and names cannot be reused.
type NamespaceMux struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	handlers map[protoreflect.FullName]namespaceEntry
	owners   map[string]*namespaceOwner
	closed   bool
}

func NewNamespaceMux(ctx context.Context) *NamespaceMux {
	if ctx == nil {
		panic("namespace mux requires its connection context")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &NamespaceMux{
		ctx:      ctx,
		cancel:   cancel,
		handlers: make(map[protoreflect.FullName]namespaceEntry),
		owners:   make(map[string]*namespaceOwner),
	}
}

// Context is the lifetime shared by this connection's namespaces.
func (m *NamespaceMux) Context() context.Context { return m.ctx }

// Cancel signals shutdown without waiting. Connection owners use it on IO
// failure, including from inside a dispatch. Close performs the final drain.
func (m *NamespaceMux) Cancel() { m.cancel() }

// Register adds a handler owned by one extension instance. An owner may install
// several namespaces; an unsuccessful registration never changes ownership.
func (m *NamespaceMux) Register(owner string, prototype proto.Message, handler NamespaceHandler) error {
	if m == nil {
		return fmt.Errorf("namespace mux is required")
	}
	if strings.TrimSpace(owner) == "" || prototype == nil || !prototype.ProtoReflect().IsValid() || handler == nil {
		return fmt.Errorf("namespace owner, prototype and handler are required")
	}
	descriptor := prototype.ProtoReflect().Descriptor()
	name := descriptor.FullName()
	if !isNamespaceProtocolMessageName(string(descriptor.Name())) {
		return fmt.Errorf("namespace message %q must have a ProtocolMessage suffix", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrNamespaceUnavailable
	}
	if m.ctx == nil {
		return fmt.Errorf("namespace mux must be constructed with NewNamespaceMux")
	}
	if _, exists := m.handlers[name]; exists {
		return fmt.Errorf("namespace %q is already registered", name)
	}
	o := m.owners[owner]
	if o != nil && o.stopping {
		return ErrNamespaceUnavailable
	}
	if o == nil {
		ctx, cancel := context.WithCancel(m.ctx)
		o = &namespaceOwner{ctx: ctx, cancel: cancel, done: make(chan struct{})}
		m.owners[owner] = o
	}
	m.handlers[name] = namespaceEntry{messageType: prototype.ProtoReflect().Type(), handler: handler, owner: o}
	return nil
}

// UnregisterOwner stops admission, cancels handlers, and waits for accepted
// dispatches. A handler's background subscriptions remain that extension's
// responsibility. Timeout retains ownership; retry with a fresh context.
func (m *NamespaceMux) UnregisterOwner(ctx context.Context, owner string) error {
	if m == nil {
		return fmt.Errorf("namespace mux is required")
	}
	m.mu.Lock()
	o := m.owners[owner]
	if o == nil {
		m.mu.Unlock()
		return fmt.Errorf("unknown namespace owner %q", owner)
	}
	m.stopOwner(o)
	m.mu.Unlock()
	o.cancel()
	return waitNamespace(ctx, o.done)
}

// Close stops every owner before waiting for any of them. Even an expired
// context stops admission; it only limits this attempt to wait for completion.
func (m *NamespaceMux) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.Cancel()
	m.mu.Lock()
	m.closed = true
	owners := make([]*namespaceOwner, 0, len(m.owners))
	for _, o := range m.owners {
		m.stopOwner(o)
		owners = append(owners, o)
	}
	m.mu.Unlock()
	for _, o := range owners {
		o.cancel()
	}
	for _, o := range owners {
		if err := waitNamespace(ctx, o.done); err != nil {
			return err
		}
	}
	return nil
}

// stopOwner runs under m.mu, the same lock used to admit dispatches.
func (m *NamespaceMux) stopOwner(o *namespaceOwner) {
	if !o.stopping {
		o.stopping = true
		if o.inflight == 0 {
			close(o.done)
		}
	}
}

func waitNamespace(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isNamespaceProtocolMessageName(name string) bool {
	return strings.HasSuffix(name, "ProtocolMessage")
}

// Dispatch decodes and handles one registered namespace. Unknown namespaces
// return handled=false so the connection owner can emit its protocol error.
func (m *NamespaceMux) Dispatch(envelope *Envelope, send SendFunc) (handled bool, err error) {
	if m == nil || envelope == nil || envelope.Payload == nil {
		return false, fmt.Errorf("AOP envelope payload is required")
	}
	name := envelope.Payload.MessageName()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false, ErrNamespaceUnavailable
	}
	entry, ok := m.handlers[name]
	if !ok {
		m.mu.Unlock()
		return false, nil
	}
	if entry.owner.stopping {
		m.mu.Unlock()
		return true, ErrNamespaceUnavailable
	}
	if err := entry.owner.ctx.Err(); err != nil {
		m.mu.Unlock()
		return true, err
	}
	entry.owner.inflight++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		entry.owner.inflight--
		if entry.owner.stopping && entry.owner.inflight == 0 {
			close(entry.owner.done)
		}
		m.mu.Unlock()
	}()
	canonical := "type.googleapis.com/" + string(name)
	if envelope.Payload.TypeUrl != canonical {
		return true, fmt.Errorf("non-canonical type URL %q, want %q", envelope.Payload.TypeUrl, canonical)
	}
	message := entry.messageType.New().Interface()
	if err := envelope.Payload.UnmarshalTo(message); err != nil {
		return true, fmt.Errorf("decode %s: %w", name, err)
	}
	if err := entry.owner.ctx.Err(); err != nil {
		return true, err
	}
	if err := entry.handler(entry.owner.ctx, envelope, message, send); err != nil {
		return true, err
	}
	return true, nil
}
