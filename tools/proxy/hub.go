package proxy

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	traffic "github.com/chainreactors/cyber/aop/traffic"
	"github.com/chainreactors/cyber/core/hooks"
	cfg "github.com/chainreactors/cyber/pkg/config"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
)

// ProxyHub is the runner-level MITM proxy capability that every tool routes
// through. It is the STABLE front hop: its local address is injected once into
// child process env and in-process HTTP clients and never changes. The DYNAMIC
// back hop — the actual egress proxy chain — lives in State and is swapped live
// via State.CurrentDial(), which hub.dial reads on every connection. Switching
// proxy nodes therefore takes effect immediately without re-injecting anything
// into already-running children.
//
// hub.dial is installed as mitmproxy Options.Dialer so it covers plain HTTP as
// well as HTTPS/CONNECT (see the local mitmproxy fork patch adding that field).
type ProxyHub struct {
	state        *State
	store        *FlowStore
	caRootPath   string
	storage      cfg.TrafficOptions
	hooks        *hooks.Registry
	bodySlots    chan struct{} // file consumers, held through publication/cleanup
	stopping     atomic.Bool
	shutdownOnce sync.Once
	shutdownDone chan struct{}

	mu          sync.Mutex
	server      *mitmproxy.Proxy
	addr        string
	caPath      string
	started     bool
	startErr    error
	shutdownErr error
	startOnce   sync.Once

	// Capture is runtime-mutable so the control plane can toggle it via the
	// traffic namespace without restarting the listener. recording gates whether
	// flows are stored; decrypt gates HTTPS MITM interception. Both
	// are read on every connection, so a change takes effect for subsequent
	// connections while in-flight children are undisturbed.
	recording atomic.Bool
	decrypt   atomic.Bool
	filterMu  sync.RWMutex
	filter    QueryOpts
	authMu    sync.RWMutex
	auth      func(*http.Request) error

	correlationMu sync.RWMutex
	correlations  map[string]*correlationLease
}

// State returns the live egress selection capability. Lifecycle remains owned
// by the proxy extension; policy tools may use this handle to change routing.
func (h *ProxyHub) State() *State {
	if h == nil {
		return nil
	}
	return h.state
}

// Resource owns a ProxyHub's listener and flow store. Extensions retain the
// Resource and publish only ProxyHub, whose public API contains no lifecycle
// operations.
type Resource struct {
	ProxyHub *ProxyHub
}

// Keep proxy-side buffering bounded. Bodies at or above this threshold are
// captured through the body stream and written to disk incrementally.
const hubStreamLargeBodies = 64 * 1024

// NewProxyHub uses caller-owned State and takes ownership of FlowStore. Query
// consumers share that store; Shutdown drains and closes it. A nil store
// creates a private store with the same ownership contract.
//
// capture selects the mode. The hub is ALWAYS the routing substrate — tools
// route through it and `proxy switch` swaps its upstream live in either mode.
//   - capture=true  (mitm on):  intercept + record HTTPS (MITM) and HTTP flows.
//   - capture=false (mitm off): pure relay — no interception, no recording, no
//     CA needed. Routing still works; nothing is decrypted or stored.
//
// Start must be called before use.
func NewProxyHub(state *State, store *FlowStore, caRootPath string, capture bool, registry *hooks.Registry) *Resource {
	if store == nil {
		store = NewFlowStore(10000)
	}
	h := &ProxyHub{
		state: state, store: store, caRootPath: caRootPath, hooks: registry,
		correlations: make(map[string]*correlationLease),
		bodySlots:    make(chan struct{}, 16), shutdownDone: make(chan struct{}),
	}
	// The CA path is always prepared so capture can be toggled on at runtime;
	// CAPath only advertises it to children while interception is actually on.
	h.caPath = filepath.Join(caRootPath, "mitmproxy-ca-cert.pem")
	h.recording.Store(capture)
	h.decrypt.Store(capture)
	return &Resource{ProxyHub: h}
}

