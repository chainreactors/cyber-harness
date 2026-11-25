package jev

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestConnectionUsesConfiguredBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tenant/v1/systemone" || r.Header.Get("Authorization") != "Bearer gateway-key" {
			t.Errorf("unexpected JEV upstream request: %s %v", r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"answers":{"action":{"type":"choice","choice":"record"}}}`)
	}))
	defer server.Close()
	fields, err := structpb.NewStruct(map[string]any{"base_url": server.URL + "/tenant/", "api_key": "gateway-key"})
	if err != nil {
		t.Fatal(err)
	}
	checks := testConnection(t.Context(), &types.DistributeConfig{Extensions: map[string]*structpb.Struct{ConfigKey: fields}}, nil)
	if len(checks) != 1 || !checks[0].Ok {
		t.Fatalf("JEV gateway probe: %v", checks)
	}
	for _, baseURL := range []string{"https://key@example.test", "https://example.test?secret=value"} {
		if _, err := configSection.Decode(map[string]any{"base_url": baseURL}); err == nil {
			t.Fatalf("invalid base_url accepted: %s", baseURL)
		}
	}
}

type probeTransport func(*http.Request) (*http.Response, error)

func (f probeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConnectionEnvironmentAndSecretPrecedence(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "fixture-env-key")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	cases := []struct{ name, stored, incoming, want string }{
		{"environment", "", "", "fixture-env-key"},
		{"stored", "fixture-stored-key", "", "fixture-stored-key"},
		{"incoming", "fixture-stored-key", "fixture-incoming-key", "fixture-incoming-key"},
	}
	config := func(key string) *types.DistributeConfig {
		fields, err := structpb.NewStruct(map[string]any{"api_key": key})
		if err != nil {
			t.Fatal(err)
		}
		return &types.DistributeConfig{Extensions: map[string]*structpb.Struct{ConfigKey: fields}}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			http.DefaultTransport = probeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer "+tc.want {
					t.Error("wrong credential precedence")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"answers":{"action":{"type":"choice","choice":"record"}}}`))}, nil
			})
			checks := testConnection(t.Context(), config(tc.incoming), config(tc.stored))
			if len(checks) != 1 || !checks[0].Ok || calls != 1 {
				t.Fatalf("unexpected probe: %v calls=%d", checks, calls)
			}
		})
	}
	http.DefaultTransport = probeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("private-provider-response"))}, nil
	})
	checks := testConnection(t.Context(), config(""), nil)
	if len(checks) != 1 || checks[0].Ok || checks[0].Error != "JEV HTTP status 401" {
		t.Fatalf("fallback masqueraded as success: %v", checks)
	}
}

func TestConnectionMissingKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	checks := testConnection(t.Context(), nil, nil)
	if len(checks) != 1 || checks[0].Ok || checks[0].Error != "JEV API key is not configured" {
		t.Fatalf("unexpected probe: %v", checks)
	}
}

func TestConnectionUsesSectionValidationBeforeRequest(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	for _, values := range []map[string]any{
		{"unregistered_field": true}, {"enabled": "true"}, {"timeout": "-1s"},
		{"criteria": map[string]any{"record": ""}}, {"level": "unknown"},
	} {
		fields, err := structpb.NewStruct(values)
		if err != nil {
			t.Fatal(err)
		}
		checks := testConnection(t.Context(), &types.DistributeConfig{Extensions: map[string]*structpb.Struct{ConfigKey: fields}}, nil)
		if len(checks) != 1 || checks[0].Ok || checks[0].Error != "Invalid JEV configuration" {
			t.Fatalf("validation %v: %v", values, checks)
		}
	}
}
