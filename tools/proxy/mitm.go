package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	operationpb "github.com/chainreactors/cyber/aop/operation"
	traffic "github.com/chainreactors/cyber/aop/traffic"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	cfg "github.com/chainreactors/cyber/pkg/config"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
	goflags "github.com/jessevdk/go-flags"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ---------------------------------------------------------------------------
// MitmCommand — top-level "mitm" command
// ---------------------------------------------------------------------------

type MitmCommand struct {
	store       *FlowStore
	hub         *ProxyHub
	execCommand CommandExecutor
}

// NewMitmCommand wires the mitm verbs to the long-lived hub's shared FlowStore
// so `mitm flows/analyze/flow` query traffic captured from every tool, not just
// a per-invocation proxy.
func NewMitmCommand(store *FlowStore, hub *ProxyHub) *MitmCommand {
	if store == nil {
		store = NewFlowStore(10000)
	}
	return &MitmCommand{store: store, hub: hub}
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

func (c *MitmCommand) Run(ctx context.Context, execution *coretool.Execution) (_ any, err error) {
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

func (c *MitmCommand) execWithCapture(ctx context.Context, args []string, execution *coretool.Execution) (any, error) {
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

func (c *MitmCommand) queryFlows(args []string) (string, error) {
	var f QueryOpts
	p := goflags.NewParser(&f, goflags.Default&^goflags.PrintErrors&^goflags.HelpFlag)
	if _, err := p.ParseArgs(args); err != nil {
		return "", err
	}
	return formatFlowList(c.store.Query(f)), nil
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

// Keep a single captured request/response from becoming an unbounded disk
// allocation. The adapter reports the observed size on truncation; only the prefix
// up to this limit is retained in the body file.
const maxBodyCaptureBytes = cfg.DefaultBodyMaxBytes

// Retain a bounded amount of body data across the ring. A value of zero on a
// custom FlowStore means unlimited (the default is deliberately finite).
const defaultMaxBodyBytes = cfg.DefaultBodyRetentionBytes

type captureAddon struct {
	mitmproxy.BaseAddon
	hub     *ProxyHub
	pending sync.Map // map[proxy flow id]*captureState
}

func correlationTokenOf(f *mitmproxy.Flow) string {
	if f != nil && f.ConnContext != nil {
		return f.ConnContext.ProxyAuthUser
	}
	return ""
}

func (a *captureAddon) Requestheaders(f *mitmproxy.Flow) {
	if a.hub == nil || a.hub.stopping.Load() || !a.hub.recording.Load() || f == nil || f.Request == nil {
		return
	}
	if f.Request.URL != nil && !a.hub.captureHostAllowed(f.Request.URL.Hostname()) {
		return
	}
	// Successful CONNECT and WebSocket handshakes are connection-level
	// lifecycles. Inner HTTPS requests and the separate WebSocket capture are
	// responsible for their own records; retaining this outer request would
	// otherwise leak a pending capture until the process exits.
	if strings.EqualFold(f.Request.Method, http.MethodConnect) ||
		strings.EqualFold(f.Request.Header.Get("Upgrade"), "websocket") {
		return
	}
	state := newCaptureState(a.hub, f)
	f.Stream = true // Capture observes native streams; no buffered-body branch.
	a.pending.Store(f.Id.String(), state)
}

func (a *captureAddon) Responseheaders(f *mitmproxy.Flow) {
	if state := a.state(f); state != nil && f.Response != nil {
		if !a.hub.captureResponseAllowed(f.Response.StatusCode, f.Response.Header.Get("Content-Type")) {
			a.pending.Delete(f.Id.String())
			state.discard()
			return
		}
		state.flow.Response = &traffic.HttpResponse{StatusCode: int32(f.Response.StatusCode), Headers: traffic.HeadersFromHTTP(f.Response.Header)}
		state.flow.ContentType = f.Response.Header.Get("Content-Type")
	}
}

// FlowFinished is the only completion path for buffered, streamed and failed
// exchanges. Forwarding errors and end time are owned by the proxy engine.
func (a *captureAddon) FlowFinished(f *mitmproxy.Flow) {
	if state := a.state(f); state != nil {
		a.pending.Delete(f.Id.String())
		state.finish(f.Error)
	}
}

func (a *captureAddon) StreamRequestModifier(f *mitmproxy.Flow, in io.Reader) io.Reader {
	if state := a.state(f); state != nil {
		return state.bodyReader(in, "req")
	}
	return in
}
func (a *captureAddon) StreamResponseModifier(f *mitmproxy.Flow, in io.Reader) io.Reader {
	if state := a.state(f); state != nil {
		return state.bodyReader(in, "resp")
	}
	return in
}

func (a *captureAddon) state(f *mitmproxy.Flow) *captureState {
	if f == nil {
		return nil
	}
	if value, ok := a.pending.Load(f.Id.String()); ok {
		state, _ := value.(*captureState)
		return state
	}
	return nil
}

type captureState struct {
	hub        *ProxyHub
	mu         sync.Mutex
	finished   bool
	flow       Flow
	bodies     [2]*bodyStream
	files      [2]*bodyFile
	captureErr error
}

func newCaptureState(hub *ProxyHub, f *mitmproxy.Flow) *captureState {
	correlation := hub.resolveCorrelation(correlationTokenOf(f))
	flow := Flow{
		Flow: &traffic.Flow{Timestamp: timestamppb.New(f.StartTime)}, Operation: correlation.operation, Invocation: correlation.invocation,
		cancel: correlation.cancel, release: correlation.finish,
	}
	if f.ConnContext != nil && f.ConnContext.ClientConn != nil {
		flow.TLS = f.ConnContext.ClientConn.Tls
	}
	if f.Request != nil {
		flow.Id = f.Id.String()
		flow.Request = &traffic.HttpRequest{Method: f.Request.Method, Url: f.Request.URL.String(), Protocol: f.Request.Proto, Headers: traffic.HeadersFromHTTPWithHost(f.Request.Header, requestHost(f.Request))}
		flow.Host = f.Request.URL.Hostname()
	}
	return &captureState{hub: hub, flow: flow}
}
func requestHost(req *mitmproxy.Request) string {
	if req == nil {
		return ""
	}
	if raw := req.Raw(); raw != nil && raw.Host != "" {
		return raw.Host
	}
	if req.URL != nil {
		return req.URL.Host
	}
	return ""
}
func (s *captureState) bodyReader(in io.Reader, side string) io.Reader {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished || in == nil {
		return in
	}
	i := 0
	if side == "resp" {
		i = 1
	}
	if s.bodies[i] == nil {
		s.bodies[i] = &bodyStream{}
		var err error
		s.files[i], err = s.hub.captureBody(s.bodies[i])
		s.captureErr = errors.Join(s.captureErr, err)
	}
	return io.TeeReader(in, s.bodies[i])
}

// finish freezes observation at the engine completion callback and publishes
// the completed flow. Reading response EOF is not a publication barrier.
func (s *captureState) finish(err error) { s.finishCapture(err, false) }
func (s *captureState) discard()         { s.finishCapture(nil, true) }

func (s *captureState) finishCapture(err error, discard bool) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	for _, body := range s.bodies {
		if body != nil {
			body.Close()
		}
	}
	if timestamp := s.flow.GetTimestamp(); timestamp != nil && timestamp.IsValid() {
		s.flow.Duration = time.Since(timestamp.AsTime())
	}
	s.mu.Unlock()
	s.completeCapture(err, discard)
}

func (s *captureState) completeCapture(err error, discard bool) {
	for _, file := range s.files {
		if file != nil {
			defer file.release()
		}
	}
	err = errors.Join(err, s.captureErr)
	flow := s.flow
	if flow.release != nil {
		defer flow.release()
		flow.release = nil
	}
	var files [2]*os.File
	for i, stream := range s.bodies {
		if stream == nil {
			continue
		}
		preview, observed := stream.snapshot()
		stored := int64(len(preview))
		if body := s.files[i]; body != nil {
			fileErr := body.finish(discard)
			file := body.file
			files[i] = file
			err = errors.Join(err, fileErr)
			stored = 0
			if file != nil {
				info, statErr := os.Stat(file.Name())
				err = errors.Join(err, statErr)
				if statErr == nil {
					stored = info.Size()
				}
			}
		}
		if i == 0 {
			flow.Request.Body = preview
		} else {
			if flow.Response == nil {
				flow.Response = &traffic.HttpResponse{}
			}
			flow.Response.Body = preview
		}
		if stored < observed {
			side := "request"
			if i == 1 {
				side = "response"
			}
			notice := fmt.Sprintf("%s body truncated (%d/%d bytes retained)", side, stored, observed)
			if s.files[i] == nil {
				notice += " (preview only; local storage disabled or unavailable)"
			}
			err = errors.Join(err, errors.New(notice))
		}
	}
	if discard {
		removeCaptureFiles(files)
		return
	}
	if err != nil {
		flow.Error = err.Error()
	}
	flow.Complete = err == nil && flow.Response != nil && flow.Response.StatusCode != 0
	s.hub.ingestFiles(flow, files)
}

func appendPreview(dst, src []byte, max int) []byte {
	if len(dst) >= max || len(src) == 0 {
		return dst
	}
	if len(src) > max-len(dst) {
		src = src[:max-len(dst)]
	}
	return append(dst, src...)
}

// ---------------------------------------------------------------------------
// Flow + FlowStore
// ---------------------------------------------------------------------------

// Flow is the hub's stored capture: the canonical traffic flow plus hub-only
// metadata (attribution, timing, TLS) the mitm query verbs filter and format
// on. Operation is the sole wire correlation authority.
type Flow struct {
	// By pointer: a canonical Flow is a protobuf message, so copying one by
	// value copies its internal mutex, and the store moves Flow constantly.
	*traffic.Flow
	Operation   *operationpb.Ref
	Invocation  operation.Invocation
	Host        string
	ContentType string
	Duration    time.Duration
	TLS         bool
	cancel      func(error) bool
	release     func()
}

type QueryOpts struct {
	Host   string `long:"host" description:"Filter by host substring"`
	Status string `long:"status" description:"Filter by status code (2xx, 404, 5xx)"`
	CType  string `long:"type" description:"Filter by Content-Type substring"`
	Last   int    `long:"last" description:"Show only the last N flows"`
}

// FlowStore owns the retained flow ring and its file-backed body lifecycle.
// bodyMu coordinates body-file readers with retention cleanup. The ring may
// evict a flow while a subscriber is hydrating a copy of it; keeping both
// operations in this lock prevents an otherwise silent read race.
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
		urlStr := f.Request.Url
		if len(urlStr) > 50 {
			urlStr = urlStr[:47] + "..."
		}
		errMark := ""
		if f.Error != "" {
			errMark = " ERR"
		}
		sb.WriteString(fmt.Sprintf("  %-6s %-6s %-4d %-50s %-14s %dms%s\n",
			f.Id, f.Request.Method, statusCodeOf(&f), urlStr, truncate(ct, 14), f.Duration.Milliseconds(), errMark))
	}
	return sb.String()
}

// statusCodeOf reports the response status, 0 for a request-only flow.
func statusCodeOf(f *Flow) int {
	if f.Response == nil {
		return 0
	}
	return int(f.Response.StatusCode)
}

func formatFlowDetail(f *Flow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Flow #%s ===\n", f.Id))
	timestamp := time.Time{}
	if f.Timestamp != nil && f.Timestamp.IsValid() {
		timestamp = f.Timestamp.AsTime()
	}
	sb.WriteString(fmt.Sprintf("Time: %s  Method: %s  Status: %d  Duration: %dms  TLS: %v\n",
		timestamp.Format(time.RFC3339), f.Request.Method, statusCodeOf(f), f.Duration.Milliseconds(), f.TLS))
	sb.WriteString(fmt.Sprintf("URL: %s\n", f.Request.Url))
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
		sb.WriteString(fmt.Sprintf("#%s [%d] %s %s (%dms)\n", f.Id, statusCodeOf(&f), f.Request.Method, f.Request.Url, f.Duration.Milliseconds()))
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

func writeHeaders(sb *strings.Builder, headers []*traffic.Header) {
	for _, header := range headers {
		if header != nil {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", header.GetName(), header.GetValue()))
		}
	}
}
