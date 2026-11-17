package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/proxyclient"
)

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary,omitempty"`
}

type searchResponse struct {
	Results []searchResult `json:"results,omitempty"`
	Summary string         `json:"summary,omitempty"`
}

// modelSearch owns a search-only HTTP client and a snapshot of the active
// profile. Chat providers do not implement or execute search requests.
type modelSearch struct {
	config   provider.ProviderConfig
	client   *http.Client
	disabled atomic.Bool
}

func newModelSearch(cfg provider.ProviderConfig) (*modelSearch, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = provider.ProviderBaseURL(cfg.Provider)
	}
	transport := &http.Transport{IdleConnTimeout: 90 * time.Second}
	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid search proxy URL: %w", err)
		}
		dial, err := proxyclient.NewClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("create search proxy client: %w", err)
		}
		transport.DialContext = dial.DialContext
	}
	timeout := 120 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	return &modelSearch{config: cfg, client: &http.Client{Transport: transport, Timeout: timeout}}, nil
}

// search matches the tool's callback directly, including the source formatting.
func (s *modelSearch) search(ctx context.Context, query string, maxResults int) (string, error) {
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}
	endpoint := deepSeekSearchEndpoint(s.config)
	deepseek := endpoint != ""
	if !deepseek && s.disabled.Load() {
		return "", fmt.Errorf("provider does not support server-side web search")
	}
	var payload any
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Authorization", "Bearer "+s.config.APIKey)
	protocol := provider.ProtocolOf(s.config.Provider)
	if protocol == "" {
		protocol = provider.InferFromBaseURL(s.config.BaseURL)
	}
	switch {
	case deepseek:
		if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 128 {
			return "", fmt.Errorf("DeepSeek web search query must contain 1-128 Unicode characters and must not be all whitespace")
		}
		payload = map[string]any{"query": query, "count": maxResults}
	case protocol == provider.ProviderAnthropic:
		endpoint = strings.TrimRight(s.config.BaseURL, "/")
		if !strings.HasSuffix(endpoint, "/messages") {
			endpoint += "/messages"
		}
		headers.Del("Authorization")
		headers.Set("x-api-key", s.config.APIKey)
		headers.Set("anthropic-version", "2023-06-01")
		headers.Set("anthropic-beta", "web-search-2025-03-05")
		payload = map[string]any{
			"model": s.config.Model, "max_tokens": 4096,
			"tools":    []map[string]any{{"type": "web_search_20250305", "name": "web_search", "max_uses": maxResults}},
			"messages": []map[string]string{{"role": "user", "content": "Search the web for: " + query}},
		}
	default:
		endpoint = strings.TrimRight(s.config.BaseURL, "/") + "/responses"
		payload = map[string]any{
			"model": s.config.Model, "input": "Search the web for: " + query,
			"tools": []map[string]any{{"type": "web_search", "search_context_size": "medium"}},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal search request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create search request: %w", err)
	}
	req.Header = headers
	// Preserve the existing unsupported-provider cache for model-backed search.
	// DeepSeek's standalone API remains usable after transient failures.
	var response *searchResponse
	defer func() {
		if !deepseek && response == nil {
			s.disabled.Store(true)
		}
	}()
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search HTTP request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read search response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &provider.APIError{StatusCode: resp.StatusCode, Message: string(data), Header: resp.Header.Clone()}
	}
	switch {
	case deepseek:
		response, err = parseDeepSeekWebSearchResponse(data, maxResults)
	case protocol == provider.ProviderAnthropic:
		response, err = parseAnthropicWebSearchResponse(data)
	default:
		response, err = parseOpenAIWebSearchResponse(data, maxResults)
	}
	if err != nil {
		return "", err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Web search results for: %s\n\n", query)
	if len(response.Results) == 0 && response.Summary == "" {
		text.WriteString("No results found.\n")
	}
	for index, result := range response.Results {
		fmt.Fprintf(&text, "[%d] %s\n    URL: %s\n", index+1, result.Title, result.URL)
		if result.Summary != "" {
			fmt.Fprintf(&text, "    %s\n", result.Summary)
		}
		text.WriteByte('\n')
	}
	if response.Summary != "" {
		text.WriteString("Summary:\n")
		text.WriteString(response.Summary)
		text.WriteByte('\n')
	}
	return text.String(), nil
}
