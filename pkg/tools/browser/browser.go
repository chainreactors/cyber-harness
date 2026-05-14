package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/ysmood/gson"
)

const (
	defaultTimeout    = 30 * time.Second
	maxOutputLen      = 100_000
	waitStableDur     = 300 * time.Millisecond
	defaultUserAgent  = ""
	defaultScreenFile = "screenshot.png"
	defaultPDFFile    = "page.pdf"
)

// Command implements command.PseudoCommand for headless browser operations.
type Command struct {
	mu      sync.Mutex
	browser *rod.Browser
	workDir string
}

// New creates a browser pseudo-command.
func New(workDir string) *Command {
	return &Command{workDir: workDir}
}

func (c *Command) Name() string { return "browser" }

func (c *Command) Usage() string {
	return `browser - Headless browser for JS-rendered pages, screenshots, and network capture
Usage:
  browser <subcommand> <url> [options]

Subcommands:
  navigate    Open URL, render JS, return visible text content
  screenshot  Take a screenshot of the page, save to file
  content     Return full rendered HTML after JS execution
  eval        Execute JavaScript on a page, return result
  network     Navigate and capture all network requests/responses (API discovery)
  pdf         Generate PDF of the rendered page

Common Options:
  --timeout <seconds>     Page load timeout in seconds (default: 30)
  --user-agent <string>   Custom User-Agent header

screenshot/pdf Options:
  --output <filename>     Output filename (auto-generated if omitted)
  --full-page             Capture full scrollable page (screenshot only)

eval Options:
  --script <js>           JavaScript expression to execute (alternative to positional arg)

Examples:
  browser navigate https://example.com
  browser screenshot https://target.com --output evidence.png --full-page
  browser content https://spa-app.com --timeout 60
  browser eval https://target.com "document.querySelectorAll('a').length"
  browser network https://target.com/app
  browser pdf https://target.com/report --output report.pdf`
}

// Execute dispatches to the appropriate sub-command.
func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("browser: subcommand required\n\n%s", c.Usage())
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "navigate":
		return c.execNavigate(ctx, subArgs)
	case "screenshot":
		return c.execScreenshot(ctx, subArgs)
	case "content":
		return c.execContent(ctx, subArgs)
	case "eval":
		return c.execEval(ctx, subArgs)
	case "network":
		return c.execNetwork(ctx, subArgs)
	case "pdf":
		return c.execPDF(ctx, subArgs)
	default:
		return "", fmt.Errorf("browser: unknown subcommand %q\n\n%s", sub, c.Usage())
	}
}

// Close shuts down the browser process if running.
func (c *Command) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.browser != nil {
		_ = c.browser.Close()
		c.browser = nil
	}
}

// ---------------------------------------------------------------------------
// Browser lifecycle
// ---------------------------------------------------------------------------

func (c *Command) getOrLaunchBrowser() (*rod.Browser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.browser != nil {
		return c.browser, nil
	}

	l := launcher.New().
		Headless(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage")

	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("browser: launch failed: %w", err)
	}

	b := rod.New().ControlURL(controlURL)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("browser: connect failed: %w", err)
	}

	c.browser = b
	return c.browser, nil
}

// newPage creates a fresh incognito page with stealth, timeout, and optional user-agent.
func (c *Command) newPage(ctx context.Context, opts commonOpts) (*rod.Page, func(), error) {
	b, err := c.getOrLaunchBrowser()
	if err != nil {
		return nil, nil, err
	}

	incognito, err := b.Incognito()
	if err != nil {
		return nil, nil, fmt.Errorf("browser: incognito context: %w", err)
	}

	page, err := incognito.Page(proto.TargetCreateTarget{})
	if err != nil {
		_ = incognito.Close()
		return nil, nil, fmt.Errorf("browser: new page: %w", err)
	}

	// Inject stealth anti-detection JS (same as go-rod/stealth but on incognito page).
	if _, err := page.EvalOnNewDocument(stealth.JS); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return nil, nil, fmt.Errorf("browser: stealth inject: %w", err)
	}

	if opts.userAgent != "" {
		if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
			UserAgent: opts.userAgent,
		}); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return nil, nil, fmt.Errorf("browser: set user-agent: %w", err)
		}
	}

	page = page.Context(ctx).Timeout(opts.timeout)

	cleanup := func() {
		_ = page.Close()
		_ = incognito.Close()
	}

	return page, cleanup, nil
}

