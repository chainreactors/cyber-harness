//go:build full

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/pkg/headless"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/ysmood/gson"
	katanajs "github.com/projectdiscovery/katana/pkg/engine/headless/js"
)

const (
	defaultSessionOperationTimeout = 30 * time.Second
	maxSessions                    = 8
)

// Session holds a persistent page across multiple Execute() calls.
type Session struct {
	Name      string
	Page      *rod.Page
	Incognito *rod.Browser // incognito context
	CreatedAt time.Time

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
	routeEntries  []routeEntry

	// Action recording for nuclei headless template generation.
	rec *recorder

	// playwright-cli parity: save-storage / save-har on close
	saveStoragePath string
	saveHARPath     string

	// Tab management (multi-page support, aligned with playwright-cli tab-* commands).
	tabs      []*rod.Page
	activeTab int

	// attached marks sessions created via attach (external browser, not launched by us).
	attached bool
}

type routeEntry struct {
	Pattern string
	Mode    string
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

	if !s.attached {
		for _, p := range s.tabs {
			if p != nil {
				_ = p.Close()
			}
		}
	}
	s.tabs = nil
	s.Page = nil

	if s.Incognito != nil && !s.attached {
		_ = s.Incognito.Close()
	}
	s.Incognito = nil
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
func (c *Command) evictLRUSession() {
	c.sessionsMu.Lock()
	var oldest *Session
	oldestName := ""
	for name, sess := range c.sessions {
		if oldest == nil || sess.CreatedAt.Before(oldest.CreatedAt) {
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
	c.sessionsMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("playwright: session %q not found", name)
	}
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

	if o.proxyServer != "" {
		c.SetProxy(o.proxyServer)
	}
	if o.headed || o.cdpURL != "" {
		c.SetBrowserMode(o.headed, o.cdpURL)
	}

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

	// 3. Console capture hook (captures all console.* calls into window.__consoleBuffer).
	if _, err := page.EvalOnNewDocument(consoleHookJS); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: console hook: %w", err)
	}

	// 4. Optionally disable setTimeout/setInterval acceleration.
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

	// 5. Katana JS environment
	if err := katanajs.InitJavascriptEnv(page); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: katana js: %w", err)
	}

	// --- playwright-cli parity: context-level configuration ---
	uaOverride := &proto.NetworkSetUserAgentOverride{}
	if o.userAgent != "" {
		uaOverride.UserAgent = o.userAgent
	}
	if o.lang != "" {
		uaOverride.AcceptLanguage = o.lang
	}
	if uaOverride.UserAgent != "" || uaOverride.AcceptLanguage != "" {
		if err := page.SetUserAgent(uaOverride); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: set user-agent: %w", err)
		}
	}

	if o.viewportSize != "" {
		w, h, parseErr := parseViewportSize(o.viewportSize)
		if parseErr != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: %w", parseErr)
		}
		if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
			Width: w, Height: h, DeviceScaleFactor: 1,
		}); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: set viewport: %w", err)
		}
	}

	if o.geolocation != "" {
		lat, lon, parseErr := parseGeolocation(o.geolocation)
		if parseErr != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: %w", parseErr)
		}
		if err := (proto.EmulationSetGeolocationOverride{
			Latitude: &lat, Longitude: &lon, Accuracy: gson.Num(1),
		}).Call(page); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: set geolocation: %w", err)
		}
	}

	if o.timezone != "" {
		if err := (proto.EmulationSetTimezoneOverride{TimezoneID: o.timezone}).Call(page); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: set timezone: %w", err)
		}
	}

	if o.colorScheme != "" {
		features := []*proto.EmulationMediaFeature{
			{Name: "prefers-color-scheme", Value: o.colorScheme},
		}
		if err := (proto.EmulationSetEmulatedMedia{Features: features}).Call(page); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: set color-scheme: %w", err)
		}
	}

	navPage := page.Context(ctx).Timeout(o.timeout)
	if err := navigateTo(navPage, o.url); err != nil {
		_ = page.Close()
		_ = incognito.Close()
		return "", fmt.Errorf("playwright open: %w", err)
	}

	// Load storage state after navigation so localStorage lands on the correct origin.
	if o.loadStoragePath != "" {
		if err := loadStorageState(page, resolvePath(c.workDir, o.loadStoragePath)); err != nil {
			_ = page.Close()
			_ = incognito.Close()
			return "", fmt.Errorf("playwright open: load-storage: %w", err)
		}
	}

	sess := &Session{
		Name:      o.sessName,
		Page:      page,
		Incognito: incognito,
		CreatedAt: time.Now(),

		OperationTimeout: o.opTimeout,
		saveStoragePath:  o.saveStoragePath,
		saveHARPath:      o.saveHARPath,

		tabs:      []*rod.Page{page},
		activeTab: 0,
	}

	if o.record {
		sess.rec = newRecorder(o.url)
		sess.rec.record(RecordedAction{
			Action: headless.ActionNavigate,
			Args:   map[string]string{"url": "{{BaseURL}}"},
		})
	}

	// Auto-capture network requests (always on for `requests` command + optional HAR).
	sess.networkMu.Lock()
	sess.networkRecorder = newNetworkRecorder()
	sess.networkActive = true
	if err := (proto.NetworkEnable{}).Call(page); err == nil {
		capCtx, capCancel := context.WithCancel(context.Background())
		sess.networkCancel = capCancel
		capturedPage := page.Context(capCtx)
		go capturedPage.EachEvent(
			sess.networkRecorder.requestWillBeSent,
			sess.networkRecorder.responseReceived,
			sess.networkRecorder.loadingFinished,
			sess.networkRecorder.loadingFailed,
		)()
	}
	sess.networkMu.Unlock()

	c.sessionsMu.Lock()
	c.sessions[o.sessName] = sess
	c.sessionsMu.Unlock()

	info, _ := navPage.Info()
	title := ""
	if info != nil {
		title = info.Title
	}

	recDisplay := "off"
	if o.record {
		recDisplay = "on"
	}
	return fmt.Sprintf("Session: %s\nURL: %s\nTitle: %s\nOperation timeout: %s\nRecording: %s",
		o.sessName, o.url, title, o.opTimeout, recDisplay), nil
}

