package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/web"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := web.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	service, _, handler := newHeadlessHandler(store, nil, "test-token")
	t.Cleanup(service.Close)
	return httptest.NewServer(handler)
}

func TestHeadlessHealthAndAuth(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	// No static UI: the root path must not serve a page.
	resp, err = http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected no UI at /, got 200")
	}

	// The AOP WebSocket requires the access token.
	_, unauthResp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/aop/ws", nil)
	if err == nil {
		t.Fatal("expected dial without token to fail")
	}
	if unauthResp != nil && unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ws status = %d", unauthResp.StatusCode)
	}
}

// TestHeadlessOpenSessionRejected drives the real chat path: a browser-peer
// OpenSession against a headless server with no agent connected must be
// rejected UNAVAILABLE, proving store + dispatch + envelope plumbing.
func TestHeadlessOpenSessionRejected(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	header := http.Header{"Authorization": []string{"Bearer test-token"}}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/aop/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	request := aop.MustWrap("req-1", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{
		OpenSessionRequest: &aop.OpenSessionRequest{NodeId: "ghost"},
	}})
	data, err := protobuf.Marshal(request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, reply, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		envelope := new(aop.Envelope)
		if err := protobuf.Unmarshal(reply, envelope); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		message, err := aop.Unwrap(envelope)
		if err != nil {
			t.Fatalf("unwrap: %v", err)
		}
		core, ok := message.(*aop.ProtocolMessage)
		if !ok {
			continue
		}
		if perr := core.GetProtocolError(); perr != nil {
			t.Fatalf("protocol error %s: %s", perr.GetCode(), perr.GetMessage())
		}
		response := core.GetOpenSessionResponse()
		if response == nil {
			continue
		}
		rejected := response.GetRejected()
		if rejected == nil {
			t.Fatalf("expected rejection, got %+v", response.GetAccepted())
		}
		if rejected.GetCode() != "UNAVAILABLE" {
			t.Fatalf("rejection code = %q (%s)", rejected.GetCode(), rejected.GetMessage())
		}
		return
	}
}
