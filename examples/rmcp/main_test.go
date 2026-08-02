package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	coretool "github.com/chainreactors/aiscan/core/tool"
	webagent "github.com/chainreactors/aiscan/pkg/web/agent"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func readEnvelope(conn *websocket.Conn) (*aop.Envelope, protobuf.Message, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, nil, err
	}
	envelope := new(aop.Envelope)
	if err := protobuf.Unmarshal(data, envelope); err != nil {
		return nil, nil, err
	}
	message, err := aop.Unwrap(envelope)
	return envelope, message, err
}

func writeEnvelope(conn *websocket.Conn, envelope *aop.Envelope) error {
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

// TestToolNodeAgainstHub runs the example registry against a scripted hub:
// the hub inspects the hello, calls the echo tool, and verifies the result.
func TestToolNodeAgainstHub(t *testing.T) {
	registered := make(chan *aop.AgentHello, 1)
	toolResult := make(chan *aop.ToolResult, 1)

	hub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		first, message, err := readEnvelope(conn)
		core, ok := message.(*aop.ProtocolMessage)
		if err != nil || !ok || core.GetAgentHello() == nil {
			t.Errorf("expected hello: %v %v", message, err)
			return
		}
		registered <- core.GetAgentHello()
		if err := writeEnvelope(conn, aop.MustWrap("accepted", first.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "rmcp-1"}}})); err != nil {
			return
		}
		arguments, _ := aop.JSONValue(map[string]any{"command": "echo ping"})
		_ = writeEnvelope(conn, aop.MustWrap("call-1", "", &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: &toolpb.Call{
			SessionId: "call-1", TurnId: "call-1", Call: &aop.ToolCall{Id: "call-1", Name: "bash", Arguments: arguments},
		}}}))
		for {
			_, message, err := readEnvelope(conn)
			if err != nil {
				return
			}
			if core, ok := message.(*aop.ProtocolMessage); ok {
				if result := core.GetEvent().GetToolResult(); result != nil {
					toolResult <- result
					return
				}
			}
		}
	})
	server := httptest.NewServer(hub)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- webagent.RunToolNode(ctx, webagent.ToolNodeConfig{
			ServerURL: server.URL, WSPath: "/ws/runner", ID: "rmcp-1", Token: "test-token",
			Registry: newRegistry(t.TempDir()), Version: "test",
		})
	}()

	var hello *aop.AgentHello
	select {
	case hello = <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hello")
	}
	tools := map[string]bool{}
	for _, def := range hello.Tools {
		tools[def.Name] = true
	}
	if !tools["bash"] {
		t.Fatalf("hello tools = %+v", hello.Tools)
	}

	select {
	case result := <-toolResult:
		if result.IsError || result.Name != "bash" || result.CallId != "call-1" {
			t.Fatalf("tool result = %+v", result)
		}
		if !strings.Contains(coretool.ResultText(result), "ping") {
			t.Fatalf("tool output = %q", coretool.ResultText(result))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bash result")
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("tool node did not stop")
	}
}
