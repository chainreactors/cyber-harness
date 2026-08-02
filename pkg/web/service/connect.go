package service

import (
	web "github.com/chainreactors/aiscan/pkg/web"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
)

// API exposes the existing business service composition to transport adapters.
func (s *Service) API() *managementapi.API {
	if s == nil {
		return nil
	}
	return s.api
}

// Auth returns the authentication mechanism shared by HTTP, ConnectRPC and
// WebSocket bindings.
func (s *Service) Auth() web.Auth {
	if s == nil || s.auth == nil {
		return NewAuth("")
	}
	return s.auth
}

var _ web.Service = (*Service)(nil)
