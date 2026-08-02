// Package service defines the transport-facing Web service contract and the
// service-owned authentication component.
package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

const CookieName = "aiscan_session"

// Auth owns the access-key policy shared by HTTP, ConnectRPC, WebSocket and
// the IOA browser bridge.
type Auth struct {
	accessKey string
}

func NewAuth(accessKey string) *Auth {
	return &Auth{accessKey: accessKey}
}

// Enabled reports whether authentication is enforced.
func (a *Auth) Enabled() bool {
	return a != nil && a.accessKey != ""
}

// Authenticate resolves an explicit bearer credential or browser session.
// An invalid explicit bearer never falls back to the cookie.
func (a *Auth) Authenticate(r *http.Request) bool {
	if a == nil || a.accessKey == "" {
		return true
	}
	if token, ok := BearerToken(r.Header.Get("Authorization")); ok {
		return AccessKeyMatches(a.accessKey, token)
	}
	if cookie, err := r.Cookie(CookieName); err == nil {
		return SessionMatches(a.accessKey, cookie.Value)
	}
	return false
}

// Middleware gates API requests while leaving health, login and static routes
// available to browsers.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	if !a.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health", "/api/auth/session", "/api/auth/login", "/api/auth/logout":
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if !a.Authenticate(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or missing access key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RegisterRoutes installs the browser session endpoints owned by Auth.
func (a *Auth) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": a.Authenticate(r)})
	})

	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Token string `json:"token"`
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !AccessKeyMatches(a.key(), strings.TrimSpace(request.Token)) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid access token"})
			return
		}

		//nolint:gosec // Local HTTP deployments cannot use Secure cookies.
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    SessionValue(a.key()),
			Path:     "/",
			HttpOnly: true,
			Secure:   RequestIsHTTPS(r),
			SameSite: http.SameSiteStrictMode,
		})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Match the transport attributes used by the login cookie.
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Path:     "/",
			HttpOnly: true,
			Secure:   RequestIsHTTPS(r),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

func (a *Auth) key() string {
	if a == nil {
		return ""
	}
	return a.accessKey
}

func BearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

func AccessKeyMatches(key, candidate string) bool {
	want := sha256.Sum256([]byte(key))
	got := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func SessionValue(key string) string {
	sum := sha256.Sum256([]byte("aiscan-web-session\x00" + key))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func SessionMatches(key, candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(SessionValue(key)), []byte(candidate)) == 1
}

func RequestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// ShareWithIOA maps an authenticated AIScan browser request to IOA's reserved
// browser token while preserving native IOA bearer identities.
func (a *Auth) ShareWithIOA(ioaToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webAuthenticated := a.Authenticate(r)
		if !a.Enabled() && r.Header.Get("Authorization") != "" {
			webAuthenticated = false
		}
		if !webAuthenticated || ioaToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		request := r.Clone(r.Context())
		request.Header = r.Header.Clone()
		request.Header.Set("Authorization", "Bearer "+ioaToken)
		if a.Enabled() {
			request.Header.Set("X-Access-Key", a.key())
		}
		next.ServeHTTP(w, request)
	})
}
