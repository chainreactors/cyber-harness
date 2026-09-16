// Package tui installs the presentation registry used by terminal hosts.
// The terminal implementation remains in pkg/console; no terminal is opened
// simply by constructing or loading this registration extension.
package tui

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/console/api"
)

type Extension struct{ registry *api.Registry }

func New() *Extension { return &Extension{registry: api.NewRegistry()} }

// Bindings seals registration. Hosts call it only after the complete profile
// has loaded its contributors. Terminal lifetimes remain owned by the host.
func (e *Extension) Bindings() *api.Bindings { return e.registry.Bindings() }
func (e *Extension) Load(scope *extension.Scope) error {
	if scope == nil {
		return fmt.Errorf("tui requires scope")
	}
	return extension.Define[*api.Bindings](scope, e.registry)
}

func (e *Extension) Close(context.Context) error { e.registry.Close(); return nil }

var _ extension.Extension = (*Extension)(nil)
