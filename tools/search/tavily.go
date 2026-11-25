package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const tavilySearchURL = "https://api.tavily.com/search"

type tavilyRequest struct {
	Query                    string `json:"query"`
	MaxResults               int    `json:"max_results"`
	SearchDepth              string `json:"search_depth"`
	IncludeAnswer            bool   `json:"include_answer"`
	IncludeRawContent        bool   `json:"include_raw_content"`
	IncludeImageDescriptions bool   `json:"include_image_descriptions"`
}

type tavilyResponse struct {
	Answer  string         `json:"answer"`
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// doTavilyRequest issues a single Tavily search and decodes the response. Both
// searchTavily (with DuckDuckGo fallback) and ProbeTavily (no fallback) share
// this request/response plumbing; only the client and the result shaping differ.
func doTavilyRequest(ctx context.Context, client *http.Client, baseURL, apiKey string, reqBody tavilyRequest) (tavilyResponse, error) {
	var out tavilyResponse
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return out, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := tavilySearchURL
	if baseURL != "" {
		endpoint = strings.TrimRight(baseURL, "/") + "/search"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("tavily request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return out, fmt.Errorf("read tavily response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("tavily returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("parse tavily response: %w", err)
	}
	return out, nil
}

func (c *Client) searchTavily(ctx context.Context, query string, num int, apiKey string) (string, error) {
	resp, err := doTavilyRequest(ctx, c.client, c.config.BaseURL, apiKey, tavilyRequest{
		Query:         query,
		MaxResults:    num,
		SearchDepth:   "basic",
		IncludeAnswer: true,
	})
	if err != nil {
		return "", err
	}
	return formatTavilyResults(resp, query), nil
}

// ProbeTavily verifies a single Tavily API key by issuing a minimal search
// directly against the Tavily API. Unlike Execute, it never falls back to
// DuckDuckGo, so an invalid or exhausted key surfaces as an error instead of
// being silently masked by the fallback. A non-empty proxy routes the request.
// On success it returns a short human-readable detail (e.g. "1 result").
func ProbeTavily(ctx context.Context, apiKey, baseURL, proxy string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("tavily api key is empty")
	}

	client := &http.Client{Timeout: searchTimeout, Transport: proxyTransport(proxy)}
	if baseURL != "" {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	defer client.CloseIdleConnections()

	resp, err := doTavilyRequest(ctx, client, baseURL, apiKey, tavilyRequest{
		Query:       "ping",
		MaxResults:  1,
		SearchDepth: "basic",
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d results", len(resp.Results)), nil
}

func formatTavilyResults(resp tavilyResponse, query string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Web search results for: %s\n\n", query))

	if resp.Answer != "" {
		sb.WriteString(fmt.Sprintf("Summary: %s\n\n", resp.Answer))
	}

	if len(resp.Results) == 0 {
		sb.WriteString("No results found.\n")
		return sb.String()
	}

	for i, r := range resp.Results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("    URL: %s\n", r.URL))
		if r.Content != "" {
			snippet := r.Content
			if len(snippet) > 300 {
				snippet = snippet[:300] + "..."
			}
			sb.WriteString(fmt.Sprintf("    %s\n", snippet))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}
