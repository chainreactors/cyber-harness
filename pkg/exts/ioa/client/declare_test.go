package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	types "github.com/chainreactors/cyber/core/types"
)

func TestConnectionUsesReadOnlyExtension(t *testing.T) {
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/spaces" {
			writes.Add(1)
			http.Error(w, "connection test must not register", http.StatusMethodNotAllowed)
			return
		}
		reads.Add(1)
		if r.Header.Get("Authorization") != "Bearer stored-token" {
			http.Error(w, "wrong token", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `[{"id":"one","name":"one"}]`)
	}))
	defer server.Close()
	stored := &types.DistributeConfig{Ioa: &types.IOAConfig{Url: server.URL, Token: "stored-token"}}
	for range 2 {
		checks := testConnection(t.Context(), &types.DistributeConfig{}, stored)
		if len(checks) != 1 || !checks[0].Ok {
			t.Fatalf("connection test = %v", checks)
		}
	}
	if reads.Load() != 2 || writes.Load() != 0 {
		t.Fatalf("connection requests: reads=%d writes=%d", reads.Load(), writes.Load())
	}
}

func TestConnectionSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spaces" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "1", "name": "default", "nodes": []any{}}})
	}))
	defer server.Close()
	checks := testConnection(context.Background(), &types.DistributeConfig{Ioa: &types.IOAConfig{Url: server.URL, Token: "t"}}, nil)
	if !checks[0].Ok || !strings.Contains(checks[0].Detail, "1 space") {
		t.Fatalf("connection test = %+v", checks)
	}
}
