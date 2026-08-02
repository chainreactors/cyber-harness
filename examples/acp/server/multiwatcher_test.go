package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

// This test runs the full topology in-process: headless server + scripted
// agent node + multiple browser-peer watchers. One watcher (the acp client)
// drives the turn; every other watcher (the web UI) must observe the same
// live events, and a late watcher must replay them from the durable store.

type wsPeer struct {
	t    *testing.T
	conn *websocket.Conn
	mu   sync.Mutex
	seq  int
}

func dialPeer(t *testing.T, baseURL string) *wsPeer {
	t.Helper()
	header := http.Header{"Authorization": []string{"Bearer test-token"}}
	conn, _, err := websocket.DefaultDialer.Dial(baseURL+"/api/aop/ws", header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return &wsPeer{t: t, conn: conn}
}

func (p *wsPeer) send(replyTo string, message protobuf.Message) string {
	p.t.Helper()
	p.seq++
	id := fmt.Sprintf("%p-%d", p, p.seq)
	data, err := protobuf.Marshal(aop.MustWrap(id, replyTo, message))
	if err != nil {
		p.t.Fatalf("marshal: %v", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		p.t.Fatalf("write: %v", err)
	}
	return id
}

func (p *wsPeer) recv() (*aop.Envelope, protobuf.Message) {
	p.t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := p.conn.ReadMessage()
	if err != nil {
		p.t.Fatalf("read: %v", err)
	}
	envelope := new(aop.Envelope)
	if err := protobuf.Unmarshal(data, envelope); err != nil {
		p.t.Fatalf("unmarshal: %v", err)
	}
	message, err := aop.Unwrap(envelope)
	if err != nil {
		p.t.Fatalf("unwrap: %v", err)
	}
	return envelope, message
}

// scriptedAgent accepts every session and answers each RunTurn with a message
// delta and turn_end — the same frames a real agent runtime emits.
func scriptedAgent(t *testing.T, baseURL, nodeID string, ready chan<- struct{}) {
	agent := dialPeer(t, baseURL)
	agent.send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentHello{
		AgentHello: &aop.AgentHello{NodeId: nodeID, Name: "scripted", Capabilities: []string{"tool"}},
	}})
	for {
		envelope, message := agent.recv()
		core, ok := message.(*aop.ProtocolMessage)
		if !ok {
			continue
		}
		switch payload := core.Message.(type) {
		case *aop.ProtocolMessage_AgentAccepted:
			close(ready)
		case *aop.ProtocolMessage_OpenSessionRequest:
			agent.send(envelope.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{
				OpenSessionResponse: &aop.OpenSessionResponse{Outcome: &aop.OpenSessionResponse_Accepted{
					Accepted: &aop.Session{Id: payload.OpenSessionRequest.GetSessionId(), State: "open", NodeId: nodeID},
				}},
			}})
		case *aop.ProtocolMessage_RunTurnRequest:
			sessionID := payload.RunTurnRequest.GetSessionId()
			turnID := payload.RunTurnRequest.GetTurnId()
			emit := func(event *aop.Event) {
				event.SessionId = sessionID
				event.TurnId = turnID
				agent.send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}})
			}
			emit(&aop.Event{Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{
				Value: &aop.MessageDelta_Text{Text: "pong"},
			}}})
			// Deltas are transient; the completed assistant message is what the
			// durable timeline persists and late watchers replay.
			emit(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{
				Role: "assistant", Content: []*aop.Content{aop.Text("pong")},
			}}})
			emit(&aop.Event{Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "stop"}}})
		}
	}
}

// browserWatcher is a read-side browser peer: it subscribes to a session and
// collects events until turn_end, like the web UI's live view.
type watched struct {
	deltas   strings.Builder
	messages strings.Builder
}

func browserWatcher(t *testing.T, baseURL, sessionID string, got chan<- watched) {
	watcher := dialPeer(t, baseURL)
	watchID := watcher.send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_WatchEventsRequest{
		WatchEventsRequest: &aop.WatchEventsRequest{SessionId: sessionID},
	}})
	var result watched
	for {
		envelope, message := watcher.recv()
		core, ok := message.(*aop.ProtocolMessage)
		if !ok || envelope.GetReplyTo() != watchID {
			continue
		}
		event := core.GetEvent()
		if event == nil {
			continue
		}
		switch payload := event.GetPayload().(type) {
		case *aop.Event_MessageDelta:
			result.deltas.WriteString(payload.MessageDelta.GetText())
		case *aop.Event_Message:
			for _, block := range payload.Message.GetContent() {
				result.messages.WriteString(block.GetText().GetText())
			}
		case *aop.Event_TurnEnded:
			got <- result
			return
		}
	}
}

func TestHeadlessWatchersShareLiveSession(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	ready := make(chan struct{})
	go scriptedAgent(t, wsBase, "agent-1", ready)
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not register")
	}

	// The acp client: opens the session and drives the turn.
	driver := dialPeer(t, wsBase)
	openID := driver.send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{
		OpenSessionRequest: &aop.OpenSessionRequest{NodeId: "agent-1"},
	}})
	sessionID := ""
	for sessionID == "" {
		envelope, message := driver.recv()
		core, ok := message.(*aop.ProtocolMessage)
		if !ok || envelope.GetReplyTo() != openID {
			continue
		}
		response := core.GetOpenSessionResponse()
		if rejected := response.GetRejected(); rejected != nil {
			t.Fatalf("open rejected %s: %s", rejected.GetCode(), rejected.GetMessage())
		}
		sessionID = response.GetAccepted().GetId()
	}

	// Two live watchers attach before the turn: a second acp client and the
	// web UI observer.
	gotA := make(chan watched, 1)
	gotB := make(chan watched, 1)
	go browserWatcher(t, wsBase, sessionID, gotA)
	go browserWatcher(t, wsBase, sessionID, gotB)

	turnID := driver.send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{
		RunTurnRequest: &aop.RunTurnRequest{
			SessionId: sessionID,
			Input:     &aop.Message{Role: "user", Content: []*aop.Content{aop.Text("ping")}},
		},
	}})
	for {
		envelope, message := driver.recv()
		core, ok := message.(*aop.ProtocolMessage)
		if !ok || envelope.GetReplyTo() != turnID {
			continue
		}
		response := core.GetRunTurnResponse()
		if rejected := response.GetRejected(); rejected != nil {
			t.Fatalf("run rejected %s: %s", rejected.GetCode(), rejected.GetMessage())
		}
		break
	}

	for name, got := range map[string]chan watched{"watcherA": gotA, "watcherB": gotB} {
		select {
		case result := <-got:
			// Live watchers see the streamed deltas AND the durable messages
			// (the user's own input is published ahead of the assistant reply).
			if result.deltas.String() != "pong" || result.messages.String() != "pingpong" {
				t.Fatalf("%s deltas=%q messages=%q", name, result.deltas.String(), result.messages.String())
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s timed out waiting for turn events", name)
		}
	}

	// A late watcher replays the durable timeline: deltas are transient, so it
	// sees only the persisted completed message, not the stream fragments.
	gotC := make(chan watched, 1)
	go browserWatcher(t, wsBase, sessionID, gotC)
	select {
	case result := <-gotC:
		if result.deltas.String() != "" || result.messages.String() != "pingpong" {
			t.Fatalf("late watcher deltas=%q messages=%q", result.deltas.String(), result.messages.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("late watcher timed out replaying events")
	}
}
