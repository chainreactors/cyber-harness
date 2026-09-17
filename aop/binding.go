package aop

import (
	"fmt"

	"google.golang.org/protobuf/proto"
)

// Binding is one typed protocol contribution. The protobuf message type is the
// namespace identity. Open returns the handler for one connection: a shared
// handler returns the same value every time, a connection-scoped one returns a
// fresh handler whose state -- stream tables, monitors, cancellations -- ends
// with the connection, so no contributor has to track or release it.
type Binding struct {
	Prototype proto.Message
	Open      func() NamespaceHandler
}

func (b Binding) Register(mux *NamespaceMux) error {
	if mux == nil || b.Prototype == nil || b.Open == nil {
		return fmt.Errorf("binding requires a mux, prototype and opener")
	}
	return mux.Register(b.Prototype, b.Open())
}

// Shared contributes one handler for every connection. Its owner controls when
// it stops serving; the binding itself holds no lifecycle. This is the verb the
// contribution site reads instead of a second binding type.
func Shared(prototype proto.Message, handler NamespaceHandler) Binding {
	if handler == nil {
		// Leave Open nil so Register and the namespace Point reject this the
		// same way they reject a missing opener.
		return Binding{Prototype: prototype}
	}
	return Binding{Prototype: prototype, Open: func() NamespaceHandler { return handler }}
}
