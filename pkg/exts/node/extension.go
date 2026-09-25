// Package node provides the optional namespace capability used by Node
// transports. The transport itself lives in pkg/node; this extension only
// owns the registry contributed to by session and PTY extensions.
package node

import (
	"fmt"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
)

// Extension publishes the connection namespace registry for a composed
// profile. Connection ownership remains with pkg/node.
type Extension struct {
	registry *namespaces.Registry
}

func New() *Extension {
	return &Extension{registry: namespaces.New()}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.registry == nil {
		return fmt.Errorf("node extension is unavailable")
	}
	if err := e.registry.Load(scope); err != nil {
		return err
	}
	return extension.Provide[*namespaces.Registry](scope, e.registry)
}

var _ extension.Extension = (*Extension)(nil)