// navigateTo navigates to URL and waits for the page to stabilise.
func navigateTo(page *rod.Page, url string) error {
	if err := page.Navigate(url); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("wait load: %w", err)
	}
	// Give JS a moment to settle after load event.
	_ = page.WaitStable(waitStableDur)
	return nil
}

// ---------------------------------------------------------------------------
// Sub-commands
// ---------------------------------------------------------------------------

func (c *Command) execNavigate(ctx context.Context, args []string) (string, error) {
	opts, err := parseCommonOpts(args, true, c.Usage())
	if err != nil {
		return "", err
	}

	page, cleanup, err := c.newPage(ctx, opts)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if err := navigateTo(page, opts.url); err != nil {
		return "", fmt.Errorf("browser navigate: %w", err)
	}

	el, err := page.Element("body")
	if err != nil {
		return "", fmt.Errorf("browser navigate: body element: %w", err)
	}
	text, err := el.Text()
	if err != nil {
		return "", fmt.Errorf("browser navigate: extract text: %w", err)
	}

	return formatTextOutput(opts.url, text), nil
}

func (c *Command) execScreenshot(ctx context.Context, args []string) (string, error) {
	opts, err := parseScreenshotOpts(args, c.Usage())
	if err != nil {
		return "", err
	}

	page, cleanup, err := c.newPage(ctx, opts.commonOpts)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if err := navigateTo(page, opts.url); err != nil {
		return "", fmt.Errorf("browser screenshot: %w", err)
	}

	var data []byte
	if opts.fullPage {
		data, err = page.Screenshot(true, &proto.PageCaptureScreenshot{
			Format:  proto.PageCaptureScreenshotFormatPng,
			Quality: gson.Int(90),
		})
	} else {
		data, err = page.Screenshot(false, &proto.PageCaptureScreenshot{
			Format:  proto.PageCaptureScreenshotFormatPng,
			Quality: gson.Int(90),
		})
	}
	if err != nil {
		return "", fmt.Errorf("browser screenshot: capture: %w", err)
	}

	outFile := opts.output
	if outFile == "" {
		outFile = fmt.Sprintf("screenshot_%d.png", time.Now().Unix())
	}
	outPath := resolvePath(c.workDir, outFile)

	if err := writeFile(outPath, data); err != nil {
		return "", fmt.Errorf("browser screenshot: write: %w", err)
	}

	return fmt.Sprintf("Screenshot saved: %s\nURL: %s\nSize: %d bytes\nFull-page: %v",
		outPath, opts.url, len(data), opts.fullPage), nil
}

func (c *Command) execContent(ctx context.Context, args []string) (string, error) {
	opts, err := parseCommonOpts(args, true, c.Usage())
	if err != nil {
		return "", err
	}

	page, cleanup, err := c.newPage(ctx, opts)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if err := navigateTo(page, opts.url); err != nil {
		return "", fmt.Errorf("browser content: %w", err)
	}

	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("browser content: extract HTML: %w", err)
	}

	return formatHTMLOutput(opts.url, html), nil
}

func (c *Command) execEval(ctx context.Context, args []string) (string, error) {
	opts, err := parseEvalOpts(args, c.Usage())
	if err != nil {
		return "", err
	}

	page, cleanup, err := c.newPage(ctx, opts.commonOpts)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if err := navigateTo(page, opts.url); err != nil {
		return "", fmt.Errorf("browser eval: %w", err)
	}

	// Wrap raw expression in arrow function for rod compatibility.
	jsFunc := fmt.Sprintf("() => (%s)", opts.script)
	res, err := page.Eval(jsFunc)
	if err != nil {
		return "", fmt.Errorf("browser eval: execute: %w", err)
	}

	var result string
	if res.Value.Nil() {
		result = "undefined"
	} else {
		raw, _ := json.MarshalIndent(res.Value, "", "  ")
		result = string(raw)
	}

	return fmt.Sprintf("URL: %s\nScript: %s\n---\n%s", opts.url, opts.script, result), nil
}

