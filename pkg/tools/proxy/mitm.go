package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/proxyclient"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
)

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
		return true
	}
}

// ---------------------------------------------------------------------------
// CaptureAddon
// ---------------------------------------------------------------------------

const maxBodySnip = 4096

type CaptureAddon struct {
	mitmproxy.BaseAddon
	store   *FlowStore
	pending sync.Map
}

func (a *CaptureAddon) Requestheaders(f *mitmproxy.Flow) {
	a.pending.Store(f.Id.String(), time.Now())
}

func (a *CaptureAddon) Response(f *mitmproxy.Flow) {
	var dur time.Duration
	if start, ok := a.pending.LoadAndDelete(f.Id.String()); ok {
		dur = time.Since(start.(time.Time))
	}
	flow := Flow{
		Timestamp:      f.StartTime,
		Method:         f.Request.Method,
		URL:            f.Request.URL.String(),
		Host:           f.Request.URL.Hostname(),
		Duration:       dur,
		TLS:            f.ConnContext.ClientConn.Tls,
		RequestHeaders: cloneHeaders(f.Request.Header),
	}
	if len(f.Request.Body) > 0 {
		flow.RequestBodySnip = snip(f.Request.Body, maxBodySnip)
	}
	if f.Response != nil {
		flow.StatusCode = f.Response.StatusCode
		flow.ResponseHeaders = cloneHeaders(f.Response.Header)
		flow.ContentType = f.Response.Header.Get("Content-Type")
		if len(f.Response.Body) > 0 {
			flow.ResponseBodySnip = snip(f.Response.Body, maxBodySnip)
		}
	}
	a.store.Add(flow)
}

func (a *CaptureAddon) RequestError(f *mitmproxy.Flow, err error) {
	var dur time.Duration
	if start, ok := a.pending.LoadAndDelete(f.Id.String()); ok {
		dur = time.Since(start.(time.Time))
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

func cloneHeaders(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func snip(b []byte, max int) []byte {
	if len(b) <= max {
		dst := make([]byte, len(b))
		copy(dst, b)
		return dst
	}
	dst := make([]byte, max)
	copy(dst, b[:max])
	return dst
}

// ---------------------------------------------------------------------------
// MITMState
// ---------------------------------------------------------------------------

type MITMState struct {
	mu         sync.RWMutex
	server     *mitmproxy.Proxy
	listenAddr string
	store      *FlowStore
	savedProxy string
	running    bool
}

func NewMITMState() *MITMState {
	return &MITMState{store: NewFlowStore(10000)}
}

func (s *MITMState) Start(addr string, upstreamProxy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("MITM already running on %s", s.listenAddr)
	}
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	p, err := mitmproxy.NewProxy(&mitmproxy.Options{
		Addr:              addr,
		StreamLargeBodies: 10 * 1024 * 1024,
		SslInsecure:       true,
	})
	if err != nil {
		return fmt.Errorf("create MITM proxy: %w", err)
	}

	if upstreamProxy != "" {
		u, err := url.Parse(upstreamProxy)
		if err != nil {
			return fmt.Errorf("parse upstream: %w", err)
		}
		dial, err := proxyclient.NewClient(u)
		if err != nil {
			return fmt.Errorf("create upstream dialer for %s: %w", u.Scheme, err)
		}
		p.SetDialer(dial.DialContext)
	}

	s.store.Clear()
	p.AddAddon(&CaptureAddon{store: s.store})

	listenAddr, _, err := p.StartAsync()
	if err != nil {
		return fmt.Errorf("start MITM proxy: %w", err)
	}

	s.server = p
	s.listenAddr = listenAddr.String()
	s.running = true
	return nil
}

func (s *MITMState) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return fmt.Errorf("MITM is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.server.Shutdown(ctx)
	s.server = nil
	s.running = false
	s.listenAddr = ""
	return err
}

func (s *MITMState) ProxyURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.running {
		return ""
	}
	return "http://" + s.listenAddr
}

func (s *MITMState) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *MITMState) ListenAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listenAddr
}

func (s *MITMState) Store() *FlowStore { return s.store }

func (s *MITMState) SavedProxy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.savedProxy
}

func (s *MITMState) SetSavedProxy(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.savedProxy = p
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
			f.ID, f.Method, f.StatusCode, urlStr,
			truncate(ct, 14), f.Duration.Milliseconds(), errMark))
	}
	return sb.String()
}

func formatFlowDetail(f *Flow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Flow #%d ===\n", f.ID))
	sb.WriteString(fmt.Sprintf("Time:     %s\n", f.Timestamp.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Method:   %s\n", f.Method))
	sb.WriteString(fmt.Sprintf("URL:      %s\n", f.URL))
	sb.WriteString(fmt.Sprintf("Status:   %d\n", f.StatusCode))
	sb.WriteString(fmt.Sprintf("Duration: %dms\n", f.Duration.Milliseconds()))
	sb.WriteString(fmt.Sprintf("TLS:      %v\n", f.TLS))
	if f.Error != "" {
		sb.WriteString(fmt.Sprintf("Error:    %s\n", f.Error))
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

	statusCounts := map[int]int{}
	hostCounts := map[string]int{}
	var errCount int
	for _, f := range flows {
		statusCounts[f.StatusCode/100]++
		hostCounts[f.Host]++
		if f.Error != "" {
			errCount++
		}
	}
	sb.WriteString("Summary:\n")
	sb.WriteString(fmt.Sprintf("  Hosts: %d unique\n", len(hostCounts)))
	for cls, n := range statusCounts {
		sb.WriteString(fmt.Sprintf("  %dxx: %d\n", cls, n))
	}
	if errCount > 0 {
		sb.WriteString(fmt.Sprintf("  Errors: %d\n", errCount))
	}
	sb.WriteString("\n")

	for i, f := range flows {
		sb.WriteString(fmt.Sprintf("FLOW #%d [%d] %s %s\n", f.ID, f.StatusCode, f.Method, f.URL))
		sb.WriteString(fmt.Sprintf("  Host: %s | Duration: %dms | TLS: %v\n", f.Host, f.Duration.Milliseconds(), f.TLS))
		if f.Error != "" {
			sb.WriteString(fmt.Sprintf("  ERROR: %s\n", f.Error))
		}
		if ct := f.ContentType; ct != "" {
			sb.WriteString(fmt.Sprintf("  Content-Type: %s\n", ct))
		}
		if len(f.ResponseBodySnip) > 0 {
			body := string(f.ResponseBodySnip)
			if len(body) > 500 {
				body = body[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("  Body: %s\n", body))
		}
		if i < len(flows)-1 {
			sb.WriteString("\n")
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
