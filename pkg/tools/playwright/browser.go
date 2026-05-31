//go:build browser

package playwright

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
	defaultTimeout = 30 * time.Second
	maxOutputLen   = 100_000
	waitStableDur  = 300 * time.Millisecond
)

// Command implements command.PseudoCommand for headless browser operations.
type Command struct {
	mu      sync.Mutex
	browser *rod.Browser
	workDir string

	// Session management for multi-step interactive workflows.
	openMu     sync.Mutex
	sessions   map[string]*Session
	sessionsMu sync.Mutex
	gcRunning  bool
	gcStop     chan struct{}

	// Proxy URL for Chrome's --proxy-server flag. Updated via SetProxy().
	proxyMu  sync.RWMutex
	proxyURL string
}

// New creates a playwright pseudo-command.
func New(workDir string) *Command {
	return &Command{workDir: workDir}
}

// SetProxy updates the proxy URL for new browser launches.
func (c *Command) SetProxy(proxyURLStr string) {
	c.proxyMu.Lock()
	defer c.proxyMu.Unlock()
	c.proxyURL = proxyURLStr
}

func (c *Command) Name() string { return "playwright" }

func (c *Command) Usage() string {
	return `playwright - Headless browser for JS-rendered pages, screenshots, network capture, and interactive vulnerability verification
Usage:
  playwright <subcommand> [args] [options]

Unified Subcommands (URL or session):
  goto <url|session> [selector]                  Navigate to URL and return text, or extract text from session
  content <url|session> [selector]               Open URL and return HTML, or extract HTML from session
  evaluate <url|session> <script>                Execute JavaScript on URL or session
  screenshot <url|session> [options]             Screenshot URL or session page
  network <url|session> [--start|--dump|--stop]  Capture URL traffic, or control session capture

Stateless-only Subcommands:
  pdf                                             Generate PDF of the rendered page

Session Subcommands (multi-step interactive workflows):
  open <url> [--session name] [--ttl secs]        Open a persistent page (no auto-expire by default)
             [--op-timeout secs]                  Per-operation timeout for session commands
             [--no-speed-up]                      Disable setTimeout/setInterval acceleration
  close <session>                                 Close a session and release resources
  sessions                                        List all active sessions

  Navigation:
    reload <session>                            Reload the current page
    go-back <session>                           Navigate back in history
    go-forward <session>                        Navigate forward in history

  Discovery (page discovery & smart form filling):
    discover <session>                          List forms, buttons, event listeners, SPA routes
    autofill <session> [--form N] [--data k=v]  Smart form fill using katana heuristics

  Interaction:
    click <session> <selector>                  Click an element
    dblclick <session> <selector>               Double-click an element
    fill <session> <selector> <value>           Type into an input field
    press <session> <selector> <key>            Press a key (Enter, Tab, Shift+Enter, etc.)
    hover <session> <selector>                  Hover over an element
    select-option <session> <selector> <value>  Select a dropdown option
    check <session> <selector>                  Check a checkbox
    uncheck <session> <selector>                Uncheck a checkbox
    set-input-files <session> <sel> <path...>   Set files for a file input (upload)
    focus <session> <selector>                  Focus an element
    blur <session> <selector>                   Blur (unfocus) an element
    wait-for <session> <selector|--idle|--stable>  Wait for element/network/DOM
    wait-for-url <session> <url-substring>      Wait for navigation to matching URL
    wait-for-request <session> <url-substring>  Wait for a matching network request
    wait-for-response <session> <url-substring> Wait for a matching network response
    dispatch-event <session> <selector> <type>  Dispatch a DOM event (change, input, submit, etc.)

  Extraction:
    text-content <session> [selector]           Extract visible text from session
    inner-html <session> [selector]             Extract HTML from session
    get-attribute <session> <selector> <name>   Get an element attribute value
    input-value <session> <selector>            Get the current value of an input
    is-visible <session> <selector>             Check if an element is visible
    evaluate <session> <script>                 Execute JS in session context
    screenshot <session> [options]              Screenshot session page
    url <session>                               Current URL and page title

  Headers & Interception:
    set-extra-headers <session> <json>          Add extra HTTP headers (e.g. Authorization)
    set-viewport <session> <width> <height>     Set viewport dimensions
    route <session> <pattern> --fulfill|--abort|--continue [options]
    unroute <session>                           Remove all request interception routes

  Vuln Verification:
    dialog <session> --arm|--check|--disarm     JS dialog capture (XSS verification)
    cookies <session> --list|--set k=v|--clear  Cookie management

Common Options:
  --timeout <seconds>     Page load timeout in seconds (default: 30)
  --user-agent <string>   Custom User-Agent header
  --selector <selector>   Element screenshot selector (session screenshot only)
  --ttl 0                 Persistent session (no auto-expire)

Examples:
  playwright goto https://example.com
  playwright open https://target.com/login --session s1
  playwright discover s1
  playwright fill s1 "input[name=user]" "admin"
  playwright press s1 "input[name=user]" Enter
  playwright hover s1 "nav .dropdown"
  playwright set-input-files s1 "input[type=file]" /tmp/shell.php
  playwright set-extra-headers s1 '{"Authorization":"Bearer token123"}'
  playwright route s1 "*/api/check" --fulfill --status 200 --body '{"valid":true}'
  playwright get-attribute s1 "a.link" href
  playwright is-visible s1 "#admin-panel"
  playwright go-back s1
  playwright close s1`
}

