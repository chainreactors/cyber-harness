package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
)

type webSearchTransport func(*http.Request) (*http.Response, error)

func (f webSearchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeepSeekWebSearch(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, baseURL, endpoint string
		limit, count                      int
	}{
		{"vendor default", "deepseek", "", "https://api.deepseek.com/v1/tools/search", 0, 5},
		{"official root", "openai", "https://api.deepseek.com", "https://api.deepseek.com/v1/tools/search", 2, 2},
		{"official v1", "openai", "https://api.deepseek.com/v1/", "https://api.deepseek.com/v1/tools/search", 50, 10},
		{"official beta", "openai", "https://api.deepseek.com/beta", "https://api.deepseek.com/v1/tools/search", -1, 5},
		{"anthropic", "anthropic", "https://api.deepseek.com/anthropic", "https://api.deepseek.com/v1/tools/search", 2, 2},
		{"anthropic v1", "anthropic", "https://api.deepseek.com/anthropic/v1/", "https://api.deepseek.com/v1/tools/search", 2, 2},
		{"anthropic messages", "anthropic", "https://api.deepseek.com/anthropic/v1/messages", "https://api.deepseek.com/v1/tools/search", 2, 2},
		{"custom vendor root", "deepseek", "https://gateway.example", "https://gateway.example/v1/tools/search", 2, 2},
		{"custom vendor prefix", "deepseek", "https://gateway.example/deepseek/v1/?tenant=test", "https://gateway.example/deepseek/v1/tools/search?tenant=test", 2, 2},
		{"custom vendor anthropic prefix", "deepseek", "https://gateway.example/anthropic/v1", "https://gateway.example/anthropic/v1/tools/search", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := newModelSearch(provider.ProviderConfig{Provider: tc.protocol, BaseURL: tc.baseURL, APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			query := `DeepSeek 搜索 site:api-docs.deepseek.com after:2026`
			client := &http.Client{Transport: webSearchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.String() != tc.endpoint {
					t.Errorf("request = %s %s; want POST %s", r.Method, r.URL, tc.endpoint)
				}
				if r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("x-api-key") != "" {
					t.Errorf("unexpected search headers: %v", r.Header)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 2 || body["query"] != query || body["count"] != float64(tc.count) {
					t.Errorf("unexpected search body: %v", body)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"DeepSeek","url":"https://api-docs.deepseek.com","summary":"官方文档摘要","snippet":"Short snippet","content":"","rerank_score":2.4,"site_name":null,"date":null,"authority":3}]}`))}, nil
			})}
			p.client = client
			result, err := p.search(t.Context(), query, tc.limit)
			if err != nil || strings.Count(result, "URL: ") != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !strings.Contains(result, "[1] DeepSeek") || !strings.Contains(result, "https://api-docs.deepseek.com") || !strings.Contains(result, "官方文档摘要") {
				t.Fatalf("search result lost source details: %+v", result)
			}
		})
	}
}

func TestDeepSeekWebSearchResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantCount  int
		wantError  string
	}{
		{"no hits", `{"results":[]}`, 0, ""},
		{"limit and title fallback", `{"results":[{"url":"https://go.dev","summary":"Go documentation"},{"title":"Extra","url":"https://go.dev/dl"}]}`, 1, ""},
		{"missing", `{}`, 0, "no results array"},
		{"null", `{"results":null}`, 0, "no results array"},
		{"chat answer", `{"choices":[{"message":{"content":"I searched the web"}}]}`, 0, "no results array"},
		{"wrong shape", `{"results":{}}`, 0, "parse DeepSeek"},
		{"invalid JSON", `{`, 0, "parse DeepSeek"},
		{"missing URL", `{"results":[{"title":"Guess","summary":"Unverified"}]}`, 0, "no URL"},
		{"API error", `{"error":{"type":"invalid_request_error","message":"Search unavailable"}}`, 0, "Search unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := newModelSearch(provider.ProviderConfig{Provider: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			p.client.Transport = webSearchTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			result, err := p.search(t.Context(), "Go", 1)
			if tc.wantError != "" {
				if err == nil || result != "" || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("result=%+v err=%v; want %q", result, err, tc.wantError)
				}
				return
			}
			if err != nil || result == "" || strings.Count(result, "URL: ") != tc.wantCount {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tc.wantCount > 0 && (!strings.Contains(result, "[1] https://go.dev") || !strings.Contains(result, "Go documentation")) {
				t.Fatalf("title fallback or summary lost: %s", result)
			}
		})
	}
}

func TestDeepSeekWebSearchQueryLimits(t *testing.T) {
	for _, query := range []string{"", " \t\n", strings.Repeat("a", 129), strings.Repeat("搜", 129), strings.Repeat("a", 128), strings.Repeat("搜", 128)} {
		p, err := newModelSearch(provider.ProviderConfig{Provider: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		called := false
		p.client.Transport = webSearchTransport(func(*http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
		})
		_, err = p.search(t.Context(), query, 5)
		valid := query == strings.Repeat("a", 128) || query == strings.Repeat("搜", 128)
		if called != valid || (err == nil) != valid {
			t.Fatalf("query %q: called=%v err=%v; valid=%v", query, called, err, valid)
		}
	}
}

func TestDeepSeekWebSearchTransientFailureDoesNotDisable(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, status := range []int{http.StatusTooManyRequests, http.StatusGatewayTimeout} {
			t.Run(fmt.Sprintf("%s/%d", protocol, status), func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: webSearchTransport(func(*http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Try later"}}`))}, nil
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
				})}
				p, err := newModelSearch(provider.ProviderConfig{Provider: protocol, BaseURL: "https://api.deepseek.com/v1", APIKey: "fixture"})
				if err != nil {
					t.Fatal(err)
				}
				p.client = client
				_, err = p.search(t.Context(), "Go", 5)
				var apiErr *provider.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != status || calls != 1 {
					t.Fatalf("API failure lost or retried automatically: calls=%d err=%v", calls, err)
				}
				if _, err = p.search(t.Context(), "Go", 5); err != nil || calls != 2 {
					t.Fatalf("search disabled after transient failure: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestDeepSeekWebSearchCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Query string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Query == "slow" {
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer server.Close()
	p, err := newModelSearch(provider.ProviderConfig{Provider: "deepseek", BaseURL: server.URL, APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.search(ctx, "slow", 5); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := p.search(t.Context(), "Go", 5); err != nil {
		t.Fatalf("search disabled after cancellation: %v", err)
	}
}

func TestWebSearchOtherProvidersKeepTheirProtocol(t *testing.T) {
	for _, tc := range []struct{ protocol, baseURL, path, body string }{
		{"openai", "https://gateway.example/v1", "/v1/responses", `{"output":[{"type":"web_search_call","status":"completed"}]}`},
		{"openai", "https://api.deepseek.com.example/v1", "/v1/responses", `{"output":[{"type":"web_search_call","status":"completed"}]}`},
		{"anthropic", "https://gateway.example/v1", "/v1/messages", `{"content":[{"type":"web_search_tool_result","content":[]}]}`},
	} {
		t.Run(tc.protocol+"/"+tc.baseURL, func(t *testing.T) {
			p, err := newModelSearch(provider.ProviderConfig{Provider: tc.protocol, BaseURL: tc.baseURL, Model: "deepseek-flash", APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: webSearchTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tc.path {
					t.Errorf("path=%s; want %s", r.URL.Path, tc.path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			p.client = client
			if _, err := p.search(t.Context(), "Go", 5); err != nil {
				t.Fatal(err)
			}
		})
	}
}