func (c *Command) execNetwork(ctx context.Context, args []string) (string, error) {
	opts, err := parseCommonOpts(args, true, c.Usage())
	if err != nil {
		return "", err
	}

	page, cleanup, err := c.newPage(ctx, opts)
	if err != nil {
		return "", err
	}
	defer cleanup()

	recorder := newNetworkRecorder()

	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return "", fmt.Errorf("browser network: enable network events: %w", err)
	}
	defer func() { _ = (proto.NetworkDisable{}).Call(page) }()

	listenCtx, stopListen := context.WithCancel(ctx)
	waitEvents := page.Context(listenCtx).EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			recorder.requestWillBeSent(e)
		},
		func(e *proto.NetworkResponseReceived) {
			recorder.responseReceived(e)
		},
		func(e *proto.NetworkLoadingFinished) {
			recorder.loadingFinished(e)
		},
		func(e *proto.NetworkLoadingFailed) {
			recorder.loadingFailed(e)
		},
	)
	done := make(chan struct{})
	go func() {
		waitEvents()
		close(done)
	}()
	defer func() {
		stopListen()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()

	if err := navigateTo(page, opts.url); err != nil {
		return "", fmt.Errorf("browser network: %w", err)
	}

	// Allow extra time for async requests to complete after page load.
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("browser network: wait after load: %w", ctx.Err())
	case <-time.After(2 * time.Second):
	}

	return formatNetworkOutput(opts.url, recorder.snapshot()), nil
}

func (c *Command) execPDF(ctx context.Context, args []string) (string, error) {
	opts, err := parsePDFOpts(args, c.Usage())
	if err != nil {
		return "", err
	}

	page, cleanup, err := c.newPage(ctx, opts.commonOpts)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if err := navigateTo(page, opts.url); err != nil {
		return "", fmt.Errorf("browser pdf: %w", err)
	}

	reader, err := page.PDF(&proto.PagePrintToPDF{
		PrintBackground: true,
		MarginTop:       gson.Num(0.4),
		MarginBottom:    gson.Num(0.4),
		MarginLeft:      gson.Num(0.4),
		MarginRight:     gson.Num(0.4),
	})
	if err != nil {
		return "", fmt.Errorf("browser pdf: generate: %w", err)
	}
	defer func() { _ = reader.Close() }()

	data, err := readAll(reader)
	if err != nil {
		return "", fmt.Errorf("browser pdf: read: %w", err)
	}

	outFile := opts.output
	if outFile == "" {
		outFile = fmt.Sprintf("page_%d.pdf", time.Now().Unix())
	}
	outPath := resolvePath(c.workDir, outFile)

	if err := writeFile(outPath, data); err != nil {
		return "", fmt.Errorf("browser pdf: write: %w", err)
	}

	return fmt.Sprintf("PDF saved: %s\nURL: %s\nSize: %d bytes", outPath, opts.url, len(data)), nil
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

type commonOpts struct {
	url       string
	timeout   time.Duration
	userAgent string
}

type screenshotOpts struct {
	commonOpts
	output   string
	fullPage bool
}

type evalOpts struct {
	commonOpts
	script string
}

type pdfOpts struct {
	commonOpts
	output string
}

func parseCommonOpts(args []string, requireURL bool, usage string) (commonOpts, error) {
	opts := commonOpts{timeout: defaultTimeout}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("browser: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("browser: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if requireURL && opts.url == "" {
		return opts, fmt.Errorf("browser: URL is required\n\n%s", usage)
	}
	return opts, nil
}

func parseScreenshotOpts(args []string, usage string) (screenshotOpts, error) {
	opts := screenshotOpts{
		commonOpts: commonOpts{timeout: defaultTimeout},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("browser: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--output":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --output requires a value")
			}
			i++
			opts.output = args[i]
		case "--full-page":
			opts.fullPage = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("browser: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if opts.url == "" {
		return opts, fmt.Errorf("browser: URL is required\n\n%s", usage)
	}
	return opts, nil
}

func parseEvalOpts(args []string, usage string) (evalOpts, error) {
	opts := evalOpts{
		commonOpts: commonOpts{timeout: defaultTimeout},
	}
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("browser: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--script":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --script requires a value")
			}
			i++
			opts.script = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("browser: unknown flag: %s", args[i])
			}
			positional = append(positional, args[i])
		}
	}

	if len(positional) > 0 {
		opts.url = positional[0]
	}
	if opts.script == "" && len(positional) > 1 {
		opts.script = strings.Join(positional[1:], " ")
	}

	if opts.url == "" {
		return opts, fmt.Errorf("browser: URL is required\n\n%s", usage)
	}
	if opts.script == "" {
		return opts, fmt.Errorf("browser: JavaScript expression is required\n\n%s", usage)
	}
	return opts, nil
}