// Start activates the hub. Lifecycle ownership belongs to the extension that
// constructed it; the raw proxy resource has no dependency on the host.
func (r *Resource) Start(ctx context.Context) error {
	if r == nil || r.ProxyHub == nil {
		return fmt.Errorf("proxy resource is required")
	}
	h := r.ProxyHub
	if ctx == nil {
		return fmt.Errorf("proxy hub start context is required")
	}
	h.startOnce.Do(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.stopping.Load() {
			h.startErr = fmt.Errorf("proxy hub is closing or closed")
			return
		}
		if err := ctx.Err(); err != nil {
			h.startErr = err
			return
		}
		h.startErr = h.start(h.caRootPath)
	})
	if h.stopping.Load() {
		return fmt.Errorf("proxy hub is closing or closed")
	}
	if h.startErr != nil {
		return h.startErr
	}
	return ctx.Err()
}

// Capturing reports whether the hub currently records traffic (mitm on) or only
// relays.
func (h *ProxyHub) Capturing() bool { return h.recording.Load() }

// SetProxyAuth installs an optional, instance-scoped ingress policy. It runs
// before HTTP forwarding or CONNECT tunneling, including in relay mode. A
// non-nil error rejects the request with HTTP 407 before contacting the target.
// The callback must be safe for concurrent requests. Passing nil allows all
// requests, which is the default. Changes apply to subsequent requests, not
// traffic inside an already established CONNECT tunnel.
func (h *ProxyHub) SetProxyAuth(authorize func(*http.Request) error) {
	h.authMu.Lock()
	defer h.authMu.Unlock()
	h.auth = authorize
}

func (h *ProxyHub) authorize(_ http.ResponseWriter, req *http.Request) (bool, error) {
	h.authMu.RLock()
	check := h.auth
	h.authMu.RUnlock()
	if check == nil {
		return true, nil
	}
	err := check(req)
	return err == nil, err
}

// SetCapture toggles capture at runtime without restarting the listener. record
// gates storage; decryptHTTPS gates HTTPS MITM interception, which
// only affects connections opened after the change because a child's CA trust
// is fixed at spawn time.
func (h *ProxyHub) SetCapture(record, decryptHTTPS bool) {
	h.recording.Store(record)
	h.decrypt.Store(decryptHTTPS)
}

// SetCaptureFilter applies the existing traffic FlowFilter before a flow is
// stored. It deliberately lives on the hub so filtering avoids unnecessary
// memory and disk work.
func (h *ProxyHub) SetCaptureFilter(filter *traffic.FlowFilter) {
	h.filterMu.Lock()
	defer h.filterMu.Unlock()
	if filter == nil {
		h.filter = QueryOpts{}
		return
	}
	h.filter = QueryOpts{Host: filter.GetHost(), Status: filter.GetStatus(), CType: filter.GetType()}
}

func (h *ProxyHub) captureMatches(flow Flow) bool {
	h.filterMu.RLock()
	f := h.filter
	h.filterMu.RUnlock()
	if f.Host != "" && !strings.Contains(strings.ToLower(flow.Host), strings.ToLower(f.Host)) {
		return false
	}
	if f.Status != "" && (flow.Response == nil || !matchStatus(int(flow.Response.StatusCode), f.Status)) {
		return false
	}
	if f.CType != "" && !strings.Contains(strings.ToLower(flow.ContentType), strings.ToLower(f.CType)) {
		return false
	}
	return true
}

func (h *ProxyHub) captureHostAllowed(host string) bool {
	h.filterMu.RLock()
	hostFilter := h.filter.Host
	h.filterMu.RUnlock()
	return hostFilter == "" || strings.Contains(strings.ToLower(host), strings.ToLower(hostFilter))
}

func (h *ProxyHub) captureResponseAllowed(status int, contentType string) bool {
	h.filterMu.RLock()
	f := h.filter
	h.filterMu.RUnlock()
	return (f.Status == "" || matchStatus(status, f.Status)) &&
		(f.CType == "" || strings.Contains(strings.ToLower(contentType), strings.ToLower(f.CType)))
}

