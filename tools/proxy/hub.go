package proxy

import (
	"context"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
)

// ProxyHub is the runner-level, long-lived MITM proxy that every tool routes
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
	state *State
	store *FlowStore

	mu        sync.Mutex
	server    *mitmproxy.Proxy
	addr      string
	caPath    string
	started   bool
	startErr  error
	startOnce sync.Once

	// Capture is runtime-mutable so the control plane can toggle it via the
	// traffic namespace without restarting the listener. recording gates whether
	// flows are stored and streamed; decrypt gates HTTPS MITM interception. Both
	// are read on every connection, so a change takes effect for subsequent
	// connections while in-flight children are undisturbed.
	recording atomic.Bool
	decrypt   atomic.Bool
	filterMu  sync.RWMutex
	filter    QueryOpts

	subsMu  sync.Mutex
	subs    map[int]*flowSubscriber
	nextSub int
}

// Keep proxy-side buffering bounded. Bodies at or above this threshold are
// captured through the recorder reader and written to disk incrementally.
const hubStreamLargeBodies = 64 * 1024

// NewProxyHub builds the hub around an existing State (egress source of truth)
// and FlowStore (capture sink). Both are owned by the caller so the mitm query
// verbs and the hub share one store.
//
// capture selects the mode. The hub is ALWAYS the routing substrate — tools
// route through it and `proxy switch` swaps its upstream live in either mode.
//   - capture=true  (mitm on):  intercept + record HTTPS (MITM) and HTTP flows.
//   - capture=false (mitm off): pure relay — no interception, no recording, no
//     CA needed. Routing still works; nothing is decrypted or stored.
//
// Start must be called before use.
func NewProxyHub(state *State, store *FlowStore, caRootPath string, capture bool) *ProxyHub {
	if store == nil {
		store = NewFlowStore(10000)
	}
	h := &ProxyHub{state: state, store: store, subs: make(map[int]*flowSubscriber)}
	// The CA path is always prepared so capture can be toggled on at runtime;
	// CAPath only advertises it to children while interception is actually on.
	h.caPath = filepath.Join(caRootPath, "mitmproxy-ca-cert.pem")
	h.recording.Store(capture)
	h.decrypt.Store(capture)
	return h
}

// Capturing reports whether the hub currently records traffic (mitm on) or only
// relays.
func (h *ProxyHub) Capturing() bool { return h.recording.Load() }

// SetCapture toggles capture at runtime without restarting the listener. record
// gates storing/streaming; decryptHTTPS gates HTTPS MITM interception, which
// only affects connections opened after the change because a child's CA trust
// is fixed at spawn time.
func (h *ProxyHub) SetCapture(record, decryptHTTPS bool) {
	h.recording.Store(record)
	h.decrypt.Store(decryptHTTPS)
}

// SetCaptureFilter applies the existing traffic FlowFilter before a flow is
// stored or published. It deliberately lives on the hub so filtering reduces
// both memory/disk work and subscriber traffic.
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
	if f.Status != "" && (flow.Response == nil || !matchStatus(flow.Response.StatusCode, f.Status)) {
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

// Start brings up the MITM listener on an ephemeral loopback port and exports
// the CA certificate so external processes can trust intercepted HTTPS. It is
// idempotent: repeated calls return the first outcome.
func (h *ProxyHub) Start(caRootPath string) error {
	h.startOnce.Do(func() {
		h.startErr = h.start(caRootPath)
	})
	return h.startErr
}

func (h *ProxyHub) start(caRootPath string) error {
	if caRootPath != "" {
		if err := os.MkdirAll(caRootPath, 0o755); err != nil {
			return fmt.Errorf("proxy hub: create CA dir: %w", err)
		}
		if h.store != nil {
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
	server.SetShouldInterceptRule(func(*http.Request) bool {
		return h.recording.Load() && h.decrypt.Load()
	})

	listenAddr, _, err := server.StartAsync()
	if err != nil {
		return fmt.Errorf("proxy hub: start MITM proxy: %w", err)
	}

	h.mu.Lock()
	h.server = server
	h.addr = listenAddr.String()
	h.started = true
	h.mu.Unlock()

	// Export the CA up front so children can trust intercepted HTTPS whenever
	// capture is toggled on later. A failure only degrades HTTPS interception to
	// CONNECT metadata; it is not fatal to the proxy itself.
	if err := h.exportCA(server); err != nil {
		h.mu.Lock()
		h.caPath = ""
		h.mu.Unlock()
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

// Shutdown stops the listener. Safe to call on a never-started hub.
func (h *ProxyHub) Shutdown(ctx context.Context) {
	h.closeSubscribers()
	h.mu.Lock()
	server := h.server
	h.server = nil
	h.mu.Unlock()
	if server == nil {
		if h.store != nil {
			_ = h.store.Close()
		}
		return
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
	}
	_ = server.Shutdown(ctx)
	if h.store != nil {
		_ = h.store.Close()
	}
}

func (h *ProxyHub) closeSubscribers() {
	h.subsMu.Lock()
	subs := h.subs
	h.subs = make(map[int]*flowSubscriber)
	for _, subscriber := range subs {
		close(subscriber.done)
	}
	h.subsMu.Unlock()
}
