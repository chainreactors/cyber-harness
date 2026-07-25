package web

import (
	"bytes"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
)

func TestAccessKeyAuthBrowserSession(t *testing.T) {
	mux := http.NewServeMux()
	registerAuthRoutes(mux, "test-token")
	mux.HandleFunc("GET /api/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	server := httptest.NewServer(AccessKeyAuth("test-token")(mux))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	assertStatus(t, client, http.MethodGet, server.URL+"/api/auth/session", nil, http.StatusOK)
	assertStatus(t, client, http.MethodGet, server.URL+"/api/protected", nil, http.StatusUnauthorized)
	// URL credentials are deliberately unsupported: they leak through browser
	// history, referrers, access logs, and screenshots.
	assertStatus(t, client, http.MethodGet, server.URL+"/api/protected?access_key=test-token", nil, http.StatusUnauthorized)

	loginBody := bytes.NewBufferString(`{"token":"test-token"}`)
	assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login", loginBody, http.StatusOK)
	assertStatus(t, client, http.MethodGet, server.URL+"/api/protected", nil, http.StatusNoContent)

	assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/logout", nil, http.StatusOK)
	assertStatus(t, client, http.MethodGet, server.URL+"/api/protected", nil, http.StatusUnauthorized)
}

func TestAccessKeyAuthBearerStillSupported(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := AccessKeyAuth("test-token")(next)

	valid := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	valid.Header.Set("Authorization", "Bearer test-token")
	validRecorder := httptest.NewRecorder()
	handler.ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusNoContent {
		t.Fatalf("valid bearer status = %d, want %d", validRecorder.Code, http.StatusNoContent)
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	invalid.Header.Set("Authorization", "Bearer wrong-token")
	invalid.AddCookie(&http.Cookie{Name: authCookieName, Value: sessionValue("test-token")})
	invalidRecorder := httptest.NewRecorder()
	handler.ServeHTTP(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer with valid cookie status = %d, want %d", invalidRecorder.Code, http.StatusUnauthorized)
	}
}

func TestLoginCookieSecurityAttributes(t *testing.T) {
	mux := http.NewServeMux()
	registerAuthRoutes(mux, "test-token")
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"token":"test-token"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	result := recorder.Result()
	defer result.Body.Close()
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("unsafe auth cookie: %#v", cookie)
	}
	if cookie.Value == "test-token" {
		t.Fatal("auth cookie contains the raw access token")
	}
}

func TestAuthenticate(t *testing.T) {
	req := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/x", nil) }

	if !authenticate(req(), "") {
		t.Fatal("empty key must authenticate (dev mode)")
	}

	bearer := req()
	bearer.Header.Set("Authorization", "Bearer test-token")
	if !authenticate(bearer, "test-token") {
		t.Fatal("valid bearer rejected")
	}

	// An invalid bearer must not fall back to a valid cookie.
	mixed := req()
	mixed.Header.Set("Authorization", "Bearer wrong-token")
	mixed.AddCookie(&http.Cookie{Name: authCookieName, Value: sessionValue("test-token")})
	if authenticate(mixed, "test-token") {
		t.Fatal("invalid bearer fell back to cookie")
	}

	cookie := req()
	cookie.AddCookie(&http.Cookie{Name: authCookieName, Value: sessionValue("test-token")})
	if !authenticate(cookie, "test-token") {
		t.Fatal("valid session cookie rejected")
	}

	if authenticate(req(), "test-token") {
		t.Fatal("credential-less request authenticated")
	}
}

func assertStatus(t *testing.T, client *http.Client, method, url string, body *bytes.Buffer, want int) {
	t.Helper()
	var requestBody *bytes.Buffer
	if body != nil {
		requestBody = body
	} else {
		requestBody = bytes.NewBuffer(nil)
	}
	req, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("%s %s status = %d, want %d", method, url, res.StatusCode, want)
	}
}
