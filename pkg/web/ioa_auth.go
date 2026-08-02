package web

import (
	"net/http"

	"github.com/chainreactors/aiscan/pkg/web/auth"
)

// ShareWebAuthWithIOA maps an authenticated AIScan Web request to the IOA
// node token reserved for the browser UI. Native IOA clients keep their own
// bearer tokens and continue through the IOA authentication middleware
// unchanged.
func ShareWebAuthWithIOA(accessKey, ioaToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webAuthenticated := auth.AuthenticateRequest(r, accessKey)
		if accessKey == "" && r.Header.Get("Authorization") != "" {
			// In auth-disabled development mode, preserve explicit IOA identities.
			webAuthenticated = false
		}
		if !webAuthenticated || ioaToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		request := r.Clone(r.Context())
		request.Header = r.Header.Clone()
		request.Header.Set("Authorization", "Bearer "+ioaToken)
		if accessKey != "" {
			request.Header.Set("X-Access-Key", accessKey)
		}
		next.ServeHTTP(w, request)
	})
}
