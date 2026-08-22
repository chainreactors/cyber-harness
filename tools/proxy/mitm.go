package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	pending sync.Map // map[proxy flow id]*captureState
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
	if a.hub == nil || !a.hub.recording.Load() || f == nil || f.Request == nil {
		return
	}
	if f.Request.URL != nil && !a.hub.captureHostAllowed(f.Request.URL.Hostname()) {
		return
	}
	// Successful CONNECT and WebSocket handshakes are connection-level
	// lifecycles. Inner HTTPS requests and the separate WebSocket recorder are
	// responsible for their own records; retaining this outer request would
	// otherwise leak a pending capture until the process exits.
	if strings.EqualFold(f.Request.Method, http.MethodConnect) ||
		strings.EqualFold(f.Request.Header.Get("Upgrade"), "websocket") {
		return
	}
	state := newCaptureState(a.hub, f)
	state.owner = a
	a.pending.Store(f.Id.String(), state)
}

func (a *captureAddon) Request(f *mitmproxy.Flow) {
	if state := a.state(f); state != nil && f.Request != nil {
		state.setRequestBody(f.Request.Body)
	}
}

func (a *captureAddon) Responseheaders(f *mitmproxy.Flow) {
	if state := a.state(f); state != nil && f.Response != nil {
		if !a.hub.captureResponseAllowed(f.Response.StatusCode, f.Response.Header.Get("Content-Type")) {
			state.discard()
			return
		}
		state.setResponseMeta(f.Response.StatusCode, f.Response.Header)
	}
}

func (a *captureAddon) Response(f *mitmproxy.Flow) {
	if state := a.state(f); state != nil {
		if f.Request != nil {
			state.setRequestBody(f.Request.Body)
		}
		if f.Response != nil {
			state.setResponseMeta(f.Response.StatusCode, f.Response.Header)
			state.setResponseBody(f.Response.Body)
		}
		state.finish(nil)
	}
}

func (a *captureAddon) StreamRequestModifier(f *mitmproxy.Flow, in io.Reader) io.Reader {
	if state := a.state(f); state != nil {
		return state.requestReader(in)
	}
	return in
}

func (a *captureAddon) StreamResponseModifier(f *mitmproxy.Flow, in io.Reader) io.Reader {
	if state := a.state(f); state != nil {
		return state.responseReader(in)
	}
	return in
}

func (a *captureAddon) RequestError(f *mitmproxy.Flow, err error) {
	if state := a.state(f); state != nil {
		state.finish(err)
	}
}

func (a *captureAddon) HTTPConnectError(f *mitmproxy.Flow, err error) {
	if state := a.state(f); state != nil {
		state.finish(err)
	} else if f != nil {
		// CONNECT failures can occur before the normal HTTP exchange starts.
		state := newCaptureState(a.hub, f)
		state.owner = a
		state.finish(err)
	}
}

func (a *captureAddon) SSEEnd(f *mitmproxy.Flow) {
	if state := a.state(f); state != nil {
		state.finish(nil)
	}
}

func (a *captureAddon) WebSocketEnd(f *mitmproxy.Flow) {
	// WebSocket messages have a separate traffic.WebSocketExchange model. The
	// HTTP capture state must still be released when the upgraded connection
	// ends, otherwise a long-lived socket leaks its pending entry.
	if f != nil {
		a.pending.Delete(f.Id.String())
	}
}

func (a *captureAddon) state(f *mitmproxy.Flow) *captureState {
	if f == nil {
		return nil
	}
	if value, ok := a.pending.Load(f.Id.String()); ok {
		return value.(*captureState)
	}
	return nil
}

type captureState struct {
	owner *captureAddon
	hub   *ProxyHub
	proxy string
	start time.Time

	mu           sync.Mutex
	finished     bool
	flow         Flow
	reqSink      *traffic.BodySink
	respSink     *traffic.BodySink
	captureErr   error
	reqCaptured  bool
	respCaptured bool
}

func newCaptureState(hub *ProxyHub, f *mitmproxy.Flow) *captureState {
	flow := Flow{Timestamp: f.StartTime, ToolID: toolIDOf(f)}
	if f.ConnContext != nil && f.ConnContext.ClientConn != nil {
		flow.TLS = f.ConnContext.ClientConn.Tls
	}
	if f.Request != nil {
		flow.Exchange.ID = f.Id.String()
		flow.Request = traffic.Request{
			Method:   f.Request.Method,
			URL:      f.Request.URL.String(),
			Protocol: f.Request.Proto,
			Headers:  traffic.PairsFromHTTP(f.Request.Header),
		}
		flow.Host = f.Request.URL.Hostname()
	}
	return &captureState{hub: hub, owner: nil, proxy: f.Id.String(), start: f.StartTime, flow: flow}
}