func parsePDFOpts(args []string, usage string) (pdfOpts, error) {
	opts := pdfOpts{
		commonOpts: commonOpts{timeout: defaultTimeout},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("browser: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--output":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("browser: --output requires a value")
			}
			i++
			opts.output = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("browser: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if opts.url == "" {
		return opts, fmt.Errorf("browser: URL is required\n\n%s", usage)
	}
	return opts, nil
}

// ---------------------------------------------------------------------------
// Output formatting
// ---------------------------------------------------------------------------

func formatTextOutput(url, text string) string {
	if len(text) > maxOutputLen {
		text = text[:maxOutputLen] + fmt.Sprintf(
			"\n\n[Content truncated: showing %d of %d characters]",
			maxOutputLen, len(text))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("URL: %s\n", url))
	sb.WriteString(fmt.Sprintf("Chars: %d\n", len(text)))
	sb.WriteString("---\n\n")
	sb.WriteString(text)
	return sb.String()
}

func formatHTMLOutput(url, html string) string {
	if len(html) > maxOutputLen {
		html = html[:maxOutputLen] + fmt.Sprintf(
			"\n\n[Content truncated: showing %d of %d characters]",
			maxOutputLen, len(html))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("URL: %s\n", url))
	sb.WriteString(fmt.Sprintf("Size: %d bytes\n", len(html)))
	sb.WriteString("---\n\n")
	sb.WriteString(html)
	return sb.String()
}

func formatNetworkOutput(url string, entries []netEntry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("URL: %s\n", url))
	sb.WriteString(fmt.Sprintf("Captured: %d requests\n", len(entries)))
	sb.WriteString("---\n\n")

	if len(entries) == 0 {
		sb.WriteString("[No network requests captured]")
		return sb.String()
	}

	// Header
	sb.WriteString(fmt.Sprintf("%-7s %-6s %-40s %-30s %s\n",
		"METHOD", "STATUS", "URL", "CONTENT-TYPE", "SIZE"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")

	for _, e := range entries {
		displayURL := e.URL
		if len(displayURL) > 80 {
			displayURL = displayURL[:77] + "..."
		}
		ct := e.ContentType
		if idx := strings.Index(ct, ";"); idx > 0 {
			ct = ct[:idx]
		}
		sb.WriteString(fmt.Sprintf("%-7s %-6d %-40s %-30s %d\n",
			e.Method, e.Status, displayURL, ct, e.Size))
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func resolvePath(workDir, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(workDir, file)
}

// netEntry used in network capture output formatting.
type netEntry struct {
	Method      string `json:"method"`
	URL         string `json:"url"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

type networkRecorder struct {
	mu      sync.Mutex
	order   []proto.NetworkRequestID
	entries map[proto.NetworkRequestID]*netEntry
}

func newNetworkRecorder() *networkRecorder {
	return &networkRecorder{entries: make(map[proto.NetworkRequestID]*netEntry)}
}

func (r *networkRecorder) requestWillBeSent(e *proto.NetworkRequestWillBeSent) {
	if e == nil || e.Request == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.ensureLocked(e.RequestID)
	entry.Method = e.Request.Method
	entry.URL = e.Request.URL
}

func (r *networkRecorder) responseReceived(e *proto.NetworkResponseReceived) {
	if e == nil || e.Response == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.ensureLocked(e.RequestID)
	if entry.URL == "" {
		entry.URL = e.Response.URL
	}
	entry.Status = e.Response.Status
	entry.ContentType = responseContentType(e.Response)
}

func (r *networkRecorder) loadingFinished(e *proto.NetworkLoadingFinished) {
	if e == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.ensureLocked(e.RequestID)
	entry.Size = int(e.EncodedDataLength)
}

func (r *networkRecorder) loadingFailed(e *proto.NetworkLoadingFailed) {
	if e == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.ensureLocked(e.RequestID)
	entry.ContentType = e.ErrorText
}

func (r *networkRecorder) snapshot() []netEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	captured := make([]netEntry, 0, len(r.order))
	for _, id := range r.order {
		if entry := r.entries[id]; entry != nil {
			captured = append(captured, *entry)
		}
	}
	return captured
}

func (r *networkRecorder) ensureLocked(id proto.NetworkRequestID) *netEntry {
	if entry := r.entries[id]; entry != nil {
		return entry
	}
	entry := &netEntry{}
	r.entries[id] = entry
	r.order = append(r.order, id)
	return entry
}

func responseContentType(resp *proto.NetworkResponse) string {
	if resp == nil {
		return ""
	}
	for name, value := range resp.Headers {
		if strings.EqualFold(name, "content-type") {
			return value.Str()
		}
	}
	return resp.MIMEType
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
