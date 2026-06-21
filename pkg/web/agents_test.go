package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/gorilla/websocket"
)

func dialAgent(t *testing.T, srv *httptest.Server, name string, commands []string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	reg, _ := json.Marshal(webproto.RegisterPayload{
		Name:     name,
		Commands: commands,
		Identity: webproto.AgentIdentity{
			NodeID:   "node-" + name,
			NodeName: name,
			Space:    "case-test",
		},
		Stats: webproto.AgentStats{TotalTokens: 42},
	})
	conn.WriteJSON(WSMessage{Type: "register", Payload: reg})
	var ack WSMessage
	conn.ReadJSON(&ack)
	if ack.Type != "connected" {
		t.Fatalf("expected connected, got %s", ack.Type)
	}
	return conn
}

func setupTestServer(t *testing.T) (*httptest.Server, *AgentPool) {
	t.Helper()
	hub := NewHub()
	pool := NewAgentPool(hub)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/ws", pool.HandleWS)
	mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		segments := pathSegments(r.URL.Path)
		if len(segments) == 5 && segments[0] == "api" && segments[1] == "agents" && segments[3] == "terminal" && segments[4] == "ws" {
			pool.HandleTerminalWS(segments[2], w, r)
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, pool
}

func TestWSRegisterAndList(t *testing.T) {
	srv, pool := setupTestServer(t)
	conn := dialAgent(t, srv, "test-agent", []string{"scan", "gogo"})
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)
	agents := pool.List()
	if len(agents) != 1 || agents[0].Name != "test-agent" {
		t.Fatalf("expected 1 agent named test-agent, got %+v", agents)
	}
	if agents[0].Identity.NodeID != "node-test-agent" || agents[0].Identity.Space != "case-test" {
		t.Fatalf("agent identity not retained: %+v", agents[0].Identity)
	}
	if agents[0].Stats.TotalTokens != 42 {
		t.Fatalf("agent stats not retained: %+v", agents[0].Stats)
	}
}

