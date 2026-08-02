package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

type recordingBash struct {
	command string
	options commands.BashExecOptions
}

func (*recordingBash) Name() string        { return "bash" }
func (*recordingBash) Description() string { return "test bash" }
func (*recordingBash) Definition() *tool.Definition {
	return tool.Def("bash", "test bash", struct {
		Command string `json:"command"`
	}{})
}
func (*recordingBash) Execute(context.Context, string) (*tool.Result, error) {
	return nil, nil
}
func (b *recordingBash) RunForegroundTool(_ context.Context, command string, options commands.BashExecOptions) (*tool.Result, error) {
	b.command = command
	b.options = options
	options.OnOutput([]byte("streamed\n"))
	result := tool.TextResult("streamed")
	return result, nil
}

type hubScript struct {
	t          *testing.T
	registered chan *aop.AgentHello
	catalog    chan *types.CommandCatalog
	toolResult chan *aop.ToolResult
	progress   chan string
	fileData   chan []byte
	toolData   chan *toolpb.Progress
	artifact   chan *toolpb.Artifact
}

func newHubScript(t *testing.T) *hubScript {
	return &hubScript{
		t: t, registered: make(chan *aop.AgentHello, 1), catalog: make(chan *types.CommandCatalog, 1), toolResult: make(chan *aop.ToolResult, 1),
		progress: make(chan string, 16), fileData: make(chan []byte, 1), toolData: make(chan *toolpb.Progress, 4), artifact: make(chan *toolpb.Artifact, 4),
	}
}

func readAgentEnvelope(conn *websocket.Conn, jsonMode bool) (*aop.Envelope, protobuf.Message, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, nil, err
	}
	envelope := new(aop.Envelope)
	if jsonMode {
		if err := protojson.Unmarshal(data, envelope); err != nil {
			return nil, nil, err
		}
	} else if err := protobuf.Unmarshal(data, envelope); err != nil {
		return nil, nil, err
	}
	message, err := aop.Unwrap(envelope)
	return envelope, message, err
}

func writeAgentEnvelope(conn *websocket.Conn, jsonMode bool, envelope *aop.Envelope) error {
	if jsonMode {
		data, err := protojson.Marshal(envelope)
		if err != nil {
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, data)
	}
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
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
	first, message, err := readAgentEnvelope(conn, false)
	core, ok := message.(*aop.ProtocolMessage)
	if err != nil || !ok || core.GetAgentHello() == nil {
		h.t.Errorf("expected hello: %v %v", message, err)
		return
	}
	h.registered <- core.GetAgentHello()
	if err := writeAgentEnvelope(conn, false, aop.MustWrap("accepted", first.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}})); err != nil {
		return
	}
	go h.drive(conn)
	for {
		_, message, err := readAgentEnvelope(conn, false)
		if err != nil {
			return
		}
		switch value := message.(type) {
		case *aop.ProtocolMessage:
			if result := value.GetEvent().GetToolResult(); result != nil {
				h.toolResult <- result
			}
		case *toolpb.ProtocolMessage:
			if progress := value.GetProgress(); progress != nil {
				h.progress <- progress.Text
			}
			if artifact := value.GetArtifact(); artifact != nil {
				h.artifact <- artifact
			}
		case *filepb.ProtocolMessage:
			if result := value.GetResult(); result != nil {
				h.fileData <- result.Data
			}
		case *types.CommandProtocolMessage:
			if catalog := value.GetCatalog(); catalog != nil {
				h.catalog <- catalog
			}
		}
	}
}

func (h *hubScript) drive(conn *websocket.Conn) {
	arguments, _ := aop.JSONValue(map[string]any{"command": "echo hello"})
	_ = writeAgentEnvelope(conn, false, aop.MustWrap("exec-1", "", &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: &toolpb.Call{
		SessionId: "exec-1", TurnId: "exec-1", Call: &aop.ToolCall{Id: "exec-1", Name: "bash", Arguments: arguments},
	}}}))
}

