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
// for active dispatches when closing.
type NamespaceHandler func(context.Context, *Envelope, proto.Message, SendFunc) error

type namespaceEntry struct {
	messageType protoreflect.MessageType
	handler     NamespaceHandler
}

var ErrNamespaceUnavailable = errors.New("namespace mux is closed")

// NamespaceMux owns namespace registrations and admission, never the resources
// used by handlers. A connection owns its mux and closes all namespaces as one
// unit before releasing the resources used by their handlers.
type NamespaceMux struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	handlers map[protoreflect.FullName]namespaceEntry
	closed   bool
	inflight int
	done     chan struct{}
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
		done:     make(chan struct{}),
	}
}

// Context is the lifetime shared by this connection's namespaces.
func (m *NamespaceMux) Context() context.Context { return m.ctx }

// Cancel signals shutdown without waiting. Connection owners use it on IO
// failure, including from inside a dispatch. Close performs the final drain.
func (m *NamespaceMux) Cancel() { m.cancel() }

// Register adds one typed protocol namespace to this connection.
func (m *NamespaceMux) Register(prototype proto.Message, handler NamespaceHandler) error {
	if m == nil {
		return fmt.Errorf("namespace mux is required")
	}
	if prototype == nil || !prototype.ProtoReflect().IsValid() || handler == nil {
		return fmt.Errorf("namespace prototype and handler are required")
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
	m.handlers[name] = namespaceEntry{messageType: prototype.ProtoReflect().Type(), handler: handler}
	return nil
}

// Close stops admission and waits for accepted dispatches. Even an expired
// context closes the mux; it only limits this attempt to wait for completion.
func (m *NamespaceMux) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		if m.inflight == 0 {
			close(m.done)
		}
	}
	done := m.done
	m.mu.Unlock()
	m.Cancel()
	return waitNamespace(ctx, done)
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
	if err := m.ctx.Err(); err != nil {
		m.mu.Unlock()
		return true, err
	}
	m.inflight++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.inflight--
		if m.closed && m.inflight == 0 {
			close(m.done)
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
	if err := m.ctx.Err(); err != nil {
		return true, err
	}
	if err := entry.handler(m.ctx, envelope, message, send); err != nil {
		return true, err
	}
	return true, nil
}
