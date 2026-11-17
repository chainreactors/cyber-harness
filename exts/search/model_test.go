package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestModelSearchProtocolContracts(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					Model     string `json:"model"`
					Input     string `json:"input"`
					MaxTokens int    `json:"max_tokens"`
					Tools     []struct {
						Type    string `json:"type"`
						Name    string `json:"name"`
						MaxUses int    `json:"max_uses"`
					} `json:"tools"`
					Messages []struct{ Role, Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if r.Method != http.MethodPost || body.Model != "fixture-model" || len(body.Tools) != 1 {
					t.Errorf("unexpected search request: %s %+v", r.Method, body)
					return
				}
				if protocol == "openai" {
					if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture" || body.Input != "Search the web for: Go" || body.Tools[0].Type != "web_search" {
						t.Error("OpenAI search request contract changed")
					}
					fmt.Fprint(w, `{"output":[{"type":"web_search_call","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"Verified answer","annotations":[{"type":"url_citation","title":"Go","url":"https://go.dev"}]}]}]}`)
				} else {
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "fixture" || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "web-search-2025-03-05" || body.MaxTokens != 4096 || body.Tools[0].Type != "web_search_20250305" || body.Tools[0].Name != "web_search" || body.Tools[0].MaxUses != 2 || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "Search the web for: Go" {
						t.Error("Anthropic search request contract changed")
					}
					fmt.Fprint(w, `{"content":[{"type":"web_search_tool_result","content":[{"title":"Go","url":"https://go.dev"}]},{"type":"text","text":"Verified answer"}]}`)
				}
			}))
			defer server.Close()
			cfg := provider.ProviderConfig{Provider: protocol, BaseURL: server.URL + "/v1", APIKey: "fixture", Model: "fixture-model"}
			_, tools := loadSearchExtension(t, cfg)
			result, err := tools.ExecuteTool(t.Context(), "web_search", `{"query":"Go","num":2}`)
			if err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			// A successful registered tool returns both sources and model summary.
			text := coretool.ResultText(result)
			for _, want := range []string{"[1] Go", "https://go.dev", "Summary:\nVerified answer"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q in %s", want, text)
				}
			}
		})
	}
}

func TestModelSearchCachesUnsupportedBackend(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			search, err := newModelSearch(provider.ProviderConfig{Provider: protocol, BaseURL: "https://fixture.invalid/v1"})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			search.client.Transport = webSearchTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"not supported"}}`))}, nil
			})
			if _, err := search.search(t.Context(), "Go", 2); err == nil {
				t.Fatal("unsupported backend accepted")
			}
			if _, err := search.search(t.Context(), "Go", 2); err == nil || !strings.Contains(err.Error(), "does not support") || calls != 1 {
				t.Fatalf("unsupported backend was probed again: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestModelSearchConfiguredTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	search, err := newModelSearch(provider.ProviderConfig{Provider: "deepseek", BaseURL: server.URL, Timeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer search.client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = search.search(ctx, "Go", 2)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("configured timeout not honored: err=%v parent=%v", err, ctx.Err())
	}
}

func TestSearchExtensionClosesOwnConnections(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[]}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	server.Start()
	defer server.Close()
	e, tools := loadSearchExtension(t, provider.ProviderConfig{Provider: "deepseek", BaseURL: server.URL, APIKey: "fixture"})
	if _, err := tools.ExecuteTool(t.Context(), "web_search", `{"query":"Go"}`); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("search extension did not close its own idle HTTP connection")
	}
}
