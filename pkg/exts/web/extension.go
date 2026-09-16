// Package web owns the typed HTTP route registry used by the Web host.
package web

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	webpkg "github.com/chainreactors/cyber/pkg/web"
)

// Extension defines web.Route as a dynamic resource and contributes the routes
// backed by the management service. Later extensions may add their own routes.
type Extension struct {
	service webpkg.Service

	mu     sync.RWMutex
	order  []string
	routes map[string]webpkg.Route
}

func New(service webpkg.Service) *Extension {
	return &Extension{service: service, routes: make(map[string]webpkg.Route)}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.service == nil {
		return fmt.Errorf("web route service is required")
	}
	if err := extension.Define[webpkg.Route](scope, e); err != nil {
		return err
	}
	return extension.Add(scope, serviceRoutes(e.service)...)
}

func (e *Extension) Add(values ...webpkg.Route) (resource.Handle, error) {
	if e == nil || len(values) == 0 {
		return nil, resource.ErrInvalid
	}
	pending := make(map[string]webpkg.Route, len(values))
	patterns := make([]string, 0, len(values))
	for _, route := range values {
		if strings.TrimSpace(route.Pattern) == "" || route.Handler == nil {
			return nil, fmt.Errorf("HTTP route requires pattern and handler")
		}
		if _, exists := pending[route.Pattern]; exists {
			return nil, fmt.Errorf("duplicate HTTP route %q", route.Pattern)
		}
		pending[route.Pattern] = route
		patterns = append(patterns, route.Pattern)
	}

	e.mu.Lock()
	for _, pattern := range patterns {
		if _, exists := e.routes[pattern]; exists {
			e.mu.Unlock()
			return nil, fmt.Errorf("duplicate HTTP route %q", pattern)
		}
	}
	for _, pattern := range patterns {
		e.routes[pattern] = pending[pattern]
		e.order = append(e.order, pattern)
	}
	e.mu.Unlock()

	closed := false
	return resource.HandleFunc(func(context.Context) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		if closed {
			return nil
		}
		closed = true
		for _, pattern := range patterns {
			delete(e.routes, pattern)
		}
		kept := e.order[:0]
		for _, pattern := range e.order {
			if _, exists := e.routes[pattern]; exists {
				kept = append(kept, pattern)
			}
		}
		e.order = kept
		return nil
	}), nil
}

// Routes returns the current immutable route snapshot in contribution order.
func (e *Extension) Routes() []webpkg.Route {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	routes := make([]webpkg.Route, 0, len(e.order))
	for _, pattern := range e.order {
		if route, exists := e.routes[pattern]; exists {
			routes = append(routes, route)
		}
	}
	return routes
}

func serviceRoutes(service webpkg.Service) []webpkg.Route {
	routes := []webpkg.Route{webpkg.AOPRoute(service)}
	if api := service.API(); api != nil {
		if api.Sessions != nil {
			routes = append(routes, webpkg.SessionRoute(service))
		}
		if api.Scans != nil {
			routes = append(routes, webpkg.ScanRoute(service))
		}
		if api.Config != nil {
			routes = append(routes, webpkg.ConfigRoute(service))
		}
		if api.Agents != nil {
			routes = append(routes, webpkg.AgentRoute(service))
		}
		if api.Status != nil {
			routes = append(routes, webpkg.SystemRoute(service))
		}
		if api.SCO != nil {
			routes = append(routes, webpkg.SCORoute(service))
		}
	}
	if handler := service.ApplicationWebSocketHandler(); handler != nil {
		routes = append(routes, webpkg.Route{Pattern: webpkg.ApplicationWebSocketPath, Handler: handler})
	}
	if handler := service.NodeWebSocketHandler(); handler != nil {
		routes = append(routes, webpkg.Route{Pattern: webpkg.NodeWebSocketPath, Handler: handler})
	}
	return routes
}

var (
	_ extension.Extension          = (*Extension)(nil)
	_ resource.Point[webpkg.Route] = (*Extension)(nil)
)
