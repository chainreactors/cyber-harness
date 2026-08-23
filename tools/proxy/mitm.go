package proxy

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
	goflags "github.com/jessevdk/go-flags"
)

// ---------------------------------------------------------------------------
// MitmCommand — top-level "mitm" command
// ---------------------------------------------------------------------------

type MitmCommand struct {
	store       *FlowStore
	hub         *ProxyHub
	execCommand CommandExecutor
	registry    *commands.CommandRegistry
}

// NewMitmCommand wires the mitm verbs to the long-lived hub's shared FlowStore
// so `mitm flows/analyze/flow` query traffic captured from every tool, not just
// a per-invocation proxy.
func NewMitmCommand(reg *commands.CommandRegistry, store *FlowStore, hub *ProxyHub) *MitmCommand {
	if store == nil {
		store = NewFlowStore(10000)
	}
	return &MitmCommand{store: store, hub: hub, registry: reg}
}

func (c *MitmCommand) SetCommandExecutor(fn CommandExecutor) {
	c.execCommand = fn
}

func (c *MitmCommand) Name() string { return "mitm" }

func (c *MitmCommand) Usage() string {
	return `mitm - Inspect traffic captured from tool execution

Tool traffic is captured automatically (default on). Inspect it with:
  mitm flows [--host X] [--status 2xx] [--type json] [--last N]   List captured flows
  mitm flow <id>                                                  Show one flow (headers + bodies)
  mitm analyze [--host X] [--last N]                              Summarize captured traffic
  mitm clear                                                      Clear the capture store
  mitm <command> [args...]                                        Run a command, report flows it added

Examples:
  mitm flows --host example.com --last 20
  mitm analyze --host example.com`
}

func (c *MitmCommand) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("mitm", &err)
	args := execution.Args
	if len(args) == 0 {
		fmt.Fprint(execution.Stdout, c.Usage())
		return nil, nil
	}

	// In relay mode (config mitm:false) nothing is recorded; steer the model
	// away from querying an empty store rather than returning misleading "no
	// flows". Routing still works, so passthrough (default) stays allowed.
	switch args[0] {
	case "flows", "flow", "analyze":
		if c.hub != nil && !c.hub.Capturing() {
			fmt.Fprint(execution.Stdout, "[mitm] traffic capture is disabled (proxy routing only). Enable with config mitm: true")
			return nil, nil
		}
	}

	var result string

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
		return c.execWithCapture(ctx, args, execution)
	}

	if err != nil {
		return nil, err
	}
	if result != "" {
		fmt.Fprint(execution.Stdout, result)
	}
	return nil, nil
}

func (c *MitmCommand) execWithCapture(ctx context.Context, args []string, execution *commands.Execution) (any, error) {
	if c.execCommand == nil {
		return nil, fmt.Errorf("mitm: command executor not available")
	}
	// Every tool already routes through the long-lived hub, so the wrapped
	// command is captured automatically. Report the flows it added. The delta
	// is approximate under concurrency (the shared store also receives other
	// commands' flows), which is acceptable for this summary.
	before := c.store.Count()
	details, err := c.execCommand(ctx, args, execution)
	added := c.store.Count() - before
	if added < 0 {
		added = 0
	}
	fmt.Fprintf(execution.Stdout, "\n[mitm] %d flows captured.", added)
	return details, err
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
// captureAddon — passive HTTP flow capture
// ---------------------------------------------------------------------------

const maxBodySnip = 4096

type captureAddon struct {
	mitmproxy.BaseAddon
	hub     *ProxyHub
	pending sync.Map
}

// toolIDOf returns the AOP tool-call id that opened this flow's connection, read
// from the per-connection proxy-auth username the client injected. Empty when no
// identity was presented (e.g. relay use or a non-Cairn client).
func toolIDOf(f *mitmproxy.Flow) string {
	if f != nil && f.ConnContext != nil {
		return f.ConnContext.ProxyAuthUser
	}
	return ""
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
		Exchange: traffic.Exchange{
			Request: traffic.Request{
				Method:   f.Request.Method,
				URL:      f.Request.URL.String(),
				Protocol: f.Request.Proto,
				Headers:  pairsFromHTTP(f.Request.Header),
			},
		},
		Timestamp: f.StartTime,
		ToolID:    toolIDOf(f),
		Host:      f.Request.URL.Hostname(),
		Duration:  dur,
		TLS:       f.ConnContext.ClientConn.Tls,
	}
	if len(f.Request.Body) > 0 {
		flow.Request.Body = snip(f.Request.Body, maxBodySnip)
	}
	if f.Response != nil {
		flow.Response = &traffic.Response{
			StatusCode: f.Response.StatusCode,
			Headers:    pairsFromHTTP(f.Response.Header),
		}
		flow.ContentType = f.Response.Header.Get("Content-Type")
		if len(f.Response.Body) > 0 {
			flow.Response.Body = snip(f.Response.Body, maxBodySnip)
		}
		flow.Complete = f.Response.StatusCode != 0
	}
	a.hub.ingest(flow)
}

