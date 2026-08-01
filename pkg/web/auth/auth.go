// Package auth contains access-key and session authentication helpers shared
// by the HTTP, ConnectRPC and gRPC surfaces.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

const CookieName = "aiscan_session"

// AuthenticateRequest resolves the request credential against the access key.
// Explicit Bearer credentials take precedence: an invalid supplied header
// cannot silently fall back to a browser cookie. An empty key disables auth.
func AuthenticateRequest(r *http.Request, key string) bool {
	if key == "" {
		return true
	}
	if token, ok := BearerToken(r.Header.Get("Authorization")); ok {
		return AccessKeyMatches(key, token)
	}
	if cookie, err := r.Cookie(CookieName); err == nil {
		return SessionMatches(key, cookie.Value)
	}
	return false
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
