package search

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestLiveDeepSeekWebSearchTool(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("set DEEPSEEK_API_KEY to test the official Search API (billed per successful nonempty request)")
	}
	for _, profile := range []struct{ name, protocol, baseURL string }{
		{"vendor", "deepseek", "https://api.deepseek.com/v1"},
		{"openai", "openai", "https://api.deepseek.com"},
		{"anthropic", "anthropic", "https://api.deepseek.com/anthropic/v1"},
	} {
		if ok := t.Run(profile.name, func(t *testing.T) {
			cfg := provider.ProviderConfig{Provider: profile.protocol, BaseURL: profile.baseURL, APIKey: key, Timeout: 30}
			e, tools := loadSearchExtension(t, cfg)
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			// Observe the extension's own request and cancel failures before the
			// tool can try another backend. Successful output must contain the
			// summaries returned by DeepSeek, not just similar fallback URLs.
			transport := e.search.client.Transport
			defer func() { e.search.client.Transport = transport }()
			var sources *searchResponse
			e.search.client.Transport = webSearchTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() != "api.deepseek.com" || r.URL.Path != "/v1/tools/search" {
					t.Errorf("unexpected search endpoint: %s", r.URL)
					cancel()
				}
				resp, err := transport.RoundTrip(r)
				if err != nil {
					cancel()
					return nil, err
				}
				data, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				resp.Body = io.NopCloser(strings.NewReader(string(data)))
				if err != nil || resp.StatusCode != http.StatusOK {
					t.Errorf("official search: status=%d err=%v body=%s", resp.StatusCode, err, strings.ReplaceAll(string(data), key, "[REDACTED]"))
					cancel()
					return resp, err
				}
				sources, err = parseDeepSeekWebSearchResponse(data, 2)
				if err != nil || len(sources.Results) == 0 {
					t.Errorf("official search returned no verified results: %v", err)
					cancel()
				}
				return resp, nil
			})
			started := time.Now()
			result, err := tools.ExecuteTool(ctx, "web_search", `{"query":"DeepSeek Search API site:api-docs.deepseek.com","num":2}`)
			if err != nil {
				t.Fatal(strings.ReplaceAll(err.Error(), key, "[REDACTED]"))
			}
			if result == nil || result.IsError {
				t.Fatal("search did not return a successful tool result")
			}
			text := coretool.ResultText(result)
			if !strings.Contains(text, "[1]") || !strings.Contains(text, "URL: https://") || strings.Contains(text, "[3]") {
				t.Fatalf("expected 1-2 search results with source URLs: %s", strings.ReplaceAll(text, key, "[REDACTED]"))
			}
			if sources == nil {
				t.Fatal("the extension did not execute the official DeepSeek search")
			}
			for _, source := range sources.Results {
				if source.Summary == "" || !strings.Contains(text, source.URL) || !strings.Contains(text, source.Summary) {
					t.Fatal("official search source or summary was lost before reaching the tool result")
				}
			}
			t.Logf("official DeepSeek Search API via %s: %s\n%s", profile.protocol, time.Since(started).Round(time.Millisecond), strings.ReplaceAll(text, key, "[REDACTED]"))
		}); !ok {
			// Do not make more billable requests after an account or API failure.
			return
		}
	}
}
