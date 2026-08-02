package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShareWebAuthWithIOA(t *testing.T) {
	const accessKey = "test-token"
	const ioaToken = "ioa-web-token"

	var authorization, forwardedAccessKey string
	handler := shareWebAuthWithIOA(accessKey, ioaToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		forwardedAccessKey = r.Header.Get("X-Access-Key")
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ioa/nodes", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: SessionValue(accessKey)})
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
	handler := shareWebAuthWithIOA("test-token", "ioa-web-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ioa/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+nativeToken)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: SessionValue("test-token")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if authorization != "Bearer "+nativeToken {
		t.Fatalf("Authorization = %q, want native token", authorization)
	}
}
