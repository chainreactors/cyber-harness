package pty

import (
	"fmt"

	"github.com/chainreactors/cyber/aop"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	"github.com/chainreactors/cyber/core/extension"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	runtimeproc "github.com/chainreactors/utils/proc"
)

// Extension owns the canonical AOP PTY protocol. It borrows the Bash tool owned
// by the terminal extension and publishes one connection-scoped binding, so
// every connection opens its own router and no contributor has to track or
// release one between connections.
type Extension struct {
	manager *runtimeproc.Manager
	opts    []Option
}

func New(bash *terminaltool.BashTool, opts ...Option) *Extension {
	var manager *runtimeproc.Manager
	if bash != nil {
		if bridge := bash.Manager(); bridge != nil {
			manager = bridge.Manager
		}
	}
	return &Extension{manager: manager, opts: append([]Option(nil), opts...)}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil {
		return fmt.Errorf("pty extension is unavailable")
	}
	return extension.Add(scope, aop.ConnectionBinding{
		Prototype: &ptypb.ProtocolMessage{},
		Open: func() aop.NamespaceHandler {
			return NewRuntimeRouter(e.manager, e.opts...).Handler()
		},
	})
}

var _ extension.Extension = (*Extension)(nil)
