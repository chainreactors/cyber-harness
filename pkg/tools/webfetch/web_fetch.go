package webfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	fetchTimeout      = 60 * time.Second
	maxFetchBody      = 10 * 1024 * 1024 // 10 MB per CC: MAX_HTTP_CONTENT_LENGTH
	maxMarkdownLength = 100_000          // chars returned to LLM — matches CC
	maxURLLength      = 2000             // CC: MAX_URL_LENGTH
	maxRedirects      = 10               // CC: MAX_REDIRECTS
	fetchUserAgent    = "Mozilla/5.0 (compatible; aiscan/1.0; +https://github.com/chainreactors/aiscan)"

	cacheTTL      = 15 * time.Minute // CC: CACHE_TTL_MS
	maxCacheBytes = 50 * 1024 * 1024 // CC: MAX_CACHE_SIZE_BYTES
)

// ---------------------------------------------------------------------------
// URL cache — hand-rolled TTL + size-bounded map, mirrors CC's LRUCache
// ---------------------------------------------------------------------------

type cacheEntry struct {
	content     string
	contentType string
	binary      bool
	bytes       int
	code        int
	codeText    string
	size        int // approximate memory footprint used for eviction
	fetchedAt   time.Time
}

type urlCache struct {
	mu        sync.Mutex
	entries   map[string]*cacheEntry
	order     []string // least-recently-used order; oldest at index 0
	totalSize int
}

func newURLCache() *urlCache {
	return &urlCache{entries: make(map[string]*cacheEntry)}
}

func (c *urlCache) Get(key string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Since(e.fetchedAt) > cacheTTL {
		c.removeLocked(key)
		return nil, false
	}
	c.touchLocked(key)
	return e, true
}

func (c *urlCache) Set(key string, e *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict existing entry for the same key first.
	if old, ok := c.entries[key]; ok {
		c.totalSize -= old.size
		delete(c.entries, key)
		c.removeFromOrderLocked(key)
	}

	// Evict stale + oldest entries until we have room.
	for c.totalSize+e.size > maxCacheBytes && len(c.order) > 0 {
		victim := c.order[0]
		c.order = c.order[1:]
		if v, ok := c.entries[victim]; ok {
			c.totalSize -= v.size
			delete(c.entries, victim)
		}
	}

	c.entries[key] = e
	c.order = append(c.order, key)
	c.totalSize += e.size
}

func (c *urlCache) removeLocked(key string) {
	if e, ok := c.entries[key]; ok {
		c.totalSize -= e.size
		delete(c.entries, key)
	}
	c.removeFromOrderLocked(key)
}

func (c *urlCache) touchLocked(key string) {
	c.removeFromOrderLocked(key)
	c.order = append(c.order, key)
}

