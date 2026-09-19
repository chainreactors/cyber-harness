package web

import (
	"context"
	"net/http"

	aop "github.com/chainreactors/cyber/aop"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
)

const (
	ApplicationWebSocketPath = "/api/aop/application/ws"
	NodeWebSocketPath        = "/api/aop/node/ws"
)

// Auth is the authentication mechanism required by Web transports. The
// concrete policy and credential state are owned by the service package.
type Auth interface {
	Enabled() bool
	Authenticate(*http.Request) bool
	Middleware(http.Handler) http.Handler
	RegisterRoutes(*http.ServeMux)
}

// Service is the single transport-facing abstraction for Cyber Web. The root
// package owns only this contract and transport mechanisms; business runtime,
// persistence, agents and authentication live in pkg/web/service.
type Service interface {
	API() *managementapi.API
	Auth() Auth
	ServeApplication(context.Context, aop.EnvelopeStream) error
	ApplicationWebSocketHandler() http.Handler
	NodeWebSocketHandler() http.Handler
}
