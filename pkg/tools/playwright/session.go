//go:build browser

package playwright

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	katanajs "github.com/projectdiscovery/katana/pkg/engine/headless/js"
)

const (
	// defaultSessionTimeout: sessions never auto-expire by default.
	// Use --ttl <seconds> to opt-in to auto-expiry.
	defaultSessionTimeout          = time.Duration(math.MaxInt64)
	defaultSessionOperationTimeout = 30 * time.Second
	maxSessions                    = 8
	gcInterval                     = 15 * time.Second

	// persistentTTL is the sentinel value for "never expire".
	persistentTTL = time.Duration(math.MaxInt64)
)

// Session holds a persistent page across multiple Execute() calls.
type Session struct {
	Name      string
	Page      *rod.Page
	Incognito *rod.Browser // incognito context
	CreatedAt time.Time
	LastUsed  time.Time
	Timeout   time.Duration

	// OperationTimeout limits a single interactive operation on this page.
	OperationTimeout time.Duration
	opMu             sync.Mutex

	// Dialog capture
	dialogMu     sync.Mutex
	dialogArmed  bool
	dialogCancel context.CancelFunc
	dialogEvents []DialogEvent

	// Network capture
	networkMu       sync.Mutex
	networkRecorder *networkRecorder
	networkCancel   context.CancelFunc
	networkActive   bool
}

// touch updates LastUsed timestamp.
func (s *Session) touch() { s.LastUsed = time.Now() }

// expired reports whether the session has exceeded its TTL.
// Sessions with persistentTTL (--ttl 0) never expire.
func (s *Session) expired() bool {
	if s.Timeout == persistentTTL {
		return false
	}
	return time.Since(s.LastUsed) > s.Timeout
}

// withPage serializes a single operation against the persistent page and
// applies the session's per-operation timeout.
func (s *Session) withPage(ctx context.Context, fn func(*rod.Page) (string, error)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.OperationTimeout
	if timeout <= 0 {
		timeout = defaultSessionOperationTimeout
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	if s.Page == nil {
		return "", fmt.Errorf("playwright: session %q is closed", s.Name)
	}

	opCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return fn(s.Page.Context(opCtx))
}

// cleanup releases page and incognito resources, stopping any
// armed dialog or network listeners first.
func (s *Session) cleanup() {
	s.dialogMu.Lock()
	if s.dialogCancel != nil {
		s.dialogCancel()
		s.dialogCancel = nil
	}
	s.dialogArmed = false
	s.dialogMu.Unlock()

	s.networkMu.Lock()
	if s.networkCancel != nil {
		s.networkCancel()
		s.networkCancel = nil
	}
	s.networkActive = false
	s.networkMu.Unlock()

	s.opMu.Lock()
	defer s.opMu.Unlock()

	if s.Page != nil {
		_ = s.Page.Close()
		s.Page = nil
	}
	if s.Incognito != nil {
		_ = s.Incognito.Close()
		s.Incognito = nil
	}
}

// sessionCounter provides unique auto-increment IDs.
var sessionCounter atomic.Int64

func nextSessionName() string {
	n := sessionCounter.Add(1)
	return fmt.Sprintf("s%d", n)
}

// ---------------------------------------------------------------------------
// Session management on Command
// ---------------------------------------------------------------------------

func (c *Command) initSessions() {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	if c.sessions == nil {
		c.sessions = make(map[string]*Session)
	}
}

func (c *Command) startGC() {
	c.gcOnce.Do(func() {
		stop := make(chan struct{})
		c.gcStop = stop
		go func() {
			ticker := time.NewTicker(gcInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					c.reapExpiredSessions()
				case <-stop:
					return
				}
			}
		}()
	})
}

func (c *Command) reapExpiredSessions() {
	c.sessionsMu.Lock()
	var expired []*Session
	for name, sess := range c.sessions {
		if sess.expired() {
			expired = append(expired, sess)
			delete(c.sessions, name)
		}
	}
	c.sessionsMu.Unlock()

	for _, sess := range expired {
		sess.cleanup()
	}
}

