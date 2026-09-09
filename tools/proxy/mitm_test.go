package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"github.com/chainreactors/proxyclient"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
)

// startTestTarget creates a local HTTP server that returns a fixed response.
func startTestTarget(bodySize int) *httptest.Server {
	body := make([]byte, bodySize)
	for i := range body {
		body[i] = 'A'
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write(body)
	}))
}

func TestLargeResponseIsStreamedToBodyFileAndHydratedOnGet(t *testing.T) {
	body := strings.Repeat("streamed-body-", 32*1024)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, body)
	}))
	defer target.Close()
	hub := startHub(t, true)

	resp, err := hubClient(t, hub, "large-body").Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	deadline := time.Now().Add(3 * time.Second)
	var flows []Flow
	for time.Now().Before(deadline) {
		flows = hub.Store().Query(QueryOpts{})
		if len(flows) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(flows) != 1 {
		t.Fatalf("captured flows = %d, want 1", len(flows))
	}
	if got := len(flows[0].Response.Body); got > maxBodySnip {
		t.Fatalf("preview size = %d, want <= %d", got, maxBodySnip)
	}
	id, err := strconv.Atoi(flows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	full := hub.Store().Get(id)
	if full == nil || full.Response == nil {
		t.Fatal("hydrated flow missing response")
	}
	if got := string(full.Response.Body); got != body {
		t.Fatalf("hydrated body length/content mismatch: got %d want %d", len(got), len(body))
	}
	wire := hub.Store().flowToProto(&flows[0])
	if got := string(wire.GetResponse().GetBody()); got != body {
		t.Fatalf("wire body length/content mismatch: got %d want %d", len(got), len(body))
	}
}

func TestLargeResponseBodyIsCappedOnDisk(t *testing.T) {
	target := startTestTarget(int(maxBodyCaptureBytes) + 1024)
	defer target.Close()
	hub := startHub(t, true)
	getThrough(t, hubClient(t, hub, "body-cap"), target.URL)
	flows := waitForFlows(t, hub.Store(), 1)
	if len(flows) != 1 || flows[0].Response == nil {
		t.Fatalf("captured flow missing response: %#v", flows)
	}
	stored := hub.Store().files[flows[0].ID][1]
	if stored < 0 || stored > maxBodyCaptureBytes {
		t.Fatalf("stored bytes = %d", stored)
	}
	info, err := os.Stat(hub.Store().bodyPath(flows[0].ID, 1))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxBodyCaptureBytes {
		t.Fatalf("body file size = %d, want <= %d", info.Size(), maxBodyCaptureBytes)
	}
	if !strings.Contains(flows[0].Error, "response body truncated") {
		t.Fatalf("flow error = %q, want truncation notice", flows[0].Error)
	}
}

func TestCaptureFilterRunsBeforeStore(t *testing.T) {
	target := startTestTarget(32)
	defer target.Close()
	hub := startHub(t, true)
	hub.SetCaptureFilter(&traffic.FlowFilter{Status: "404"})
	resp, err := hubClient(t, hub, "filter").Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	time.Sleep(100 * time.Millisecond)
	if got := hub.Store().Count(); got != 0 {
		t.Fatalf("filtered flow count = %d, want 0", got)
	}
}

func TestFlowStoreEvictionRemovesBodyFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewFlowStore(1)
	if err := store.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	writeBody := func(name string) string {
		t.Helper()
		path := filepath.Join(dir, "body", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	firstPath := writeBody("first.resp")
	addTestBody(t, store, Flow{Exchange: traffic.Exchange{
		Request:  traffic.Request{Method: "GET", URL: "https://example.test/"},
		Response: &traffic.Response{StatusCode: 200},
	}}, firstPath)
	secondPath := writeBody("second.resp")
	addTestBody(t, store, Flow{Exchange: traffic.Exchange{
		Request:  traffic.Request{Method: "GET", URL: "https://example.test/2"},
		Response: &traffic.Response{StatusCode: 200},
	}}, secondPath)

	if _, err := os.Stat(store.bodyPath("1", 1)); !os.IsNotExist(err) {
		t.Fatalf("evicted body still exists: stat err = %v", err)
	}
	if _, err := os.Stat(store.bodyPath("2", 1)); err != nil {
		t.Fatalf("retained body was removed: %v", err)
	}
}

func TestFlowStoreBodyBudgetEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	store := NewFlowStoreWithLimits(8, 10)
	if err := store.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	write := func(name string, size int64) string {
		t.Helper()
		path := filepath.Join(dir, "body", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	firstPath := write("budget-first.resp", 6)
	addTestBody(t, store, Flow{Exchange: traffic.Exchange{
		Request:  traffic.Request{Method: "GET", URL: "https://example.test/1"},
		Response: &traffic.Response{StatusCode: 200},
	}}, firstPath)
	secondPath := write("budget-second.resp", 6)
	addTestBody(t, store, Flow{Exchange: traffic.Exchange{
		Request:  traffic.Request{Method: "GET", URL: "https://example.test/2"},
		Response: &traffic.Response{StatusCode: 200},
	}}, secondPath)

	if got := store.Count(); got != 1 {
		t.Fatalf("budget-retained flow count = %d, want 1", got)
	}
	if got := store.bodyBytes; got > 10 {
		t.Fatalf("retained body bytes = %d, want <= 10", got)
	}
	if _, err := os.Stat(store.bodyPath("1", 1)); !os.IsNotExist(err) {
		t.Fatalf("budget-evicted body still exists: stat err = %v", err)
	}
	if _, err := os.Stat(store.bodyPath("2", 1)); err != nil {
		t.Fatalf("budget-retained body missing: %v", err)
	}
}

func TestFlowStoreStartupPrunesUnreferencedBodies(t *testing.T) {
	dir := t.TempDir()
	first := NewFlowStoreWithLimits(4, 1024)
	if err := first.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "body", "live.resp")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	addTestBody(t, first, Flow{Exchange: traffic.Exchange{
		Request:  traffic.Request{Method: "GET", URL: "https://example.test/"},
		Response: &traffic.Response{StatusCode: 200},
	}}, path)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "body", "capture-orphan")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanFinal := filepath.Join(dir, "body", "999.resp")
	if err := os.WriteFile(orphanFinal, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := NewFlowStoreWithLimits(4, 1024)
	if err := second.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("startup orphan still exists: stat err = %v", err)
	}
	if _, err := os.Stat(orphanFinal); !os.IsNotExist(err) {
		t.Fatalf("startup orphan final still exists: stat err = %v", err)
	}
	if _, err := os.Stat(second.bodyPath("1", 1)); err != nil {
		t.Fatalf("startup pruner removed live body: %v", err)
	}
}

func TestCaptureReportsBodyTruncation(t *testing.T) {
	dir := t.TempDir()
	store := NewFlowStoreWithLimits(4, 1024)
	if err := store.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hub := NewProxyHub(nil, store, "", true)
	hub.storage.BodyMaxBytes = 4
	state := &captureState{
		hub: hub,
		flow: Flow{Exchange: traffic.Exchange{
			Request:  traffic.Request{Method: "GET", URL: "https://example.test/"},
			Response: &traffic.Response{StatusCode: 200},
		}},
	}
	_, _ = io.Copy(io.Discard, state.bodyReader(strings.NewReader("abcdefgh"), "resp"))
	state.finish(nil)
	flows := waitForFlows(t, store, 1)
	if len(flows) != 1 {
		t.Fatalf("captured flows = %d, want 1", len(flows))
	}
	if !strings.Contains(flows[0].Error, "response body truncated") {
		t.Fatalf("flow error = %q, want truncation notice", flows[0].Error)
	}
	wire := store.flowToProto(&flows[0])
	if !strings.Contains(wire.GetError(), "response body truncated") {
		t.Fatalf("wire error = %q, want truncation notice", wire.GetError())
	}
}

func TestIngestRejectRemovesBodyFiles(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*ProxyHub)
		file  string
	}{
		{name: "recording disabled", setup: func(h *ProxyHub) { h.SetCapture(false, false) }, file: "disabled.resp"},
		{name: "capture filter", setup: func(h *ProxyHub) { h.SetCaptureFilter(&traffic.FlowFilter{Host: "keep.test"}) }, file: "filtered.resp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := NewFlowStore(4)
			if err := store.SetBodyDir(dir); err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			hub := NewProxyHub(nil, store, "", true)
			tc.setup(hub)
			path := filepath.Join(dir, "body", tc.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("discard me"), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			_ = file.Close()
			hub.ingestFiles(Flow{Host: "drop.test", Exchange: traffic.Exchange{
				Request:  traffic.Request{Method: "GET", URL: "https://drop.test/"},
				Response: &traffic.Response{StatusCode: 200},
			}}, [2]*os.File{nil, file})
			if got := store.Count(); got != 0 {
				t.Fatalf("rejected flow count = %d, want 0", got)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("rejected body still exists: stat err = %v", err)
			}
		})
	}
}

