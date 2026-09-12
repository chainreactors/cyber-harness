// Package exts contains the product's extension adapters. Implementations
// remain in agent, tools, and pkg resources; this package gives each one a
// small lifecycle boundary for composition by extension.Set.
package tools

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
)

// Tools is the common adapter for a group of already constructed tools. It
// only declares them to the owning Set. The Set owns publication, admission,
// cancellation, and draining; this adapter owns no registry or lease.
type Extension struct {
	mu     sync.Mutex
	values []tool.Tool
	closed bool
}

func New(values ...tool.Tool) (*Extension, error) {
	if len(values) == 0 {
		return nil, errors.New("tools extension requires at least one tool")
	}
	for _, value := range values {
		if value == nil {
			return nil, errors.New("tools extension contains nil tool")
		}
	}
	return &Extension{values: slices.Clone(values)}, nil
}

func (e *Extension) Load(scope *extension.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return extension.ErrToolsUnavailable
	}
	return scope.RegisterTools(e.values...)
}

func (e *Extension) Close(context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.values = nil
	e.mu.Unlock()
	return nil
}

var _ extension.Extension = (*Extension)(nil)
