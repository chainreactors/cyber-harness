package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/provider"
)

const (
	fetchTimeout      = 60 * time.Second
	maxFetchBody      = 10 * 1024 * 1024 // 10MB
	maxMarkdownLength = 100_000           // chars returned to LLM
	fetchUserAgent    = "Mozilla/5.0 (compatible; aiscan/1.0; +https://github.com/chainreactors/aiscan)"
)

// WebFetchTool fetches a URL, converts HTML to Markdown, and returns the
// content for the agent to process. Inspired by Claude Code's WebFetch.
type WebFetchTool struct {
	client *http.Client
}

// NewWebFetchTool creates a web page fetch tool.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{
			Timeout: fetchTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects (exceeded 10)")
				}
				return nil
			},
		},
	}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string {
	return "Fetch content from a URL and return it as readable text. " +
		"Use this to read web pages, documentation, CVE details, exploit writeups, or any online resource. " +
		"HTML pages are automatically converted to Markdown for readability."
}

func (t *WebFetchTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        "web_fetch",
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to fetch content from. Must be a valid HTTP/HTTPS URL.",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	rawURL := strings.TrimSpace(args.URL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}

	// Validate and normalize URL
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
		rawURL = parsed.String()
	}
	if parsed.Scheme == "http" {
		parsed.Scheme = "https"
		rawURL = parsed.String()
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("URL must include a hostname")
	}

	// Fetch
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	req.Header.Set("Accept", "text/html, text/markdown, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	content := string(body)

	// Convert HTML to Markdown
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
		content = htmlToMarkdown(content)
	}

	// Truncate if too long
	if len(content) > maxMarkdownLength {
		content = content[:maxMarkdownLength] +
			fmt.Sprintf("\n\n[Content truncated: showing %d of %d characters]",
				maxMarkdownLength, len(string(body)))
	}

	// Build result
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Fetched: %s\n", rawURL))
	sb.WriteString(fmt.Sprintf("Status: %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode)))
	sb.WriteString(fmt.Sprintf("Size: %d bytes\n", len(body)))
	sb.WriteString("---\n\n")
	sb.WriteString(content)

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// HTML → Markdown conversion (pure Go, no external dependencies)
// ---------------------------------------------------------------------------

var (
	// Block-level tags to strip entirely (scripts, styles, etc.)
	// Go regexp (RE2) does not support backreferences, so we use separate patterns.
	stripScriptRe  = regexp.MustCompile(`(?si)<script[^>]*>.*?</script>`)
	stripStyleRe   = regexp.MustCompile(`(?si)<style[^>]*>.*?</style>`)
	stripNoscriptRe = regexp.MustCompile(`(?si)<noscript[^>]*>.*?</noscript>`)
	stripSvgRe     = regexp.MustCompile(`(?si)<svg[^>]*>.*?</svg>`)
	stripIframeRe  = regexp.MustCompile(`(?si)<iframe[^>]*>.*?</iframe>`)
	stripObjectRe  = regexp.MustCompile(`(?si)<object[^>]*>.*?</object>`)
	stripEmbedRe   = regexp.MustCompile(`(?si)<embed[^>]*>.*?</embed>`)

	// Common HTML patterns → Markdown
	headingH1Re = regexp.MustCompile(`(?si)<h1[^>]*>(.*?)</h1>`)
	headingH2Re = regexp.MustCompile(`(?si)<h2[^>]*>(.*?)</h2>`)
	headingH3Re = regexp.MustCompile(`(?si)<h3[^>]*>(.*?)</h3>`)
	headingH4Re = regexp.MustCompile(`(?si)<h4[^>]*>(.*?)</h4>`)
	headingH5Re = regexp.MustCompile(`(?si)<h5[^>]*>(.*?)</h5>`)
	headingH6Re = regexp.MustCompile(`(?si)<h6[^>]*>(.*?)</h6>`)

	linkRe       = regexp.MustCompile(`(?si)<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	boldBRe      = regexp.MustCompile(`(?si)<b[^>]*>(.*?)</b>`)
	boldStrongRe = regexp.MustCompile(`(?si)<strong[^>]*>(.*?)</strong>`)
	italicIRe    = regexp.MustCompile(`(?si)<i[^>]*>(.*?)</i>`)
	italicEmRe   = regexp.MustCompile(`(?si)<em[^>]*>(.*?)</em>`)
	codeInlineRe = regexp.MustCompile(`(?si)<code[^>]*>(.*?)</code>`)
	codeBlockRe  = regexp.MustCompile(`(?si)<pre[^>]*>(.*?)</pre>`)
	listItemRe   = regexp.MustCompile(`(?si)<li[^>]*>(.*?)</li>`)
	paragraphRe  = regexp.MustCompile(`(?si)<p[^>]*>(.*?)</p>`)
	brRe         = regexp.MustCompile(`(?i)<br\s*/?>`)
	hrRe         = regexp.MustCompile(`(?i)<hr[^>]*>`)
	imgRe        = regexp.MustCompile(`(?si)<img[^>]*alt="([^"]*)"[^>]*>`)

	// Table patterns
	tableRowRe  = regexp.MustCompile(`(?si)<tr[^>]*>(.*?)</tr>`)
	tableCellRe = regexp.MustCompile(`(?si)<t[dh][^>]*>(.*?)</t[dh]>`)

	// Block-level elements that should be separated by newlines
	blockEndRe = regexp.MustCompile(`(?i)</?(div|section|article|header|footer|main|nav|aside|blockquote|ul|ol|table|thead|tbody|tfoot|dl|dd|dt|figure|figcaption|details|summary|form|fieldset)[^>]*>`)

	// Remaining HTML tags
	allTagRe = regexp.MustCompile(`<[^>]+>`)

	// Whitespace normalization
	multiNewlineRe = regexp.MustCompile(`\n{4,}`)
	multiSpaceRe   = regexp.MustCompile(`[ \t]{2,}`)

	// HTML comments
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
)

func htmlToMarkdown(html string) string {
	s := html

	// Remove comments
	s = commentRe.ReplaceAllString(s, "")

	// Strip script, style, etc.
	for _, re := range []*regexp.Regexp{stripScriptRe, stripStyleRe, stripNoscriptRe, stripSvgRe, stripIframeRe, stripObjectRe, stripEmbedRe} {
		s = re.ReplaceAllString(s, "")
	}

	// Code blocks (before other transformations to preserve content)
	s = codeBlockRe.ReplaceAllStringFunc(s, func(match string) string {
		inner := codeBlockRe.FindStringSubmatch(match)
		if len(inner) > 1 {
			code := allTagRe.ReplaceAllString(inner[1], "")
			code = decodeHTMLEntities(code)
			return "\n```\n" + strings.TrimSpace(code) + "\n```\n"
		}
		return match
	})

	// Headings
	s = headingH1Re.ReplaceAllString(s, "\n# $1\n")
	s = headingH2Re.ReplaceAllString(s, "\n## $1\n")
	s = headingH3Re.ReplaceAllString(s, "\n### $1\n")
	s = headingH4Re.ReplaceAllString(s, "\n#### $1\n")
	s = headingH5Re.ReplaceAllString(s, "\n##### $1\n")
	s = headingH6Re.ReplaceAllString(s, "\n###### $1\n")

	// Links
	s = linkRe.ReplaceAllString(s, "[$2]($1)")

	// Bold / Italic
	s = boldBRe.ReplaceAllString(s, "**$1**")
	s = boldStrongRe.ReplaceAllString(s, "**$1**")
	s = italicIRe.ReplaceAllString(s, "*$1*")
	s = italicEmRe.ReplaceAllString(s, "*$1*")

	// Inline code
	s = codeInlineRe.ReplaceAllString(s, "`$1`")

	// Images
	s = imgRe.ReplaceAllString(s, "![$1]")

	// List items
	s = listItemRe.ReplaceAllString(s, "\n- $1")

	// Paragraphs
	s = paragraphRe.ReplaceAllString(s, "\n\n$1\n\n")

	// Line breaks
	s = brRe.ReplaceAllString(s, "\n")

	// Horizontal rules
	s = hrRe.ReplaceAllString(s, "\n---\n")

	// Tables: simple row → pipe-separated
	s = tableRowRe.ReplaceAllStringFunc(s, func(row string) string {
		cells := tableCellRe.FindAllStringSubmatch(row, -1)
		if len(cells) == 0 {
			return ""
		}
		var parts []string
		for _, cell := range cells {
			text := strings.TrimSpace(allTagRe.ReplaceAllString(cell[1], ""))
			parts = append(parts, text)
		}
		return "| " + strings.Join(parts, " | ") + " |\n"
	})

	// Block-level elements → newlines
	s = blockEndRe.ReplaceAllString(s, "\n")

	// Strip remaining HTML tags
	s = allTagRe.ReplaceAllString(s, "")

	// Decode HTML entities
	s = decodeHTMLEntities(s)

	// Normalize whitespace
	s = multiSpaceRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	var trimmed []string
	for _, line := range lines {
		trimmed = append(trimmed, strings.TrimSpace(line))
	}
	s = strings.Join(trimmed, "\n")
	s = multiNewlineRe.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)

	return s
}