// newCapturingHub wraps a store in a recording ProxyHub so a bare captureAddon
// can route flows through hub.ingest in tests without starting the hub's own
// listener (the test attaches the addon to its own proxy).
func newCapturingHub(store *FlowStore) *ProxyHub {
	return NewProxyHub(nil, store, "", true)
}

// startMITMProxy creates a MITM proxy with a captureAddon and returns its address.
func startMITMProxy(t *testing.T) (*mitmproxy.Proxy, *FlowStore, string) {
	t.Helper()
	store := NewFlowStore(100000)
	p, err := mitmproxy.NewProxy(&mitmproxy.Options{
		Addr:              "127.0.0.1:0",
		SslInsecure:       true,
		StreamLargeBodies: 10 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.AddAddon(&captureAddon{hub: newCapturingHub(store)})
	addr, _, err := p.StartAsync()
	if err != nil {
		t.Fatal(err)
	}
	return p, store, addr.String()
}

// === Correctness Tests ===

func TestMITMCapture_HTTP(t *testing.T) {
	target := startTestTarget(128)
	defer target.Close()

	p, store, mitmAddr := startMITMProxy(t)
	defer p.Shutdown(context.Background())

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseProxyURL("http://" + mitmAddr)),
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(target.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(waitForFlows(t, store, 1)) != 1 {
		t.Fatalf("expected 1 flow, got %d", store.Count())
	}
	f := store.Get(1)
	if f.Response.StatusCode != 200 {
		t.Fatalf("captured flow status %d, want 200", f.Response.StatusCode)
	}
}

func TestMITMCapture_CONNECT(t *testing.T) {
	target := startTestTarget(128)
	defer target.Close()

	p, store, mitmAddr := startMITMProxy(t)
	defer p.Shutdown(context.Background())

	proxyURL := mustParseProxyURL("http://" + mitmAddr)
	dial, err := proxyclient.NewClient(proxyURL)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{
		Transport: &http.Transport{DialContext: dial.DialContext},
		Timeout:   5 * time.Second,
	}

	for i := 0; i < 5; i++ {
		resp, err := client.Get(target.URL + fmt.Sprintf("/path%d", i))
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: got %d", i, resp.StatusCode)
		}
	}

	time.Sleep(100 * time.Millisecond)
	if store.Count() < 1 {
		t.Fatalf("expected at least 1 flow, got %d", store.Count())
	}
	t.Logf("captured %d/%d flows via CONNECT tunnel", store.Count(), 5)
}

func TestMITMCapture_NonHTTP_Fallback(t *testing.T) {
	// Start a TCP server where the CLIENT sends first (not server-first like SSH).
	// Server echoes back whatever it receives — this tests the raw transfer fallback.
	tcpServer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpServer.Close()
	go func() {
		for {
			conn, err := tcpServer.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 256)
			n, _ := conn.Read(buf)
			if n > 0 {
				conn.Write(buf[:n])
			}
			conn.Close()
		}
	}()

	p, store, mitmAddr := startMITMProxy(t)
	defer p.Shutdown(context.Background())

	proxyURL := mustParseProxyURL("http://" + mitmAddr)
	dial, err := proxyclient.NewClient(proxyURL)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", tcpServer.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Send non-HTTP data (binary) — should trigger transfer fallback
	conn.Write([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07})
	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _ := conn.Read(buf)
	conn.Close()

	if n < 8 {
		t.Fatalf("expected echo of 8 bytes through tunnel, got %d", n)
	}
	if store.Count() != 0 {
		t.Fatalf("non-HTTP traffic should not capture flows, got %d", store.Count())
	}
}

