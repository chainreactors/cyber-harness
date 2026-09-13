// Package exts contains the product's extension adapters. Implementations
// remain in agent, tools, and pkg resources; this package gives each one a
// small lifecycle boundary for composition by extension.Set.
package tools

import (
	"context"
	"errors"
	"slices"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

// Extension is the common owner for a group of already constructed tool
// declarations. The Registry owns publication, admission, cancellation, and
// draining; this contribution owns no registry or execution lease.
type Extension struct {
	registry *toolset.Registry
	values   []tool.Tool
}

func New(registry *toolset.Registry, values ...tool.Tool) (*Extension, error) {
	if registry == nil || len(values) == 0 {
		return nil, errors.New("tools extension requires at least one tool")
	}
	for _, value := range values {
		if value == nil {
			return nil, errors.New("tools extension contains nil tool")
		}
	}
	return &Extension{registry: registry, values: slices.Clone(values)}, nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	return e.registry.Register(scope, e.values...)
}

func (e *Extension) Close(context.Context) error {
	e.values = nil
	return nil
}

var _ extension.Extension = (*Extension)(nil)
