package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	p.AddAddon(&captureAddon{store: store})
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
	if store.Count() != 1 {
		t.Fatalf("expected 1 flow, got %d", store.Count())
	}
	f := store.Get(1)
	if f.StatusCode != 200 {
		t.Fatalf("captured flow status %d, want 200", f.StatusCode)
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
	go func() {
		for {
			conn, err := tcpServer.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("SSH-2.0-TestServer\r\n"))
			buf := make([]byte, 256)
			conn.Read(buf)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", tcpServer.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	n, err := io.ReadAtLeast(conn, buf, 3)
	if err != nil {
		t.Fatalf("expected SSH banner data, got error: %v", err)
	}
	banner := string(buf[:n])
	if !strings.Contains(banner, "SSH-") && !strings.Contains(banner, "SH-") {
		t.Fatalf("expected SSH banner fragment, got %q", banner)
	}
	if store.Count() != 0 {
		t.Fatalf("server-first protocol should not capture flows, got %d", store.Count())
	}
	t.Logf("server-first fallback OK: received %q, 0 flows captured", string(buf[:n]))
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
	p.AddAddon(&captureAddon{store: store})
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
	p.AddAddon(&captureAddon{store: store})
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
	f := Flow{Method: "GET", URL: "http://example.com/", StatusCode: 200, Host: "example.com"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Add(f)
	}
}

func BenchmarkFlowStore_Query(b *testing.B) {
	store := NewFlowStore(10000)
	for i := 0; i < 10000; i++ {
		store.Add(Flow{
			Method:     "GET",
			URL:        fmt.Sprintf("http://host%d.com/path%d", i%10, i),
			StatusCode: 200 + (i % 5) * 100,
			Host:       fmt.Sprintf("host%d.com", i%10),
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
			Method:           "GET",
			URL:              fmt.Sprintf("http://example.com/path/%d", i),
			StatusCode:       200,
			Host:             "example.com",
			ContentType:      "text/html",
			RequestHeaders:   http.Header{"User-Agent": {"test"}},
			ResponseHeaders:  http.Header{"Content-Type": {"text/html"}},
			ResponseBodySnip: make([]byte, 4096),
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