func (s *captureState) setRequestBody(body []byte) {
	if len(body) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished || s.reqCaptured {
		return
	}
	s.reqCaptured = true
	if s.reqSink == nil {
		var err error
		s.reqSink, err = s.hub.store.bodySink(s.proxy, "req")
		if err != nil {
			s.captureErr = err
		}
	}
	if s.reqSink != nil {
		_, _ = s.reqSink.Write(body)
		s.flow.Request.Body = s.reqSink.Preview()
		return
	}
	s.flow.Request.Body = appendPreview(s.flow.Request.Body, body, maxBodySnip)
}

func (s *captureState) setResponseMeta(status int, headers http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	var body []byte
	var bodyRef *traffic.BodyRef
	if s.flow.Response != nil {
		body = s.flow.Response.Body
		bodyRef = s.flow.Response.BodyRef
	}
	s.flow.Response = &traffic.Response{
		StatusCode: status, Headers: traffic.PairsFromHTTP(headers),
		Body: body, BodyRef: bodyRef,
	}
	s.flow.ContentType = headers.Get("Content-Type")
}

func (s *captureState) setResponseBody(body []byte) {
	if len(body) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished || s.respCaptured {
		return
	}
	s.respCaptured = true
	if s.respSink == nil {
		var err error
		s.respSink, err = s.hub.store.bodySink(s.proxy, "resp")
		if err != nil {
			s.captureErr = err
		}
	}
	if s.respSink != nil {
		_, _ = s.respSink.Write(body)
		if s.flow.Response == nil {
			s.flow.Response = &traffic.Response{}
		}
		s.flow.Response.Body = s.respSink.Preview()
		return
	}
	if s.flow.Response == nil {
		s.flow.Response = &traffic.Response{}
	}
	s.flow.Response.Body = appendPreview(s.flow.Response.Body, body, maxBodySnip)
}

func (s *captureState) requestReader(in io.Reader) io.Reader {
	s.mu.Lock()
	if s.reqCaptured {
		s.mu.Unlock()
		return in
	}
	if s.reqSink == nil {
		var err error
		s.reqSink, err = s.hub.store.bodySink(s.proxy, "req")
		if err != nil {
			s.captureErr = err
		}
	}
	s.reqCaptured = true
	sink := s.reqSink
	s.mu.Unlock()
	if sink == nil {
		return &previewReader{src: in, add: func(p []byte) {
			s.mu.Lock()
			s.flow.Request.Body = appendPreview(s.flow.Request.Body, p, maxBodySnip)
			s.mu.Unlock()
		}}
	}
	return sink.Reader(in)
}

func (s *captureState) responseReader(in io.Reader) io.Reader {
	s.mu.Lock()
	if s.respCaptured {
		s.mu.Unlock()
		return in
	}
	if s.respSink == nil {
		var err error
		s.respSink, err = s.hub.store.bodySink(s.proxy, "resp")
		if err != nil {
			s.captureErr = err
		}
	}
	s.respCaptured = true
	sink := s.respSink
	s.mu.Unlock()
	if sink == nil {
		return &finishReader{src: &previewReader{src: in, add: func(p []byte) {
			s.mu.Lock()
			if s.flow.Response == nil {
				s.flow.Response = &traffic.Response{}
			}
			s.flow.Response.Body = appendPreview(s.flow.Response.Body, p, maxBodySnip)
			s.mu.Unlock()
		}}, done: func(err error) { s.finish(err) }}
	}
	return &finishReader{src: sink.Reader(in), done: func(err error) { s.finish(err) }}
}

func (s *captureState) finish(err error) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	complete := err == nil && s.flow.Response != nil && s.flow.Response.StatusCode != 0
	if err == nil && s.captureErr != nil {
		err = s.captureErr
	}
	if s.reqSink != nil {
		ref, closeErr := s.reqSink.Close(complete)
		s.flow.Request.BodyRef = &ref
		s.flow.Request.Body = s.reqSink.Preview()
		if closeErr != nil && err == nil {
			err = closeErr
		}
	}
	if s.respSink != nil {
		ref, closeErr := s.respSink.Close(complete)
		if s.flow.Response == nil {
			s.flow.Response = &traffic.Response{}
		}
		s.flow.Response.BodyRef = &ref
		s.flow.Response.Body = s.respSink.Preview()
		if closeErr != nil && err == nil {
			err = closeErr
		}
	}
	if err != nil {
		// A body write/close failure is part of the observation outcome. Do not
		// publish a complete exchange whose file-backed payload is incomplete.
		complete = false
	}
	if err != nil {
		s.flow.Error = err.Error()
	}
	s.flow.Complete = complete
	s.flow.Duration = time.Since(s.start)
	flow := s.flow
	s.mu.Unlock()
	s.hub.ingest(flow)
	// Keep the pending map bounded even when mitmproxy does not issue a later
	// lifecycle callback for a failed/streaming connection.
	if s.owner != nil {
		s.owner.pending.Delete(s.proxy)
	}
}

