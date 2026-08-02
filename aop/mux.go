package aop

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// SendFunc writes one Envelope on the connection owned by the caller.
type SendFunc func(*Envelope) error

// NamespaceHandler processes one registered top-level namespace message.
// The handler owns business semantics; NamespaceMux only decodes and routes.
type NamespaceHandler func(context.Context, *Envelope, proto.Message, SendFunc) error

type namespaceEntry struct {
	messageType protoreflect.MessageType
	handler     NamespaceHandler
}

// NamespaceMux routes Envelope.payload by protobuf full name. Mux instances
// are application-owned; there is no global registry or lifecycle ownership.
type NamespaceMux struct {
	handlers map[protoreflect.FullName]namespaceEntry
}

func NewNamespaceMux() *NamespaceMux {
	return &NamespaceMux{handlers: make(map[protoreflect.FullName]namespaceEntry)}
}

// Register adds one <namespace>.ProtocolMessage handler. Applications register
// namespaces during construction, before Dispatch is used concurrently.
func (m *NamespaceMux) Register(prototype proto.Message, handler NamespaceHandler) error {
	if m == nil {
		return fmt.Errorf("namespace mux is required")
	}
	if prototype == nil || handler == nil {
		return fmt.Errorf("namespace prototype and handler are required")
	}
	descriptor := prototype.ProtoReflect().Descriptor()
	name := descriptor.FullName()
	if !strings.HasSuffix(string(name), ".ProtocolMessage") && name != "aop.ProtocolMessage" {
		return fmt.Errorf("namespace message %q must be named ProtocolMessage", name)
	}
	if m.handlers == nil {
		m.handlers = make(map[protoreflect.FullName]namespaceEntry)
	}
	if _, exists := m.handlers[name]; exists {
		return fmt.Errorf("namespace %q is already registered", name)
	}
	m.handlers[name] = namespaceEntry{messageType: prototype.ProtoReflect().Type(), handler: handler}
	return nil
}

// Dispatch decodes and handles one registered namespace. Unknown namespaces
// return handled=false so the connection owner can emit its protocol error.
func (m *NamespaceMux) Dispatch(ctx context.Context, envelope *Envelope, send SendFunc) (handled bool, err error) {
	if m == nil || envelope == nil || envelope.Payload == nil {
		return false, fmt.Errorf("AOP envelope payload is required")
	}
	name := envelope.Payload.MessageName()
	entry, ok := m.handlers[name]
	if !ok {
		return false, nil
	}
	canonical := "type.googleapis.com/" + string(name)
	if envelope.Payload.TypeUrl != canonical {
		return true, fmt.Errorf("non-canonical type URL %q, want %q", envelope.Payload.TypeUrl, canonical)
	}
	message := entry.messageType.New().Interface()
	if err := envelope.Payload.UnmarshalTo(message); err != nil {
		return true, fmt.Errorf("decode %s: %w", name, err)
	}
	if err := entry.handler(ctx, envelope, message, send); err != nil {
		return true, err
	}
	return true, nil
}
