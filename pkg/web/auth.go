package web

import (
	"net/http"
	"strings"

	"github.com/chainreactors/aiscan/pkg/web/auth"
)

// AccessKeyAuth returns middleware that gates requests behind access-key credentials.
// Browser logins exchange the access key for an HttpOnly session cookie so the
// key never needs to live in JavaScript or appear in a URL. Requests without a
// valid credential get a 401. An empty key disables auth (dev mode).
func AccessKeyAuth(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if key == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health check, static SPA, and IOA (has its own auth)
			switch r.URL.Path {
			case "/health", "/api/auth/session", "/api/auth/login", "/api/auth/logout":
				next.ServeHTTP(w, r)
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			if !auth.AuthenticateRequest(r, key) {
				writeError(w, http.StatusUnauthorized, "invalid or missing access key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func registerAuthRoutes(mux *http.ServeMux, key string) {
	mux.HandleFunc("GET /api/auth/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": auth.AuthenticateRequest(r, key)})
	})

	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token string `json:"token"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		if !auth.AccessKeyMatches(key, strings.TrimSpace(req.Token)) {
			writeError(w, http.StatusUnauthorized, "invalid access token")
			return
		}

		//nolint:gosec // Local HTTP deployments cannot use Secure cookies.
		http.SetCookie(w, &http.Cookie{
			Name:     auth.CookieName,
			Value:    auth.SessionValue(key),
			Path:     "/",
			HttpOnly: true,
			Secure:   auth.RequestIsHTTPS(r),
			SameSite: http.SameSiteStrictMode,
		})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Match the transport attributes used by the login cookie.
		http.SetCookie(w, &http.Cookie{
			Name:     auth.CookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   auth.RequestIsHTTPS(r),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}
