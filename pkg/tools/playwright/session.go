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
	// persistentTTL is the sentinel value for "never expire".
	// Sessions default to this; use --ttl <seconds> to opt-in to auto-expiry.
	persistentTTL                  = time.Duration(math.MaxInt64)
	defaultSessionOperationTimeout = 30 * time.Second
	maxSessions                    = 8
	gcInterval                     = 15 * time.Second
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

	// Extra headers
	headerMu      sync.Mutex
	headerCleanup func()

	// Request interception
	hijackMu      sync.Mutex
	hijackRouter  *rod.HijackRouter
	hijackRunning bool
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

	s.headerMu.Lock()
	if s.headerCleanup != nil {
		s.headerCleanup()
		s.headerCleanup = nil
	}
	s.headerMu.Unlock()

	s.hijackMu.Lock()
	if s.hijackRouter != nil {
		_ = s.hijackRouter.Stop()
		s.hijackRouter = nil
		s.hijackRunning = false
	}
	s.hijackMu.Unlock()

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
	c.sessionsMu.Lock()
	if c.gcRunning {
		c.sessionsMu.Unlock()
		return
	}
	c.gcRunning = true
	stop := make(chan struct{})
	c.gcStop = stop
	c.sessionsMu.Unlock()

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

// evictLRUSession removes the least-recently-used session to make room.
func (c *Command) evictLRUSession() {
	c.sessionsMu.Lock()
	var oldest *Session
	oldestName := ""
	for name, sess := range c.sessions {
		if oldest == nil || sess.LastUsed.Before(oldest.LastUsed) {
			oldest = sess
			oldestName = name
		}
	}
	if oldest != nil {
		delete(c.sessions, oldestName)
	}
	c.sessionsMu.Unlock()

	if oldest != nil {
		oldest.cleanup()
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
	c.gcRunning = false
	c.sessionsMu.Unlock()

	for _, sess := range sessions {
		sess.cleanup()
	}
}

// ---------------------------------------------------------------------------
// open / close / sessions sub-commands
// ---------------------------------------------------------------------------

func (c *Command) execOpen(ctx context.Context, args []string) (string, error) {
	o, err := parseOpenOpts(args, c.Usage())
	if err != nil {
		return "", err
	}

	c.openMu.Lock()
	defer c.openMu.Unlock()

	c.initSessions()
	c.startGC()
	c.reapExpiredSessions()

	c.sessionsMu.Lock()
	if _, exists := c.sessions[o.sessName]; exists {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright open: session %q already exists", o.sessName)
	}
	needEvict := len(c.sessions) >= maxSessions
	c.sessionsMu.Unlock()

	if needEvict {
		c.evictLRUSession()
	}

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
	hooksActivation := `window.__katanaHooksOptions = { hooked: true, preventFormReset: true };`
	if _, err := page.EvalOnNewDocument(hooksActivation); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: hooks activation: %w", err)
	}

	// 3. Optionally disable setTimeout/setInterval acceleration.
	if o.noSpeedUp {
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

	if o.userAgent != "" {
		if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: o.userAgent}); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: set user-agent: %w", err)
		}
	}

	navPage := page.Context(ctx).Timeout(o.timeout)
	if err := navigateTo(navPage, o.url); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: %w", err)
	}

	sess := &Session{
		Name:      o.sessName,
		Page:      page,
		Incognito: incognito,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		Timeout:   o.ttl,

		OperationTimeout: o.opTimeout,
	}

	c.sessionsMu.Lock()
	c.sessions[o.sessName] = sess
	c.sessionsMu.Unlock()

	info, _ := navPage.Info()
	title := ""
	if info != nil {
		title = info.Title
	}

	ttlDisplay := o.ttl.String()
	if o.ttl == persistentTTL {
		ttlDisplay = "∞ (persistent)"
	}

	return fmt.Sprintf("Session: %s\nURL: %s\nTitle: %s\nTTL: %s\nOperation timeout: %s",
		o.sessName, o.url, title, ttlDisplay, o.opTimeout), nil
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

type openOpts struct {
	commonOpts
	sessName  string
	ttl       time.Duration
	opTimeout time.Duration
	noSpeedUp bool
}

func parseOpenOpts(args []string, usage string) (openOpts, error) {
	o := openOpts{
		commonOpts: commonOpts{timeout: defaultTimeout},
		ttl:        persistentTTL,
		opTimeout:  defaultSessionOperationTimeout,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return o, fmt.Errorf("playwright open: --timeout must be an integer: %w", err)
			}
			if secs <= 0 {
				return o, fmt.Errorf("playwright open: --timeout must be > 0")
			}
			o.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --user-agent requires a value")
			}
			i++
			o.userAgent = args[i]
		case "--session":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --session requires a value")
			}
			i++
			o.sessName = args[i]
		case "--ttl":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --ttl requires a value in seconds")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return o, fmt.Errorf("playwright open: --ttl must be an integer: %w", err)
			}
			if secs < 0 {
				return o, fmt.Errorf("playwright open: --ttl must be >= 0")
			}
			if secs == 0 {
				o.ttl = persistentTTL
			} else {
				o.ttl = time.Duration(secs) * time.Second
			}
		case "--op-timeout":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --op-timeout requires a value in seconds")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return o, fmt.Errorf("playwright open: --op-timeout must be an integer: %w", err)
			}
			if secs <= 0 {
				return o, fmt.Errorf("playwright open: --op-timeout must be > 0")
			}
			o.opTimeout = time.Duration(secs) * time.Second
		case "--no-speed-up":
			o.noSpeedUp = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return o, fmt.Errorf("playwright open: unknown flag: %s", args[i])
			}
			if o.url == "" {
				o.url = args[i]
			}
		}
	}

	if o.url == "" {
		return o, fmt.Errorf("playwright open: URL is required\n\n%s", usage)
	}
	if o.sessName == "" {
		o.sessName = nextSessionName()
	}
	return o, nil
}
