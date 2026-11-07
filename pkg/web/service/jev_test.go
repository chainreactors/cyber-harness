package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/exts/jev"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestJEVLibraryQueryUsesBoundNodeWithoutStartingWork(t *testing.T) {
	for _, mode := range []string{"library", "wait_idle"} {
		t.Run(mode, func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			svc := NewService(ServiceConfig{Store: store})
			defer svc.Close(context.Background())
			pool := NewAgentPool(svc.Hub(), nil)
			svc.SetAgentPool(pool)
			worker := &remoteAgent{nodeID: "bound", nodeState: &nodeState{tasks: make(map[string]chan proto.Message), openSessions: map[string]struct{}{}, toolCalls: map[string]struct{}{}}}
			queue := bindAgentQueue(worker, 8)
			pool.agents[worker.nodeID] = worker
			session := createTestSession(t, svc, worker.nodeID, "JEV library")
			request := &jev.ProtocolMessage{Message: &jev.ProtocolMessage_Request{Request: &jev.GetLibraryRequest{SessionId: session.GetSession().GetId()}}}
			if mode == "wait_idle" {
				request.Message = &jev.ProtocolMessage_WaitIdle{WaitIdle: &jev.WaitIdleRequest{SessionId: session.GetSession().GetId(), TimeoutMs: 50}}
			}
			replies, failures := make(chan *jev.ProtocolMessage, 1), make(chan error, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			go func() {
				reply, err := svc.forwardJEV(ctx, request)
				replies <- reply
				failures <- err
			}()
			var envelope *aop.Envelope
			select {
			case envelope = <-queue:
			case <-ctx.Done():
				t.Fatal("query did not reach the bound node")
			}
			message, err := aop.Unwrap(envelope)
			if err != nil || !proto.Equal(message, request) {
				t.Fatalf("query became executable work: %T %v", message, err)
			}
			mux := aop.NewNamespaceMux(t.Context())
			defer mux.Close(context.Background())
			if err := pool.registerAgentNamespaces(mux, worker); err != nil {
				t.Fatal(err)
			}
			library := &jev.ProtocolMessage{Message: &jev.ProtocolMessage_Library{Library: &jev.GetLibraryResponse{Mode: "auto", Status: "ready", Revision: "revision"}}}
			if mode == "wait_idle" {
				library.Message = &jev.ProtocolMessage_Idle{Idle: &jev.WaitIdleResponse{Settled: false, Error: "context deadline exceeded"}}
			}
			if handled, err := mux.Dispatch(aop.Reply(envelope.Id, library), func(*aop.Envelope) error { return nil }); err != nil || !handled {
				t.Fatalf("reply namespace: handled=%t err=%v", handled, err)
			}
			if reply, err := <-replies, <-failures; err != nil || !proto.Equal(reply, library) {
				t.Fatalf("query reply=%v err=%v", reply, err)
			}
			worker.mu.Lock()
			active := len(worker.tasks)
			worker.mu.Unlock()
			events, err := store.ListAOPEvents(t.Context(), session.GetSession().GetId(), 10)
			if err != nil || len(events) != 0 || len(queue) != 0 || active != 0 {
				t.Fatalf("query started work: events=%d queued=%d tasks=%d err=%v", len(events), len(queue), active, err)
			}
			if mode == "wait_idle" {
				request.GetWaitIdle().SessionId = "unknown-session"
			} else {
				request.GetRequest().SessionId = "unknown-session"
			}
			if _, err := svc.forwardJEV(t.Context(), request); err == nil || len(queue) != 0 {
				t.Fatal("unknown session dispatched to a node")
			}
		})
	}
}

func TestJEVLatePublicationSurvivesBackpressureAndDurableReplay(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "parent")
	svc := NewService(ServiceConfig{Store: store})
	defer svc.Close(context.Background())
	live, stop := svc.hub.SubscribeAOP("parent")
	defer stop()
	for i := 0; i < 70; i++ {
		svc.BroadcastAOPEvent("parent", &aop.Event{SessionId: "parent", Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{Value: &aop.MessageDelta_Text{Text: "live"}}}})
	}
	svc.BroadcastAOPEvent("parent", &aop.Event{Id: "turn-end", SessionId: "child", TurnId: "ended-turn", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}})
	payload, _ := anypb.New(&jev.RuntimeEvent{TaskId: "source-task", Background: true, Payload: &jev.RuntimeEvent_LibraryChange{LibraryChange: &jev.LibraryChange{State: "reflex_published", Reflex: &jev.ReflexDefinition{Id: "r", Observe: "js:original"}}}})
	event := &aop.Event{Id: "late-publication", SessionId: "child", TurnId: "ended-turn", Emitter: "jev", Payload: &aop.Event_Extension{Extension: payload}}
	if !isReliableAOPEvent(event) {
		t.Fatal("JEV evidence is droppable")
	}
	svc.BroadcastAOPEvent("parent", event)
	seen := false
	for len(live) > 0 {
		seen = (<-live).GetEvent().GetId() == event.Id || seen
	}
	if !seen {
		t.Fatal("late publication lost under backpressure")
	}
	stored, err := store.ListAOPEvents(t.Context(), "parent", 100)
	if err != nil || len(stored) != 2 || stored[1].SessionId != "child" || stored[1].TurnId != "ended-turn" || !proto.Equal(stored[1].GetExtension(), payload) {
		t.Fatalf("replay lost source identity or evidence: events=%v err=%v", stored, err)
	}
	restarted := NewService(ServiceConfig{Store: store})
	defer restarted.Close(context.Background())
	restarted.BroadcastAOPEvent("parent", event)
	response, err := restarted.api.Sessions.ListEvents(t.Context(), &aop.ListEventsRequest{SessionId: "parent", Limit: 100})
	if err != nil || len(response.GetEvents()) != 2 || !proto.Equal(response.GetEvents()[1].GetEvent(), stored[1]) {
		t.Fatalf("restart replay changed JEV history: %v", err)
	}
}
