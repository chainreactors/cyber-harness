package web

import (
	"net/http"
	"strings"
)

// AccessKeyAuth returns middleware that gates requests behind a Bearer token.
// Requests without a valid token get a 401. An empty key disables auth (dev mode).
func AccessKeyAuth(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if key == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health check, static SPA, and IOA (has its own auth)
			if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			// Accept token from: Authorization: Bearer <key>, or ?access_key=<key>
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" {
				token = r.URL.Query().Get("access_key")
			}
			if strings.TrimSpace(token) != key {
				writeError(w, http.StatusUnauthorized, "invalid or missing access key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
