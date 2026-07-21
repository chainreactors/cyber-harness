package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
)

func sessionEvent(t *testing.T, typ, sessionID string, data any) aop.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return aop.Event{
		Type:      typ,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		Agent:     "test-agent",
		Data:      raw,
	}
}

func forwardEvent(t *testing.T, pool *AgentPool, remote *remoteAgent, taskID string, ev aop.Event) {
	t.Helper()
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	pool.forwardAOPEvent(remote, WSMessage{Type: "aop", TaskID: taskID, Payload: payload})
}

func newChatTaskRemote() (*remoteAgent, chan taskResult) {
	remote := &remoteAgent{
		id:    "agent-1",
		name:  "worker",
		tasks: map[string]chan taskResult{},
		turns: map[string]int{},
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

func TestChatTaskConvergesOnRootSessionEnd(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeTurnStart, "agent-session", aop.TurnData{Turn: 2}))
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeSessionEnd, "agent-session", aop.SessionEndData{Stop: "completed", Turns: 2}))

	res := readResult(t, ch)
	if res.Turn != 2 {
		t.Fatalf("turn = %d, want 2", res.Turn)
	}
	if res.Err != "" {
		t.Fatalf("err = %q, want empty", res.Err)
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after the result")
	}
}

func TestChatTaskSessionEndErrorPopulatesErr(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	// A mid-run AOP error is display-only; the terminal session.end carries
	// the failure.
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeError, "agent-session", aop.ErrorData{Message: "boom"}))
	assertTaskOpen(t, remote, ch)

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeSessionEnd, "agent-session", aop.SessionEndData{Stop: "error", Error: "boom"}))
	res := readResult(t, ch)
	if res.Err != "boom" {
		t.Fatalf("err = %q, want %q", res.Err, "boom")
	}
}

func TestChatTaskCanceledSessionEndHasNoErr(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	// The agent reports the ctx error on cancel; it must not surface as a task error.
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeSessionEnd, "agent-session", aop.SessionEndData{Stop: "canceled", Error: "context canceled"}))
	res := readResult(t, ch)
	if res.Err != "" {
		t.Fatalf("err = %q, want empty for canceled run", res.Err)
	}
}

func TestChildSessionEndDoesNotConvergeTask(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeSessionStart, "child-1", aop.SessionStartData{ParentSessionID: "agent-session"}))
	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeSessionEnd, "child-1", aop.SessionEndData{Stop: "completed"}))
	assertTaskOpen(t, remote, ch)

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeSessionEnd, "agent-session", aop.SessionEndData{Stop: "completed"}))
	readResult(t, ch)
}

func TestTaskConvergesOnceWhenSessionEndAndCompleteArrive(t *testing.T) {
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(&evalSink{sid: "sess-1"})
	remote, ch := newChatTaskRemote()

	forwardEvent(t, pool, remote, "task-1", sessionEvent(t, aop.TypeSessionEnd, "agent-session", aop.SessionEndData{Stop: "completed"}))
	res := readResult(t, ch)
	if res.Err != "" {
		t.Fatalf("err = %q, want empty", res.Err)
	}

	// A leftover complete frame (mixed-version agent) must be a no-op.
	pool.handleAgentMessage(remote, WSMessage{Type: "complete", TaskID: "task-1"})
	if _, ok := <-ch; ok {
		t.Fatal("channel delivered a second result")
	}
}
