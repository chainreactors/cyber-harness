package service

import (
	"context"
	"github.com/chainreactors/cyber/pkg/web"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOptionalRoutesAndConflicts(t *testing.T) {
	svc := NewService(ServiceConfig{})
	defer svc.Close(context.Background())
	fixture := web.Route{Source: "fixture", Pattern: "/fixture/", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	handler, err := web.NewHandler(svc.Auth(), nil, fixture)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/fixture/": http.StatusNoContent, "/ioa/": http.StatusNotFound} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Fatalf("%s = %d, want %d", path, recorder.Code, want)
		}
	}
	if _, err := web.NewHandler(svc.Auth(), nil, fixture, fixture); err == nil {
		t.Fatal("duplicate route accepted")
	}
	if _, err := web.NewHandler(svc.Auth(), nil, web.Route{Source: "fixture", Pattern: "GET /health", Handler: fixture.Handler}); err == nil {
		t.Fatal("built-in route collision accepted")
	}
}