func (c *Command) execClose(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright close: session name required")
	}
	name := args[0]

	// Parse optional --save-storage / --save-har overrides.
	saveStor, saveHAR := "", ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--save-storage":
			if i+1 < len(args) {
				i++
				saveStor = args[i]
			}
		case "--save-har":
			if i+1 < len(args) {
				i++
				saveHAR = args[i]
			}
		}
	}

	c.sessionsMu.Lock()
	sess, ok := c.sessions[name]
	if !ok {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright close: session %q not found", name)
	}
	delete(c.sessions, name)
	c.sessionsMu.Unlock()

	var sb strings.Builder

	// Save storage state before cleanup.
	storPath := saveStor
	if storPath == "" {
		storPath = sess.saveStoragePath
	}
	if storPath != "" && sess.Page != nil {
		if err := saveStorageState(sess.Page, resolvePath(c.workDir, storPath)); err != nil {
			sb.WriteString(fmt.Sprintf("Warning: save-storage failed: %v\n", err))
		} else {
			sb.WriteString(fmt.Sprintf("Storage saved: %s\n", storPath))
		}
	}

	// Save HAR before cleanup.
	harPath := saveHAR
	if harPath == "" {
		harPath = sess.saveHARPath
	}
	if harPath != "" {
		sess.networkMu.Lock()
		if sess.networkRecorder != nil {
			entries := sess.networkRecorder.snapshot()
			sess.networkMu.Unlock()
			if err := writeHAR(resolvePath(c.workDir, harPath), entries); err != nil {
				sb.WriteString(fmt.Sprintf("Warning: save-har failed: %v\n", err))
			} else {
				sb.WriteString(fmt.Sprintf("HAR saved: %s (%d entries)\n", harPath, len(entries)))
			}
		} else {
			sess.networkMu.Unlock()
		}
	}

	if sess.rec != nil && sess.rec.len() > 0 {
		sb.WriteString(fmt.Sprintf("Warning: %d recorded actions not saved (use 'record --dump' or 'record --save' before closing)\n", sess.rec.len()))
	}

	sess.cleanup()
	sb.WriteString(fmt.Sprintf("Session %q closed", name))
	return sb.String(), nil
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
		url := "(unknown)"
		if sess.Page != nil {
			if info, err := sess.Page.Info(); err == nil && info != nil {
				url = info.URL
			}
		}
		recStr := ""
		if sess.rec != nil {
			recStr = fmt.Sprintf("  rec=%d", sess.rec.len())
		}
		sb.WriteString(fmt.Sprintf("  %-8s %s  age=%s%s\n", name, url, age, recStr))
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// list / close-all / kill-all / attach / detach / delete-data
// ---------------------------------------------------------------------------

