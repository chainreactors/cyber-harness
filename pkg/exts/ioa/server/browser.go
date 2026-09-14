package server

import (
	"context"
	"fmt"
	service "github.com/chainreactors/aiscan/tools/ioa/server"
	"github.com/chainreactors/ioa/protocols"
	"net/http"
)

func BrowserHandler(ctx context.Context, server *service.Server, authenticate func(*http.Request) bool, enabled bool) (http.Handler, error) {
	identity, err := server.RegisterIdentity(ctx, protocols.AuthRegister{Name: "aiscan.web", Description: "AIScan Web console", AccessKey: server.AccessKey(), Meta: map[string]any{"role": "web"}})
	if err != nil {
		return nil, fmt.Errorf("register IOA web identity: %w", err)
	}
	return bridgeBrowser(identity.Token, server.Handler(), authenticate, enabled, server.AccessKey()), nil
}

// bridgeBrowser maps an authenticated AIScan browser request to IOA's reserved
// browser token while preserving native IOA bearer identities.
func bridgeBrowser(ioaToken string, next http.Handler, authenticate func(*http.Request) bool, enabled bool, key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webAuthenticated := authenticate(r)
		if !enabled && r.Header.Get("Authorization") != "" {
			webAuthenticated = false
		}
		if !webAuthenticated || ioaToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		request := r.Clone(r.Context())
		request.Header = r.Header.Clone()
		request.Header.Set("Authorization", "Bearer "+ioaToken)
		if enabled {
			request.Header.Set("X-Access-Key", key)
		}
		next.ServeHTTP(w, request)
	})
}
