package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
)

const (
	ddgSearchURL    = "https://html.duckduckgo.com/html/"
	ddgUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	searchTimeout   = 30 * time.Second
	maxResponseBody = truncate.MaxResponseBody

	defaultNumResults = 5
	maxNumResults     = 10

	tavilySearchURL = "https://api.tavily.com/search"
)

type searchBackend int

const (
	backendDDG searchBackend = iota
	backendTavily
)

type TavilySearch struct {
	client     *http.Client
	backend    searchBackend
	apiKey     string
	apiKeys    []string
	currentKey int
	mu         sync.Mutex
}

func NewTavilySearch(builtinKeys string) *TavilySearch {
	c := &TavilySearch{
		client: &http.Client{
			Timeout:   searchTimeout,
			Transport: proxyTransport(""),
		},
	}

	var keys []string
	seen := make(map[string]struct{})

	addKeys := func(raw string) {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}

	addKeys(os.Getenv("TAVILY_API_KEY"))
	addKeys(os.Getenv("TAVILY_API_KEYS"))
	addKeys(builtinKeys)

	if len(keys) > 0 {
		c.backend = backendTavily
		c.apiKeys = keys
		c.apiKey = keys[0]
	}
	return c
}

// proxyTransport builds an HTTP transport that routes through proxy when it is a
// valid URL, falling back to the environment proxy for an empty or unparseable
// value.
func proxyTransport(proxy string) *http.Transport {
	t := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			t.Proxy = http.ProxyURL(u)
		}
	}
	return t
}

func (c *TavilySearch) SetProxy(proxyURLStr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client.Transport = proxyTransport(proxyURLStr)
}

func (c *TavilySearch) rotateKey() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.apiKeys) <= 1 {
		return false
	}
	c.currentKey = (c.currentKey + 1) % len(c.apiKeys)
	c.apiKey = c.apiKeys[c.currentKey]
	return true
}

// currentAPIKey snapshots the active key index and value under the lock. The
// agent runs tool calls in parallel, so a shared *TavilySearch may have Execute
// racing rotateKey; callers must read the key through here rather than touch
// c.apiKey/c.currentKey directly.
func (c *TavilySearch) currentAPIKey() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentKey, c.apiKey
}

func (c *TavilySearch) Execute(ctx context.Context, args []string) (string, error) {
	query, num, err := parseTavilyArgs(args)
	if err != nil {
		return "", err
	}

	switch c.backend {
	case backendTavily:
		startKey, key := c.currentAPIKey()
		for {
			result, err := c.searchTavily(ctx, query, num, key)
			if err == nil {
				return result, nil
			}
			if !isKeyExhausted(err) {
				break
			}
			if !c.rotateKey() {
				break
			}
			var idx int
			if idx, key = c.currentAPIKey(); idx == startKey {
				break
			}
		}
		fallback, fbErr := c.searchDDG(ctx, query, num)
		if fbErr != nil {
			return "", fmt.Errorf("tavily keys exhausted; ddg fallback: %w", fbErr)
		}
		return fallback, nil
	default:
		return c.searchDDG(ctx, query, num)
	}
}

func parseTavilyArgs(args []string) (query string, num int, err error) {
	num = defaultNumResults

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--num":
			i++
			if i >= len(args) {
				return "", 0, fmt.Errorf("search tavily: --num requires a value")
			}
			value := args[i] //nolint:gosec // G602: bounds checked by i >= len(args) above
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return "", 0, fmt.Errorf("search tavily: invalid --num value: %s", value)
			}
			num = n
		default:
			if strings.HasPrefix(args[i], "--") {
				return "", 0, fmt.Errorf("search tavily: unknown flag: %s", args[i])
			}
			if query == "" {
				query = args[i]
			} else {
				query += " " + args[i]
			}
		}
	}

	if query == "" {
		return "", 0, fmt.Errorf("search tavily: query is required\n\nUsage: search tavily <query> [--num <N>]")
	}
	if num <= 0 {
		num = defaultNumResults
	}
	if num > maxNumResults {
		num = maxNumResults
	}
	return query, num, nil
}

