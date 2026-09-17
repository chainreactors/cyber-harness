package pty

import (
	"fmt"

	procbus "github.com/chainreactors/cyber/agent/proc"
	"github.com/chainreactors/cyber/aop"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	"github.com/chainreactors/cyber/core/extension"
)

// Extension owns the canonical AOP PTY protocol. It borrows the session
// registry and publishes one connection-scoped binding, so every connection
// opens its own router and no contributor has to track or release one between
// connections.
type Extension struct {
	sessions procbus.Sessions
	opts     []Option
}

func New(opts ...Option) *Extension {
	return &Extension{opts: append([]Option(nil), opts...)}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil {
		return fmt.Errorf("pty extension is unavailable")
	}
	sessions, err := extension.Use[procbus.Sessions](scope)
	if err != nil {
		return err
	}
	e.sessions = sessions
	return extension.Add(scope, aop.ConnectionBinding{
		Prototype: &ptypb.ProtocolMessage{},
		Open: func() aop.NamespaceHandler {
			opts := append([]Option{WithOpeners(e.sessions.Openers())}, e.opts...)
			return NewRouter(e.sessions, opts...).Handler()
		},
	})
}

var _ extension.Extension = (*Extension)(nil)