func (c *Command) execCloseAll(ctx context.Context, args []string) (string, error) {
	c.sessionsMu.Lock()
	count := len(c.sessions)
	c.sessionsMu.Unlock()

	c.closeAllSessions()
	return fmt.Sprintf("Closed %d session(s)", count), nil
}

func (c *Command) execKillAll(ctx context.Context, args []string) (string, error) {
	c.sessionsMu.Lock()
	count := len(c.sessions)
	c.sessionsMu.Unlock()

	c.Close()
	return fmt.Sprintf("Killed browser and %d session(s)", count), nil
}

func (c *Command) execDeleteData(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright delete-data: session name required")
	}
	name := args[0]

	c.sessionsMu.Lock()
	sess, ok := c.sessions[name]
	if !ok {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright delete-data: session %q not found", name)
	}
	delete(c.sessions, name)
	c.sessionsMu.Unlock()

	sess.cleanup()
	return fmt.Sprintf("Deleted data and closed session %q", name), nil
}

func (c *Command) execAttach(ctx context.Context, args []string) (string, error) {
	var cdpURL, sessName string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cdp":
			if i+1 >= len(args) {
				return "", fmt.Errorf("playwright attach: --cdp requires a WebSocket/HTTP URL")
			}
			i++
			cdpURL = args[i]
		case "--session":
			if i+1 >= len(args) {
				return "", fmt.Errorf("playwright attach: --session requires a name")
			}
			i++
			sessName = args[i]
		default:
			if !strings.HasPrefix(args[i], "-") && cdpURL == "" {
				cdpURL = args[i]
			}
		}
	}
	if cdpURL == "" {
		return "", fmt.Errorf("playwright attach: --cdp <url> required\n\nUsage: playwright attach --cdp <ws-url|http-url> [--session <name>]")
	}
	if sessName == "" {
		sessName = nextSessionName()
	}

	c.initSessions()

	c.sessionsMu.Lock()
	if _, exists := c.sessions[sessName]; exists {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright attach: session %q already exists", sessName)
	}
	needEvict := len(c.sessions) >= maxSessions
	c.sessionsMu.Unlock()

	if needEvict {
		c.evictLRUSession()
	}

	b := rod.New().ControlURL(cdpURL)
	if err := b.Connect(); err != nil {
		return "", fmt.Errorf("playwright attach: connect to %s: %w", cdpURL, err)
	}

	pages, err := b.Pages()
	if err != nil {
		return "", fmt.Errorf("playwright attach: list pages: %w", err)
	}
	if len(pages) == 0 {
		return "", fmt.Errorf("playwright attach: no pages found in browser at %s", cdpURL)
	}

	page := pages[0]
	tabs := make([]*rod.Page, len(pages))
	copy(tabs, pages)

	sess := &Session{
		Name:             sessName,
		Page:             page,
		Incognito:        b,
		CreatedAt:        time.Now(),
		OperationTimeout: defaultSessionOperationTimeout,
		tabs:             tabs,
		activeTab:        0,
		attached:         true,
	}

	// Auto-capture network requests.
	sess.networkMu.Lock()
	sess.networkRecorder = newNetworkRecorder()
	sess.networkActive = true
	if err := (proto.NetworkEnable{}).Call(page); err == nil {
		capCtx, capCancel := context.WithCancel(context.Background())
		sess.networkCancel = capCancel
		capturedPage := page.Context(capCtx)
		go capturedPage.EachEvent(
			sess.networkRecorder.requestWillBeSent,
			sess.networkRecorder.responseReceived,
			sess.networkRecorder.loadingFinished,
			sess.networkRecorder.loadingFailed,
		)()
	}
	sess.networkMu.Unlock()

	c.sessionsMu.Lock()
	c.sessions[sessName] = sess
	c.sessionsMu.Unlock()

	info, _ := page.Info()
	url, title := "", ""
	if info != nil {
		url = info.URL
		title = info.Title
	}

	return fmt.Sprintf("Attached session: %s\nCDP: %s\nURL: %s\nTitle: %s\nTabs: %d",
		sessName, cdpURL, url, title, len(pages)), nil
}

