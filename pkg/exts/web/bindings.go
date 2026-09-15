// Package web supplies Cyber's optional Web protocol bindings.
// Handler declarations own no resources and need no lifecycle interface.
package web

import "github.com/chainreactors/cyber/pkg/web"

func Routes(service web.Service) []web.Route {
	routes := []web.Route{web.AOPRoute(service)}
	if api := service.API(); api != nil {
		if api.Sessions != nil {
			routes = append(routes, web.SessionRoute(service))
		}
		if api.Scans != nil {
			routes = append(routes, web.ScanRoute(service))
		}
		if api.Config != nil {
			routes = append(routes, web.ConfigRoute(service))
		}
		if api.Agents != nil {
			routes = append(routes, web.AgentRoute(service))
		}
		if api.Status != nil {
			routes = append(routes, web.SystemRoute(service))
		}
		if api.SCO != nil {
			routes = append(routes, web.SCORoute(service))
		}
	}
	if handler := service.ApplicationWebSocketHandler(); handler != nil {
		routes = append(routes, web.Route{Source: "agent", Pattern: web.ApplicationWebSocketPath, Handler: handler})
	}
	if handler := service.NodeWebSocketHandler(); handler != nil {
		routes = append(routes, web.Route{Source: "node", Pattern: web.NodeWebSocketPath, Handler: handler})
	}
	return routes
}
