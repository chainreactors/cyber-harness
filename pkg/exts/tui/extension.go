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

func (e *Extension) Load(scope *extension.Scope) error {
	if scope == nil {
		return fmt.Errorf("tui requires scope")
	}
	// The point stores bindings; the registry is the capability a host borrows
	// to seal and read them once the whole profile has contributed. Sealing is
	// why this is not published as the snapshot itself: the snapshot does not
	// exist until the last contributor has loaded.
	if err := extension.Define[*api.Bindings](scope, e.registry); err != nil {
		return err
	}
	return extension.Provide[*api.Registry](scope, e.registry)
}

func (e *Extension) Close(context.Context) error { e.registry.Close(); return nil }

var _ extension.Extension = (*Extension)(nil)
