package service

import "net/http"

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
