package proxy

import (
	"context"
	"encoding/json"
	"errors"
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
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
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

// Keep a single captured request/response from becoming an unbounded disk
// allocation. The full observed size remains in BodyRef.Size; only the prefix
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
		state.flow.Response = &traffic.Response{StatusCode: f.Response.StatusCode, Headers: traffic.PairsFromHTTP(f.Response.Header)}
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
	hub               *ProxyHub
	mu                sync.Mutex
	finished          bool
	flow              Flow
	reqSink, respSink *bodyRecorder
	captureErr        error
}

func newCaptureState(hub *ProxyHub, f *mitmproxy.Flow) *captureState {
	flow := Flow{Timestamp: f.StartTime, ToolID: toolIDOf(f)}
	if f.ConnContext != nil && f.ConnContext.ClientConn != nil {
		flow.TLS = f.ConnContext.ClientConn.Tls
	}
	if f.Request != nil {
		flow.ID = f.Id.String()
		flow.Request = traffic.Request{Method: f.Request.Method, URL: f.Request.URL.String(), Protocol: f.Request.Proto, Headers: traffic.PairsFromHTTPWithHost(f.Request.Header, requestHost(f.Request))}
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
	sink := &s.reqSink
	if side == "resp" {
		sink = &s.respSink
	}
	if *sink == nil {
		var err error
		*sink, err = s.hub.store.bodySink(s.flow.ID, side)
		if err != nil {
			s.captureErr = err
			*sink = &bodyRecorder{release: func() {}}
		}
	}
	return io.TeeReader(in, *sink)
}
func (s *captureState) finish(err error) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	if !s.flow.Timestamp.IsZero() {
		s.flow.Duration = time.Since(s.flow.Timestamp)
	}
	async := (s.reqSink != nil && s.reqSink.sub != nil) || (s.respSink != nil && s.respSink.sub != nil)
	s.mu.Unlock()
	if async {
		s.hub.finalize(s.completeCapture, err)
	} else {
		s.completeCapture(err)
	}
}
func (s *captureState) completeCapture(err error) {
	if s.reqSink != nil {
		defer s.reqSink.release()
	}
	if s.respSink != nil {
		defer s.respSink.release()
	}
	s.mu.Lock()
	err = errors.Join(err, s.captureErr)
	complete := err == nil && s.flow.Response != nil && s.flow.Response.StatusCode != 0
	var notices []string
	capture := func(sink *bodyRecorder, side string, body *[]byte, ref **traffic.BodyRef) {
		if sink == nil {
			return
		}
		value, closeErr := sink.Close(complete)
		*body = sink.Preview()
		if value.Path != "" {
			*ref = &value
		}
		err = errors.Join(err, closeErr)
		if value.Truncated {
			notice := fmt.Sprintf("%s body truncated (%d/%d bytes retained)", side, value.StoredSize, value.Size)
			if sink.sub == nil {
				notice += " (preview only; local storage disabled or unavailable)"
			}
			notices = append(notices, notice)
		}
	}
	capture(s.reqSink, "request", &s.flow.Request.Body, &s.flow.Request.BodyRef)
	if s.respSink != nil {
		if s.flow.Response == nil {
			s.flow.Response = &traffic.Response{}
		}
		capture(s.respSink, "response", &s.flow.Response.Body, &s.flow.Response.BodyRef)
	}
	if err != nil {
		s.flow.Error = err.Error()
	}
	if len(notices) > 0 {
		if s.flow.Error != "" {
			s.flow.Error += "; "
		}
		s.flow.Error += strings.Join(notices, "; ")
	}
	s.flow.Complete = complete && err == nil && len(notices) == 0
	flow := s.flow
	s.mu.Unlock()
	s.hub.ingest(flow)
}
func (s *captureState) discard() {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	req, resp := s.reqSink, s.respSink
	s.mu.Unlock()
	cleanup := func(error) {
		if req != nil {
			_ = req.Discard()
		}
		if resp != nil {
			_ = resp.Discard()
		}
	}
	if (req != nil && req.sub != nil) || (resp != nil && resp.sub != nil) {
		s.hub.finalize(cleanup, nil)
	} else {
		cleanup(nil)
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
func (s *FlowStore) bodySink(proxyID, side string) (*bodyRecorder, error) {
	dir := s.BodyDir()
	if dir == "" {
		return &bodyRecorder{release: func() {}}, nil
	}
	select {
	case s.bodySlots <- struct{}{}:
		return newBodyRecorder(filepath.Join(dir, "body"), proxyID+"."+side, s.bodyMaxBytes, func() { <-s.bodySlots })
	default:
		return nil, fmt.Errorf("traffic: concurrent body recorder limit exceeded")
	}
}

type QueryOpts struct {
	Host   string
	Status string
	CType  string
	Last   int
}

// FlowStore owns the retained flow ring and its file-backed body lifecycle.
// bodyMu coordinates body-file readers with retention cleanup. The ring may
// evict a flow while a subscriber is hydrating a copy of it; keeping both
// operations in this lock prevents an otherwise silent read race.
type FlowStore struct {
	publishMu    sync.Mutex
	events       eventbus.Bus[Flow]
	indexSub     *eventbus.Subscription[Flow]
	bodyMaxBytes int64
	bodySlots    chan struct{}
	mu           sync.RWMutex
	bodyMu       sync.RWMutex
	flows        []Flow
	head         int
	size         int
	seq          int
	cap          int
	bodyBytes    int64
	maxBodyBytes int64
	bodyRefs     map[string]int
	bodyDir      string
	indexPath    string
	indexFile    *os.File
	indexMu      sync.Mutex
	indexErr     error
}

func NewFlowStore(cap int) *FlowStore {
	return NewFlowStoreWithLimits(cap, defaultMaxBodyBytes)
}

// NewFlowStoreWithLimits is NewFlowStore with an explicit retained-body byte
// budget. maxBodyBytes <= 0 disables the aggregate budget; per-body capture is
// still capped by the proxy's BodySink policy.
func NewFlowStoreWithLimits(cap int, maxBodyBytes int64) *FlowStore {
	if cap <= 0 {
		cap = 10000
	}
	if maxBodyBytes < 0 {
		maxBodyBytes = 0
	}
	return &FlowStore{
		bodyMaxBytes: maxBodyCaptureBytes,
		bodySlots:    make(chan struct{}, 16),
		flows:        make([]Flow, cap),
		cap:          cap,
		maxBodyBytes: maxBodyBytes,
		bodyRefs:     make(map[string]int),
	}
}

// SetBodyDir enables disk-backed request/response bodies for flows captured by
// this store. The directory is intentionally configured by the runner rather
// than by the traffic protocol, keeping the storage policy local to the tool.
// Existing body files are reconciled with the retained ring before capture
// starts, so crash leftovers do not survive indefinitely.
func (s *FlowStore) SetBodyDir(dir string) error {
	if dir == "" {
		return nil
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.indexSub != nil {
		if err := s.indexSub.Close(context.Background()); err != nil {
			return err
		}
	}
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
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
	// loadIndex runs before bodyDir is installed so it can reuse the existing
	// decoder. Rebuild the path index once the directory is known, then remove
	// files left by prior crashes or flows that have fallen out of the ring.
	s.rebuildBodyRefsLocked()
	s.pruneUnreferencedBodiesLocked()
	return s.subscribeIndex()
}

func (s *FlowStore) BodyDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bodyDir
}

// hydrate loads file-backed bodies while holding the store's body read lock.
// Readers operate on a flow copy, so retention cleanup can safely remove an
// evicted flow's files as soon as no in-flight store read still needs them.
func (s *FlowStore) hydrate(flow *Flow) error {
	if flow == nil {
		return nil
	}
	s.bodyMu.RLock()
	defer s.bodyMu.RUnlock()
	return flow.HydrateBodies()
}

func flowBodyRefs(flow Flow) []*traffic.BodyRef {
	refs := make([]*traffic.BodyRef, 0, 2)
	if flow.Request.BodyRef != nil {
		refs = append(refs, flow.Request.BodyRef)
	}
	if flow.Response != nil && flow.Response.BodyRef != nil {
		refs = append(refs, flow.Response.BodyRef)
	}
	return refs
}

func bodyStoredSize(ref *traffic.BodyRef) int64 {
	if ref == nil {
		return 0
	}
	// StoredSize was added after the original index format. Fall back to Size
	// for old records, where Size was the on-disk byte count. A truncated
	// record from an intermediate writer may not carry StoredSize; counting its
	// observed Size is conservative and keeps the aggregate budget bounded.
	if ref.StoredSize > 0 {
		return ref.StoredSize
	}
	if ref.Size > 0 {
		return ref.Size
	}
	return 0
}

func flowBodyStoredSize(flow Flow) int64 {
	seen := make(map[string]struct{}, 2)
	var total int64
	for _, ref := range flowBodyRefs(flow) {
		if ref == nil {
			continue
		}
		key := ref.Path
		if key != "" {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		if n := bodyStoredSize(ref); n > 0 {
			total += n
		}
	}
	return total
}

// resolveBodyPath only permits cleanup inside the store's dedicated body
// directory. BodyRef values are persisted metadata, so rejecting paths that
// escape that directory avoids turning retention into an arbitrary file delete.
func resolveBodyPath(root, raw string) (string, bool) {
	if root == "" || raw == "" {
		return "", false
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	bodyRoot := filepath.Join(root, "body")
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(bodyRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path, true
}

func bodyPathVariants(path string) []string {
	if strings.HasSuffix(path, ".part") {
		return []string{path, strings.TrimSuffix(path, ".part")}
	}
	return []string{path, path + ".part"}
}

func bodyRefPaths(root string, flow Flow) []string {
	seen := make(map[string]struct{}, 4)
	paths := make([]string, 0, 4)
	for _, ref := range flowBodyRefs(flow) {
		if ref == nil {
			continue
		}
		path, ok := resolveBodyPath(root, ref.Path)
		if !ok {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func (s *FlowStore) addBodyRefsLocked(flow Flow) {
	if s.bodyRefs == nil {
		s.bodyRefs = make(map[string]int)
	}
	for _, path := range bodyRefPaths(s.bodyDir, flow) {
		s.bodyRefs[path]++
	}
}

func (s *FlowStore) removeBodyRefsLocked(flow Flow) []string {
	paths := bodyRefPaths(s.bodyDir, flow)
	if len(paths) == 0 || len(s.bodyRefs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths)*2)
	var removable []string
	for _, path := range paths {
		count := s.bodyRefs[path]
		if count <= 1 {
			delete(s.bodyRefs, path)
			for _, candidate := range bodyPathVariants(path) {
				if _, exists := seen[candidate]; exists {
					continue
				}
				seen[candidate] = struct{}{}
				removable = append(removable, candidate)
			}
		} else {
			s.bodyRefs[path] = count - 1
		}
	}
	return removable
}

// cleanupBodyPaths removes files after the caller has dropped their ring
// references. The body write lock serializes this I/O with hydration; the
// reference-count recheck closes the race with a concurrently reused path.
func (s *FlowStore) cleanupBodyPaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	unique := make(map[string]struct{}, len(paths))
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	s.mu.RLock()
	bodyDir := s.bodyDir
	var removable []string
	for _, path := range paths {
		if _, seen := unique[path]; seen || s.bodyRefs[path] > 0 {
			continue
		}
		if _, ok := resolveBodyPath(bodyDir, path); !ok {
			continue
		}
		unique[path] = struct{}{}
		removable = append(removable, path)
	}
	s.mu.RUnlock()
	for _, path := range removable {
		_ = os.Remove(path)
	}
}

// cleanupFlowBodies releases a flow which was observed but never admitted to
// the ring (for example because capture was toggled off or a filter rejected
// it). It is deliberately best-effort: cleanup failure must not affect proxy
// forwarding.
func (s *FlowStore) cleanupFlowBodies(flow Flow) {
	s.mu.RLock()
	actual := bodyRefPaths(s.bodyDir, flow)
	s.mu.RUnlock()
	paths := make([]string, 0, len(actual)*2)
	for _, path := range actual {
		paths = append(paths, bodyPathVariants(path)...)
	}
	s.cleanupBodyPaths(paths)
}

func (s *FlowStore) rebuildBodyRefsLocked() {
	s.mu.Lock()
	s.bodyRefs = make(map[string]int)
	s.bodyBytes = 0
	for n := 0; n < s.size; n++ {
		idx := (s.head + n) % s.cap
		flow := s.flows[idx]
		s.addBodyRefsLocked(flow)
		s.bodyBytes += flowBodyStoredSize(flow)
	}
	s.mu.Unlock()
}

func (s *FlowStore) pruneUnreferencedBodiesLocked() {
	s.mu.RLock()
	bodyDir := s.bodyDir
	live := make(map[string]struct{}, len(s.bodyRefs))
	for path := range s.bodyRefs {
		live[path] = struct{}{}
	}
	s.mu.RUnlock()
	if bodyDir == "" {
		return
	}
	bodyRoot := filepath.Join(bodyDir, "body")
	_ = filepath.Walk(bodyRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil || info.IsDir() {
			return nil
		}
		absolute, absErr := filepath.Abs(path)
		if absErr != nil {
			return absErr
		}
		if _, ok := live[absolute]; !ok {
			_ = os.Remove(path)
		}
		return nil
	})
}

// IndexError reports a metadata append failure. The in-memory/ring capture is
// still usable when the optional index cannot be written, but callers can
// surface this diagnostic instead of mistaking the index for durable storage.
func (s *FlowStore) IndexError() error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.indexSub != nil {
		if err := s.indexSub.Err(); err != nil {
			return err
		}
	}
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
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	// Bound previews before cloning so external callers cannot make the ring
	// or admission queue retain an entire body allocation.
	if len(f.Request.Body) > maxBodySnip {
		f.Request.Body = f.Request.Body[:maxBodySnip]
		if f.Request.BodyRef == nil {
			f.Complete = false
			f.Error += "; request body truncated (preview only)"
		}
	}
	if f.Response != nil && len(f.Response.Body) > maxBodySnip {
		resp := *f.Response
		f.Response = &resp
		f.Response.Body = f.Response.Body[:maxBodySnip]
		if f.Response.BodyRef == nil {
			f.Complete = false
			f.Error += "; response body truncated (preview only)"
		}
	}
	f = cloneFlowMetadata(f)
	incomingBytes := flowBodyStoredSize(f)
	var cleanup []string
	s.bodyMu.RLock()
	s.mu.Lock()
	s.seq++
	f.ID = strconv.Itoa(s.seq)
	// Evict until both dimensions of the retention policy fit. A single flow
	// may exceed a custom aggregate budget; it is retained as the newest flow
	// rather than being silently discarded.
	for s.size > 0 && (s.size == s.cap ||
		(s.maxBodyBytes > 0 && s.bodyBytes+incomingBytes > s.maxBodyBytes)) {
		idx := s.head
		victim := s.flows[idx]
		cleanup = append(cleanup, s.removeBodyRefsLocked(victim)...)
		s.bodyBytes -= flowBodyStoredSize(victim)
		if s.bodyBytes < 0 {
			s.bodyBytes = 0
		}
		s.head = (s.head + 1) % s.cap
		s.size--
	}
	idx := (s.head + s.size) % s.cap
	s.flows[idx] = f
	s.size++
	s.bodyBytes += incomingBytes
	s.addBodyRefsLocked(f)
	s.mu.Unlock()
	s.bodyMu.RUnlock()
	s.cleanupBodyPaths(cleanup)
	s.events.Emit(f)
	return f
}

func (s *FlowStore) subscribeIndex() error {
	sub, err := s.events.SubscribeAsync(eventbus.SubscribeOptions[Flow]{
		Buffer: 256, MaxBytes: 4 << 20, Size: flowMetadataSize, Clone: cloneFlowMetadata,
	}, s.appendIndex)
	s.indexSub = sub
	return err
}

func (s *FlowStore) appendIndex(f Flow) error {
	s.mu.RLock()
	file := s.indexFile
	s.mu.RUnlock()
	if file == nil {
		return io.ErrClosedPipe
	}
	f = cloneFlowMetadata(f)
	f.Request.Body = nil
	if f.Response != nil {
		f.Response.Body = nil
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
		var n int
		n, err = file.Write(line)
		if err == nil && n != len(line) {
			err = io.ErrShortWrite
		}
		if err != nil && s.indexErr == nil {
			s.indexErr = err
		}
	}
	return err
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
	incomingBytes := flowBodyStoredSize(f)
	for s.size > 0 && (s.size == s.cap ||
		(s.maxBodyBytes > 0 && s.bodyBytes+incomingBytes > s.maxBodyBytes)) {
		idx := s.head
		victim := s.flows[idx]
		s.bodyBytes -= flowBodyStoredSize(victim)
		if s.bodyBytes < 0 {
			s.bodyBytes = 0
		}
		s.removeBodyRefsLocked(victim)
		s.head = (s.head + 1) % s.cap
		s.size--
	}
	idx := (s.head + s.size) % s.cap
	s.flows[idx] = f
	s.size++
	s.bodyBytes += incomingBytes
	s.addBodyRefsLocked(f)
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
	// Hold the body read lock across the lookup and hydration. Otherwise an
	// eviction could copy a flow, delete its file, and win the race before the
	// caller has loaded the body.
	s.bodyMu.RLock()
	defer s.bodyMu.RUnlock()
	s.mu.RLock()
	want := strconv.Itoa(id)
	for n := 0; n < s.size; n++ {
		idx := (s.head + n) % s.cap
		if s.flows[idx].ID == want {
			f := s.flows[idx]
			s.mu.RUnlock()
			f.Exchange = f.Clone()
			if err := f.HydrateBodies(); err != nil {
				f.Complete = false
				f.Error += fmt.Sprintf("; body unavailable: %v", err)
			}
			return &f
		}
	}
	s.mu.RUnlock()
	return nil
}

func (s *FlowStore) Clear() {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.indexSub != nil {
		_ = s.indexSub.Close(context.Background())
	}
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	s.mu.Lock()
	bodyDir := s.bodyDir
	var bodyPaths []string
	for path := range s.bodyRefs {
		bodyPaths = append(bodyPaths, path)
	}
	indexFile := s.indexFile
	indexPath := s.indexPath
	s.indexFile = nil
	for i := range s.flows {
		s.flows[i] = Flow{}
	}
	s.head = 0
	s.size = 0
	s.bodyBytes = 0
	s.bodyRefs = make(map[string]int)
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
		for _, path := range bodyPaths {
			_ = os.Remove(path)
		}
		if indexPath != "" {
			if file, err := os.OpenFile(indexPath, os.O_CREATE|os.O_TRUNC|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				s.mu.Lock()
				s.indexFile = file
				s.mu.Unlock()
			} else {
				s.indexMu.Lock()
				s.indexErr = err
				s.indexMu.Unlock()
			}
		}
		_ = s.subscribeIndex()
	}
}

// Close releases the append-only metadata handle. Body files belonging to the
// retained ring are deliberately kept so a caller can inspect a capture after
// the proxy listener stops; the next SetBodyDir startup sweep removes files
// which are no longer reachable.
func (s *FlowStore) Close() error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	var recordErr error
	if s.indexSub != nil {
		recordErr = s.indexSub.Close(context.Background())
	}
	s.mu.Lock()
	file := s.indexFile
	s.indexFile = nil
	s.mu.Unlock()
	if file == nil {
		return recordErr
	}
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	return errors.Join(recordErr, file.Sync(), file.Close())
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
