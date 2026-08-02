package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// scriptedHub speaks just enough browser-peer AOP to exercise the client:
// it accepts a session, records the watch subscription, and on RunTurn
// replies with a receipt and streams a delta plus turn_end over the watch.
type scriptedHub struct {
	t      *testing.T
	prompt chan string
}

func (h *scriptedHub) send(conn *websocket.Conn, replyTo string, message protobuf.Message) {
	envelope := aop.MustWrap("hub-"+replyTo, replyTo, message)
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		h.t.Errorf("marshal: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		h.t.Errorf("write: %v", err)
	}
}

func (h *scriptedHub) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		h.t.Errorf("authorization = %q", got)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := testUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	watchID := make(chan string, 1)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		envelope := new(aop.Envelope)
		if err := protobuf.Unmarshal(data, envelope); err != nil {
			continue
		}
		message, err := aop.Unwrap(envelope)
		if err != nil {
			continue
		}
		core, ok := message.(*aop.ProtocolMessage)
		if !ok {
			continue
		}
		switch payload := core.Message.(type) {
		case *aop.ProtocolMessage_OpenSessionRequest:
			if payload.OpenSessionRequest.GetNodeId() != "node-1" {
				h.t.Errorf("open node = %q", payload.OpenSessionRequest.GetNodeId())
			}
			h.send(conn, envelope.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{
				OpenSessionResponse: &aop.OpenSessionResponse{Outcome: &aop.OpenSessionResponse_Accepted{
					Accepted: &aop.Session{Id: "s1", State: "open", NodeId: "node-1"},
				}},
			}})
		case *aop.ProtocolMessage_WatchEventsRequest:
			if payload.WatchEventsRequest.GetSessionId() != "s1" {
				h.t.Errorf("watch session = %q", payload.WatchEventsRequest.GetSessionId())
			}
			watchID <- envelope.Id
		case *aop.ProtocolMessage_RunTurnRequest:
			text := ""
			for _, block := range payload.RunTurnRequest.GetInput().GetContent() {
				text += block.GetText().GetText()
			}
			h.prompt <- text
			h.send(conn, envelope.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnResponse{
				RunTurnResponse: &aop.RunTurnResponse{Outcome: &aop.RunTurnResponse_Accepted{
					Accepted: &aop.TurnReceipt{SessionId: "s1", TurnId: "t1", State: "running"},
				}},
			}})
			subscription := <-watchID
			h.send(conn, subscription, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{
				Event: &aop.Event{Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{
					Value: &aop.MessageDelta_Text{Text: "pong"},
				}}},
			}})
			h.send(conn, subscription, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{
				Event: &aop.Event{Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "stop"}}},
			}})
		}
	}
}

func TestClientChatFlow(t *testing.T) {
	hub := &scriptedHub{t: t, prompt: make(chan string, 1)}
	server := httptest.NewServer(http.HandlerFunc(hub.serveHTTP))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, server.URL, "/api/aop/application/ws", "test-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	session, err := client.OpenSession(ctx, "node-1", "")
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	if session.GetId() != "s1" {
		t.Fatalf("session = %+v", session)
	}

	events, err := client.Watch(session.GetId(), "")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	receipt, err := client.RunTurn(ctx, session.GetId(), "ping")
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if receipt.GetTurnId() != "t1" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if got := <-hub.prompt; got != "ping" {
		t.Fatalf("hub received prompt %q", got)
	}

	var delta string
	ended := false
	for event := range events {
		switch payload := event.GetPayload().(type) {
		case *aop.Event_MessageDelta:
			delta += payload.MessageDelta.GetText()
		case *aop.Event_TurnEnded:
			ended = true
		}
		if ended {
			break
		}
	}
	if delta != "pong" || !ended {
		t.Fatalf("delta = %q ended = %v", delta, ended)
	}
}

func TestClientOpenSessionRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		envelope := new(aop.Envelope)
		if err := protobuf.Unmarshal(data, envelope); err != nil {
			return
		}
		reply := aop.MustWrap("r1", envelope.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{
			OpenSessionResponse: &aop.OpenSessionResponse{Outcome: &aop.OpenSessionResponse_Rejected{
				Rejected: &aop.Rejection{Code: "UNAVAILABLE", Message: "node is not connected"},
			}},
		}})
		out, _ := protobuf.Marshal(reply)
		_ = conn.WriteMessage(websocket.BinaryMessage, out)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, server.URL, "", "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if _, err := client.OpenSession(ctx, "ghost", ""); err == nil {
		t.Fatal("expected rejection error")
	}
}
