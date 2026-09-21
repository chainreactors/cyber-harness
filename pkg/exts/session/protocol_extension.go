package session

import (
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
)

// ProtocolExtension contributes Session handlers to the installed namespace registry.
type ProtocolExtension struct{}

func NewProtocol() *ProtocolExtension { return &ProtocolExtension{} }
func (*ProtocolExtension) Load(scope *extension.Scope) error {
	runtime, err := extension.Use[*session.Runtime](scope)
	if err != nil {
		return err
	}
	return extension.Add(scope, runtime.NamespaceBindings()...)
}
