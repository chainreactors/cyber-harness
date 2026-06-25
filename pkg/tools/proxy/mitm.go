package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
	goflags "github.com/jessevdk/go-flags"
)

// ---------------------------------------------------------------------------
// MitmCommand — top-level "mitm" command
// ---------------------------------------------------------------------------

type MitmCommand struct {
	store       *FlowStore
	execCommand func(ctx context.Context, tokens []string) (string, error)
	registry    *commands.CommandRegistry
}

func NewMitmCommand(reg *commands.CommandRegistry) *MitmCommand {
	return &MitmCommand{
		store:    NewFlowStore(10000),
		registry: reg,
	}
}

func (c *MitmCommand) SetCommandExecutor(fn func(ctx context.Context, tokens []string) (string, error)) {
	c.execCommand = fn
}

func (c *MitmCommand) Name() string { return "mitm" }

func (c *MitmCommand) Usage() string {
	return `mitm - Run a command with MITM traffic capture

Usage:
  mitm <command> [args...]               Run command with traffic interception
  mitm flows [--host X] [--last N]       List captured flows from last run
  mitm flow <id>                         Show full flow details
  mitm analyze [--host X] [--last N]     Format flows for AI security analysis
  mitm clear                             Clear captured flows

Examples:
  mitm scan -i http://example.com --mode quick
  mitm spray -i http://target.com
  mitm gogo -i 10.0.0.1 -p top2
  mitm flows --last 20
  mitm analyze --host example.com`
}

func (c *MitmCommand) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(commands.Output, c.Usage())
		return nil
	}

	var result string
	var err error

	switch args[0] {
	case "flows":
		result, err = c.queryFlows(args[1:])
	case "flow":
		result, err = c.flowDetail(args[1:])
	case "analyze":
		result, err = c.analyze(args[1:])
	case "clear":
		c.store.Clear()
		result = "[mitm] flow store cleared"
	default:
		result, err = c.execWithCapture(ctx, args)
	}

	if err != nil {
		return err
	}
	if result != "" {
		fmt.Fprint(commands.Output, result)
	}
	return nil
}

func (c *MitmCommand) execWithCapture(ctx context.Context, args []string) (string, error) {
	if c.execCommand == nil {
		return "", fmt.Errorf("mitm: command executor not available")
	}

	state := &mitmState{store: c.store}
	if err := state.start(); err != nil {
		return "", err
	}

	// Set MITM proxy on the target command only
	targetName := args[0]
	var prevProxy string
	if cmd, ok := c.registry.Get(targetName); ok {
		if updater, ok := cmd.(interface{ SetProxy(string) }); ok {
			if getter, ok := cmd.(interface{ Proxy() string }); ok {
				prevProxy = getter.Proxy()
			}
			updater.SetProxy(state.proxyURL())
			defer updater.SetProxy(prevProxy)
		}
	}
	defer state.stop()

	result, err := c.execCommand(ctx, args)

	flowCount := c.store.Count()
	summary := fmt.Sprintf("\n[mitm] %d flows captured. Use 'mitm flows' or 'mitm analyze' to inspect.", flowCount)
	return result + summary, err
}

type flowQueryFlags struct {
	Host   string `long:"host" description:"Filter by host substring"`
	Status string `long:"status" description:"Filter by status code (2xx, 404, 5xx)"`
	Type   string `long:"type" description:"Filter by Content-Type substring"`
	Last   int    `long:"last" description:"Show only the last N flows"`
}

func (c *MitmCommand) queryFlows(args []string) (string, error) {
	var f flowQueryFlags
	p := goflags.NewParser(&f, goflags.Default&^goflags.PrintErrors&^goflags.HelpFlag)
	if _, err := p.ParseArgs(args); err != nil {
		return "", err
	}
	return formatFlowList(c.store.Query(QueryOpts{Host: f.Host, Status: f.Status, CType: f.Type, Last: f.Last})), nil
}

func (c *MitmCommand) flowDetail(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: mitm flow <id>")
	}
	var id int
	if _, err := fmt.Sscanf(args[0], "%d", &id); err != nil {
		return "", fmt.Errorf("invalid flow ID: %s", args[0])
	}
	f := c.store.Get(id)
	if f == nil {
		return "", fmt.Errorf("flow #%d not found", id)
	}
	return formatFlowDetail(f), nil
}

func (c *MitmCommand) analyze(args []string) (string, error) {
	var f struct {
		Host string `long:"host" description:"Filter by host substring"`
		Last int    `long:"last" description:"Analyze only the last N flows"`
	}
	p := goflags.NewParser(&f, goflags.Default&^goflags.PrintErrors&^goflags.HelpFlag)
	if _, err := p.ParseArgs(args); err != nil {
		return "", err
	}
	return formatFlowAnalysis(c.store.Query(QueryOpts{Host: f.Host, Last: f.Last})), nil
}