// Execute dispatches to the appropriate sub-command.
func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright: subcommand required\n\n%s", c.Usage())
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	// --- Unified URL/session commands (Playwright-aligned) ---
	case "goto", "navigate": // navigate is backward-compat alias
		if c.firstArgIsSession(subArgs) {
			return c.execSessionText(ctx, subArgs, "goto")
		}
		return c.execNavigate(ctx, subArgs)
	case "screenshot":
		if c.firstArgIsSession(subArgs) {
			return c.execSessionScreenshot(ctx, subArgs)
		}
		return c.execScreenshot(ctx, subArgs)
	case "content":
		if c.firstArgIsSession(subArgs) {
			return c.execSessionContent(ctx, subArgs)
		}
		return c.execContent(ctx, subArgs)
	case "evaluate", "eval": // eval is backward-compat alias
		if c.firstArgIsSession(subArgs) {
			return c.execSessionEval(ctx, subArgs)
		}
		return c.execEval(ctx, subArgs)
	case "network", "netcap": // netcap is backward-compat alias
		if c.firstArgIsSession(subArgs) {
			return c.execSessionNetwork(ctx, subArgs)
		}
		return c.execNetwork(ctx, subArgs)
	case "text-content", "text": // text is backward-compat alias
		return c.execSessionText(ctx, subArgs, "text-content")
	case "inner-html", "html": // html is backward-compat alias
		return c.execSessionContent(ctx, subArgs)
	case "seval":
		return c.execSessionEval(ctx, subArgs)
	case "sshot":
		return c.execSessionScreenshot(ctx, subArgs)

	// --- Stateless-only ---
	case "pdf":
		return c.execPDF(ctx, subArgs)

	// --- Session lifecycle ---
	case "open":
		return c.execOpen(ctx, subArgs)
	case "close":
		return c.execClose(ctx, subArgs)
	case "sessions":
		return c.execSessions(ctx, subArgs)

	// --- Discovery ---
	case "discover":
		return c.execDiscover(ctx, subArgs)
	case "autofill":
		return c.execAutofill(ctx, subArgs)

	// --- Navigation ---
	case "reload":
		return c.execReload(ctx, subArgs)
	case "go-back", "back":
		return c.execGoBack(ctx, subArgs)
	case "go-forward", "forward":
		return c.execGoForward(ctx, subArgs)

	// --- Interactive (Playwright-aligned) ---
	case "click":
		return c.execClick(ctx, subArgs)
	case "fill":
		return c.execFill(ctx, subArgs)
	case "press":
		return c.execPress(ctx, subArgs)
	case "hover":
		return c.execHover(ctx, subArgs)
	case "dblclick":
		return c.execDblclick(ctx, subArgs)
	case "select-option", "select": // select is backward-compat alias
		return c.execSelect(ctx, subArgs)
	case "check":
		return c.execCheck(ctx, subArgs)
	case "uncheck":
		return c.execUncheck(ctx, subArgs)
	case "set-input-files":
		return c.execSetInputFiles(ctx, subArgs)
	case "focus":
		return c.execFocus(ctx, subArgs)
	case "blur":
		return c.execBlur(ctx, subArgs)
	case "wait-for", "wait": // wait is backward-compat alias
		return c.execWait(ctx, subArgs)
	case "wait-for-url":
		return c.execWaitForURL(ctx, subArgs)
	case "wait-for-request":
		return c.execWaitForRequest(ctx, subArgs)
	case "wait-for-response":
		return c.execWaitForResponse(ctx, subArgs)

	// --- Extraction ---
	case "url":
		return c.execURL(ctx, subArgs)
	case "get-attribute":
		return c.execGetAttribute(ctx, subArgs)
	case "input-value":
		return c.execInputValue(ctx, subArgs)
	case "is-visible":
		return c.execIsVisible(ctx, subArgs)

	// --- Vuln verification ---
	case "dialog":
		return c.execDialog(ctx, subArgs)
	case "cookies":
		return c.execCookies(ctx, subArgs)

	// --- Network & Headers ---
	case "set-extra-headers":
		return c.execSetExtraHeaders(ctx, subArgs)
	case "set-viewport":
		return c.execSetViewport(ctx, subArgs)
	case "dispatch-event":
		return c.execDispatchEvent(ctx, subArgs)
	case "route":
		return c.execRoute(ctx, subArgs)
	case "unroute":
		return c.execUnroute(ctx, subArgs)

	default:
		return "", fmt.Errorf("playwright: unknown subcommand %q\n\n%s", sub, c.Usage())
	}
}