func (h *hubScript) driveFileRead(conn *websocket.Conn, path string) {
	_ = writeAgentEnvelope(conn, false, aop.MustWrap("read-1", "", &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_ReadRequest{ReadRequest: &filepb.ReadRequest{Path: path}}}))
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
	registry.Register(commands.Command{
		Name: "gogo", Usage: "gogo [OPTIONS]",
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/gogo.md",
	}, "scanner")
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
	if hello.Name != "runner-1" || hello.NodeId != "runner-1" {
		t.Fatalf("hello identity = %+v", hello)
	}
	if hello.Runtime.Os == "" {
		t.Fatalf("runtime missing OS: %+v", hello.Runtime)
	}
	metadata := hello.Runtime.Metadata.AsMap()
	if metadata["home"] == "" {
		t.Fatalf("runtime metadata = %+v", metadata)
	}
	capabilities := map[string]bool{}
	for _, capability := range hello.Capabilities {
		capabilities[capability] = true
	}
	if !capabilities["file"] || !capabilities["tool"] || !capabilities["artifact"] {
		t.Fatalf("capabilities = %+v", hello.Capabilities)
	}
	if len(hello.Tools) != 1 || hello.Tools[0].Name != "bash" {
		t.Fatalf("tools = %+v", hello.Tools)
	}
	catalog := wait(t, hub.catalog, "command catalog")
	if len(catalog.Commands) != 1 || catalog.Commands[0].GetName() != "!gogo" {
		t.Fatalf("command catalog = %+v", catalog.Commands)
	}
	if got := catalog.Commands[0].GetDescription(); got != "Use this playbook when working with gogo for host, port, service, banner, fingerprint, or vulnerability-hint discovery." {
		t.Fatalf("gogo description = %q", got)
	}
	dataBus.Emit(output.ToolDataEvent{
		Tool: "gogo", Kind: output.ToolDataService, Target: "192.0.2.1:80",
		Data: map[string]string{"ip": "192.0.2.1", "port": "80"}, CallID: "exec-1",
	})
	artifact := wait(t, hub.artifact, "tool artifact")
	if artifact.Tool != "gogo" || artifact.Kind != output.ToolDataService || string(artifact.Data) != `{"ip":"192.0.2.1","port":"80"}` {
		t.Fatalf("artifact = %+v data=%s", artifact, artifact.Data)
	}
	if line := wait(t, hub.progress, "tool progress"); line != "streamed" {
		t.Fatalf("progress = %q", line)
	}
	result := wait(t, hub.toolResult, "tool result")
	if result.IsError || result.CallId != "exec-1" || result.Name != "bash" {
		t.Fatalf("tool result = %+v", result)
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
		first, message, err := readAgentEnvelope(conn, false)
		core, ok := message.(*aop.ProtocolMessage)
		if err != nil || !ok || core.GetAgentHello() == nil {
			return
		}
		hub.registered <- core.GetAgentHello()
		if writeAgentEnvelope(conn, false, aop.MustWrap("accepted", first.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}})) != nil {
			return
		}
		hub.driveFileRead(conn, path)
		for {
			_, message, err := readAgentEnvelope(conn, false)
			if err != nil {
				return
			}
			if value, ok := message.(*filepb.ProtocolMessage); ok && value.GetResult() != nil {
				hub.fileData <- value.GetResult().Data
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

// TestRunToolNodeWireInteropProtoJSON verifies the same tool node speaks
// standard ProtoJSON text frames when the hub expects JSON semantics.
func TestRunToolNodeWireInteropProtoJSON(t *testing.T) {
	registry := commands.NewRegistry()
	registry.RegisterTool(&recordingBash{})
	hub := newHubScript(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		first, message, err := readAgentEnvelope(conn, true)
		core, ok := message.(*aop.ProtocolMessage)
		if err != nil || !ok || core.GetAgentHello() == nil {
			t.Errorf("expected hello over ProtoJSON: %v %v", message, err)
			return
		}
		hub.registered <- core.GetAgentHello()
		if writeAgentEnvelope(conn, true, aop.MustWrap("accepted", first.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}})) != nil {
			return
		}
		arguments, _ := aop.JSONValue(map[string]any{"command": "echo hello"})
		_ = writeAgentEnvelope(conn, true, aop.MustWrap("exec-1", "", &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: &toolpb.Call{
			SessionId: "exec-1", TurnId: "exec-1", Call: &aop.ToolCall{Id: "exec-1", Name: "bash", Arguments: arguments},
		}}}))
		for {
			_, message, err := readAgentEnvelope(conn, true)
			if err != nil {
				return
			}
			if value, ok := message.(*aop.ProtocolMessage); ok {
				if result := value.GetEvent().GetToolResult(); result != nil {
					hub.toolResult <- result
					return
				}
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunToolNode(ctx, ToolNodeConfig{ServerURL: server.URL, WSPath: "/ws/runner", ID: "runner-1", Token: "test-token", Registry: registry, JSONFrames: true})
	}()
	hello := wait(t, hub.registered, "hello over ProtoJSON")
	if hello.NodeId != "runner-1" {
		t.Fatalf("hello identity = %+v", hello)
	}
	result := wait(t, hub.toolResult, "tool result over ProtoJSON")
	if result.IsError || result.CallId != "exec-1" {
		t.Fatalf("tool result = %+v", result)
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("tool node did not stop")
	}
}