// ---------------------------------------------------------------------------
// mitmState — lightweight MITM proxy lifecycle (no exported API needed)
// ---------------------------------------------------------------------------

type mitmState struct {
	server *mitmproxy.Proxy
	addr   string
	store  *FlowStore
}

func (s *mitmState) start() error {
	p, err := mitmproxy.NewProxy(&mitmproxy.Options{
		Addr:              "127.0.0.1:0",
		SslInsecure:       true,
		StreamLargeBodies: 10 * 1024 * 1024,
	})
	if err != nil {
		return fmt.Errorf("create MITM proxy: %w", err)
	}
	p.AddAddon(&captureAddon{store: s.store})
	listenAddr, _, err := p.StartAsync()
	if err != nil {
		return fmt.Errorf("start MITM proxy: %w", err)
	}
	s.server = p
	s.addr = listenAddr.String()
	return nil
}

func (s *mitmState) stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.server.Shutdown(ctx)
		cancel()
		s.server = nil
	}
}

func (s *mitmState) proxyURL() string {
	return "http://" + s.addr
}

// ---------------------------------------------------------------------------
// captureAddon — passive HTTP flow capture
// ---------------------------------------------------------------------------

const maxBodySnip = 4096

type captureAddon struct {
	mitmproxy.BaseAddon
	store   *FlowStore
	pending sync.Map
}

func (a *captureAddon) Requestheaders(f *mitmproxy.Flow) {
	a.pending.Store(f.Id.String(), time.Now())
}

func (a *captureAddon) Response(f *mitmproxy.Flow) {
	var dur time.Duration
	if start, ok := a.pending.LoadAndDelete(f.Id.String()); ok {
		if t, ok := start.(time.Time); ok {
			dur = time.Since(t)
		}
	}
	flow := Flow{
		Timestamp:      f.StartTime,
		Method:         f.Request.Method,
		URL:            f.Request.URL.String(),
		Host:           f.Request.URL.Hostname(),
		Duration:       dur,
		TLS:            f.ConnContext.ClientConn.Tls,
		RequestHeaders: f.Request.Header.Clone(),
	}
	if len(f.Request.Body) > 0 {
		flow.RequestBodySnip = snip(f.Request.Body, maxBodySnip)
	}
	if f.Response != nil {
		flow.StatusCode = f.Response.StatusCode
		flow.ResponseHeaders = f.Response.Header.Clone()
		flow.ContentType = f.Response.Header.Get("Content-Type")
		if len(f.Response.Body) > 0 {
			flow.ResponseBodySnip = snip(f.Response.Body, maxBodySnip)
		}
	}
	a.store.Add(flow)
}

func (a *captureAddon) RequestError(f *mitmproxy.Flow, err error) {
	var dur time.Duration
	if start, ok := a.pending.LoadAndDelete(f.Id.String()); ok {
		if t, ok := start.(time.Time); ok {
			dur = time.Since(t)
		}
	}
	a.store.Add(Flow{
		Timestamp: f.StartTime,
		Method:    f.Request.Method,
		URL:       f.Request.URL.String(),
		Host:      f.Request.URL.Hostname(),
		Duration:  dur,
		Error:     err.Error(),
	})
}

func snip(b []byte, max int) []byte {
	if len(b) > max {
		b = b[:max]
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// ---------------------------------------------------------------------------
// Flow + FlowStore
// ---------------------------------------------------------------------------

type Flow struct {
	ID               int
	Timestamp        time.Time
	Method           string
	URL              string
	Host             string
	StatusCode       int
	ContentType      string
	Duration         time.Duration
	RequestHeaders   http.Header
	RequestBodySnip  []byte
	ResponseHeaders  http.Header
	ResponseBodySnip []byte
	TLS              bool
	Error            string
}

type QueryOpts struct {
	Host   string
	Status string
	CType  string
	Last   int
}

type FlowStore struct {
	mu    sync.RWMutex
	flows []Flow
	seq   int
	cap   int
}

func NewFlowStore(cap int) *FlowStore {
	if cap <= 0 {
		cap = 10000
	}
	return &FlowStore{flows: make([]Flow, 0, 256), cap: cap}
}

func (s *FlowStore) Add(f Flow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	f.ID = s.seq
	if len(s.flows) >= s.cap {
		copy(s.flows, s.flows[1:])
		s.flows[len(s.flows)-1] = f
	} else {
		s.flows = append(s.flows, f)
	}
}

func (s *FlowStore) Query(opts QueryOpts) []Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Flow
	for i := range s.flows {
		f := &s.flows[i]
		if opts.Host != "" && !strings.Contains(strings.ToLower(f.Host), strings.ToLower(opts.Host)) {
			continue
		}
		if opts.Status != "" && !matchStatus(f.StatusCode, opts.Status) {
			continue
		}
		if opts.CType != "" && !strings.Contains(strings.ToLower(f.ContentType), strings.ToLower(opts.CType)) {
			continue
		}
		result = append(result, *f)
	}
	if opts.Last > 0 && len(result) > opts.Last {
		result = result[len(result)-opts.Last:]
	}
	return result
}

func (s *FlowStore) Get(id int) *Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.flows {
		if s.flows[i].ID == id {
			f := s.flows[i]
			return &f
		}
	}
	return nil
}