func TestMITMCapture_ServerFirst_Fallback(t *testing.T) {
	// Server-first protocol (like SSH): server sends banner, client waits.
	// MITM should timeout on Peek and fallback to raw transfer.
	tcpServer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpServer.Close()
	serverReady := make(chan error, 1)
	go func() {
		conn, err := tcpServer.Accept()
		if err != nil {
			serverReady <- err
			return
		}
		defer conn.Close()
		_, err = conn.Write([]byte("SSH-2.0-TestServer\r\n"))
		serverReady <- err
		if err == nil {
			buf := make([]byte, 256)
			_, _ = conn.Read(buf)
		}
	}()

	p, store, mitmAddr := startMITMProxy(t)
	defer p.Shutdown(context.Background())

	proxyURL := mustParseProxyURL("http://" + mitmAddr)
	dial, err := proxyclient.NewClient(proxyURL)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", tcpServer.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case err := <-serverReady:
		if err != nil {
			t.Fatalf("server banner write: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("server did not accept proxy connection: %v", ctx.Err())
	}

	buf := make([]byte, 64)
	type readResult struct {
		n   int
		err error
	}
	readDone := make(chan readResult, 1)
	go func() {
		n, err := io.ReadAtLeast(conn, buf, 3)
		readDone <- readResult{n: n, err: err}
	}()
	var read readResult
	select {
	case read = <-readDone:
	case <-ctx.Done():
		t.Fatalf("proxy did not enter raw fallback after synchronized banner write: %v", ctx.Err())
	}
	if read.err != nil {
		t.Fatalf("expected SSH banner data, got error: %v", read.err)
	}
	banner := string(buf[:read.n])
	if !strings.Contains(banner, "SSH-") && !strings.Contains(banner, "SH-") {
		t.Fatalf("expected SSH banner fragment, got %q", banner)
	}
	if store.Count() != 0 {
		t.Fatalf("server-first protocol should not capture flows, got %d", store.Count())
	}
	t.Logf("server-first fallback OK: received %q, 0 flows captured", string(buf[:read.n]))
}

// === Latency Benchmark ===

func BenchmarkDirect(b *testing.B) {
	target := startTestTarget(1024)
	defer target.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(target.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
}

func BenchmarkMITM_HTTPProxy(b *testing.B) {
	target := startTestTarget(1024)
	defer target.Close()

	store := NewFlowStore(b.N + 100)
	p, _ := mitmproxy.NewProxy(&mitmproxy.Options{Addr: "127.0.0.1:0", SslInsecure: true})
	p.AddAddon(&captureAddon{hub: newCapturingHub(store)})
	addr, _, _ := p.StartAsync()
	defer p.Shutdown(context.Background())

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseProxyURL("http://" + addr.String())),
		},
		Timeout: 5 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(target.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	b.ReportMetric(float64(store.Count()), "flows")
}

func BenchmarkMITM_CONNECT(b *testing.B) {
	target := startTestTarget(1024)
	defer target.Close()

	store := NewFlowStore(b.N + 100)
	p, _ := mitmproxy.NewProxy(&mitmproxy.Options{Addr: "127.0.0.1:0", SslInsecure: true})
	p.AddAddon(&captureAddon{hub: newCapturingHub(store)})
	addr, _, _ := p.StartAsync()
	defer p.Shutdown(context.Background())

	proxyURL := mustParseProxyURL("http://" + addr.String())
	dial, _ := proxyclient.NewClient(proxyURL)
	client := &http.Client{
		Transport: &http.Transport{DialContext: dial.DialContext},
		Timeout:   5 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(target.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	b.ReportMetric(float64(store.Count()), "flows")
}

// === Throughput / Concurrency Test ===

func TestMITMThroughput(t *testing.T) {
	target := startTestTarget(512)
	defer target.Close()

	p, store, mitmAddr := startMITMProxy(t)
	defer p.Shutdown(context.Background())

	proxyURL := mustParseProxyURL("http://" + mitmAddr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConnsPerHost: 50,
		},
		Timeout: 10 * time.Second,
	}

	concurrency := 20
	totalRequests := 200
	duration := time.Duration(0)

	var wg sync.WaitGroup
	var success, fail atomic.Int64
	start := time.Now()

	for c := 0; c < concurrency; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < totalRequests/concurrency; i++ {
				resp, err := client.Get(target.URL + fmt.Sprintf("/%d", i))
				if err != nil {
					fail.Add(1)
					continue
				}
				io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					success.Add(1)
				} else {
					fail.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	duration = time.Since(start)

	rps := float64(success.Load()) / duration.Seconds()
	t.Logf("concurrency=%d total=%d success=%d fail=%d duration=%s rps=%.0f flows=%d",
		concurrency, totalRequests, success.Load(), fail.Load(), duration.Round(time.Millisecond), rps, store.Count())

	if success.Load() < int64(totalRequests)*80/100 {
		t.Errorf("too many failures: %d/%d", fail.Load(), totalRequests)
	}
}

// === FlowStore Benchmark ===

func BenchmarkFlowStore_Add(b *testing.B) {
	store := NewFlowStore(10000)
	f := Flow{
		Exchange: traffic.Exchange{
			Request:  traffic.Request{Method: "GET", URL: "http://example.com/"},
			Response: &traffic.Response{StatusCode: 200},
		},
		Host: "example.com",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Add(f)
	}
}

func BenchmarkFlowStore_Query(b *testing.B) {
	store := NewFlowStore(10000)
	for i := 0; i < 10000; i++ {
		store.Add(Flow{
			Exchange: traffic.Exchange{
				Request:  traffic.Request{Method: "GET", URL: fmt.Sprintf("http://host%d.com/path%d", i%10, i)},
				Response: &traffic.Response{StatusCode: 200 + (i%5)*100},
			},
			Host: fmt.Sprintf("host%d.com", i%10),
		})
	}
	opts := QueryOpts{Host: "host5", Status: "2xx", Last: 20}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Query(opts)
	}
}

// === Memory Test ===

func TestFlowStoreMemory(t *testing.T) {
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	store := NewFlowStore(10000)
	for i := 0; i < 10000; i++ {
		store.Add(Flow{
			Exchange: traffic.Exchange{
				Request: traffic.Request{
					Method:  "GET",
					URL:     fmt.Sprintf("http://example.com/path/%d", i),
					Headers: []traffic.Pair{{Name: "User-Agent", Value: "test"}},
				},
				Response: &traffic.Response{
					StatusCode: 200,
					Headers:    []traffic.Pair{{Name: "Content-Type", Value: "text/html"}},
					Body:       make([]byte, 4096),
				},
			},
			Host:        "example.com",
			ContentType: "text/html",
		})
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)
	allocMB := float64(m2.Alloc-m1.Alloc) / 1024 / 1024
	t.Logf("10000 flows (4KB body each): %.1f MB allocated, %d flows in store", allocMB, store.Count())
}

func mustParseProxyURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

// addTestBody transfers an actual closed file to the store, just like capture.
func addTestBody(t *testing.T, store *FlowStore, flow Flow, path string) Flow {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return store.addFiles(flow, [2]*os.File{nil, file})
}
