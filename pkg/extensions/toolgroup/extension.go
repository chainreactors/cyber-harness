// Package toolgroup owns the common lifecycle of a group of tool registrations.
// Tools and their borrowed resources are constructed by the profile; this
// extension only publishes, revokes and drains its registration lease.
package toolgroup

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
)

type Extension struct {
	mu        sync.Mutex
	registrar tool.Registrar
	tools     []tool.Tool
	lease     tool.Registration
	closed    bool
}

var _ extension.Extension = (*Extension)(nil)

// New is inert. The profile must order the registry and borrowed resources
// before this extension. Owner identity comes from the installation Context.
func New(registrar tool.Registrar, tools ...tool.Tool) (*Extension, error) {
	if registrar == nil || len(tools) == 0 {
		return nil, errors.New("tool group requires a registrar and tools")
	}
	for _, t := range tools {
		if t == nil {
			return nil, errors.New("tool group contains a nil tool")
		}
	}
	return &Extension{registrar: registrar, tools: slices.Clone(tools)}, nil
}

func (e *Extension) Load(scope *extension.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("tool group is closed")
	}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if e.lease != nil {
		return nil
	}
	lease, err := e.registrar.Register(scope.Owner(), e.tools...)
	if err != nil {
		return err
	}
	e.lease = lease
	if _, err := scope.Track(lease.Revoke); err != nil {
		return errors.Join(err, lease.Close(scope.Init()))
	}
	return nil
}

func (e *Extension) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	lease := e.lease
	e.tools = nil
	e.mu.Unlock()
	if lease != nil {
		return lease.Close(ctx)
	}
	return nil
}
