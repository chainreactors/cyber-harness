package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

type lifecycleAddon struct {
	BaseAddon
	response *Response
	finished []*Flow
	stream   bool
	ended    chan *Flow
}

func (a *lifecycleAddon) Requestheaders(f *Flow) { f.Response = a.response; f.Stream = a.stream }
func (a *lifecycleAddon) FlowFinished(f *Flow) {
	if a.ended != nil {
		a.ended <- f
		return
	}
	a.finished = append(a.finished, f)
}

func TestRawStreamingSSEKeepsNoHistoryAndPreservesKeepAlive(t *testing.T) {
	const count = 20
	body := strings.Repeat("data: payload\n\n", 128)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer target.Close()
	addon := &lifecycleAddon{stream: true, ended: make(chan *Flow, count)}
	p, err := NewProxy(&Options{Addr: "127.0.0.1:0", CaRootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	p.AddAddon(addon)
	addr, _, err := p.StartAsync()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(context.Background())
	u, err := url.Parse("http://" + addr.String())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(u)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for i := 0; i < count; i++ {
		resp, err := client.Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || string(got) != body {
			t.Fatalf("request %d: status=%d body=%d err=%v", i, resp.StatusCode, len(got), err)
		}
	}
	for i := 0; i < count; i++ {
		select {
		case f := <-addon.ended:
			<-f.Done()
			if f.Error != nil || f.SSE != nil || len(f.Response.Body) != 0 {
				t.Fatalf("retained raw stream or failed flow: %v", f.Error)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing terminal hook")
		}
	}
}

func TestFlowFinishedIncludesStreamReadError(t *testing.T) {
	want := errors.New("body read failed")
	addon := &lifecycleAddon{response: &Response{
		StatusCode: 200, BodyReader: io.MultiReader(strings.NewReader("prefix"), iotest.ErrReader(want)),
	}}
	p := &Proxy{Addons: []Addon{addon}}
	conn := &ConnContext{proxy: p, ClientConn: &ClientConn{}}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	req = req.WithContext(context.WithValue(req.Context(), connContextKey, conn))
	response := httptest.NewRecorder()
	(&interceptor{proxy: p}).attack(response, req)
	if response.Body.String() != "prefix" || len(addon.finished) != 1 {
		t.Fatalf("body=%q finished=%d", response.Body.String(), len(addon.finished))
	}
	f := addon.finished[0]
	if !errors.Is(f.Error, want) || f.EndTime.IsZero() {
		t.Fatalf("flow error=%v end=%v", f.Error, f.EndTime)
	}
	select {
	case <-f.Done():
	default:
		t.Fatal("Done not closed")
	}
	f.finish()
	if len(addon.finished) != 1 {
		t.Fatal("finished more than once")
	}
}

type failingResponseWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w failingResponseWriter) Write([]byte) (int, error) { return 0, w.err }

func TestFlowFinishedIncludesClientWriteError(t *testing.T) {
	want := errors.New("client disconnected")
	addon := &lifecycleAddon{response: &Response{StatusCode: 200, BodyReader: strings.NewReader("body")}}
	p := &Proxy{Addons: []Addon{addon}}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	req = req.WithContext(context.WithValue(req.Context(), connContextKey, &ConnContext{proxy: p, ClientConn: &ClientConn{}}))
	(&interceptor{proxy: p}).attack(failingResponseWriter{httptest.NewRecorder(), want}, req)
	if len(addon.finished) != 1 || !errors.Is(addon.finished[0].Error, want) {
		t.Fatalf("finished=%v", addon.finished)
	}
}

func TestUpstreamClosePreservesHTTPClientReadSide(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	upstream, upstreamPeer := net.Pipe()
	defer upstreamPeer.Close()
	p := &Proxy{}
	conn := &ConnContext{proxy: p, ClientConn: &ClientConn{Conn: client}}
	server := &wrapServerConn{Conn: upstream, proxy: p, connCtx: conn}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.WriteString(peer, "next") }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(client, buf); err != nil || string(buf) != "next" {
		t.Fatalf("read=%q err=%v", buf, err)
	}
}