func (c *Command) getSession(name string) (*Session, error) {
	c.sessionsMu.Lock()
	sess, ok := c.sessions[name]
	if !ok {
		c.sessionsMu.Unlock()
		return nil, fmt.Errorf("playwright: session %q not found", name)
	}
	if sess.expired() {
		delete(c.sessions, name)
		c.sessionsMu.Unlock()
		sess.cleanup()
		return nil, fmt.Errorf("playwright: session %q expired", name)
	}
	sess.touch()
	c.sessionsMu.Unlock()
	return sess, nil
}

func (c *Command) firstArgIsSession(args []string) bool {
	if len(args) == 0 {
		return false
	}
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	_, ok := c.sessions[args[0]]
	return ok
}

func (c *Command) closeAllSessions() {
	c.sessionsMu.Lock()
	sessions := make([]*Session, 0, len(c.sessions))
	for name, sess := range c.sessions {
		sessions = append(sessions, sess)
		delete(c.sessions, name)
	}
	if c.gcStop != nil {
		close(c.gcStop)
		c.gcStop = nil
	}
	c.sessionsMu.Unlock()

	for _, sess := range sessions {
		sess.cleanup()
	}
}

// ---------------------------------------------------------------------------
// open / close / sessions sub-commands
// ---------------------------------------------------------------------------

func (c *Command) execOpen(ctx context.Context, args []string) (string, error) {
	opts, sessName, sessTTL, opTimeout, noSpeedUp, err := parseOpenOpts(args, c.Usage())
	if err != nil {
		return "", err
	}

	c.openMu.Lock()
	defer c.openMu.Unlock()

	c.initSessions()
	c.startGC()
	c.reapExpiredSessions()

	c.sessionsMu.Lock()
	if len(c.sessions) >= maxSessions {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright open: max sessions (%d) reached; close an existing session first", maxSessions)
	}
	if _, exists := c.sessions[sessName]; exists {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright open: session %q already exists", sessName)
	}
	c.sessionsMu.Unlock()

	// Launch browser and create incognito page.
	b, err := c.getOrLaunchBrowser()
	if err != nil {
		return "", err
	}
	incognito, err := b.Incognito()
	if err != nil {
		return "", fmt.Errorf("playwright open: incognito: %w", err)
	}
	page, err := incognito.Page(proto.TargetCreateTarget{})
	if err != nil {
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: new page: %w", err)
	}

	// --- Script injection order matters ---
	// 1. Stealth anti-detection
	if _, err := page.EvalOnNewDocument(stealth.JS); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: stealth: %w", err)
	}

	// 2. Activate katana hooks BEFORE page-init.js registers them.
	//    page-init.js checks window.__katanaHooksOptions.hooked === true
	//    to decide whether to install event listener capture, SPA route
	//    hooks, and setTimeout acceleration.
	hooksActivation := `window.__katanaHooksOptions = { hooked: true, preventFormReset: true };`
	if _, err := page.EvalOnNewDocument(hooksActivation); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: hooks activation: %w", err)
	}

	// 3. Optionally disable setTimeout/setInterval acceleration.
	//    page-init.js's hookMiscellaneousUtilities() redefines both timers with
	//    a 0.1x factor. By pinning the native timers as non-configurable here —
	//    before katana injects — katana's Object.defineProperty on them throws,
	//    and that throw is swallowed by page-init.js's own try/catch. The
	//    form-reset and window.close hooks still install since they run first.
	if noSpeedUp {
		pinTimers := `(function () {
    Object.defineProperty(window, "setTimeout", { value: window.setTimeout, writable: false, configurable: false });
    Object.defineProperty(window, "setInterval", { value: window.setInterval, writable: false, configurable: false });
})();`
		if _, err := page.EvalOnNewDocument(pinTimers); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: pin timers: %w", err)
		}
	}

	// 4. Katana JS environment (utils.js + page-init.js via EvalOnNewDocument)
	if err := katanajs.InitJavascriptEnv(page); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: katana js: %w", err)
	}

	if opts.userAgent != "" {
		if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: opts.userAgent}); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: set user-agent: %w", err)
		}
	}

	navPage := page.Context(ctx).Timeout(opts.timeout)
	if err := navigateTo(navPage, opts.url); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: %w", err)
	}

	sess := &Session{
		Name:      sessName,
		Page:      page,
		Incognito: incognito,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		Timeout:   sessTTL,

		OperationTimeout: opTimeout,
	}

	c.sessionsMu.Lock()
	c.sessions[sessName] = sess
	c.sessionsMu.Unlock()

	info, _ := navPage.Info()
	title := ""
	if info != nil {
		title = info.Title
	}

	ttlDisplay := sessTTL.String()
	if sessTTL == persistentTTL {
		ttlDisplay = "∞ (persistent)"
	}

	return fmt.Sprintf("Session: %s\nURL: %s\nTitle: %s\nTTL: %s\nOperation timeout: %s",
		sessName, opts.url, title, ttlDisplay, opTimeout), nil
}

