package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	ddgSearchURL = "https://html.duckduckgo.com/html/"
	ddgUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

func (c *Client) searchDDG(ctx context.Context, query string, num int) (string, error) {
	if c.config.DisableDDG {
		return "", fmt.Errorf("DuckDuckGo is disabled")
	}
	form := url.Values{"q": {query}, "b": {""}}
	endpoint := ddgSearchURL
	if c.config.DDGBaseURL != "" {
		endpoint = strings.TrimRight(c.config.DDGBaseURL, "/") + "/html/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
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

	return parseDDGHTML(string(body), query, num), nil
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

func parseDDGHTML(html, query string, num int) string {
	results := extractDDGResults(html, num)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Web search results for: %s\n\n", query))

	if len(results) == 0 {
		sb.WriteString("No results found.\n")
		return sb.String()
	}

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("    URL: %s\n", r.URL))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("    %s\n", r.Snippet))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
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