func (h *ProxyHub) start(caRootPath string) error {
	if caRootPath != "" {
		if err := os.MkdirAll(caRootPath, 0o755); err != nil {
			return fmt.Errorf("proxy hub: create CA dir: %w", err)
		}
		if h.store != nil && h.storage.BodyStorage == "disk" {
			if err := h.store.SetBodyDir(filepath.Join(caRootPath, "capture")); err != nil {
				return fmt.Errorf("proxy hub: create capture dir: %w", err)
			}
		}
	}
	server, err := mitmproxy.NewProxy(&mitmproxy.Options{
		Addr:              "127.0.0.1:0",
		SslInsecure:       true,
		StreamLargeBodies: hubStreamLargeBodies,
		CaRootPath:        caRootPath,
		Dialer:            h.dial,
	})
	if err != nil {
		return fmt.Errorf("proxy hub: create MITM proxy: %w", err)
	}
	// The addon is always installed; recording gates whether it stores/streams
	// (see ingest). HTTPS CONNECTs are MITM-decrypted only while capture and
	// decrypt are both on, so a relay-mode child that tunnels HTTPS is never
	// handed a forged certificate its env does not trust.
	server.AddAddon(&captureAddon{hub: h})
	server.SetAuthProxy(h.authorize)
	server.SetShouldInterceptRule(func(*http.Request) bool {
		return h.recording.Load() && h.decrypt.Load()
	})

	listenAddr, _, err := server.StartAsync()
	if err != nil {
		return fmt.Errorf("proxy hub: start MITM proxy: %w", err)
	}

	h.server = server
	h.addr = listenAddr.String()
	h.started = true

	// Export the CA up front so children can trust intercepted HTTPS whenever
	// capture is toggled on later. A failure only degrades HTTPS interception to
	// CONNECT metadata; it is not fatal to the proxy itself.
	if err := h.exportCA(server); err != nil {
		h.caPath = ""
	}
	return nil
}

// dial is the stable indirection: it reads the current egress chain from State
// on every connection, so `proxy switch/auto/clear` swaps the upstream live.
func (h *ProxyHub) dial(ctx context.Context, network, address string) (net.Conn, error) {
	if h.state == nil {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return h.state.CurrentDial()(ctx, network, address)
}

// exportCA writes the proxy's root CA to caPath in PEM so external tools can be
// pointed at it via CURL_CA_BUNDLE / SSL_CERT_FILE / NODE_EXTRA_CA_CERTS.
func (h *ProxyHub) exportCA(server *mitmproxy.Proxy) error {
	if h.caPath == "" {
		return nil
	}
	crt := server.GetCertificate()
	if len(crt.Raw) == 0 {
		return fmt.Errorf("proxy hub: empty root CA")
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: crt.Raw})
	if err := os.WriteFile(h.caPath, pemBytes, 0o644); err != nil {
		return fmt.Errorf("proxy hub: write CA: %w", err)
	}
	return nil
}

// ProxyURL is the stable http:// address injected into children and in-process
// clients. Empty until Start succeeds.
func (h *ProxyHub) ProxyURL() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.addr == "" {
		return ""
	}
	return "http://" + h.addr
}

// CAPath is the exported CA PEM path to advertise to children, or "" when the
// hub is not currently MITM-decrypting HTTPS. It returns a path only while
// capture and decrypt are both on: a child must trust the hub's CA exactly when
// the hub forges certificates for it, and must not when HTTPS is tunneled (a
// CA-only bundle would then fail to validate the real server certificate).
func (h *ProxyHub) CAPath() string {
	if !(h.recording.Load() && h.decrypt.Load()) {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.caPath
}

// Close stops the listener. Safe to call on a never-started hub. Cleanup
// keeps its ownership after a caller timeout, and a later call can wait again.
func (r *Resource) Close(ctx context.Context) error {
	if r == nil || r.ProxyHub == nil {
		return nil
	}
	h := r.ProxyHub
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
	}
	h.stopping.Store(true)
	h.shutdownOnce.Do(func() {
		go func() {
			err := h.shutdown(context.Background())
			h.mu.Lock()
			h.shutdownErr = err
			h.mu.Unlock()
			close(h.shutdownDone)
		}()
	})
	select {
	case <-h.shutdownDone:
	default:
		select {
		case <-h.shutdownDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	err := h.shutdownErr
	h.shutdownErr = nil
	return err
}

// Blocking filesystem calls retain their cleanup owner after the caller's
// deadline. Neither the caller nor a late MITM callback waits for file I/O.
func (h *ProxyHub) shutdown(ctx context.Context) error {
	h.mu.Lock()
	server := h.server
	h.server = nil
	h.mu.Unlock()
	var err error
	if server != nil {
		err = server.Shutdown(ctx)
	}
	h.correlationMu.Lock()
	clear(h.correlations)
	h.correlationMu.Unlock()
	if h.store != nil {
		err = errors.Join(err, h.store.closeContext(ctx))
	}
	return err
}
