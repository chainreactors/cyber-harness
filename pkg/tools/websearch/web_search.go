package websearch

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
)

const (
	ddgSearchURL    = "https://html.duckduckgo.com/html/"
	ddgUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	searchTimeout   = 30 * time.Second
	maxResponseBody = 1024 * 1024 // 1MB for HTML

	defaultNumResults = 5
	maxNumResults     = 10

	tavilySearchURL = "https://api.tavily.com/search"
)

type searchBackend int

const (
	backendDDG    searchBackend = iota
	backendTavily               // requires TAVILY_API_KEY
)

// Command implements command.PseudoCommand for web search.
type Command struct {
	client      *http.Client
	backend     searchBackend
	apiKey      string
	apiKeys     []string // multiple Tavily keys for rotation
	currentKey  int      // index into apiKeys
	mu          sync.Mutex
}

// New creates a web_search pseudo-command. It auto-detects the backend:
// TAVILY_API_KEY set → Tavily, otherwise → DuckDuckGo.
// Multiple keys can be provided via TAVILY_API_KEYS (comma-separated) for
// automatic rotation when a key is exhausted (HTTP 401/429).
// An optional builtinKeys string (comma-separated) supplies build-time
// fallback keys that are appended after any environment-sourced keys.
// Proxy is managed via SetProxy() and the proxy command's OnProxyChange callback.
func New(builtinKeys string) *Command {
	c := &Command{
		client: &http.Client{
			Timeout:   searchTimeout,
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
		},
	}

	// Collect all available Tavily keys: env vars first, then build-time defaults.
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

	// 1. Environment (highest priority).
	addKeys(os.Getenv("TAVILY_API_KEY"))
	addKeys(os.Getenv("TAVILY_API_KEYS"))

	// 2. Build-time fallback keys (via ldflags → Deps → register.go).
	addKeys(builtinKeys)

	if len(keys) > 0 {
		c.backend = backendTavily
		c.apiKeys = keys
		c.apiKey = keys[0]
	}
	return c
}

// SetProxy updates the HTTP proxy used for web search requests.
// Implements the interface used by the proxy command's OnProxyChange callback.
func (c *Command) SetProxy(proxyURLStr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	transport := &http.Transport{}
	if proxyURLStr != "" {
		proxyURL, err := url.Parse(proxyURLStr)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		} else {
			transport.Proxy = http.ProxyFromEnvironment
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}
	c.client.Transport = transport
}

// rotateKey advances to the next Tavily API key. Returns false if all keys
// have been exhausted (wrapped around to the starting key).
func (c *Command) rotateKey() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.apiKeys) <= 1 {
		return false
	}
	c.currentKey = (c.currentKey + 1) % len(c.apiKeys)
	c.apiKey = c.apiKeys[c.currentKey]
	return true // still have keys to try
}

func (c *Command) Name() string { return "web_search" }

func (c *Command) Usage() string {
	return `web_search - Search the web for information
Usage:
  web_search <query> [options]

Options:
  --num <N>   Number of results to return (1-10, default 5)

Examples:
  web_search "CVE-2024-1234 exploit"
  web_search "nginx reverse proxy config" --num 10`
}

// Execute parses CLI-style arguments and performs a web search.
//
//	web_search <query> [--num <N>]
func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	query, num, err := parseArgs(args, c.Usage())
	if err != nil {
		return "", err
	}

	switch c.backend {
	case backendTavily:
		// Try current key; on auth/quota failure rotate through remaining keys.
		startKey := c.currentKey
		for {
			result, err := c.searchTavily(ctx, query, num)
			if err == nil {
				return result, nil
			}
			// Only rotate on key-exhaustion errors (401 Unauthorized, 429 Rate Limit).
			if !isKeyExhausted(err) {
				// Non-key error — fall through to DDG.
				break
			}
			if !c.rotateKey() {
				break // single key, nothing to rotate
			}
			if c.currentKey == startKey {
				break // wrapped around, all keys exhausted
			}
		}
		// All Tavily keys failed — fallback to DuckDuckGo.
		fallback, fbErr := c.searchDDG(ctx, query, num)
		if fbErr != nil {
			return "", fmt.Errorf("tavily keys exhausted; ddg fallback: %w", fbErr)
		}
		return fallback, nil
	default:
		return c.searchDDG(ctx, query, num)
	}
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

func parseArgs(args []string, usage string) (query string, num int, err error) {
	num = defaultNumResults

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--num":
			if i+1 >= len(args) {
				return "", 0, fmt.Errorf("web_search: --num requires a value")
			}
			// #nosec G602 -- i+1 is checked immediately above.
			value := args[i+1]
			i++
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return "", 0, fmt.Errorf("web_search: invalid --num value: %s", value)
			}
			num = n
		default:
			if strings.HasPrefix(args[i], "--") {
				return "", 0, fmt.Errorf("web_search: unknown flag: %s", args[i])
			}
			if query == "" {
				query = args[i]
			} else {
				query += " " + args[i]
			}
		}
	}

	if query == "" {
		return "", 0, fmt.Errorf("web_search: query is required\n\n%s", usage)
	}
	if num <= 0 {
		num = defaultNumResults
	}
	if num > maxNumResults {
		num = maxNumResults
	}
	return query, num, nil
}

// isKeyExhausted checks whether a Tavily error indicates the API key is
// invalid or rate-limited (HTTP 401 / 429), meaning we should rotate.
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

func (c *Command) searchTavily(ctx context.Context, query string, num int) (string, error) {
	reqBody := tavilyRequest{
		Query:             query,
		MaxResults:        num,
		SearchDepth:       "basic",
		IncludeAnswer:     true,
		IncludeRawContent: false,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilySearchURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("tavily request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", fmt.Errorf("read tavily response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tavily returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tavilyResp tavilyResponse
	if err := json.Unmarshal(body, &tavilyResp); err != nil {
		return "", fmt.Errorf("parse tavily response: %w", err)
	}

	return formatTavilyResults(tavilyResp, query), nil
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

func (c *Command) searchDDG(ctx context.Context, query string, num int) (string, error) {
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
