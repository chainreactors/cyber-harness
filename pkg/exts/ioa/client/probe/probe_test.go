package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/aiscan/pkg/types"
)

func TestIOAProbeUsesReadOnlyExtension(t *testing.T) {
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/spaces" {
			writes.Add(1)
			http.Error(w, "probe must not register", http.StatusMethodNotAllowed)
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
		checks := Check(t.Context(), &types.DistributeConfig{}, stored)
		if len(checks) != 1 || !checks[0].Ok {
			t.Fatalf("probe = %v", checks)
		}
	}
	if reads.Load() != 2 || writes.Load() != 0 {
		t.Fatalf("probe requests: reads=%d writes=%d", reads.Load(), writes.Load())
	}
}

func TestProbeIOASuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spaces" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "1", "name": "default", "nodes": []any{}}})
	}))
	defer srv.Close()

	resp := Check(context.Background(), &types.DistributeConfig{Ioa: &types.IOAConfig{Url: srv.URL, Token: "t"}}, nil)
	c := resp[0]
	if !c.Ok {
		t.Fatalf("expected ioa ok, got %+v", resp)
	}
	if !strings.Contains(c.Detail, "1 space") {
		t.Fatalf("expected space count in detail, got %q", c.Detail)
	}
}
