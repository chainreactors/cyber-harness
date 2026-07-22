package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

const authCookieName = "aiscan_session"

// authenticate resolves the request credential against the access key.
// Explicit Bearer credentials take precedence: an invalid supplied header
// cannot silently fall back to a browser cookie. An empty key disables auth.
func authenticate(r *http.Request, key string) bool {
	if key == "" {
		return true
	}
	if token, ok := bearerToken(r.Header.Get("Authorization")); ok {
		return accessKeyMatches(key, token)
	}
	if cookie, err := r.Cookie(authCookieName); err == nil {
		return sessionMatches(key, cookie.Value)
	}
	return false
}

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

			if !authenticate(r, key) {
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
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": authenticate(r, key)})
	})

	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token string `json:"token"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		if !accessKeyMatches(key, strings.TrimSpace(req.Token)) {
			writeError(w, http.StatusUnauthorized, "invalid access token")
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     authCookieName,
			Value:    sessionValue(key),
			Path:     "/",
			HttpOnly: true,
			Secure:   requestIsHTTPS(r),
			SameSite: http.SameSiteStrictMode,
		})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     authCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   requestIsHTTPS(r),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

func accessKeyMatches(key, candidate string) bool {
	want := sha256.Sum256([]byte(key))
	got := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func sessionValue(key string) string {
	sum := sha256.Sum256([]byte("aiscan-web-session\x00" + key))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func sessionMatches(key, candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(sessionValue(key)), []byte(candidate)) == 1
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}
