package web

import (
	"context"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
)

func sessionEvent(t *testing.T, sessionID string, event *aop.Event) *aop.Event {
	t.Helper()
	event.SessionId = sessionID
	event.Emitter = "test-agent"
	return event
}

func forwardEvent(t *testing.T, pool *AgentPool, remote *remoteAgent, taskID string, event *aop.Event) {
	t.Helper()
	pool.forwardAOPFrame(remote, taskID, event)
}

func newChatTaskRemote() (*remoteAgent, chan taskResult) {
	remote := &remoteAgent{
		nodeState: newNodeState(),
		nodeID: "agent-1",
		name:    "worker",
	}
	ch := make(chan taskResult, 1)
	remote.tasks["task-1"] = ch
	remote.turns["task-1"] = 0
	return remote, ch
}

func readResult(t *testing.T, ch chan taskResult) taskResult {
	t.Helper()
	select {
	case res, ok := <-ch:
		if !ok {
			t.Fatal("task channel closed without a result")
		}
		return res
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task result")
		return taskResult{}
	}
}

func assertTaskOpen(t *testing.T, remote *remoteAgent, ch chan taskResult) {
	t.Helper()
	select {
	case res, ok := <-ch:
		t.Fatalf("task closed unexpectedly: res=%+v ok=%v", res, ok)
	default:
	}
	remote.mu.Lock()
	_, registered := remote.tasks["task-1"]
	remote.mu.Unlock()
	if !registered {
		t.Fatal("task was removed from the registry")
	}
}

func TestChatTaskConvergesOnTurnEnd(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "agent-session", &aop.Event{TurnId: "task-1", Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}}))
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "agent-session", &aop.Event{TurnId: "task-1", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}}))

	res := readResult(t, ch)
	if res.Err != "" {
		t.Fatalf("err = %q, want empty", res.Err)
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after the result")
	}
}

func TestChatTaskTurnEndErrorPopulatesErr(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	// A mid-run AOP error is display-only; the terminal turn.end carries
	// the failure.
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "agent-session", &aop.Event{TurnId: "task-1", Payload: &aop.Event_Error{Error: &aop.ProtocolError{Message: "boom"}}}))
	assertTaskOpen(t, remote, ch)

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "agent-session", &aop.Event{
		TurnId: "task-1",
		Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{
			StopReason: "error", Error: &aop.ProtocolError{Message: "boom"},
		}},
	}))
	res := readResult(t, ch)
	if res.Err != "boom" {
		t.Fatalf("err = %q, want %q", res.Err, "boom")
	}
}

func TestChatTaskCanceledTurnEndHasNoErr(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	// The agent reports the ctx error on cancel; it must not surface as a task error.
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "agent-session", &aop.Event{
		TurnId: "task-1",
		Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{
			StopReason: "canceled", Error: &aop.ProtocolError{Message: "context canceled"},
		}},
	}))
	res := readResult(t, ch)
	if res.Err != "" {
		t.Fatalf("err = %q, want empty for canceled run", res.Err)
	}
}

func TestChildSessionEndDoesNotConvergeTask(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "child-1", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{ParentSessionId: "agent-session"}}}))
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "child-1", &aop.Event{Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: "completed"}}}))
	assertTaskOpen(t, remote, ch)

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, "agent-session", &aop.Event{TurnId: "task-1", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}}))
	readResult(t, ch)
}

func TestTaskConvergesOnceWhenTurnEndAndCompleteArrive(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	event := sessionEvent(t, "agent-session", &aop.Event{TurnId: "task-1", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}})
	forwardEvent(t, pool, remote, "task-1", event)
	res := readResult(t, ch)
	if res.Err != "" {
		t.Fatalf("err = %q, want empty", res.Err)
	}

	// Duplicate terminal events must be idempotent.
	forwardEvent(t, pool, remote, "task-1", event)
	if _, ok := <-ch; ok {
		t.Fatal("channel delivered a second result")
	}
}

func TestDisconnectedAcceptedTurnEmitsOneTerminalEvent(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/chat.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "session-1")
	service := NewService(ServiceConfig{Store: store})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	remote := &remoteAgent{
		nodeState: newNodeState(),
		nodeID: "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 2),
		done: make(chan struct{}),
	}
	pool.agents[remote.nodeID] = remote
	session, _ := store.GetSession(context.Background(), "session-1")
	if session != nil {
		if session.Session == nil {
			session.Session = &aop.Session{}
		}
		session.Session.NodeId = remote.nodeID
		_ = store.UpdateSession(context.Background(), session)
	}
	service.handleAgentRun("session-1", &aop.RunTurnRequest{
		SessionId: "session-1", TurnId: "turn-1",
		Input: &aop.Message{Role: "user", Content: []*aop.Content{aop.Text("hello")}},
	})
	pool.unregister(remote)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events, _ := store.ListAOPEvents(context.Background(), "session-1", 10)
		if len(events) == 1 {
			ended := events[0].GetTurnEnded()
			if events[0].TurnId != "turn-1" || events[0].Seq != 1 || ended == nil || ended.Error.GetCode() != "agent_disconnected" {
				t.Fatalf("terminal event = %+v", events[0])
			}
			service.BroadcastAOPEvent("session-1", &aop.Event{SessionId: "session-1", TurnId: "turn-1", Seq: 2, Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}})
			after, _ := store.ListAOPEvents(context.Background(), "session-1", 10)
			if len(after) != 1 {
				t.Fatalf("late duplicate terminal persisted: %+v", after)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("disconnect terminal event was not persisted")
}
