package webagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// hubScript simulates the hub dialect: register→connected handshake, a
// structured Command, file.read, and tool.data correlation.
var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

type hubScript struct {
	t *testing.T

	mu         sync.Mutex
	registered chan webproto.RegisterPayload
	toolResult chan webproto.CommandResultPayload
	progress   chan string
	fileData   chan []byte
	toolData   chan webproto.Message
}

func newHubScript(t *testing.T) *hubScript {
	return &hubScript{
		t:          t,
		registered: make(chan webproto.RegisterPayload, 1),
		toolResult: make(chan webproto.CommandResultPayload, 1),
		progress:   make(chan string, 16),
		fileData:   make(chan []byte, 1),
		toolData:   make(chan webproto.Message, 4),
	}
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

	var hello webproto.Message
	if err := conn.ReadJSON(&hello); err != nil || hello.Type != "register" {
		h.t.Errorf("expected register, got %q (err=%v)", hello.Type, err)
		return
	}
	var reg webproto.RegisterPayload
	if err := json.Unmarshal(hello.Payload, &reg); err != nil {
		h.t.Errorf("register payload: %v", err)
		return
	}
	h.registered <- reg
	if err := conn.WriteJSON(webproto.Message{Type: "connected"}); err != nil {
		return
	}
	go h.drive(conn)

	for {
		var msg webproto.Message
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case webproto.TypeCommandResult:
			var result webproto.CommandResultPayload
			if err := json.Unmarshal(msg.Payload, &result); err != nil {
				h.t.Errorf("command.result: %v", err)
				return
			}
			h.toolResult <- result
		case "complete":
			if strings.HasPrefix(msg.TaskID, "read-") {
				data, err := base64.StdEncoding.DecodeString(msg.DataB64)
				if err != nil {
					h.t.Errorf("file data: %v", err)
					return
				}
				h.fileData <- data
			}
		case "tool.data":
			var event output.ToolDataEvent
			if err := json.Unmarshal(msg.Payload, &event); err == nil && event.Kind == output.ToolDataProgress {
				if line, ok := event.Data.(string); ok {
					h.progress <- line
					continue
				}
			}
			h.toolData <- msg
		}
	}
}

// drive issues the server→runner calls once the connection is live.
func (h *hubScript) drive(conn *websocket.Conn) {
	call := aop.ToolCallData{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Args:       map[string]any{"command": "echo hello"},
	}
	payload, _ := json.Marshal(webproto.CommandPayload{SessionID: "exec-1", ToolCall: &call})
	if err := conn.WriteJSON(webproto.Message{Type: webproto.TypeCommand, TaskID: "exec-1", Payload: payload}); err != nil {
		return
	}
}

func (h *hubScript) driveFileRead(conn *websocket.Conn, path string) {
	payload, _ := json.Marshal(webproto.FileRPCPayload{Path: path})
	_ = conn.WriteJSON(webproto.Message{Type: "file.read", TaskID: "read-1", Payload: payload})
}

func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// TestRunToolNodeWireInterop runs a tool node against a mock hub and verifies
// the full register / AOP tool.call / file.read / tool.data round trip.
func TestRunToolNodeWireInterop(t *testing.T) {
	reg := commands.NewRegistry()
	reg.RegisterTool(&recordingBash{})
	dataBus := eventbus.New[output.ToolDataEvent]()

	hub := newHubScript(t)
	server := httptest.NewServer(http.HandlerFunc(hub.serveHTTP))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunToolNode(ctx, ToolNodeConfig{
			ServerURL: server.URL,
			WSPath:    "/ws/runner",
			ID:        "runner-1",
			Token:     "test-token",
			Registry:  reg,
			DataBus:   dataBus,
			Version:   "test",
		})
	}()

	registered := wait(t, hub.registered, "register")
	if registered.Name != "runner-1" || registered.Node.ID != "runner-1" {
		t.Fatalf("register identity = %+v node=%+v", registered.Name, registered.Node)
	}
	if registered.Runtime.OS == "" {
		t.Fatalf("register runtime missing OS: %+v", registered.Runtime)
	}
	capabilities := map[string]bool{}
	for _, capability := range registered.Runtime.Capabilities {
		capabilities[capability] = true
	}
	if !capabilities["file.list"] || !capabilities["file.mkdir"] {
		t.Fatalf("runner runtime missing native file capabilities: %+v", registered.Runtime.Capabilities)
	}
	if len(registered.Tools) != 1 || registered.Tools[0].Function.Name != "bash" {
		t.Fatalf("register tools = %+v", registered.Tools)
	}

	// The hub issues a structured Command once the runner's first post-handshake
	// message arrives; recordingBash streams one progress line and returns.
	line := wait(t, hub.progress, "tool.data progress")
	if line != "streamed" {
		t.Fatalf("progress line = %q", line)
	}
	result := wait(t, hub.toolResult, "command.result")
	if isError, _ := result.Metadata["is_error"].(bool); isError || result.Metadata["tool_call_id"] != "call-1" || result.Metadata["tool_name"] != "bash" {
		t.Fatalf("command.result = %+v", result)
	}

	// tool.data rides the same connection, correlated by call ID.
	dataBus.Emit(output.ToolDataEvent{Tool: "gogo", Kind: "service", CallID: "exec-1"})
	toolMsg := wait(t, hub.toolData, "tool.data")
	if toolMsg.TaskID != "exec-1" {
		t.Fatalf("tool.data task id = %q", toolMsg.TaskID)
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("tool node did not stop after cancel")
	}
}

// TestRunToolNodeFileRead verifies the file.read path against a real file.
func TestRunToolNodeFileRead(t *testing.T) {
	reg := commands.NewRegistry()
	reg.RegisterTool(&recordingBash{})

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
		var hello webproto.Message
		if err := conn.ReadJSON(&hello); err != nil || hello.Type != "register" {
			return
		}
		hub.registered <- webproto.RegisterPayload{}
		if err := conn.WriteJSON(webproto.Message{Type: "connected"}); err != nil {
			return
		}
		hub.driveFileRead(conn, path)
		for {
			var msg webproto.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "complete" && strings.HasPrefix(msg.TaskID, "read-") {
				data, err := base64.StdEncoding.DecodeString(msg.DataB64)
				if err != nil {
					return
				}
				hub.fileData <- data
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = RunToolNode(ctx, ToolNodeConfig{
			ServerURL: server.URL, WSPath: "/ws/runner", ID: "runner-1",
			Registry: reg,
		})
	}()

	wait(t, hub.registered, "register")
	data := wait(t, hub.fileData, "file.read complete")
	if string(data) != "file-body" {
		t.Fatalf("file.read data = %q", data)
	}
}
