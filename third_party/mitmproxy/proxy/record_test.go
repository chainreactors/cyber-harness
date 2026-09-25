package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func TestNewFlowRecord(t *testing.T) {
	f := newFlow()
	f.Request = &Request{
		Method: "POST",
		URL:    mustParseURL("https://example.com/api"),
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"key":"value"}`),
	}
	f.Response = &Response{
		StatusCode: 201,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       make([]byte, 8000),
	}
	f.ConnContext = &ConnContext{
		ClientConn: &ClientConn{Tls: true},
	}

	r := NewFlowRecord(f, 4096)

	if r.Method != "POST" {
		t.Fatalf("method: expected POST, got %s", r.Method)
	}
	if r.Host != "example.com" {
		t.Fatalf("host: expected example.com, got %s", r.Host)
	}
	if r.StatusCode != 201 {
		t.Fatalf("status: expected 201, got %d", r.StatusCode)
	}
	if !r.TLS {
		t.Fatal("expected TLS=true")
	}
	if len(r.ResponseBody) != 4096 {
		t.Fatalf("body snip: expected 4096, got %d", len(r.ResponseBody))
	}
}

func TestSnipBytes(t *testing.T) {
	if s := snipBytes(nil, 100); s != nil {
		t.Fatal("nil input should return nil")
	}
	if s := snipBytes([]byte("hello"), 0); len(s) != 5 {
		t.Fatalf("max=0 should keep all, got %d", len(s))
	}
	if s := snipBytes([]byte("hello world"), 5); len(s) != 5 {
		t.Fatalf("should truncate to 5, got %d", len(s))
	}
}

func TestNewFlowRecord_Integration(t *testing.T) {
	var mu sync.Mutex
	var captured []*FlowRecord

	addon := &captureAddon{
		onRecord: func(r *FlowRecord) {
			mu.Lock()
			captured = append(captured, r)
			mu.Unlock()
		},
	}

	p, err := NewProxy(&Options{Addr: "127.0.0.1:0", SslInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	p.AddAddon(addon)
	addr, _, err := p.StartAsync()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(context.Background())

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		fmt.Fprint(w, "hello")
	}))
	defer target.Close()

	proxyURL, _ := url.Parse("http://" + addr.String())
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Get(target.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("expected 1, got %d", len(captured))
	}
	r := captured[0]
	if r.Method != "GET" || r.StatusCode != 200 || r.Duration <= 0 {
		t.Fatalf("unexpected record: method=%s status=%d dur=%v", r.Method, r.StatusCode, r.Duration)
	}
	if string(r.ResponseBody) != "hello" {
		t.Fatalf("body: %q", string(r.ResponseBody))
	}
}

// captureAddon is a minimal consumer-side addon using BaseAddon + NewFlowRecord.
type captureAddon struct {
	BaseAddon
	pending  sync.Map
	onRecord func(*FlowRecord)
}

func (a *captureAddon) Requestheaders(f *Flow) {
	a.pending.Store(f.Id.String(), time.Now())
}

func (a *captureAddon) Response(f *Flow) {
	var dur time.Duration
	if v, ok := a.pending.LoadAndDelete(f.Id.String()); ok {
		dur = time.Since(v.(time.Time))
	}
	r := NewFlowRecord(f, 0)
	r.Duration = dur
	a.onRecord(r)
}
