package aop

import (
	"fmt"

	"google.golang.org/protobuf/proto"
)

// NamespaceBinding is one typed protocol contribution. The protobuf message
// type is the namespace identity.
type NamespaceBinding struct {
	Prototype proto.Message
	Handler   NamespaceHandler
}

func (b NamespaceBinding) Register(mux *NamespaceMux) error {
	if mux == nil || b.Prototype == nil || b.Handler == nil {
		return fmt.Errorf("namespace binding requires a mux, prototype and handler")
	}
	return mux.Register(b.Prototype, b.Handler)
}

// ConnectionBinding contributes one protocol whose handler is created once per
// connection. The mux keeps only that handler, so connection-local state —
// stream tables, monitors, cancellations — ends with the connection and no
// contributor has to track or release it.
type ConnectionBinding struct {
	Prototype proto.Message
	Open      func() NamespaceHandler
}

func (b ConnectionBinding) Register(mux *NamespaceMux) error {
	if mux == nil || b.Prototype == nil || b.Open == nil {
		return fmt.Errorf("connection binding requires a mux, prototype and opener")
	}
	return mux.Register(b.Prototype, b.Open())
}