// Close shuts down the browser process if running.
func (c *Command) Close() {
	c.closeAllSessions()

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
		Set("disable-dev-shm-usage").
		Set("ignore-certificate-errors").
		Set("allow-insecure-localhost")

	c.proxyMu.RLock()
	proxy := c.proxyURL
	c.proxyMu.RUnlock()
	if proxy != "" {
		l = l.Set("proxy-server", proxy)
	}

	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("playwright: launch failed: %w", err)
	}

	b := rod.New().ControlURL(controlURL)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("playwright: connect failed: %w", err)
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
		return nil, nil, fmt.Errorf("playwright: incognito context: %w", err)
	}

	page, err := incognito.Page(proto.TargetCreateTarget{})
	if err != nil {
		_ = incognito.Close()
		return nil, nil, fmt.Errorf("playwright: new page: %w", err)
	}

	// Inject stealth anti-detection JS (same as go-rod/stealth but on incognito page).
	if _, err := page.EvalOnNewDocument(stealth.JS); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return nil, nil, fmt.Errorf("playwright: stealth inject: %w", err)
	}

	if opts.userAgent != "" {
		if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
			UserAgent: opts.userAgent,
		}); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return nil, nil, fmt.Errorf("playwright: set user-agent: %w", err)
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
		return "", fmt.Errorf("playwright navigate: %w", err)
	}

	el, err := page.Element("body")
	if err != nil {
		return "", fmt.Errorf("playwright navigate: body element: %w", err)
	}
	text, err := el.Text()
	if err != nil {
		return "", fmt.Errorf("playwright navigate: extract text: %w", err)
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
		return "", fmt.Errorf("playwright screenshot: %w", err)
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
		return "", fmt.Errorf("playwright screenshot: capture: %w", err)
	}

	outFile := opts.output
	if outFile == "" {
		outFile = fmt.Sprintf("screenshot_%d.png", time.Now().Unix())
	}
	outPath := resolvePath(c.workDir, outFile)

	if err := writeFile(outPath, data); err != nil {
		return "", fmt.Errorf("playwright screenshot: write: %w", err)
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
		return "", fmt.Errorf("playwright content: %w", err)
	}

	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("playwright content: extract HTML: %w", err)
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
		return "", fmt.Errorf("playwright eval: %w", err)
	}

	// Wrap raw expression in arrow function for rod compatibility.
	jsFunc := fmt.Sprintf("() => (%s)", opts.script)
	res, err := page.Eval(jsFunc)
	if err != nil {
		return "", fmt.Errorf("playwright eval: execute: %w", err)
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
		return "", fmt.Errorf("playwright network: enable network events: %w", err)
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
		return "", fmt.Errorf("playwright network: %w", err)
	}

	// Allow extra time for async requests to complete after page load.
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("playwright network: wait after load: %w", ctx.Err())
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
		return "", fmt.Errorf("playwright pdf: %w", err)
	}

	reader, err := page.PDF(&proto.PagePrintToPDF{
		PrintBackground: true,
		MarginTop:       gson.Num(0.4),
		MarginBottom:    gson.Num(0.4),
		MarginLeft:      gson.Num(0.4),
		MarginRight:     gson.Num(0.4),
	})
	if err != nil {
		return "", fmt.Errorf("playwright pdf: generate: %w", err)
	}
	defer func() { _ = reader.Close() }()

	data, err := readAll(reader)
	if err != nil {
		return "", fmt.Errorf("playwright pdf: read: %w", err)
	}

	outFile := opts.output
	if outFile == "" {
		outFile = fmt.Sprintf("page_%d.pdf", time.Now().Unix())
	}
	outPath := resolvePath(c.workDir, outFile)

	if err := writeFile(outPath, data); err != nil {
		return "", fmt.Errorf("playwright pdf: write: %w", err)
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
				return opts, fmt.Errorf("playwright: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("playwright: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("playwright: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("playwright: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if requireURL && opts.url == "" {
		return opts, fmt.Errorf("playwright: URL is required\n\n%s", usage)
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
				return opts, fmt.Errorf("playwright: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("playwright: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("playwright: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--output":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("playwright: --output requires a value")
			}
			i++
			opts.output = args[i]
		case "--full-page":
			opts.fullPage = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("playwright: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if opts.url == "" {
		return opts, fmt.Errorf("playwright: URL is required\n\n%s", usage)
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
				return opts, fmt.Errorf("playwright: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("playwright: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("playwright: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--script":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("playwright: --script requires a value")
			}
			i++
			opts.script = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("playwright: unknown flag: %s", args[i])
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
		return opts, fmt.Errorf("playwright: URL is required\n\n%s", usage)
	}
	if opts.script == "" {
		return opts, fmt.Errorf("playwright: JavaScript expression is required\n\n%s", usage)
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
				return opts, fmt.Errorf("playwright: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("playwright: --timeout must be an integer: %w", err)
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("playwright: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--output":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("playwright: --output requires a value")
			}
			i++
			opts.output = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("playwright: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if opts.url == "" {
		return opts, fmt.Errorf("playwright: URL is required\n\n%s", usage)
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
