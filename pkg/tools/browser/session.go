//go:build browser

package browser

import (
	"context"
	"fmt"
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
	defaultSessionOperationTimeout = 30 * time.Second
	maxSessions                    = 8
)

// Session holds a persistent page across multiple Execute() calls.
// Sessions are explicitly managed: created via "open" and released via "close".
// When the max session limit is reached, the least-recently-used session is
// evicted automatically to make room for a new one.
type Session struct {
	Name      string
	Page      *rod.Page
	Incognito *rod.Browser // incognito context
	CreatedAt time.Time
	LastUsed  time.Time

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
		return "", fmt.Errorf("browser: session %q is closed", s.Name)
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

// evictLRUSession removes the least-recently-used session to make room.
// Caller must NOT hold sessionsMu.
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
		return nil, fmt.Errorf("browser: session %q not found", name)
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
	c.sessionsMu.Unlock()

	for _, sess := range sessions {
		sess.cleanup()
	}
}

// ---------------------------------------------------------------------------
// open / close sub-commands
// ---------------------------------------------------------------------------

func (c *Command) execOpen(ctx context.Context, args []string) (string, error) {
	opts, sessName, opTimeout, err := parseOpenOpts(args, c.Usage())
	if err != nil {
		return "", err
	}

	c.openMu.Lock()
	defer c.openMu.Unlock()

	c.initSessions()

	c.sessionsMu.Lock()
	if _, exists := c.sessions[sessName]; exists {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("browser open: session %q already exists", sessName)
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
		return "", fmt.Errorf("browser open: incognito: %w", err)
	}
	page, err := incognito.Page(proto.TargetCreateTarget{})
	if err != nil {
		_ = incognito.Close()
		return "", fmt.Errorf("browser open: new page: %w", err)
	}

	// Inject stealth + katana JS environment.
	if _, err := page.EvalOnNewDocument(stealth.JS); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("browser open: stealth: %w", err)
	}
	if err := katanajs.InitJavascriptEnv(page); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("browser open: katana js: %w", err)
	}

	if opts.userAgent != "" {
		if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: opts.userAgent}); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("browser open: set user-agent: %w", err)
		}
	}

	navPage := page.Context(ctx).Timeout(opts.timeout)
	if err := navigateTo(navPage, opts.url); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("browser open: %w", err)
	}

	sess := &Session{
		Name:      sessName,
		Page:      page,
		Incognito: incognito,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),

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

	return fmt.Sprintf("Session: %s\nURL: %s\nTitle: %s\nOperation timeout: %s",
		sessName, opts.url, title, opTimeout), nil
}

func (c *Command) execClose(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("browser close: session name required")
	}
	name := args[0]

	c.sessionsMu.Lock()
	sess, ok := c.sessions[name]
	if !ok {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("browser close: session %q not found", name)
	}
	delete(c.sessions, name)
	c.sessionsMu.Unlock()

	sess.cleanup()
	return fmt.Sprintf("Session %q closed", name), nil
}

// ---------------------------------------------------------------------------
// Argument parsing for open
// ---------------------------------------------------------------------------

func parseOpenOpts(args []string, usage string) (commonOpts, string, time.Duration, error) {
	opts := commonOpts{timeout: defaultTimeout}
	sessName := ""
	opTimeout := defaultSessionOperationTimeout

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return opts, "", 0, fmt.Errorf("browser open: --timeout requires a value")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, "", 0, fmt.Errorf("browser open: --timeout must be an integer: %w", err)
			}
			if secs <= 0 {
				return opts, "", 0, fmt.Errorf("browser open: --timeout must be > 0")
			}
			opts.timeout = time.Duration(secs) * time.Second
		case "--user-agent":
			if i+1 >= len(args) {
				return opts, "", 0, fmt.Errorf("browser open: --user-agent requires a value")
			}
			i++
			opts.userAgent = args[i]
		case "--session":
			if i+1 >= len(args) {
				return opts, "", 0, fmt.Errorf("browser open: --session requires a value")
			}
			i++
			sessName = args[i]
		case "--op-timeout":
			if i+1 >= len(args) {
				return opts, "", 0, fmt.Errorf("browser open: --op-timeout requires a value in seconds")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, "", 0, fmt.Errorf("browser open: --op-timeout must be an integer: %w", err)
			}
			if secs <= 0 {
				return opts, "", 0, fmt.Errorf("browser open: --op-timeout must be > 0")
			}
			opTimeout = time.Duration(secs) * time.Second
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, "", 0, fmt.Errorf("browser open: unknown flag: %s", args[i])
			}
			if opts.url == "" {
				opts.url = args[i]
			}
		}
	}

	if opts.url == "" {
		return opts, "", 0, fmt.Errorf("browser open: URL is required\n\n%s", usage)
	}
	if sessName == "" {
		sessName = nextSessionName()
	}
	return opts, sessName, opTimeout, nil
}