func (c *Command) execDetach(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright detach: session name required")
	}
	name := args[0]

	c.sessionsMu.Lock()
	sess, ok := c.sessions[name]
	if !ok {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright detach: session %q not found", name)
	}
	if !sess.attached {
		c.sessionsMu.Unlock()
		return "", fmt.Errorf("playwright detach: session %q was not created via attach (use close instead)", name)
	}
	delete(c.sessions, name)
	c.sessionsMu.Unlock()

	sess.cleanup()
	return fmt.Sprintf("Detached session %q (browser still running)", name), nil
}

// ---------------------------------------------------------------------------
// Argument parsing for open
// ---------------------------------------------------------------------------

type openOpts struct {
	commonOpts
	sessName         string
	opTimeout        time.Duration
	noSpeedUp        bool
	ignoreHTTPSErrs  bool
	viewportSize     string // "WxH"
	geolocation      string // "lat,lon"
	timezone         string
	colorScheme      string // light|dark
	lang             string
	device           string
	loadStoragePath  string
	saveStoragePath  string // stored on Session, dumped at close
	saveHARPath      string // stored on Session, dumped at close
	saveHARGlob      string
	proxyServer      string
	proxyBypass      string
	blockSW          bool
	record           bool
	headed           bool
	cdpURL           string
}

func parseOpenOpts(args []string, usage string) (openOpts, error) {
	o := openOpts{
		commonOpts: commonOpts{timeout: defaultTimeout},
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
		case "--ignore-https-errors":
			o.ignoreHTTPSErrs = true
		case "--viewport-size":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --viewport-size requires WxH (e.g. 1280x720)")
			}
			i++
			o.viewportSize = args[i]
		case "--geolocation":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --geolocation requires lat,lon")
			}
			i++
			o.geolocation = args[i]
		case "--timezone":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --timezone requires a value")
			}
			i++
			o.timezone = args[i]
		case "--color-scheme":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --color-scheme requires light|dark")
			}
			i++
			o.colorScheme = args[i]
		case "--lang":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --lang requires a value")
			}
			i++
			o.lang = args[i]
		case "--device":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --device requires a device name")
			}
			i++
			o.device = args[i]
		case "--load-storage":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --load-storage requires a file path")
			}
			i++
			o.loadStoragePath = args[i]
		case "--save-storage":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --save-storage requires a file path")
			}
			i++
			o.saveStoragePath = args[i]
		case "--save-har":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --save-har requires a file path")
			}
			i++
			o.saveHARPath = args[i]
		case "--proxy-server":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --proxy-server requires a value")
			}
			i++
			o.proxyServer = args[i]
		case "--proxy-bypass":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --proxy-bypass requires a value")
			}
			i++
			o.proxyBypass = args[i]
		case "--save-har-glob":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --save-har-glob requires a pattern")
			}
			i++
			o.saveHARGlob = args[i]
		case "--block-service-workers":
			o.blockSW = true
		case "--record":
			o.record = true
		case "--headed":
			o.headed = true
		case "--cdp":
			if i+1 >= len(args) {
				return o, fmt.Errorf("playwright open: --cdp requires a WebSocket URL")
			}
			i++
			o.cdpURL = args[i]
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