func (s *captureState) discard() {
	s.mu.Lock()
	if s.reqSink != nil {
		_ = s.reqSink.Discard()
	}
	if s.respSink != nil {
		_ = s.respSink.Discard()
	}
	s.finished = true
	s.mu.Unlock()
	if s.owner != nil {
		s.owner.pending.Delete(s.proxy)
	}
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

type previewReader struct {
	src io.Reader
	add func([]byte)
}

func (r *previewReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.add(p[:n])
	}
	return n, err
}

type finishReader struct {
	src  io.Reader
	done func(error)
	once sync.Once
}

func (r *finishReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if err != nil {
		finishErr := err
		if err == io.EOF {
			finishErr = nil
		}
		r.once.Do(func() { r.done(finishErr) })
	}
	return n, err
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

// bodySink creates a file-backed capture when the runner configured a body
// directory. Tests and embedded users can leave it empty and retain the
// bounded in-memory preview behavior.
func (s *FlowStore) bodySink(proxyID, side string) (*traffic.BodySink, error) {
	dir := s.BodyDir()
	if dir == "" {
		return nil, nil
	}
	return traffic.NewBodySink(filepath.Join(dir, "body"), proxyID+"."+side, maxBodySnip)
}

type QueryOpts struct {
	Host   string
	Status string
	CType  string
	Last   int
}

type FlowStore struct {
	mu        sync.RWMutex
	flows     []Flow
	head      int
	size      int
	seq       int
	cap       int
	bodyDir   string
	indexPath string
	indexFile *os.File
	indexMu   sync.Mutex
	indexErr  error
}

func NewFlowStore(cap int) *FlowStore {
	if cap <= 0 {
		cap = 10000
	}
	return &FlowStore{flows: make([]Flow, cap), cap: cap}
}

// SetBodyDir enables disk-backed request/response bodies for flows captured by
// this store. The directory is intentionally configured by the runner rather
// than by the traffic protocol, keeping the storage policy local to the tool.
func (s *FlowStore) SetBodyDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	indexPath := filepath.Join(dir, "flows.jsonl")
	if err := s.loadIndex(indexPath); err != nil {
		return err
	}
	file, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("proxy flow store: open metadata index: %w", err)
	}
	s.mu.Lock()
	if s.indexFile != nil {
		s.indexMu.Lock()
		_ = s.indexFile.Close()
		s.indexMu.Unlock()
	}
	s.bodyDir = dir
	s.indexPath = indexPath
	s.indexFile = file
	s.mu.Unlock()
	return nil
}

func (s *FlowStore) BodyDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bodyDir
}

// IndexError reports a metadata append failure. The in-memory/ring capture is
// still usable when the optional index cannot be written, but callers can
// surface this diagnostic instead of mistaking the index for durable storage.
func (s *FlowStore) IndexError() error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	return s.indexErr
}

// Sequence returns the newest assigned flow id. It does not change when the
// ring is cleared, so a reconnecting consumer can safely use it as a cursor.
func (s *FlowStore) Sequence() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// after returns the ordered flows whose ids are greater than id. The ring is
// deliberately the source of truth for replay; callers that ask for an id
// older than the retained window receive the oldest retained flow onward.
func (s *FlowStore) after(id int) []Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Flow, 0, s.size)
	for n := 0; n < s.size; n++ {
		idx := (s.head + n) % s.cap
		flow := s.flows[idx]
		if flowSequence(flow.ID) > id {
			result = append(result, flow)
		}
	}
	return result
}

// After returns a replay window for callers that need to recover from a
// reconnect. The returned slice is ordered by the store's monotonic id.
func (s *FlowStore) After(id int) []Flow { return s.after(id) }

// Add stores f, assigns it a monotonic ID, and returns the stored copy so the
// caller can fan the ID-bearing flow out to subscribers.
func (s *FlowStore) Add(f Flow) Flow {
	s.mu.Lock()
	s.seq++
	f.ID = strconv.Itoa(s.seq)
	idx := (s.head + s.size) % s.cap
	if s.size == s.cap {
		idx = s.head
		s.head = (s.head + 1) % s.cap
	} else {
		s.size++
	}
	s.flows[idx] = f
	s.mu.Unlock()
	s.appendIndex(f)
	return f
}

