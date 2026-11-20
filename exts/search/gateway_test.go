package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/exts/gateway"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestGatewaySearchAndConnectionUseSameUpstream(t *testing.T) {
	for _, source := range []string{"tavily", "duckduckgo"} {
		t.Run(source, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if source == "tavily" {
					if r.URL.Path != "/tenant/search" || r.Header.Get("Authorization") != "Bearer gateway-key" {
						t.Errorf("incorrect Tavily upstream request: %s %v", r.URL, r.Header)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["query"] == "fail" {
						w.WriteHeader(403)
						return
					}
					fmt.Fprint(w, `{"results":[{"title":"Fixture","url":"https://example.test","content":"Verified fixture"}]}`)
				} else {
					if r.URL.Path != "/tenant/html/" {
						t.Errorf("incorrect DDG upstream path: %s", r.URL.Path)
					}
					fmt.Fprint(w, `<a class="result__a" href="https://example.test">Fixture</a>`)
				}
			}))
			defer server.Close()
			fields := map[string]any{"base_url": server.URL + "/tenant/"}
			if source == "tavily" {
				fields["key"] = "gateway-key"
			}
			value := cfg.Values{source: fields}
			feature := NewExecution(Options{})
			tools := coretool.NewToolRegistry()
			set, err := extension.New(extension.Provided[egress.Endpoint](egress.Disabled()), extension.Provided[*hooks.Registry](hooks.New()), tools, coretool.NewCommandRegistry(), gateway.New(value, feature.Gateway()...), feature)
			if err != nil {
				t.Fatal(err)
			}
			if err := set.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = set.Close(context.Background()) })
			result, err := tools.ExecuteTool(t.Context(), "web_search", `{"query":"fixture"}`)
			if err != nil || !strings.Contains(coretool.ResultText(result), "https://example.test") {
				t.Fatalf("search=%v err=%v", result, err)
			}
			if source == "tavily" {
				extensions, err := cfg.ValuesToProto(cfg.Values{"gateway": map[string]any{source: fields}})
				if err != nil {
					t.Fatal(err)
				}
				checks := testConnection(t.Context(), &types.DistributeConfig{Extensions: extensions}, nil)
				if len(checks) != 1 || !checks[0].Ok {
					t.Fatalf("probe failed: %v", checks)
				}
				edited, err := cfg.ValuesToProto(cfg.Values{"gateway": map[string]any{source: map[string]any{"key": ""}}})
				if err != nil {
					t.Fatal(err)
				}
				checks = testConnection(t.Context(), &types.DistributeConfig{Extensions: edited}, &types.DistributeConfig{Extensions: extensions})
				if len(checks) != 1 || !checks[0].Ok {
					t.Fatalf("partial probe lost saved base URL or credential: %v", checks)
				}
				_, err = tools.ExecuteTool(t.Context(), "web_search", `{"query":"fail"}`)
				if err == nil || !strings.Contains(err.Error(), "403") {
					t.Fatalf("gateway error lost or silently fell back: %v", err)
				}
				if calls.Load() != 4 {
					t.Fatalf("unexpected upstream calls: %d", calls.Load())
				}
			}
		})
	}
}

func TestGatewaySearchDisabledDoesNotUseInheritedKey(t *testing.T) {
	feature := NewExecution(Options{TavilyKeys: "inherited-key"})
	tools := coretool.NewToolRegistry()
	values := cfg.Values{"tavily": {"enabled": false}, "duckduckgo": {"enabled": false}}
	set, err := extension.New(extension.Provided[egress.Endpoint](egress.Disabled()), extension.Provided[*hooks.Registry](hooks.New()), tools, coretool.NewCommandRegistry(), gateway.New(values, feature.Gateway()...), feature)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	_, err = tools.ExecuteTool(t.Context(), "web_search", `{"query":"fixture"}`)
	if err == nil || !strings.Contains(err.Error(), "DuckDuckGo is disabled") {
		t.Fatalf("disabled search used inherited credentials: %v", err)
	}
}