func (c *urlCache) removeFromOrderLocked(key string) {
	for i, existing := range c.order {
		if existing == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// Clear drops all cached entries. Exported for tests.
func (c *urlCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
	c.order = nil
	c.totalSize = 0
}

// ---------------------------------------------------------------------------
// Command
// ---------------------------------------------------------------------------

// Command implements command.PseudoCommand for fetching web pages.
type Command struct {
	client *http.Client
	cache  *urlCache
}

// New creates a web_fetch pseudo-command with proxy support via
// standard HTTP_PROXY / HTTPS_PROXY environment variables.
func New() *Command {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	return &Command{
		client: &http.Client{
			Transport: transport,
			Timeout:   fetchTimeout,
			// Disable automatic redirects — we handle them manually so
			// cross-host redirects can be reported back to the agent.
			// Mirrors CC's getWithPermittedRedirects pattern.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache: newURLCache(),
	}
}

// ClearCache drops all cached entries. Useful for tests.
func (c *Command) ClearCache() { c.cache.Clear() }

func (c *Command) Name() string { return "web_fetch" }

func (c *Command) Usage() string {
	return `web_fetch - Fetch content from a URL and return as readable text
Usage:
  web_fetch <url> [options]

Options:
  --extract <hint>   Return matching sections from the page when possible

Examples:
  web_fetch https://example.com/advisory
  web_fetch https://nvd.nist.gov/vuln/detail/CVE-2024-1234 --extract "CVSS score and description"`
}

// Execute parses CLI-style arguments and fetches the URL.
//
//	web_fetch <url> [--extract <hint>]
func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	rawURL, extract, err := parseArgs(args, c.Usage())
	if err != nil {
		return "", err
	}

	// ---- Validate & normalise URL ----
	normalizedURL, err := normalizeURL(rawURL)
	if err != nil {
		return "", err
	}
	if err := validateURL(normalizedURL); err != nil {
		return "", err
	}

	// ---- Cache lookup (keyed on original URL, matching CC) ----
	if cached, ok := c.cache.Get(normalizedURL); ok {
		if cached.binary {
			return formatBinaryCacheOutput(normalizedURL, cached), nil
		}
		return formatOutput(normalizedURL, cached, extract), nil
	}

	// ---- Fetch with redirect handling ----
	result, redir, err := c.fetchWithRedirects(ctx, normalizedURL, 0)
	if err != nil {
		return "", err
	}

	// Cross-host redirect: report back to agent so it can re-fetch.
	if redir != nil {
		return formatRedirectMessage(redir), nil
	}

	// ---- Binary detection ----
	if isBinaryContentType(result.contentType) {
		entry := &cacheEntry{
			contentType: result.contentType,
			binary:      true,
			bytes:       result.bytes,
			code:        result.code,
			codeText:    result.codeText,
			size:        binaryCacheEntrySize(result),
			fetchedAt:   time.Now(),
		}
		c.cache.Set(normalizedURL, entry)
		return formatBinaryCacheOutput(normalizedURL, entry), nil
	}

	// ---- HTML → markdown ----
	content := result.body
	if strings.Contains(result.contentType, "text/html") || strings.Contains(result.contentType, "application/xhtml") {
		content = htmlToMarkdown(content)
	}

	// ---- Cache the result ----
	entry := &cacheEntry{
		content:     content,
		contentType: result.contentType,
		bytes:       result.bytes,
		code:        result.code,
		codeText:    result.codeText,
		size:        len(content),
		fetchedAt:   time.Now(),
	}
	c.cache.Set(normalizedURL, entry)

	return formatOutput(normalizedURL, entry, extract), nil
}

// ---------------------------------------------------------------------------
// Redirect handling — mirrors CC's getWithPermittedRedirects
// ---------------------------------------------------------------------------

type fetchResult struct {
	body        string
	contentType string
	bytes       int
	code        int
	codeText    string
}

type redirectInfo struct {
	originalURL string
	redirectURL string
	statusCode  int
}

func (c *Command) fetchWithRedirects(ctx context.Context, targetURL string, depth int) (*fetchResult, *redirectInfo, error) {
	if depth > maxRedirects {
		return nil, nil, fmt.Errorf("too many redirects (exceeded %d)", maxRedirects)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	req.Header.Set("Accept", "text/markdown, text/html, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle redirects.
	if isRedirectStatus(resp.StatusCode) {
		location := resp.Header.Get("Location")
		if location == "" {
			return nil, nil, fmt.Errorf("redirect missing Location header")
		}
		redirectURL, err := resolveRedirectURL(targetURL, location)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve redirect: %w", err)
		}

		if isPermittedRedirect(targetURL, redirectURL) {
			// Same-host redirect — follow it.
			return c.fetchWithRedirects(ctx, redirectURL, depth+1)
		}
		// Cross-host redirect — report back to agent.
		return nil, &redirectInfo{
			originalURL: targetURL,
			redirectURL: redirectURL,
			statusCode:  resp.StatusCode,
		}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}

	return &fetchResult{
		body:        string(body),
		contentType: resp.Header.Get("Content-Type"),
		bytes:       len(body),
		code:        resp.StatusCode,
		codeText:    http.StatusText(resp.StatusCode),
	}, nil, nil
}

func isRedirectStatus(code int) bool {
	return code == 301 || code == 302 || code == 303 || code == 307 || code == 308
}

func resolveRedirectURL(base, location string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(ref).String(), nil
}

// isPermittedRedirect mirrors CC's logic: same host (ignoring www. prefix),
// same protocol, same port. Path/query changes are always allowed.
func isPermittedRedirect(originalURL, redirectURL string) bool {
	orig, err := url.Parse(originalURL)
	if err != nil {
		return false
	}
	redir, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}
	if orig.Scheme != redir.Scheme {
		return false
	}
	if orig.Port() != redir.Port() {
		return false
	}
	if redir.User != nil {
		return false
	}
	return stripWWW(orig.Hostname()) == stripWWW(redir.Hostname())
}

func stripWWW(host string) string {
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

// ---------------------------------------------------------------------------
// URL validation — mirrors CC's validateURL
// ---------------------------------------------------------------------------

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("URL must include a hostname")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q (supported: http, https)", parsed.Scheme)
	}
	return parsed.String(), nil
}

func validateURL(normalized string) error {
	if len(normalized) > maxURLLength {
		return fmt.Errorf("URL too long (%d chars, max %d)", len(normalized), maxURLLength)
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.User != nil {
		return fmt.Errorf("URL must not contain username or password")
	}
	parts := strings.Split(parsed.Hostname(), ".")
	if len(parts) < 2 {
		return fmt.Errorf("URL hostname must have at least two parts (got %q)", parsed.Hostname())
	}
	return nil
}

// ---------------------------------------------------------------------------
// Binary content detection — mirrors CC's isBinaryContentType
// ---------------------------------------------------------------------------

func isBinaryContentType(ct string) bool {
	ct = strings.ToLower(ct)
	for _, prefix := range []string{
		"image/",
		"audio/",
		"video/",
		"application/pdf",
		"application/zip",
		"application/gzip",
		"application/x-tar",
		"application/x-7z",
		"application/x-rar",
		"application/octet-stream",
		"application/x-executable",
		"application/x-mach-binary",
		"application/java-archive",
		"application/wasm",
	} {
		if strings.HasPrefix(ct, prefix) || strings.Contains(ct, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Output formatting
// ---------------------------------------------------------------------------

func formatOutput(fetchedURL string, entry *cacheEntry, extract string) string {
	content := entry.content
	if strings.TrimSpace(extract) != "" {
		content = extractRelevantContent(content, extract)
	}

	if len(content) > maxMarkdownLength {
		content = content[:maxMarkdownLength] +
			fmt.Sprintf("\n\n[Content truncated: showing %d of %d characters. Use --extract to focus on specific content.]",
				maxMarkdownLength, len(entry.content))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Fetched: %s\n", fetchedURL))
	sb.WriteString(fmt.Sprintf("Status: %d %s\n", entry.code, entry.codeText))
	sb.WriteString(fmt.Sprintf("Content-Type: %s\n", entry.contentType))
	sb.WriteString(fmt.Sprintf("Size: %d bytes\n", entry.bytes))
	sb.WriteString("---\n\n")
	sb.WriteString(content)
	return sb.String()
}

func formatBinaryOutput(fetchedURL string, result *fetchResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Fetched: %s\n", fetchedURL))
	sb.WriteString(fmt.Sprintf("Status: %d %s\n", result.code, result.codeText))
	sb.WriteString(fmt.Sprintf("Content-Type: %s\n", result.contentType))
	sb.WriteString(fmt.Sprintf("Size: %d bytes\n", result.bytes))
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("[Binary content: %s, %d bytes. Download the file to inspect it.]", result.contentType, result.bytes))
	return sb.String()
}

func formatBinaryCacheOutput(fetchedURL string, entry *cacheEntry) string {
	return formatBinaryOutput(fetchedURL, &fetchResult{
		contentType: entry.contentType,
		bytes:       entry.bytes,
		code:        entry.code,
		codeText:    entry.codeText,
	})
}

func binaryCacheEntrySize(result *fetchResult) int {
	return len(result.contentType) + len(result.codeText) + 64
}

func formatRedirectMessage(redir *redirectInfo) string {
	statusText := http.StatusText(redir.statusCode)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("REDIRECT DETECTED: The URL redirects to a different host.\n\n"))
	sb.WriteString(fmt.Sprintf("Original URL: %s\n", redir.originalURL))
	sb.WriteString(fmt.Sprintf("Redirect URL: %s\n", redir.redirectURL))
	sb.WriteString(fmt.Sprintf("Status: %d %s\n\n", redir.statusCode, statusText))
	sb.WriteString(fmt.Sprintf("To fetch the redirected content, run:\n  web_fetch %s", redir.redirectURL))
	return sb.String()
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

func parseArgs(args []string, usage string) (rawURL, extract string, err error) {
	var extractParts []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--extract":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("web_fetch: --extract requires a value")
			}
			i++
			extract = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("web_fetch: unknown flag: %s", args[i])
			}
			if rawURL == "" {
				rawURL = args[i]
			} else {
				extractParts = append(extractParts, args[i])
			}
		}
	}

	if rawURL == "" {
		return "", "", fmt.Errorf("web_fetch: url is required\n\n%s", usage)
	}
	if extract == "" && len(extractParts) > 0 {
		extract = strings.Join(extractParts, " ")
	}
	return rawURL, extract, nil
}

// ---------------------------------------------------------------------------
// HTML → Markdown conversion (pure Go, no external dependencies)
// ---------------------------------------------------------------------------

var (
	stripScriptRe   = regexp.MustCompile(`(?si)<script[^>]*>.*?</script>`)
	stripStyleRe    = regexp.MustCompile(`(?si)<style[^>]*>.*?</style>`)
	stripNoscriptRe = regexp.MustCompile(`(?si)<noscript[^>]*>.*?</noscript>`)
	stripSvgRe      = regexp.MustCompile(`(?si)<svg[^>]*>.*?</svg>`)
	stripIframeRe   = regexp.MustCompile(`(?si)<iframe[^>]*>.*?</iframe>`)
	stripObjectRe   = regexp.MustCompile(`(?si)<object[^>]*>.*?</object>`)
	stripEmbedRe    = regexp.MustCompile(`(?si)<embed[^>]*>.*?</embed>`)

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

	tableRowRe  = regexp.MustCompile(`(?si)<tr[^>]*>(.*?)</tr>`)
	tableCellRe = regexp.MustCompile(`(?si)<t[dh][^>]*>(.*?)</t[dh]>`)

	blockEndRe = regexp.MustCompile(`(?i)</?(div|section|article|header|footer|main|nav|aside|blockquote|ul|ol|table|thead|tbody|tfoot|dl|dd|dt|figure|figcaption|details|summary|form|fieldset)[^>]*>`)

	allTagRe = regexp.MustCompile(`<[^>]+>`)

	multiNewlineRe = regexp.MustCompile(`\n{4,}`)
	multiSpaceRe   = regexp.MustCompile(`[ \t]{2,}`)

	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
)

func htmlToMarkdown(html string) string {
	s := html

	s = commentRe.ReplaceAllString(s, "")

	for _, re := range []*regexp.Regexp{stripScriptRe, stripStyleRe, stripNoscriptRe, stripSvgRe, stripIframeRe, stripObjectRe, stripEmbedRe} {
		s = re.ReplaceAllString(s, "")
	}

	s = codeBlockRe.ReplaceAllStringFunc(s, func(match string) string {
		inner := codeBlockRe.FindStringSubmatch(match)
		if len(inner) > 1 {
			code := allTagRe.ReplaceAllString(inner[1], "")
			code = decodeHTMLEntities(code)
			return "\n```\n" + strings.TrimSpace(code) + "\n```\n"
		}
		return match
	})

	s = headingH1Re.ReplaceAllString(s, "\n# $1\n")
	s = headingH2Re.ReplaceAllString(s, "\n## $1\n")
	s = headingH3Re.ReplaceAllString(s, "\n### $1\n")
	s = headingH4Re.ReplaceAllString(s, "\n#### $1\n")
	s = headingH5Re.ReplaceAllString(s, "\n##### $1\n")
	s = headingH6Re.ReplaceAllString(s, "\n###### $1\n")

	s = linkRe.ReplaceAllString(s, "[$2]($1)")
	s = boldBRe.ReplaceAllString(s, "**$1**")
	s = boldStrongRe.ReplaceAllString(s, "**$1**")
	s = italicIRe.ReplaceAllString(s, "*$1*")
	s = italicEmRe.ReplaceAllString(s, "*$1*")
	s = codeInlineRe.ReplaceAllString(s, "`$1`")
	s = imgRe.ReplaceAllString(s, "![$1]")
	s = listItemRe.ReplaceAllString(s, "\n- $1")
	s = paragraphRe.ReplaceAllString(s, "\n\n$1\n\n")
	s = brRe.ReplaceAllString(s, "\n")
	s = hrRe.ReplaceAllString(s, "\n---\n")

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

	s = blockEndRe.ReplaceAllString(s, "\n")
	s = allTagRe.ReplaceAllString(s, "")
	s = decodeHTMLEntities(s)
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

func extractRelevantContent(content, hint string) string {
	terms := extractTerms(hint)
	if len(terms) == 0 {
		return content
	}

	blocks := splitContentBlocks(content)
	if len(blocks) == 0 {
		return content
	}

	selected := make([]string, 0)
	seen := make(map[int]struct{})
	for i, block := range blocks {
		if !blockMatchesTerms(block, terms) {
			continue
		}
		for _, idx := range []int{i - 1, i, i + 1} {
			if idx < 0 || idx >= len(blocks) {
				continue
			}
			if _, ok := seen[idx]; ok {
				continue
			}
			seen[idx] = struct{}{}
			selected = append(selected, blocks[idx])
		}
	}
	if len(selected) == 0 {
		return fmt.Sprintf("[No exact matches for extract hint: %s]\n\n%s", hint, content)
	}

	return fmt.Sprintf("Extract hint: %s\n\n%s", hint, strings.Join(selected, "\n\n"))
}

func extractTerms(hint string) []string {
	fields := strings.FieldsFunc(strings.ToLower(hint), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.')
	})
	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{})
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len(field) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}

func splitContentBlocks(content string) []string {
	parts := regexp.MustCompile(`\n\s*\n`).Split(content, -1)
	blocks := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			blocks = append(blocks, part)
		}
	}
	return blocks
}

func blockMatchesTerms(block string, terms []string) bool {
	block = strings.ToLower(block)
	for _, term := range terms {
		if strings.Contains(block, term) {
			return true
		}
	}
	return false
}
