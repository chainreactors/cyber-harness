// Package web owns the typed HTTP route registry used by the Web host.
package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/core/extension"
	coreregistry "github.com/chainreactors/cyber/core/registry"
	"github.com/chainreactors/cyber/core/resource"
	webpkg "github.com/chainreactors/cyber/pkg/web"
)

// Extension defines web.Route as a dynamic resource and contributes the routes
// backed by the management service. Later extensions may add their own routes.
//
// Batching, ordering, duplicate rules and revocation are the same problem the
// command and tool registries solve, so they use the same store rather than a
// third copy of it. The route pattern is the name.
type Extension struct {
	service webpkg.Service
	store   *coreregistry.Store[webpkg.Route]
}

func New(service webpkg.Service) *Extension {
	return &Extension{service: service, store: coreregistry.New[webpkg.Route]()}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.service == nil || e.store == nil {
		return fmt.Errorf("web route service is required")
	}
	if err := extension.Define[webpkg.Route](scope, e); err != nil {
		return err
	}
	if err := extension.Add(scope, webpkg.ManagementRoutes(e.service)...); err != nil {
		return err
	}
	return e.store.Activate(scope.Init())
}

func (e *Extension) Add(values ...webpkg.Route) (resource.Handle, error) {
	if e == nil || e.store == nil || len(values) == 0 {
		return nil, resource.ErrInvalid
	}
	batch := make([]coreregistry.Value[webpkg.Route], 0, len(values))
	for _, route := range values {
		if strings.TrimSpace(route.Pattern) == "" || route.Handler == nil {
			return nil, fmt.Errorf("HTTP route requires pattern and handler")
		}
		batch = append(batch, coreregistry.Value[webpkg.Route]{Name: route.Pattern, Value: route})
	}
	return e.store.Add(batch...)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.store == nil {
		return nil
	}
	return e.store.Close(ctx)
}

// Routes returns the current immutable route snapshot in contribution order.
// It is empty until the graph activates, so a half-built profile cannot be
// served.
func (e *Extension) Routes() []webpkg.Route {
	if e == nil || e.store == nil {
		return nil
	}
	entries := e.store.Entries()
	routes := make([]webpkg.Route, 0, len(entries))
	for _, entry := range entries {
		routes = append(routes, entry.Value)
	}
	return routes
}

var (
	_ extension.Extension          = (*Extension)(nil)
	_ resource.Point[webpkg.Route] = (*Extension)(nil)
)
