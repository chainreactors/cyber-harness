//go:build browser

// Engine manages the headless browser lifecycle.
// Ported from nuclei pkg/protocols/headless/engine/engine.go.

package headless

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Engine manages the headless browser lifecycle.
type Engine struct {
	browser  *rod.Browser
	external bool
	mu       sync.Mutex

	// Options
	options HeadlessOptions

	// HTTP client for hijack LoadResponse (lazy-initialized).
	httpClient     *http.Client
	httpClientOnce sync.Once
}

// HeadlessOptions configures the headless engine.
type HeadlessOptions struct {
	Proxy          string
	UserAgent      string
	Headers        map[string]string
	ShowBrowser    bool
	PageTimeout    int // seconds, default 30
	DisableCookie  bool
}

// EngineOption configures Engine creation.
type EngineOption func(*Engine)

// WithBrowser injects an existing go-rod browser.
func WithBrowser(b *rod.Browser) EngineOption {
	return func(e *Engine) {
		e.browser = b
		e.external = true
	}
}

// WithProxy sets the proxy server URL.
func WithProxy(proxy string) EngineOption {
	return func(e *Engine) { e.options.Proxy = proxy }
}

// WithUserAgent sets the default user-agent.
func WithUserAgent(ua string) EngineOption {
	return func(e *Engine) { e.options.UserAgent = ua }
}

// WithHeaders sets default extra HTTP headers.
func WithHeaders(h map[string]string) EngineOption {
	return func(e *Engine) { e.options.Headers = h }
}

// WithOptions sets the full options struct.
func WithOptions(opts HeadlessOptions) EngineOption {
	return func(e *Engine) { e.options = opts }
}

// NewEngine creates a headless browser engine.
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Init starts the browser if one wasn't provided externally.
func (e *Engine) Init() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.browser != nil {
		return nil
	}

	l := launcher.New().
		Headless(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-software-rasterizer").
		Set("ignore-certificate-errors").
		Set("allow-insecure-localhost").
		Set("disable-notifications").
		Set("mute-audio").
		Set("window-size", "1920,1080")

	if e.options.Proxy != "" {
		l = l.Set("proxy-server", e.options.Proxy)
	}
	if e.options.ShowBrowser {
		l = l.Headless(false)
	}

	u, err := l.Launch()
	if err != nil {
		return err
	}

	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		return err
	}
	e.browser = browser
	return nil
}

// Browser returns the underlying go-rod browser.
func (e *Engine) Browser() *rod.Browser {
	return e.browser
}

// NewPage creates a new page (without instance isolation).
func (e *Engine) NewPage() (*rod.Page, error) {
	page, err := e.browser.Page(proto.TargetCreateTarget{URL: ""})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// getHTTPClient returns the shared HTTP client for hijack LoadResponse.
func (e *Engine) getHTTPClient() *http.Client {
	e.httpClientOnce.Do(func() {
		timeout := time.Duration(e.options.PageTimeout) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		e.httpClient = newHTTPClient(e.options.Proxy, timeout*3)
	})
	return e.httpClient
}

// Close shuts down the browser if it was created by this engine.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.browser != nil && !e.external {
		e.browser.Close()
		e.browser = nil
	}
}
