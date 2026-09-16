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