func (a *captureAddon) RequestError(f *mitmproxy.Flow, err error) {
	var dur time.Duration
	if start, ok := a.pending.LoadAndDelete(f.Id.String()); ok {
		if t, ok := start.(time.Time); ok {
			dur = time.Since(t)
		}
	}
	a.hub.ingest(Flow{
		Exchange: traffic.Exchange{
			Request: traffic.Request{
				Method:   f.Request.Method,
				URL:      f.Request.URL.String(),
				Protocol: f.Request.Proto,
			},
			Error: err.Error(),
		},
		Timestamp: f.StartTime,
		ToolID:    toolIDOf(f),
		Host:      f.Request.URL.Hostname(),
		Duration:  dur,
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

// pairsFromHTTP flattens an http.Header into the canonical pair sequence. The
// wire order is already lost inside net/http, so names are sorted to keep the
// stored form deterministic.
func pairsFromHTTP(headers http.Header) []traffic.Pair {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]traffic.Pair, 0, len(headers))
	for _, name := range names {
		for _, value := range headers[name] {
			out = append(out, traffic.Pair{Name: name, Value: value})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Flow + FlowStore
// ---------------------------------------------------------------------------

// Flow is the hub's stored capture: the canonical exchange plus the hub-only
// metadata (attribution, timing, TLS) the mitm query verbs filter and format
// on. The wire view is Exchange.Proto with ToolID/Timestamp stamped.
type Flow struct {
	traffic.Exchange
	ToolID      string
	Timestamp   time.Time
	Host        string
	ContentType string
	Duration    time.Duration
	TLS         bool
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

// Add stores f, assigns it a monotonic ID, and returns the stored copy so the
// caller can fan the ID-bearing flow out to subscribers.
func (s *FlowStore) Add(f Flow) Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	f.ID = strconv.Itoa(s.seq)
	if len(s.flows) >= s.cap {
		copy(s.flows, s.flows[1:])
		s.flows[len(s.flows)-1] = f
	} else {
		s.flows = append(s.flows, f)
	}
	return f
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
		if opts.Status != "" {
			if f.Response == nil || !matchStatus(f.Response.StatusCode, opts.Status) {
				continue
			}
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
	want := strconv.Itoa(id)
	for i := range s.flows {
		if s.flows[i].ID == want {
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
		urlStr := f.Request.URL
		if len(urlStr) > 50 {
			urlStr = urlStr[:47] + "..."
		}
		errMark := ""
		if f.Error != "" {
			errMark = " ERR"
		}
		sb.WriteString(fmt.Sprintf("  %-6s %-6s %-4d %-50s %-14s %dms%s\n",
			f.ID, f.Request.Method, statusCodeOf(&f), urlStr, truncate(ct, 14), f.Duration.Milliseconds(), errMark))
	}
	return sb.String()
}

// statusCodeOf reports the response status, 0 for a request-only flow.
func statusCodeOf(f *Flow) int {
	if f.Response == nil {
		return 0
	}
	return f.Response.StatusCode
}

func formatFlowDetail(f *Flow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Flow #%s ===\n", f.ID))
	sb.WriteString(fmt.Sprintf("Time: %s  Method: %s  Status: %d  Duration: %dms  TLS: %v\n",
		f.Timestamp.Format(time.RFC3339), f.Request.Method, statusCodeOf(f), f.Duration.Milliseconds(), f.TLS))
	sb.WriteString(fmt.Sprintf("URL: %s\n", f.Request.URL))
	if f.Error != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", f.Error))
	}
	sb.WriteString("\n--- Request Headers ---\n")
	writeHeaders(&sb, f.Request.Headers)
	if len(f.Request.Body) > 0 {
		sb.WriteString(fmt.Sprintf("\n--- Request Body (%d bytes) ---\n%s\n", len(f.Request.Body), f.Request.Body))
	}
	if f.Response != nil {
		sb.WriteString("\n--- Response Headers ---\n")
		writeHeaders(&sb, f.Response.Headers)
		if len(f.Response.Body) > 0 {
			sb.WriteString(fmt.Sprintf("\n--- Response Body (%d bytes) ---\n%s\n", len(f.Response.Body), f.Response.Body))
		}
	}
	return sb.String()
}

func formatFlowAnalysis(flows []Flow) string {
	if len(flows) == 0 {
		return "[mitm] no flows to analyze"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Captured Traffic Summary (%d flows) ===\n\n", len(flows)))

	hostCounts := map[string]int{}
	statusCounts := map[int]int{}
	var errCount int
	for _, f := range flows {
		hostCounts[f.Host]++
		statusCounts[statusCodeOf(&f)/100]++
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
		sb.WriteString(fmt.Sprintf("#%s [%d] %s %s (%dms)\n", f.ID, statusCodeOf(&f), f.Request.Method, f.Request.URL, f.Duration.Milliseconds()))
		if f.Error != "" {
			sb.WriteString(fmt.Sprintf("  ERROR: %s\n", f.Error))
		}
		if f.Response != nil && len(f.Response.Body) > 0 {
			body := string(f.Response.Body)
			if len(body) > 500 {
				body = body[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("  %s\n", body))
		}
	}
	return sb.String()
}

func writeHeaders(sb *strings.Builder, headers []traffic.Pair) {
	for _, p := range headers {
		sb.WriteString(fmt.Sprintf("  %s: %s\n", p.Name, p.Value))
	}
}