func (c *Command) execClose(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright close: session name required")
	}
	name := args[0]

	c.sessionsMu.Lock()
	sess, ok := c.sessions[name]
	if !ok {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright close: session %q not found", name)
	}
	delete(c.sessions, name)
	c.sessionsMu.Unlock()

	sess.cleanup()
	return fmt.Sprintf("Session %q closed", name), nil
}

// execSessions lists all active sessions.
func (c *Command) execSessions(ctx context.Context, args []string) (string, error) {
	c.initSessions()

	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	if len(c.sessions) == 0 {
		return "No active sessions", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Active Sessions (%d):\n", len(c.sessions)))
	for name, sess := range c.sessions {
		age := time.Since(sess.CreatedAt).Round(time.Second)
		var ttlStr string
		if sess.Timeout == persistentTTL {
			ttlStr = "∞"
		} else {
			remaining := sess.Timeout - time.Since(sess.LastUsed)
			if remaining < 0 {
				remaining = 0
			}
			ttlStr = remaining.Round(time.Second).String()
		}
		url := "(unknown)"
		if sess.Page != nil {
			if info, err := sess.Page.Info(); err == nil && info != nil {
				url = info.URL
			}
		}
		sb.WriteString(fmt.Sprintf("  %-8s %s  age=%s  ttl=%s\n", name, url, age, ttlStr))
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Argument parsing for open
// ---------------------------------------------------------------------------

func parseOpenOpts(args []string, usage string) (commonOpts, string, time.Duration, time.Duration, bool, error) {
	opts := commonOpts{timeout: defaultTimeout}
	sessName := ""
	sessTTL := defaultSessionTimeout
	opTimeout := defaultSessionOperationTimeout
	noSpeedUp := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --timeout must be an integer: %w", err)
			}
			if secs <= 0 {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --timeout must be > 0")
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--session":
			if i+1 >= len(args) {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --session requires a value")
			}
			i++
			sessName = args[i]
		case "--ttl":
			if i+1 >= len(args) {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --ttl requires a value in seconds")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --ttl must be an integer: %w", err)
			}
			if secs < 0 {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --ttl must be >= 0")
			}
			if secs == 0 {
				sessTTL = persistentTTL // never expire
			} else {
				sessTTL = time.Duration(secs) * time.Second
			}
		case "--op-timeout":
			if i+1 >= len(args) {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --op-timeout requires a value in seconds")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --op-timeout must be an integer: %w", err)
			}
			if secs <= 0 {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: --op-timeout must be > 0")
			}
			opTimeout = time.Duration(secs) * time.Second
		case "--no-speed-up":
			noSpeedUp = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, "", 0, 0, false, fmt.Errorf("playwright open: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if opts.url == "" {
		return opts, "", 0, 0, false, fmt.Errorf("playwright open: URL is required\n\n%s", usage)
	}
	if sessName == "" {
		sessName = nextSessionName()
	}
	return opts, sessName, sessTTL, opTimeout, noSpeedUp, nil
}