func TestWSDispatchAndComplete(t *testing.T) {
	srv, pool := setupTestServer(t)
	conn := dialAgent(t, srv, "worker", []string{"scan"})
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)
	agentID := pool.List()[0].ID

	progressCh, unsub := pool.hub.Subscribe("task-1")
	defer unsub()

	resultCh, err := pool.DispatchCommand(agentID, "task-1", "scan -i 1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}

	var cmd WSMessage
	conn.ReadJSON(&cmd)
	if cmd.Type != "exec" || cmd.Data != "scan -i 1.2.3.4" {
		t.Fatalf("unexpected: %+v", cmd)
	}

	conn.WriteJSON(WSMessage{Type: "output", TaskID: "task-1", Data: "port 80 open"})
	select {
	case evt := <-progressCh:
		if !strings.Contains(string(evt.Data), "port 80 open") {
			t.Fatalf("unexpected progress: %s", evt.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	result, _ := json.Marshal(map[string]int{"ports": 3})
	conn.WriteJSON(WSMessage{Type: "complete", TaskID: "task-1", Data: "done", Payload: result})
	select {
	case res := <-resultCh:
		if res.Err != "" || res.Output != "done" {
			t.Fatalf("unexpected result: %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestWSPick(t *testing.T) {
	_, pool := setupTestServer(t)
	if pool.Pick() != nil {
		t.Fatal("expected nil when no agents")
	}
}

func TestWSTelemetryForwarding(t *testing.T) {
	srv, pool := setupTestServer(t)
	conn := dialAgent(t, srv, "tele-agent", []string{"scan"})
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	progressCh, unsub := pool.hub.Subscribe("task-2")
	defer unsub()

	conn.WriteJSON(WSMessage{Type: "agent.turn_start", TaskID: "task-2", Data: "turn 1"})

	select {
	case evt := <-progressCh:
		if !strings.Contains(string(evt.Data), "turn 1") {
			t.Fatalf("unexpected: %s", evt.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestWSTerminalRelay(t *testing.T) {
	srv, pool := setupTestServer(t)
	agentConn := dialAgent(t, srv, "pty-agent", []string{"tmux"})
	defer agentConn.Close()

	time.Sleep(50 * time.Millisecond)
	agentID := pool.List()[0].ID
	terminalURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agents/" + agentID + "/terminal/ws"
	browserConn, resp, err := websocket.DefaultDialer.Dial(terminalURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("terminal dial: %v", err)
	}
	defer browserConn.Close()

	if err := browserConn.WriteJSON(WSMessage{Type: "pty.open"}); err != nil {
		t.Fatalf("browser pty.open: %v", err)
	}

	var open WSMessage
	if err := agentConn.ReadJSON(&open); err != nil {
		t.Fatalf("agent read pty.open: %v", err)
	}
	if open.Type != "pty.open" || open.StreamID == "" || open.TaskID != "" {
		t.Fatalf("unexpected pty.open: %+v", open)
	}

	openedPayload, _ := json.Marshal(map[string]string{"session_id": "session-1"})
	if err := agentConn.WriteJSON(WSMessage{Type: "pty.opened", StreamID: open.StreamID, Payload: openedPayload}); err != nil {
		t.Fatalf("agent pty.opened: %v", err)
	}

	var opened WSMessage
	if err := browserConn.ReadJSON(&opened); err != nil {
		t.Fatalf("browser read pty.opened: %v", err)
	}
	if opened.Type != "pty.opened" || opened.StreamID != open.StreamID || opened.TaskID != "" || !strings.Contains(string(opened.Payload), "session-1") {
		t.Fatalf("unexpected pty.opened: %+v", opened)
	}

	inputPayload, _ := json.Marshal(map[string]string{"session_id": "session-1", "data": "echo pty-ok\n"})
	if err := browserConn.WriteJSON(WSMessage{Type: "pty.input", Payload: inputPayload}); err != nil {
		t.Fatalf("browser pty.input: %v", err)
	}

	var input WSMessage
	if err := agentConn.ReadJSON(&input); err != nil {
		t.Fatalf("agent read pty.input: %v", err)
	}
	if input.Type != "pty.input" || input.StreamID != open.StreamID || input.TaskID != "" || !strings.Contains(string(input.Payload), "pty-ok") {
		t.Fatalf("unexpected pty.input: %+v", input)
	}

	if err := agentConn.WriteJSON(WSMessage{Type: "pty.output", StreamID: open.StreamID, Data: "pty-ok\n"}); err != nil {
		t.Fatalf("agent pty.output: %v", err)
	}

	var output WSMessage
	if err := browserConn.ReadJSON(&output); err != nil {
		t.Fatalf("browser read pty.output: %v", err)
	}
	if output.Type != "pty.output" || output.TaskID != "" || output.StreamID != open.StreamID || output.Data != "pty-ok\n" {
		t.Fatalf("unexpected pty.output: %+v", output)
	}
}

func TestWSTerminalSessionLifecycle(t *testing.T) {
	srv, pool := setupTestServer(t)
	agentConn := dialAgent(t, srv, "lifecycle-agent", []string{"tmux"})
	defer agentConn.Close()

	time.Sleep(50 * time.Millisecond)
	agentID := pool.List()[0].ID
	terminalURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agents/" + agentID + "/terminal/ws"
	browserConn, resp, err := websocket.DefaultDialer.Dial(terminalURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer browserConn.Close()

	readAgent := func(typ string) WSMessage {
		t.Helper()
		var m WSMessage
		if err := agentConn.ReadJSON(&m); err != nil {
			t.Fatalf("agent read %s: %v", typ, err)
		}
		if m.Type != typ {
			t.Fatalf("agent expected %s, got %s", typ, m.Type)
		}
		return m
	}
	readBrowser := func(typ string) WSMessage {
		t.Helper()
		var m WSMessage
		if err := browserConn.ReadJSON(&m); err != nil {
			t.Fatalf("browser read %s: %v", typ, err)
		}
		if m.Type != typ {
			t.Fatalf("browser expected %s, got %s", typ, m.Type)
		}
		return m
	}
	agentReply := func(m WSMessage) {
		t.Helper()
		if err := agentConn.WriteJSON(m); err != nil {
			t.Fatalf("agent write %s: %v", m.Type, err)
		}
	}
	browserSend := func(m WSMessage) {
		t.Helper()
		if err := browserConn.WriteJSON(m); err != nil {
			t.Fatalf("browser write %s: %v", m.Type, err)
		}
	}

	// open
	browserSend(WSMessage{Type: "pty.open", Payload: mustJSON(map[string]any{
		"kind": "shell", "name": "test-shell", "cols": 80, "rows": 24,
	})})
	open := readAgent("pty.open")
	streamID := open.StreamID

	agentReply(WSMessage{Type: "pty.opened", StreamID: streamID,
		Payload: mustJSON(map[string]any{"session_id": "sess-1", "kind": "shell"})})
	opened := readBrowser("pty.opened")
	if !strings.Contains(string(opened.Payload), "sess-1") {
		t.Fatalf("opened missing session_id: %s", opened.Payload)
	}

	// input → output
	browserSend(WSMessage{Type: "pty.input", Payload: mustJSON(map[string]any{"data": "ls\n"})})
	inp := readAgent("pty.input")
	if !strings.Contains(string(inp.Payload), "ls") {
		t.Fatalf("input data lost: %s", inp.Payload)
	}
	agentReply(WSMessage{Type: "pty.output", StreamID: streamID, Data: "file1 file2\n"})
	out := readBrowser("pty.output")
	if out.Data != "file1 file2\n" {
		t.Fatalf("output: %q", out.Data)
	}

	// resize
	browserSend(WSMessage{Type: "pty.resize", Payload: mustJSON(map[string]any{"cols": 120, "rows": 40})})
	resize := readAgent("pty.resize")
	if !strings.Contains(string(resize.Payload), "120") {
		t.Fatalf("resize cols lost: %s", resize.Payload)
	}

	// list
	browserSend(WSMessage{Type: "pty.list"})
	list := readAgent("pty.list")
	agentReply(WSMessage{Type: "pty.sessions", StreamID: list.StreamID,
		Payload: mustJSON(map[string]any{"sessions": []map[string]any{
			{"id": "sess-1", "kind": "shell", "state": "running"},
		}})})
	sessions := readBrowser("pty.sessions")
	if !strings.Contains(string(sessions.Payload), "sess-1") {
		t.Fatalf("sessions missing: %s", sessions.Payload)
	}

	// detach
	browserSend(WSMessage{Type: "pty.detach"})
	det := readAgent("pty.detach")
	agentReply(WSMessage{Type: "pty.detached", StreamID: det.StreamID,
		Payload: mustJSON(map[string]any{"session_id": "sess-1"})})
	readBrowser("pty.detached")

	// attach
	browserSend(WSMessage{Type: "pty.attach", Payload: mustJSON(map[string]any{"session_id": "sess-1"})})
	att := readAgent("pty.attach")
	agentReply(WSMessage{Type: "pty.attached", StreamID: att.StreamID,
		Payload: mustJSON(map[string]any{"session_id": "sess-1"})})
	readBrowser("pty.attached")

	// closed
	agentReply(WSMessage{Type: "pty.closed", StreamID: streamID,
		Payload: mustJSON(map[string]any{"session_id": "sess-1", "state": "completed", "exit_code": 0})})
	closed := readBrowser("pty.closed")
	if !strings.Contains(string(closed.Payload), "completed") {
		t.Fatalf("closed state lost: %s", closed.Payload)
	}
}

func TestWSTerminalSingleton(t *testing.T) {
	srv, pool := setupTestServer(t)
	agentConn := dialAgent(t, srv, "singleton-agent", []string{"tmux"})
	defer agentConn.Close()

	time.Sleep(50 * time.Millisecond)
	agentID := pool.List()[0].ID
	terminalURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agents/" + agentID + "/terminal/ws"
	browserConn, resp, err := websocket.DefaultDialer.Dial(terminalURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer browserConn.Close()

	browserConn.WriteJSON(WSMessage{Type: "pty.open", Payload: mustJSON(map[string]any{
		"kind": "repl", "name": "main-repl", "singleton": true, "cols": 80, "rows": 24,
	})})

	var open WSMessage
	agentConn.ReadJSON(&open)
	if open.Type != "pty.open" {
		t.Fatalf("expected pty.open, got %s", open.Type)
	}
	var payload webproto.PTYPayload
	json.Unmarshal(open.Payload, &payload)
	if !payload.Singleton || payload.Kind != "repl" || payload.Name != "main-repl" {
		t.Fatalf("singleton not preserved: %+v", payload)
	}
}

func TestWSTerminalBufferPressure(t *testing.T) {
	srv, pool := setupTestServer(t)
	agentConn := dialAgent(t, srv, "pressure-agent", []string{"tmux"})
	defer agentConn.Close()

	time.Sleep(50 * time.Millisecond)
	agentID := pool.List()[0].ID
	terminalURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agents/" + agentID + "/terminal/ws"
	browserConn, resp, err := websocket.DefaultDialer.Dial(terminalURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer browserConn.Close()

	browserConn.WriteJSON(WSMessage{Type: "pty.open"})
	var open WSMessage
	agentConn.ReadJSON(&open)
	streamID := open.StreamID
	agentConn.WriteJSON(WSMessage{Type: "pty.opened", StreamID: streamID,
		Payload: mustJSON(map[string]any{"session_id": "sess-1"})})
	browserConn.ReadJSON(&open) // consume opened

	// Flood: agent sends 100 output messages without browser reading
	for i := 0; i < 100; i++ {
		agentConn.WriteJSON(WSMessage{Type: "pty.output", StreamID: streamID, Data: strings.Repeat("x", 100)})
	}
	time.Sleep(100 * time.Millisecond)

	// Browser should still receive messages (newest preserved via backpressure)
	browserConn.SetReadDeadline(time.Now().Add(time.Second))
	received := 0
	for {
		var m WSMessage
		if err := browserConn.ReadJSON(&m); err != nil {
			break
		}
		if m.Type == "pty.output" {
			received++
		}
	}
	if received == 0 {
		t.Fatal("browser received no output under pressure")
	}
	t.Logf("received %d/%d messages under buffer pressure", received, 100)
}

