package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/gorilla/websocket"
)

func newEndpointTestServer(t *testing.T) (*httptest.Server, *Service) {
	t.Helper()
	service := NewService(ServiceConfig{})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	server := httptest.NewServer(NewHandler(service, nil, nil, ""))
	t.Cleanup(func() {
		server.Close()
		service.Close()
	})
	return server, service
}

func TestLegacyAOPWebSocketPathReturnsNotFound(t *testing.T) {
	server, _ := newEndpointTestServer(t)
	response, err := http.Get(server.URL + "/api/aop/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy endpoint status = %d, want 404", response.StatusCode)
	}
}

func TestApplicationEndpointRejectsAgentHello(t *testing.T) {
	server, _ := newEndpointTestServer(t)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ApplicationWebSocketPath
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	writeAgentEnvelope(t, conn, aop.MustWrap("hello-1", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentHello{AgentHello: &aop.AgentHello{NodeId: "node-1"}}}))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	message := unwrapEnvelope(t, readHubEnvelope(t, conn))
	core, ok := message.(*aop.ProtocolMessage)
	if !ok || core.GetProtocolError().GetCode() != "WRONG_ENDPOINT" {
		t.Fatalf("application AgentHello response = %+v", message)
	}
}

func TestNodeEndpointRejectsNonAgentHelloFirstFrame(t *testing.T) {
	server, _ := newEndpointTestServer(t)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + NodeWebSocketPath
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	writeAgentEnvelope(t, conn, aop.MustWrap("bad-1", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ListEventsRequest{ListEventsRequest: &aop.ListEventsRequest{SessionId: "session-1"}}}))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("node endpoint kept a non-AgentHello connection open")
	}
}