func (s *FlowStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows = s.flows[:0]
	s.seq = 0
}

func (s *FlowStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.flows)
}

func matchStatus(code int, pattern string) bool {
	p := strings.ToLower(strings.TrimSpace(pattern))
	switch p {
	case "1xx":
		return code >= 100 && code < 200
	case "2xx":
		return code >= 200 && code < 300
	case "3xx":
		return code >= 300 && code < 400
	case "4xx":
		return code >= 400 && code < 500
	case "5xx":
		return code >= 500 && code < 600
	default:
		if n, err := strconv.Atoi(p); err == nil {
			return code == n
		}
		return false
	}
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

func formatFlowList(flows []Flow) string {
	if len(flows) == 0 {
		return "[mitm] no flows captured"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[mitm] %d flows\n", len(flows)))
	sb.WriteString(fmt.Sprintf("  %-6s %-6s %-4s %-50s %-14s %s\n", "ID", "Method", "Code", "URL", "Content-Type", "Duration"))
	sb.WriteString(fmt.Sprintf("  %-6s %-6s %-4s %-50s %-14s %s\n", "---", "---", "---", "---", "---", "---"))
	for _, f := range flows {
		ct := f.ContentType
		if idx := strings.Index(ct, ";"); idx > 0 {
			ct = ct[:idx]
		}
		urlStr := f.URL
		if len(urlStr) > 50 {
			urlStr = urlStr[:47] + "..."
		}
		errMark := ""
		if f.Error != "" {
			errMark = " ERR"
		}
		sb.WriteString(fmt.Sprintf("  %-6d %-6s %-4d %-50s %-14s %dms%s\n",
			f.ID, f.Method, f.StatusCode, urlStr, truncate(ct, 14), f.Duration.Milliseconds(), errMark))
	}
	return sb.String()
}

func formatFlowDetail(f *Flow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Flow #%d ===\n", f.ID))
	sb.WriteString(fmt.Sprintf("Time: %s  Method: %s  Status: %d  Duration: %dms  TLS: %v\n",
		f.Timestamp.Format(time.RFC3339), f.Method, f.StatusCode, f.Duration.Milliseconds(), f.TLS))
	sb.WriteString(fmt.Sprintf("URL: %s\n", f.URL))
	if f.Error != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", f.Error))
	}
	sb.WriteString("\n--- Request Headers ---\n")
	writeHeaders(&sb, f.RequestHeaders)
	if len(f.RequestBodySnip) > 0 {
		sb.WriteString(fmt.Sprintf("\n--- Request Body (%d bytes) ---\n%s\n", len(f.RequestBodySnip), f.RequestBodySnip))
	}
	sb.WriteString("\n--- Response Headers ---\n")
	writeHeaders(&sb, f.ResponseHeaders)
	if len(f.ResponseBodySnip) > 0 {
		sb.WriteString(fmt.Sprintf("\n--- Response Body (%d bytes) ---\n%s\n", len(f.ResponseBodySnip), f.ResponseBodySnip))
	}
	return sb.String()
}

func formatFlowAnalysis(flows []Flow) string {
	if len(flows) == 0 {
		return "[mitm] no flows to analyze"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== MITM Traffic Analysis (%d flows) ===\n\n", len(flows)))

	hostCounts := map[string]int{}
	statusCounts := map[int]int{}
	var errCount int
	for _, f := range flows {
		hostCounts[f.Host]++
		statusCounts[f.StatusCode/100]++
		if f.Error != "" {
			errCount++
		}
	}
	sb.WriteString(fmt.Sprintf("Hosts: %d unique | ", len(hostCounts)))
	for cls, n := range statusCounts {
		sb.WriteString(fmt.Sprintf("%dxx:%d ", cls, n))
	}
	if errCount > 0 {
		sb.WriteString(fmt.Sprintf("| Errors:%d", errCount))
	}
	sb.WriteString("\n\n")

	for _, f := range flows {
		sb.WriteString(fmt.Sprintf("#%d [%d] %s %s (%dms)\n", f.ID, f.StatusCode, f.Method, f.URL, f.Duration.Milliseconds()))
		if f.Error != "" {
			sb.WriteString(fmt.Sprintf("  ERROR: %s\n", f.Error))
		}
		if len(f.ResponseBodySnip) > 0 {
			body := string(f.ResponseBodySnip)
			if len(body) > 500 {
				body = body[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("  %s\n", body))
		}
	}
	return sb.String()
}

func writeHeaders(sb *strings.Builder, h http.Header) {
	for k, vals := range h {
		for _, v := range vals {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
		}
	}
}