// ---------------------------------------------------------------------------
// playwright-cli parity helpers
// ---------------------------------------------------------------------------

func parseViewportSize(s string) (int, int, error) {
	// Accept "1280x720", "1280,720", "1280 720"
	s = strings.ReplaceAll(s, ",", "x")
	s = strings.ReplaceAll(s, " ", "x")
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("--viewport-size must be WxH, got %q", s)
	}
	w, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || w <= 0 {
		return 0, 0, fmt.Errorf("--viewport-size width invalid: %q", parts[0])
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || h <= 0 {
		return 0, 0, fmt.Errorf("--viewport-size height invalid: %q", parts[1])
	}
	return w, h, nil
}

func parseGeolocation(s string) (float64, float64, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("--geolocation must be lat,lon, got %q", s)
	}
	lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("--geolocation lat invalid: %w", err)
	}
	lon, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("--geolocation lon invalid: %w", err)
	}
	return lat, lon, nil
}

// storageState mirrors the Playwright storage state format.
type storageState struct {
	Cookies        []json.RawMessage        `json:"cookies"`
	LocalStorage   []localStorageEntry       `json:"origins"`
}

type localStorageEntry struct {
	Origin       string           `json:"origin"`
	LocalStorage []nameValuePair  `json:"localStorage"`
}

type nameValuePair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func loadStorageState(page *rod.Page, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var state storageState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("invalid storage state JSON: %w", err)
	}
	// Restore cookies via CDP.
	for _, raw := range state.Cookies {
		var cookie proto.NetworkCookieParam
		if err := json.Unmarshal(raw, &cookie); err != nil {
			continue
		}
		_ = page.SetCookies([]*proto.NetworkCookieParam{&cookie})
	}
	// Restore localStorage entries via JS.
	for _, origin := range state.LocalStorage {
		for _, kv := range origin.LocalStorage {
			k, _ := json.Marshal(kv.Name)
			v, _ := json.Marshal(kv.Value)
			_, _ = page.Eval(fmt.Sprintf("localStorage.setItem(%s, %s)", string(k), string(v)))
		}
	}
	return nil
}

func saveStorageState(page *rod.Page, path string) error {
	// Collect cookies.
	cookies, err := page.Cookies(nil)
	if err != nil {
		return err
	}
	cookieJSON := make([]json.RawMessage, 0, len(cookies))
	for _, c := range cookies {
		raw, _ := json.Marshal(c)
		cookieJSON = append(cookieJSON, raw)
	}
	// Collect localStorage via JS.
	res, err := page.Eval("() => { const items = []; for (let i = 0; i < localStorage.length; i++) { const k = localStorage.key(i); items.push({name: k, value: localStorage.getItem(k)}); } return items; }")
	var lsItems []nameValuePair
	if err == nil {
		_ = res.Value.Unmarshal(&lsItems)
	}
	info, _ := page.Info()
	origin := ""
	if info != nil {
		origin = info.URL
	}
	state := storageState{
		Cookies: cookieJSON,
		LocalStorage: []localStorageEntry{
			{Origin: origin, LocalStorage: lsItems},
		},
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeHAR(path string, entries []netEntry) error {
	type harReq struct {
		Method string `json:"method"`
		URL    string `json:"url"`
	}
	type harResp struct {
		Status      int    `json:"status"`
		ContentType string `json:"content_type,omitempty"`
		BodySize    int    `json:"bodySize"`
	}
	type harEntry struct {
		Request  harReq  `json:"request"`
		Response harResp `json:"response"`
	}
	type harLog struct {
		Version string     `json:"version"`
		Entries []harEntry `json:"entries"`
	}
	type har struct {
		Log harLog `json:"log"`
	}
	h := har{Log: harLog{Version: "1.2"}}
	for _, e := range entries {
		h.Log.Entries = append(h.Log.Entries, harEntry{
			Request:  harReq{Method: e.Method, URL: e.URL},
			Response: harResp{Status: e.Status, ContentType: e.ContentType, BodySize: e.Size},
		})
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
