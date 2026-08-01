package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

type hubScript struct {
	t          *testing.T
	registered chan *transport.AgentHello
	toolResult chan *aop.ToolResult
	progress   chan string
	fileData   chan []byte
	toolData   chan *transport.ToolTelemetry
}

func newHubScript(t *testing.T) *hubScript {
	return &hubScript{t: t, registered: make(chan *transport.AgentHello, 1), toolResult: make(chan *aop.ToolResult, 1), progress: make(chan string, 16), fileData: make(chan []byte, 1), toolData: make(chan *transport.ToolTelemetry, 4)}
}

func readAgentFrame(conn *websocket.Conn) (*transport.AgentFrame, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	frame := new(transport.AgentFrame)
	if err := protojson.Unmarshal(data, frame); err != nil {
		return nil, err
	}
	return frame, nil
}
func writeServerFrame(conn *websocket.Conn, frame *transport.ServerFrame) error {
	data, err := protojson.Marshal(frame)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func (h *hubScript) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		h.t.Errorf("authorization = %q", got)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := testUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.t.Errorf("upgrade: %v", err)
		return
	}
	defer conn.Close()
	first, err := readAgentFrame(conn)
	if err != nil || first.GetHello() == nil {
		h.t.Errorf("expected hello: %v %v", first, err)
		return
	}
	h.registered <- first.GetHello()
	if err := writeServerFrame(conn, &transport.ServerFrame{Payload: &transport.ServerFrame_Accepted{Accepted: &transport.ConnectionAccepted{AgentId: "runner-1"}}}); err != nil {
		return
	}
	go h.drive(conn)
	for {
		frame, err := readAgentFrame(conn)
		if err != nil {
			return
		}
		switch payload := frame.Payload.(type) {
		case *transport.AgentFrame_Event:
			if result := payload.Event.GetToolResult(); result != nil {
				h.toolResult <- result
			}
		case *transport.AgentFrame_ToolTelemetry:
			telemetry := payload.ToolTelemetry
			if telemetry.Kind == output.ToolDataProgress {
				line, _ := aop.DecodeJSON[string](telemetry.Data)
				h.progress <- line
			} else {
				h.toolData <- telemetry
			}
		case *transport.AgentFrame_FileResult:
			h.fileData <- payload.FileResult.Data
		}
	}
}

func (h *hubScript) drive(conn *websocket.Conn) {
	arguments, _ := aop.JSONValue(map[string]any{"command": "echo hello"})
	_ = writeServerFrame(conn, &transport.ServerFrame{CorrelationId: "exec-1", Payload: &transport.ServerFrame_ToolCall{ToolCall: &transport.ToolCallRequest{TaskId: "exec-1", SessionId: "exec-1", TurnId: "exec-1", Call: &aop.ToolCall{Id: "exec-1", Name: "bash", Arguments: arguments}}}})
}
func (h *hubScript) driveFileRead(conn *websocket.Conn, path string) {
	_ = writeServerFrame(conn, &transport.ServerFrame{CorrelationId: "read-1", Payload: &transport.ServerFrame_FileRead{FileRead: &transport.FileReadRequest{TaskId: "read-1", Path: path}}})
}

func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestRunToolNodeWireInterop(t *testing.T) {
	registry := commands.NewRegistry()
	registry.RegisterTool(&recordingBash{})
	dataBus := eventbus.New[output.ToolDataEvent]()
	hub := newHubScript(t)
	server := httptest.NewServer(http.HandlerFunc(hub.serveHTTP))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunToolNode(ctx, ToolNodeConfig{ServerURL: server.URL, WSPath: "/ws/runner", ID: "runner-1", Token: "test-token", Registry: registry, DataBus: dataBus, Version: "test"})
	}()
	hello := wait(t, hub.registered, "hello")
	if hello.Name != "runner-1" || hello.AgentId != "runner-1" {
		t.Fatalf("hello identity = %+v", hello)
	}
	if hello.Runtime.Os == "" {
		t.Fatalf("runtime missing OS: %+v", hello.Runtime)
	}
	metadata, err := aop.DecodeJSON[map[string]any](hello.Runtime.Metadata)
	if err != nil || metadata["home"] == "" {
		t.Fatalf("runtime metadata = %+v, err=%v", metadata, err)
	}
	capabilities := map[string]bool{}
	for _, capability := range hello.Runtime.Capabilities {
		capabilities[capability] = true
	}
	if !capabilities["file.list"] || !capabilities["file.mkdir"] {
		t.Fatalf("capabilities = %+v", hello.Runtime.Capabilities)
	}
	if len(hello.Tools) != 1 || hello.Tools[0].Name != "bash" {
		t.Fatalf("tools = %+v", hello.Tools)
	}
	if line := wait(t, hub.progress, "tool progress"); line != "streamed" {
		t.Fatalf("progress = %q", line)
	}
	result := wait(t, hub.toolResult, "tool result")
	if result.IsError || result.CallId != "exec-1" || result.Name != "bash" {
		t.Fatalf("tool result = %+v", result)
	}
	dataBus.Emit(output.ToolDataEvent{Tool: "gogo", Kind: "service", CallID: "exec-1"})
	if telemetry := wait(t, hub.toolData, "tool telemetry"); telemetry.CallId != "exec-1" {
		t.Fatalf("telemetry = %+v", telemetry)
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("tool node did not stop")
	}
}

func TestRunToolNodeFileRead(t *testing.T) {
	registry := commands.NewRegistry()
	registry.RegisterTool(&recordingBash{})
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("file-body"), 0o644); err != nil {
		t.Fatal(err)
	}
	hub := newHubScript(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		first, err := readAgentFrame(conn)
		if err != nil || first.GetHello() == nil {
			return
		}
		hub.registered <- first.GetHello()
		if writeServerFrame(conn, &transport.ServerFrame{Payload: &transport.ServerFrame_Accepted{Accepted: &transport.ConnectionAccepted{AgentId: "runner-1"}}}) != nil {
			return
		}
		hub.driveFileRead(conn, path)
		for {
			frame, err := readAgentFrame(conn)
			if err != nil {
				return
			}
			if result := frame.GetFileResult(); result != nil {
				hub.fileData <- result.Data
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = RunToolNode(ctx, ToolNodeConfig{ServerURL: server.URL, WSPath: "/ws/runner", ID: "runner-1", Registry: registry})
	}()
	wait(t, hub.registered, "hello")
	if data := wait(t, hub.fileData, "file result"); string(data) != "file-body" {
		t.Fatalf("file data = %q", data)
	}
}
