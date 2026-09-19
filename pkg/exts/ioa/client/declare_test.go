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

	cfg "github.com/chainreactors/cyber/core/config"
	types "github.com/chainreactors/cyber/core/types"
)

func testDistributeConfig(t *testing.T, values cfg.Values) *types.DistributeConfig {
	t.Helper()
	extensions, err := cfg.ValuesToProto(values)
	if err != nil {
		t.Fatal(err)
	}
	return &types.DistributeConfig{Extensions: extensions}
}

// An access key cannot be verified read-only: the read endpoints reject access
// keys outright and accept only the token POST /auth/register issues, so a
// connectivity check has to exchange the credential before it can read. Both
// places the key may live — URL userinfo, and the token field — must work.
func TestConnectionExchangesTheAccessKey(t *testing.T) {
	for _, test := range []struct {
		name    string
		credent func(string) cfg.Values
	}{
		{"endpoint userinfo", func(endpoint string) cfg.Values {
			return cfg.Values{ConfigKey: {"url": "http://access-key@" + strings.TrimPrefix(endpoint, "http://")}}
		}},
		{"token field", func(endpoint string) cfg.Values {
			return cfg.Values{ConfigKey: {"url": endpoint, "token": "access-key"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var reads, registrations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/auth/register":
					var body struct {
						AccessKey string `json:"access_key"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.AccessKey != "access-key" {
						http.Error(w, "invalid access key", http.StatusUnauthorized)
						return
					}
					registrations.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "node-1", "name": "worker", "token": "issued-token"})
				case r.Method == http.MethodGet && r.URL.Path == "/spaces":
					if r.Header.Get("Authorization") != "Bearer issued-token" {
						http.Error(w, "authorization token required", http.StatusUnauthorized)
						return
					}
					reads.Add(1)
					fmt.Fprint(w, `[{"id":"one","name":"one"}]`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			checks := testConnection(t.Context(), testDistributeConfig(t, test.credent(server.URL)), nil)
			if len(checks) != 1 || !checks[0].Ok || !strings.Contains(checks[0].Detail, "1 space") {
				t.Fatalf("connection test = %+v", checks)
			}
			if registrations.Load() != 1 || reads.Load() != 1 {
				t.Fatalf("requests: registrations=%d reads=%d", registrations.Load(), reads.Load())
			}
		})
	}
}

func TestConnectionSuccess(t *testing.T) {
	var registrations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/nodes":
			registrations.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "node-1", "name": "worker"})
		case r.URL.Path == "/spaces":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "1", "name": "default", "nodes": []any{}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	checks := testConnection(context.Background(), testDistributeConfig(t, cfg.Values{ConfigKey: {"url": server.URL}}), nil)
	if len(checks) != 1 || !checks[0].Ok || !strings.Contains(checks[0].Detail, "1 space") {
		t.Fatalf("connection test = %+v", checks)
	}
	if registrations.Load() != 1 {
		t.Fatalf("registrations = %d", registrations.Load())
	}
}
