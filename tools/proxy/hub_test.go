package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
)

// newTestHub starts a hub (capture or relay) with an isolated CA dir and returns
// it plus an HTTP client that routes through it and trusts its CA. Keep-alives
// are disabled so every request forces a fresh upstream dial — otherwise a
// pooled connection to the same host masks egress-chain changes.
func newTestHub(t *testing.T, capture bool) (*ProxyHub, *State, *http.Client) {
	t.Helper()
	state := NewState("")
	store := NewFlowStore(10000)
	caRoot := t.TempDir()
	hub := NewProxyHub(state, store, caRoot, capture)
	if err := hub.Start(caRoot); err != nil {
		t.Fatalf("hub start: %v", err)
	}
	t.Cleanup(func() { hub.Shutdown(context.Background()) })

	pool := x509.NewCertPool()
	if ca := hub.CAPath(); ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			t.Fatalf("read hub CA: %v", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			t.Fatal("hub CA not added to pool")
		}
	}
	proxyURL, err := url.Parse(hub.ProxyURL())
	if err != nil {
		t.Fatalf("parse hub url: %v", err)
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			TLSClientConfig:   &tls.Config{RootCAs: pool},
			DisableKeepAlives: true,
		},
	}
	return hub, state, client
}

// runMitm executes a mitm verb and returns its stdout.
func runMitm(t *testing.T, store *FlowStore, hub *ProxyHub, args ...string) string {
	t.Helper()
	cmd := NewMitmCommand(nil, store, hub)
	var out bytes.Buffer
	exec := &commands.Execution{Args: args, Stdout: &out, Stderr: &out}
	if _, err := cmd.Run(context.Background(), exec); err != nil {
		t.Fatalf("mitm %v: %v", args, err)
	}
	return out.String()
}

// ---------------------------------------------------------------------------
// Capture scenarios (mitm on)
// ---------------------------------------------------------------------------

func TestCaptureHTTPAndHTTPS(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>plain</html>")
	}))
	defer httpSrv.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer tlsSrv.Close()

	hub, _, client := newTestHub(t, true)

	// Plain HTTP — exercises the fork Options.Dialer patch path.
	if body := get(t, client, httpSrv.URL); !strings.Contains(body, "plain") {
		t.Fatalf("http body = %q", body)
	}
	// HTTPS — MITM decrypt with the client trusting the hub CA. Use a hostname
	// target: the hub forges a cert by CN, and a bare-IP CN yields no IP SAN,
	// which strict verifiers reject (a real MITM-of-IP-HTTPS limitation).
	if body := get(t, client, localhost(tlsSrv.URL)); !strings.Contains(body, `"ok":true`) {
		t.Fatalf("https body = %q", body)
	}

	flows := hub.Store().Query(QueryOpts{})
	if len(flows) < 2 {
		t.Fatalf("want >=2 flows, got %d", len(flows))
	}
	var sawHTTP, sawHTTPSDecoded bool
	for _, f := range flows {
		if !f.TLS && strings.Contains(string(f.ResponseBody), "plain") {
			sawHTTP = true
		}
		if f.TLS && strings.Contains(string(f.ResponseBody), `"ok":true`) {
			sawHTTPSDecoded = true // decrypted body proves real MITM
		}
	}
	if !sawHTTP {
		t.Error("plain HTTP flow with body not captured")
	}
	if !sawHTTPSDecoded {
		t.Error("HTTPS flow not decrypted/captured (MITM or CA trust failed)")
	}
}

func TestCapturePostRequestBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(201)
	}))
	defer srv.Close()
	hub, _, client := newTestHub(t, true)

	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"probe":"payload-marker"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	flows := hub.Store().Query(QueryOpts{})
	var found bool
	for _, f := range flows {
		if f.Method == "POST" && strings.Contains(string(f.RequestBody), "payload-marker") {
			found = true
			if f.StatusCode != 201 {
				t.Errorf("status = %d, want 201", f.StatusCode)
			}
		}
	}
	if !found {
		t.Error("POST request body not captured")
	}
}

func TestCaptureFiltersAndVerbs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	hub, _, client := newTestHub(t, true)

	for _, p := range []string{"/ok", "/missing", "/boom"} {
		resp, err := client.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("get %s: %v", p, err)
		}
		resp.Body.Close()
	}

	store := hub.Store()
	if got := len(store.Query(QueryOpts{Status: "404"})); got != 1 {
		t.Errorf("status 404 filter = %d, want 1", got)
	}
	if got := len(store.Query(QueryOpts{Status: "5xx"})); got != 1 {
		t.Errorf("status 5xx filter = %d, want 1", got)
	}
	if got := len(store.Query(QueryOpts{CType: "json"})); got != 1 {
		t.Errorf("content-type json filter = %d, want 1", got)
	}
	if got := len(store.Query(QueryOpts{Last: 2})); got != 2 {
		t.Errorf("last 2 = %d, want 2", got)
	}

	// Verbs: flows, flow <id>, analyze, clear.
	if out := runMitm(t, store, hub, "flows"); !strings.Contains(out, "flows") {
		t.Errorf("flows output = %q", out)
	}
	first := store.Query(QueryOpts{Last: 1})
	if len(first) == 1 {
		out := runMitm(t, store, hub, "flow", first[0].Exchange.ID)
		if !strings.Contains(out, "Request Headers") {
			t.Errorf("flow detail missing headers: %q", out)
		}
	}
	if out := runMitm(t, store, hub, "analyze"); !strings.Contains(out, "Summary") {
		t.Errorf("analyze output = %q", out)
	}
	runMitm(t, store, hub, "clear")
	if store.Count() != 0 {
		t.Errorf("store not cleared: %d", store.Count())
	}
}

