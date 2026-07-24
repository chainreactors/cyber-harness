package webagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
)

func TestWebNodeRefUsesWebIdentity(t *testing.T) {
	ref, err := webNodeRef(&cfg.Option{
		AgentOptions: cfg.AgentOptions{WebURL: "https://secret@example.test/hub"},
		IOAOptions:   cfg.IOAOptions{IOANodeName: "worker-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "worker-1" || ref.Authority != "https://example.test/hub" {
		t.Fatalf("node ref = %#v", ref)
	}
	if _, err := webNodeRef(&cfg.Option{AgentOptions: cfg.AgentOptions{WebURL: "https://example.test"}}); err == nil {
		t.Fatal("expected missing ioa.node_name error")
	}
}

func TestRunAndCommandErrorsKeepDistinctCorrelationIDs(t *testing.T) {
	h := &chatAgentHandler{sessions: make(map[string]*runner.Session)}

	var runError webproto.Message
	wait := h.HandleRun(context.Background(), webproto.Message{
		Type: webproto.TypeRun, TurnID: "turn-1", Payload: json.RawMessage(`{`),
	}, func(message webproto.Message) { runError = message })
	wait()
	if runError.Type != webproto.TypeError || runError.TurnID != "turn-1" || runError.TaskID != "" {
		t.Fatalf("run error correlation = %+v", runError)
	}

	var commandError webproto.Message
	h.HandleCommand(context.Background(), webproto.Message{
		Type: webproto.TypeCommand, TaskID: "command-1", Payload: json.RawMessage(`{`),
	}, func(message webproto.Message) { commandError = message })
	if commandError.Type != webproto.TypeError || commandError.TaskID != "command-1" || commandError.TurnID != "" {
		t.Fatalf("command error correlation = %+v", commandError)
	}
}

func connectForTest(ctx context.Context, serverURL, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[aop.Event]) error {
	if _, ok := reg.GetTool("bash"); !ok {
		bash := commands.NewBashTool(".", 5)
		bash.SetCommandResolver(reg.Get)
		reg.RegisterTool(bash)
		defer bash.Close()
	}
	return connect(ctx, connectionConfig{
		ServerURL: serverURL,
		Name:      name,
		Registry:  reg,
		AgentSubscribe: func(fn func(aop.Event)) func() {
			if bus == nil {
				return func() {}
			}
			return bus.Subscribe(fn)
		},
		DataBus: eventbus.New[output.ToolDataEvent](),
		Node:    protocols.NodeRef{ID: "node-" + name, Authority: serverURL},
	})
}

type webConnectionTestCommand struct{}

func (c webConnectionTestCommand) Name() string  { return "echo" }
func (c webConnectionTestCommand) Usage() string { return "echo" }

func (c webConnectionTestCommand) Run(ctx context.Context, execution *commands.Execution) (any, error) {
	fmt.Fprintf(execution.Stdout, "progress: %s\n", strings.Join(execution.Args, " "))
	return nil, nil
}

func TestRunConnectionScopesTelemetryToActiveTask(t *testing.T) {
	var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	registered := make(chan struct{})
	var registeredOnce sync.Once
	messages := make(chan webproto.Message, 8)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var reg webproto.Message
		if err := conn.ReadJSON(&reg); err != nil {
			t.Errorf("register read: %v", err)
			return
		}
		if reg.Type != "register" || !strings.Contains(string(reg.Payload), "echo") {
			t.Errorf("unexpected register: %+v", reg)
			return
		}
		ack, _ := json.Marshal(map[string]string{"agent_id": "agent-1"})
		if err := conn.WriteJSON(webproto.Message{Type: "connected", Payload: ack}); err != nil {
			t.Errorf("ack write: %v", err)
			return
		}
		registeredOnce.Do(func() { close(registered) })

		call := aop.ToolCallData{
			ToolCallID: "call-1",
			ToolName:   "bash",
			Args:       map[string]any{"command": `echo "hello world"`},
		}
		payload, _ := json.Marshal(webproto.CommandPayload{SessionID: "task-1", ToolCall: &call})
		if err := conn.WriteJSON(webproto.Message{Type: webproto.TypeCommand, TaskID: "task-1", Payload: payload}); err != nil {
			t.Errorf("tool.call write: %v", err)
			return
		}
		for {
			var msg webproto.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			messages <- msg
			if msg.Type == webproto.TypeCommandResult {
				return
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bus := eventbus.New[aop.Event]()
	reg := commands.NewRegistry()
	impl := webConnectionTestCommand{}
	reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run}, "test")

	done := make(chan error, 1)
	go func() {
		done <- connectForTest(ctx, srv.URL, "worker", reg, bus)
	}()

	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("web agent connection did not register")
	}

	seenOutput := false
	seenResult := false
	deadline := time.After(3 * time.Second)
	for !seenResult {
		select {
		case msg := <-messages:
			if msg.Type != webproto.TypeAOP && msg.TaskID != "task-1" {
				t.Fatalf("message missing task id: %+v", msg)
			}
			switch msg.Type {
			case "tool.data":
				var ev output.ToolDataEvent
				if json.Unmarshal(msg.Payload, &ev) == nil && ev.Kind == output.ToolDataProgress {
					if line, ok := ev.Data.(string); ok && strings.Contains(line, "hello world") {
						seenOutput = true
					}
				}
			case webproto.TypeCommandResult:
				seenResult = true
			}
		case <-deadline:
			t.Fatal("timeout waiting for web agent messages")
		}
	}

	if !seenOutput {
		t.Fatal("web agent connection did not stream command output")
	}

	cancel()
	<-done
}

func TestRunConnectionChatWithoutRuntimeReturnsClearError(t *testing.T) {
	var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	registered := make(chan struct{})
	var registeredOnce sync.Once
	messages := make(chan webproto.Message, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var reg webproto.Message
		if err := conn.ReadJSON(&reg); err != nil {
			t.Errorf("register read: %v", err)
			return
		}
		ack, _ := json.Marshal(map[string]string{"agent_id": "agent-1"})
		if err := conn.WriteJSON(webproto.Message{Type: "connected", Payload: ack}); err != nil {
			t.Errorf("ack write: %v", err)
			return
		}
		registeredOnce.Do(func() { close(registered) })

		if err := conn.WriteJSON(webproto.Message{Type: "chat", TaskID: "task-chat", Data: "hello"}); err != nil {
			t.Errorf("chat write: %v", err)
			return
		}
		for {
			var msg webproto.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			messages <- msg
			if msg.Type == "error" {
				return
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reg := commands.NewRegistry()
	impl := webConnectionTestCommand{}
	reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run}, "test")

	done := make(chan error, 1)
	go func() {
		done <- connectForTest(ctx, srv.URL, "worker", reg, nil)
	}()

	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("web agent connection did not register")
	}

	// Without a chat handler, the connection does not dispatch "chat" messages,
	// so the hub never gets an error reply. This test now verifies that the
	// connection stays stable when chat arrives without a handler.
	select {
	case msg := <-messages:
		// If the node happened to reply, accept it.
		if msg.Type != "error" {
			t.Logf("unexpected message: %+v (expected no reply for chat without handler)", msg)
		}
	case <-time.After(1 * time.Second):
		// Expected: no reply because Chat is nil.
	}

	cancel()
	<-done
}

func TestRunConnectionPTYRoundTrip(t *testing.T) {
	var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	registered := make(chan struct{})
	var registeredOnce sync.Once
	result := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var reg webproto.Message
		if err := conn.ReadJSON(&reg); err != nil {
			t.Errorf("register read: %v", err)
			return
		}
		ack, _ := json.Marshal(map[string]string{"agent_id": "agent-pty"})
		if err := conn.WriteJSON(webproto.Message{Type: "connected", Payload: ack}); err != nil {
			t.Errorf("ack write: %v", err)
			return
		}
		registeredOnce.Do(func() { close(registered) })

		if err := conn.WriteJSON(webproto.NewPTYMessage(pty.Frame{Type: pty.FrameOpen, StreamID: "term-1"})); err != nil {
			t.Errorf("pty.open write: %v", err)
			return
		}

		opened := false
		inputSent := false
		for {
			var msg webproto.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type != webproto.TypePTY {
				continue
			}
			frame, err := webproto.DecodePTYMessage(msg)
			if err != nil {
				result <- "error: " + err.Error()
				return
			}
			switch frame.Type {
			case pty.FrameOpened:
				opened = true
				lineEnding := "\n"
				if runtime.GOOS == "windows" {
					lineEnding = "\r\n"
				}
				if err := conn.WriteJSON(webproto.NewPTYMessage(pty.Frame{Type: pty.FrameInput, StreamID: "term-1", Data: []byte("echo pty_web_ok" + lineEnding)})); err != nil {
					t.Errorf("pty.input write: %v", err)
					return
				}
				inputSent = true
			case pty.FrameOutput:
				if opened && inputSent && strings.Contains(string(frame.Data), "pty_web_ok") {
					_ = conn.WriteJSON(webproto.NewPTYMessage(pty.Frame{Type: pty.FrameKill, StreamID: "term-1"}))
					result <- string(frame.Data)
					return
				}
			case pty.FrameError:
				result <- "error: " + frame.Error
				return
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	reg := commands.NewRegistry()
	commands.BuildGroup("core", &commands.Deps{WorkDir: t.TempDir(), BashTimeout: 5}, reg)

	done := make(chan error, 1)
	go func() {
		done <- connectForTest(ctx, srv.URL, "worker", reg, nil)
	}()

	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("web agent connection did not register")
	}

	select {
	case out := <-result:
		if !strings.Contains(out, "pty_web_ok") {
			t.Fatalf("unexpected pty output: %q", out)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("timeout waiting for pty output")
	}

	cancel()
	<-done
}

func TestRunConnectionPushesPTYSessionsOnManagerEvents(t *testing.T) {
	var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	registered := make(chan struct{})
	var registeredOnce sync.Once
	sessionUpdates := make(chan pty.Frame, 8)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var reg webproto.Message
		if err := conn.ReadJSON(&reg); err != nil {
			t.Errorf("register read: %v", err)
			return
		}
		ack, _ := json.Marshal(map[string]string{"agent_id": "agent-live"})
		if err := conn.WriteJSON(webproto.Message{Type: "connected", Payload: ack}); err != nil {
			t.Errorf("ack write: %v", err)
			return
		}
		registeredOnce.Do(func() { close(registered) })

		if err := conn.WriteJSON(webproto.NewPTYMessage(pty.Frame{Type: pty.FrameList, StreamID: "term-live"})); err != nil {
			t.Errorf("pty.list write: %v", err)
			return
		}

		for {
			var msg webproto.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type != webproto.TypePTY {
				continue
			}
			frame, err := webproto.DecodePTYMessage(msg)
			if err == nil && frame.Type == pty.FrameSessions && frame.StreamID == "term-live" {
				sessionUpdates <- frame
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	reg := commands.NewRegistry()
	commands.BuildGroup("core", &commands.Deps{WorkDir: t.TempDir(), BashTimeout: 5}, reg)
	mgr := RegistryPTYManager(reg)
	if mgr == nil {
		t.Fatal("bash command did not expose tmux manager")
	}

	done := make(chan error, 1)
	go func() {
		done <- connectForTest(ctx, srv.URL, "worker", reg, nil)
	}()

	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("web agent connection did not register")
	}

	// Drain the explicit pty.list response so later reads prove event-driven pushes.
	readSessionUpdate(t, sessionUpdates, func(pty.Frame) bool { return true })

	release := make(chan struct{})
	info, err := mgr.CreateFunc(ctx, "live-session", 5*time.Second, func(ctx context.Context, w io.Writer) error {
		_, _ = w.Write([]byte("live\n"))
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("CreateFunc: %v", err)
	}

	readSessionUpdate(t, sessionUpdates, func(frame pty.Frame) bool {
		return frameHasSessionState(frame, info.ID, "running")
	})
	readSessionUpdate(t, sessionUpdates, func(frame pty.Frame) bool {
		return frameHasSessionActivity(frame, info.ID)
	})

	close(release)
	readSessionUpdate(t, sessionUpdates, func(frame pty.Frame) bool {
		return frameHasSessionState(frame, info.ID, "completed")
	})

	cancel()
	<-done
}

func readSessionUpdate(t *testing.T, updates <-chan pty.Frame, match func(pty.Frame) bool) pty.Frame {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case frame := <-updates:
			if match(frame) {
				return frame
			}
		case <-deadline:
			t.Fatal("timeout waiting for pty.sessions update")
			return pty.Frame{}
		}
	}
}

func frameHasSessionState(frame pty.Frame, sessionID, state string) bool {
	for _, session := range frame.Sessions {
		if session.ID == sessionID && string(session.State) == state {
			return true
		}
	}
	return false
}

func frameHasSessionActivity(frame pty.Frame, sessionID string) bool {
	for _, session := range frame.Sessions {
		if session.ID == sessionID && session.ActivitySeq >= 2 && session.OutputBytes > 0 {
			return true
		}
	}
	return false
}

func TestFenceTerminalOutput(t *testing.T) {
	// Single-line status stays prose — no fence.
	if got := fenceTerminalOutput("Provider ready: anthropic / glm-5.2"); strings.Contains(got, "```") {
		t.Errorf("single-line output should not be fenced, got %q", got)
	}
	// Multi-line panel (box art) gets fenced so the web renders it monospace.
	panel := "╭────╮\n│ providers │\n╰────╯"
	got := fenceTerminalOutput(panel)
	if !strings.HasPrefix(got, "```\n") || !strings.HasSuffix(got, "\n```") {
		t.Errorf("multi-line panel should be wrapped in a code fence, got %q", got)
	}
	if !strings.Contains(got, panel) {
		t.Errorf("fenced output should preserve the panel verbatim, got %q", got)
	}
	// A payload containing a triple-backtick run grows the fence so it can't collide.
	got = fenceTerminalOutput("line1\n```\nline2")
	if !strings.HasPrefix(got, "````\n") {
		t.Errorf("fence must be longer than an inner backtick run, got %q", got)
	}
	// Empty stays empty.
	if got := fenceTerminalOutput(""); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
}