func isKeyExhausted(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 401") ||
		strings.Contains(msg, "HTTP 429") ||
		strings.Contains(msg, "HTTP 403")
}

// ---------------------------------------------------------------------------
// Tavily API backend
// ---------------------------------------------------------------------------

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
	Query   string         `json:"query"`
}

type tavilyResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// doTavilyRequest issues a single Tavily search and decodes the response. Both
// searchTavily (with DuckDuckGo fallback) and ProbeTavily (no fallback) share
// this request/response plumbing; only the client and the result shaping differ.
func doTavilyRequest(ctx context.Context, client *http.Client, apiKey string, reqBody tavilyRequest) (tavilyResponse, error) {
	var out tavilyResponse
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return out, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilySearchURL, strings.NewReader(string(bodyBytes)))
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

func (c *TavilySearch) searchTavily(ctx context.Context, query string, num int, apiKey string) (string, error) {
	resp, err := doTavilyRequest(ctx, c.client, apiKey, tavilyRequest{
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
func ProbeTavily(ctx context.Context, apiKey, proxy string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("tavily api key is empty")
	}

	client := &http.Client{Timeout: searchTimeout, Transport: proxyTransport(proxy)}

	resp, err := doTavilyRequest(ctx, client, apiKey, tavilyRequest{
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

// ---------------------------------------------------------------------------
// DuckDuckGo HTML backend (fallback)
// ---------------------------------------------------------------------------

func (c *TavilySearch) searchDDG(ctx context.Context, query string, num int) (string, error) {
	form := url.Values{"q": {query}, "b": {""}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ddgSearchURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", ddgUserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("DuckDuckGo request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DuckDuckGo returned HTTP %d", resp.StatusCode)
	}

	return parseDDGHTML(string(body), query, num)
}

type ddgResult struct {
	Title   string
	URL     string
	Snippet string
}

var (
	ddgLinkRe    = regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
)

func parseDDGHTML(html, query string, num int) (string, error) {
	results := extractDDGResults(html, num)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Web search results for: %s\n\n", query))

	if len(results) == 0 {
		sb.WriteString("No results found.\n")
		return sb.String(), nil
	}

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("    URL: %s\n", r.URL))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("    %s\n", r.Snippet))
		}
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

func extractDDGResults(html string, max int) []ddgResult {
	linkMatches := ddgLinkRe.FindAllStringSubmatchIndex(html, -1)
	snippetMatches := ddgSnippetRe.FindAllStringSubmatch(html, -1)

	var results []ddgResult
	for i, loc := range linkMatches {
		if len(results) >= max {
			break
		}
		if len(loc) < 6 {
			continue
		}
		rawURL := html[loc[2]:loc[3]]
		rawTitle := html[loc[4]:loc[5]]

		resolvedURL := resolveDDGURL(rawURL)
		if resolvedURL == "" {
			continue
		}
		title := cleanHTML(rawTitle)
		if title == "" {
			continue
		}

		var snippet string
		if i < len(snippetMatches) && len(snippetMatches[i]) > 1 {
			snippet = cleanHTML(snippetMatches[i][1])
		}

		results = append(results, ddgResult{
			Title:   title,
			URL:     resolvedURL,
			Snippet: snippet,
		})
	}
	return results
}

func resolveDDGURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "duckduckgo.com/l/") {
		parsed, err := url.Parse(raw)
		if err == nil {
			if uddg := parsed.Query().Get("uddg"); uddg != "" {
				return uddg
			}
		}
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return ""
}

func cleanHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = decodeHTMLEntities(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func decodeHTMLEntities(s string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
		"&nbsp;", " ",
		"&mdash;", "—",
		"&ndash;", "–",
		"&hellip;", "...",
	)
	return replacer.Replace(s)
}
