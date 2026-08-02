package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chainreactors/aiscan/pkg/web/auth"
)

func TestShareWebAuthWithIOA(t *testing.T) {
	const accessKey = "test-token"
	const ioaToken = "ioa-web-token"

	var authorization, forwardedAccessKey string
	handler := ShareWebAuthWithIOA(accessKey, ioaToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		forwardedAccessKey = r.Header.Get("X-Access-Key")
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ioa/nodes", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: auth.SessionValue(accessKey)})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if authorization != "Bearer "+ioaToken {
		t.Fatalf("Authorization = %q", authorization)
	}
	if forwardedAccessKey != accessKey {
		t.Fatalf("X-Access-Key = %q", forwardedAccessKey)
	}
}

func TestShareWebAuthWithIOAPreservesNativeIdentity(t *testing.T) {
	const nativeToken = "native-ioa-token"

	var authorization string
	handler := ShareWebAuthWithIOA("test-token", "ioa-web-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ioa/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+nativeToken)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: auth.SessionValue("test-token")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if authorization != "Bearer "+nativeToken {
		t.Fatalf("Authorization = %q, want native token", authorization)
	}
}
