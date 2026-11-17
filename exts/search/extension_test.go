package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func loadSearchExtension(t *testing.T, cfg provider.ProviderConfig) (*Extension, *coretool.ToolRegistry) {
	t.Helper()
	p, err := provider.NewProvider(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.(interface{ CloseIdleConnections() }).CloseIdleConnections() })
	var state provider.State
	state.Set(p, cfg)
	tools := coretool.NewToolRegistry()
	e := New(Options{})
	set, err := extension.New(
		extension.Provided[*provider.State](&state),
		extension.Provided[egress.Endpoint](egress.Disabled()),
		extension.Provided[*hooks.Registry](hooks.New()),
		tools, coretool.NewCommandRegistry(), e,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	definitions := tools.ToolDefinitions()
	if len(definitions) != 1 || definitions[0].Name != "web_search" || definitions[0].Type != "function" {
		t.Fatalf("search must expose exactly one web_search function: %v", definitions)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(definitions[0].InputSchema.GetData(), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) != 2 || schema.Properties["query"] == nil || schema.Properties["num"] == nil || len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Fatalf("web_search must keep its query/num contract: %+v", schema)
	}
	return e, tools
}

func TestDeepSeekWebSearchTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/search" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("unexpected search request: %s %v", r.URL, r.Header)
		}
		var body struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Query == "no hits" {
			fmt.Fprint(w, `{"results":[]}`)
			return
		}
		if body.Query != "Go releases" || body.Count != 1 {
			t.Errorf("unexpected search arguments: %+v", body)
		}
		fmt.Fprint(w, `{"results":[{"title":"Go downloads","url":"https://go.dev/dl/","summary":"Download the latest Go release from the official website."},{"title":"Extra result","url":"https://go.dev/","summary":"This result exceeds the requested limit."}]}`)
	}))
	defer server.Close()
	cfg := provider.ProviderConfig{Provider: "deepseek", BaseURL: server.URL + "/v1", APIKey: "fixture"}
	_, tools := loadSearchExtension(t, cfg)
	for _, query := range []string{"Go releases", "no hits"} {
		args, _ := json.Marshal(map[string]any{"query": query, "num": 1})
		result, err := tools.ExecuteTool(t.Context(), "web_search", string(args))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		text := coretool.ResultText(result)
		if query == "no hits" {
			if !strings.Contains(text, "No results found.") {
				t.Fatalf("empty search not surfaced: %s", text)
			}
			continue
		}
		for _, want := range []string{"[1] Go downloads", "https://go.dev/dl/", "Download the latest Go release from the official website."} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q in model feedback: %s", want, text)
			}
		}
		if strings.Contains(text, "Extra result") {
			t.Fatalf("result limit ignored: %s", text)
		}
	}
}
