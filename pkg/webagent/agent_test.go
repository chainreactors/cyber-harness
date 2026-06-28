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

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/gorilla/websocket"
)

type webConnectionTestCommand struct {
	bus *eventbus.Bus[agent.Event]
}

func (c webConnectionTestCommand) Name() string  { return "echo" }
func (c webConnectionTestCommand) Usage() string { return "echo" }

func (c webConnectionTestCommand) Execute(_ context.Context, args []string) error {
	if c.bus != nil {
		c.bus.Emit(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	}
	fmt.Fprintf(commands.Output, "progress: %s\n", strings.Join(args, " "))
	return nil
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

		if err := conn.WriteJSON(webproto.Message{Type: "exec", TaskID: "task-1", Data: `echo "hello world"`}); err != nil {
			t.Errorf("exec write: %v", err)
			return
		}
		for {
			var msg webproto.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			messages <- msg
			if msg.Type == "complete" {
				return
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bus := eventbus.New[agent.Event]()
	reg := commands.NewRegistry()
	reg.Register(webConnectionTestCommand{bus: bus}, "test")

	done := make(chan error, 1)
	go func() {
		done <- RunConnection(ctx, srv.URL, "worker", reg, bus)
	}()

	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("web agent connection did not register")
	}

	seenOutput := false
	seenTelemetry := false
	seenComplete := false
	deadline := time.After(3 * time.Second)
	for !seenComplete {
		select {
		case msg := <-messages:
			if msg.TaskID != "task-1" {
				t.Fatalf("message missing task id: %+v", msg)
			}
			switch msg.Type {
			case "output":
				seenOutput = strings.Contains(msg.Data, "hello world")
			case "agent.turn_start":
				seenTelemetry = strings.Contains(msg.Data, "turn 1")
			case "complete":
				seenComplete = true
			}
		case <-deadline:
			t.Fatal("timeout waiting for web agent messages")
		}
	}

	if !seenOutput {
		t.Fatal("web agent connection did not stream command output")
	}
	if !seenTelemetry {
		t.Fatal("web agent connection did not scope telemetry to task")
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
	reg.Register(webConnectionTestCommand{}, "test")

	done := make(chan error, 1)
	go func() {
		done <- RunConnection(ctx, srv.URL, "worker", reg, nil)
	}()

	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("web agent connection did not register")
	}

	select {
	case msg := <-messages:
		if msg.Type != "error" || msg.TaskID != "task-chat" || !strings.Contains(msg.Data, "LLM provider is not configured") {
			t.Fatalf("unexpected message: %+v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for chat error")
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

		if err := conn.WriteJSON(webproto.Message{Type: "pty.open", StreamID: "term-1"}); err != nil {
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
			switch msg.Type {
			case "pty.opened":
				opened = true
				lineEnding := "\n"
				if runtime.GOOS == "windows" {
					lineEnding = "\r\n"
				}
				payload, _ := json.Marshal(map[string]string{"data": "echo pty_web_ok" + lineEnding})
				if err := conn.WriteJSON(webproto.Message{Type: "pty.input", StreamID: "term-1", Payload: payload}); err != nil {
					t.Errorf("pty.input write: %v", err)
					return
				}
				inputSent = true
			case "pty.output":
				if opened && inputSent && strings.Contains(msg.Data, "pty_web_ok") {
					_ = conn.WriteJSON(webproto.Message{Type: "pty.kill", StreamID: "term-1"})
					result <- msg.Data
					return
				}
			case "pty.error":
				result <- "error: " + msg.Data
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
		done <- RunConnection(ctx, srv.URL, "worker", reg, nil)
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
	sessionUpdates := make(chan webproto.Message, 8)

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

		if err := conn.WriteJSON(webproto.Message{Type: "pty.list", StreamID: "term-live"}); err != nil {
			t.Errorf("pty.list write: %v", err)
			return
		}

		for {
			var msg webproto.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "pty.sessions" && msg.StreamID == "term-live" {
				sessionUpdates <- msg
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	reg := commands.NewRegistry()
	commands.BuildGroup("core", &commands.Deps{WorkDir: t.TempDir(), BashTimeout: 5}, reg)
	mgr := registryPTYManager(reg)
	if mgr == nil {
		t.Fatal("bash command did not expose tmux manager")
	}

	done := make(chan error, 1)
	go func() {
		done <- RunConnection(ctx, srv.URL, "worker", reg, nil)
	}()

	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("web agent connection did not register")
	}

	// Drain the explicit pty.list response so later reads prove event-driven pushes.
	readSessionUpdate(t, sessionUpdates, func(webproto.PTYPayload) bool { return true })

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

	readSessionUpdate(t, sessionUpdates, func(payload webproto.PTYPayload) bool {
		return payloadHasSessionState(payload, info.ID, "running")
	})
	readSessionMessage(t, sessionUpdates, func(msg webproto.Message) bool {
		return payloadHasSessionActivity(msg.Payload, info.ID)
	})

	close(release)
	readSessionUpdate(t, sessionUpdates, func(payload webproto.PTYPayload) bool {
		return payloadHasSessionState(payload, info.ID, "completed")
	})

	cancel()
	<-done
}

func readSessionMessage(t *testing.T, updates <-chan webproto.Message, match func(webproto.Message) bool) webproto.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-updates:
			if match(msg) {
				return msg
			}
		case <-deadline:
			t.Fatal("timeout waiting for pty.sessions message")
			return webproto.Message{}
		}
	}
}

func readSessionUpdate(t *testing.T, updates <-chan webproto.Message, match func(webproto.PTYPayload) bool) webproto.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-updates:
			payload, err := webproto.DecodePTYPayload(msg.Payload)
			if err != nil {
				t.Fatalf("decode pty payload: %v", err)
			}
			if match(payload) {
				return msg
			}
		case <-deadline:
			t.Fatal("timeout waiting for pty.sessions update")
			return webproto.Message{}
		}
	}
}

func payloadHasSessionState(payload webproto.PTYPayload, sessionID, state string) bool {
	for _, session := range payload.Sessions {
		if session.ID == sessionID && string(session.State) == state {
			return true
		}
	}
	return false
}

func payloadHasSessionActivity(raw json.RawMessage, sessionID string) bool {
	var payload struct {
		Sessions []struct {
			ID          string `json:"id"`
			ActivitySeq int64  `json:"activity_seq"`
			OutputBytes int64  `json:"output_bytes"`
		} `json:"sessions"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	for _, session := range payload.Sessions {
		if session.ID == sessionID && session.ActivitySeq >= 2 && session.OutputBytes > 0 {
			return true
		}
	}
	return false
}