func (s *FlowStore) appendIndex(f Flow) {
	s.mu.RLock()
	file := s.indexFile
	s.mu.RUnlock()
	if file == nil {
		return
	}
	record := map[string]any{
		"id": f.ID, "tool_id": f.ToolID, "timestamp": f.Timestamp,
		"host": f.Host, "content_type": f.ContentType, "duration": int64(f.Duration),
		"tls": f.TLS, "exchange": f.Exchange,
	}
	if f.Request.BodyRef != nil {
		record["request_body_ref"] = f.Request.BodyRef
	}
	if f.Response != nil && f.Response.BodyRef != nil {
		record["response_body_ref"] = f.Response.BodyRef
	}
	line, err := json.Marshal(record)
	if err == nil {
		line = append(line, '\n')
		s.indexMu.Lock()
		defer s.indexMu.Unlock()
		if _, err = file.Write(line); err == nil {
			err = file.Sync()
		}
		if err != nil && s.indexErr == nil {
			s.indexErr = err
		}
	}
}

func (s *FlowStore) loadIndex(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("proxy flow store: open metadata index: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	for {
		var record map[string]json.RawMessage
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			// A torn final append must not make the whole capture unreadable.
			break
		}
		var flow Flow
		if err := decodeIndexRecord(record, &flow); err != nil {
			continue
		}
		s.mu.Lock()
		s.putLocked(flow)
		s.mu.Unlock()
	}
	return nil
}

func decodeIndexRecord(record map[string]json.RawMessage, flow *Flow) error {
	decode := func(key string, dst any) error {
		raw, ok := record[key]
		if !ok {
			return fmt.Errorf("missing %s", key)
		}
		return json.Unmarshal(raw, dst)
	}
	if err := decode("id", &flow.ID); err != nil {
		return err
	}
	if err := decode("exchange", &flow.Exchange); err != nil {
		return err
	}
	_ = decode("tool_id", &flow.ToolID)
	_ = decode("timestamp", &flow.Timestamp)
	_ = decode("host", &flow.Host)
	_ = decode("content_type", &flow.ContentType)
	var duration int64
	if decode("duration", &duration) == nil {
		flow.Duration = time.Duration(duration)
	}
	_ = decode("tls", &flow.TLS)
	if raw, ok := record["request_body_ref"]; ok {
		var ref traffic.BodyRef
		if json.Unmarshal(raw, &ref) == nil {
			flow.Request.BodyRef = &ref
		}
	}
	if raw, ok := record["response_body_ref"]; ok && flow.Response != nil {
		var ref traffic.BodyRef
		if json.Unmarshal(raw, &ref) == nil {
			flow.Response.BodyRef = &ref
		}
	}
	return nil
}

func (s *FlowStore) putLocked(f Flow) {
	if f.ID == "" {
		return
	}
	if seq := flowSequence(f.ID); seq > s.seq {
		s.seq = seq
	}
	idx := (s.head + s.size) % s.cap
	if s.size == s.cap {
		idx = s.head
		s.head = (s.head + 1) % s.cap
	} else {
		s.size++
	}
	s.flows[idx] = f
}

func (s *FlowStore) Query(opts QueryOpts) []Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Flow, 0, s.size)
	for n := 0; n < s.size; n++ {
		idx := (s.head + n) % s.cap
		f := &s.flows[idx]
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
	want := strconv.Itoa(id)
	for n := 0; n < s.size; n++ {
		idx := (s.head + n) % s.cap
		if s.flows[idx].ID == want {
			f := s.flows[idx]
			s.mu.RUnlock()
			f.Exchange = f.Exchange.Clone()
			_ = f.Exchange.HydrateBodies()
			return &f
		}
	}
	s.mu.RUnlock()
	return nil
}

func (s *FlowStore) Clear() {
	s.mu.Lock()
	bodyDir := s.bodyDir
	indexFile := s.indexFile
	indexPath := s.indexPath
	s.indexFile = nil
	for i := range s.flows {
		s.flows[i] = Flow{}
	}
	s.head = 0
	s.size = 0
	s.mu.Unlock()
	s.indexMu.Lock()
	s.indexErr = nil
	s.indexMu.Unlock()
	if indexFile != nil {
		s.indexMu.Lock()
		_ = indexFile.Close()
		s.indexMu.Unlock()
	}
	if bodyDir != "" {
		_ = os.RemoveAll(bodyDir)
		_ = os.MkdirAll(bodyDir, 0o755)
		if indexPath != "" {
			if file, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				s.mu.Lock()
				s.indexFile = file
				s.mu.Unlock()
			}
		}
	}
}

// Close releases the append-only metadata handle. Body files are deliberately
// retained so a caller can inspect a capture after the proxy listener stops.
func (s *FlowStore) Close() error {
	s.mu.Lock()
	file := s.indexFile
	s.indexFile = nil
	s.mu.Unlock()
	if file == nil {
		return nil
	}
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	return file.Close()
}

func (s *FlowStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
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