func TestCaptureLargeBodyIsSnipped(t *testing.T) {
	big := strings.Repeat("A", maxBodySnip*3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, big)
	}))
	defer srv.Close()
	hub, _, client := newTestHub(t, true)
	get(t, client, srv.URL)

	for _, f := range hub.Store().Query(QueryOpts{}) {
		if len(f.ResponseBody) > maxBodySnip {
			t.Fatalf("body snip = %d, want <= %d", len(f.ResponseBody), maxBodySnip)
		}
	}
}

func TestCaptureConcurrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	hub, _, client := newTestHub(t, true)

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, err := client.Get(srv.URL); err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	if got := hub.Store().Count(); got != n {
		t.Errorf("captured %d flows, want %d", got, n)
	}
}

func TestCaptureConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close() // now refused
	hub, _, client := newTestHub(t, true)

	resp, err := client.Get(deadURL)
	if err == nil {
		resp.Body.Close()
	}
	// The failed upstream is recorded as a flow carrying the error.
	var sawErr bool
	for _, f := range hub.Store().Query(QueryOpts{}) {
		if f.Error != "" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("connection error not captured as a flow")
	}
}

// ---------------------------------------------------------------------------
// Proxy mechanism
// ---------------------------------------------------------------------------

// TestChainTraversesUpstreamProxy proves tool → hub → upstream proxy → target:
// a counting CONNECT proxy set as the egress upstream must see the connection.
func TestChainTraversesUpstreamProxy(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "reached-via-chain")
	}))
	defer target.Close()

	upstreamAddr, connects := startCountingConnectProxy(t)

	_, state, client := newTestHub(t, true)
	restore, err := state.WithOverrideDial("http://" + upstreamAddr)
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	defer restore()

	body := get(t, client, localhost(target.URL))
	if !strings.Contains(body, "reached-via-chain") {
		t.Fatalf("target not reached through chain: %q", body)
	}
	if atomic.LoadInt32(connects) == 0 {
		t.Fatal("upstream proxy was not traversed (egress chain not applied)")
	}
}

// TestFailClosedNoDirectLeak proves an unreachable egress upstream causes the
// request to fail rather than silently leaking a direct connection.
func TestFailClosedNoDirectLeak(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should-not-be-reached")
	}))
	defer target.Close()

	_, state, client := newTestHub(t, true)
	restore, err := state.WithOverrideDial("socks5://127.0.0.1:1") // nothing listening
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	defer restore()

	resp, err := client.Get(target.URL)
	if err == nil {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		t.Fatalf("request unexpectedly succeeded (leaked direct): %q", b)
	}
}

// TestRelayModeRoutesButDoesNotCapture proves capture=false forwards traffic
// (routing works) without intercepting/recording it.
func TestRelayModeRoutesButDoesNotCapture(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "relayed-ok")
	}))
	defer target.Close()

	hub, _, client := newTestHub(t, false) // relay mode
	if hub.Capturing() {
		t.Fatal("hub should not be capturing in relay mode")
	}
	if hub.CAPath() != "" {
		t.Error("relay mode should not export a CA")
	}
	body := get(t, client, target.URL)
	if !strings.Contains(body, "relayed-ok") {
		t.Fatalf("relay routing failed: %q", body)
	}
	// Plain HTTP has no addon in relay mode, so nothing is recorded.
	if got := hub.Store().Count(); got != 0 {
		t.Errorf("relay mode captured %d flows, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// localhost rewrites a 127.0.0.1 test URL to a hostname so the hub's forged
// leaf cert (CN-based, DNS SAN) verifies. Bare-IP targets have no IP SAN.
func localhost(u string) string { return strings.Replace(u, "127.0.0.1", "localhost", 1) }

func get(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b)
}

// startCountingConnectProxy is a minimal HTTP CONNECT proxy that counts tunnels
// and relays bytes, used to prove egress actually traverses the upstream.
func startCountingConnectProxy(t *testing.T) (addr string, count *int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var c int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConnect(conn, &c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), &c
}

func serveConnect(client net.Conn, count *int32) {
	defer client.Close()
	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	atomic.AddInt32(count, 1)
	server, err := net.DialTimeout("tcp", req.Host, 5*time.Second)
	if err != nil {
		io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer server.Close()
	io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go io.Copy(server, br)
	io.Copy(client, server)
}
