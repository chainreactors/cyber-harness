package service

import (
	"net/http"

	web "github.com/chainreactors/aiscan/pkg/web"
)

func newHandler(service web.Service, ioaHandler http.Handler, static http.Handler, _ ...string) *web.Handler {
	return web.NewHandler(service, ioaHandler, static)
}

func registerConnectServices(mux *http.ServeMux, _ string, service web.Service) {
	web.RegisterConnectServices(mux, service)
}

func newAccessKeyAuth(key string) func(http.Handler) http.Handler {
	return NewAuth(key).Middleware
}

func registerTestAuthRoutes(mux *http.ServeMux, key string) {
	NewAuth(key).RegisterRoutes(mux)
}

func shareWebAuthWithIOA(accessKey, ioaToken string, next http.Handler) http.Handler {
	return NewAuth(accessKey).ShareWithIOA(ioaToken, next)
}
