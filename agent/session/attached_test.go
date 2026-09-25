package session

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/cyber/agent"
	aop "github.com/chainreactors/cyber/aop"
)

func TestAttachedSessionClosesBeforeParentInboxWithoutDelegation(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	parent, err := rt.OpenSession(t.Context(), SessionOptions{ID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	var ended atomic.Bool
	rt.events.Observe(func(ev *aop.Event) {
		if ev.SessionId == "child" && ev.GetSessionEnded() != nil {
			ended.Store(true)
		}
	})
	var called atomic.Int32
	_, err = rt.OpenSession(t.Context(), SessionOptions{ID: "child", ParentSessionID: parent.ID(), Attached: true, SingleTask: true, OnClosed: func(outcome Outcome) {
		called.Add(1)
		if !ended.Load() || parent.state.inbox.Closed() || !outcome.Started {
			t.Error("invalid close ordering")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.CloseSession(t.Context(), parent.ID(), SessionCloseCanceled); err != nil {
		t.Fatal(err)
	}
	if called.Load() != 1 || !parent.state.inbox.Closed() {
		t.Fatal("parent returned before child callback")
	}
	if _, err := rt.OpenSession(t.Context(), SessionOptions{ParentSessionID: parent.ID(), Attached: true}); err == nil {
		t.Fatal("attached to closed parent")
	}
}

func TestSingleTaskDoesNotScheduleLateInput(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	var calls atomic.Int32
	cfg := rt.agentConfig.WithLoop(taskLoop(func(context.Context, agent.Config) (*agent.Result, error) {
		calls.Add(1)
		return &agent.Result{Output: "done"}, nil
	}))
	s, err := rt.OpenSession(t.Context(), SessionOptions{ID: "single", Config: &cfg, SingleTask: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(t.Context(), RunInput{Message: agent.TextInput("task")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if s.state.inbox.automatic != nil || calls.Load() != 1 {
		t.Fatal("single task enables automatic execution")
	}
}

func TestCloseCallbackPanicDoesNotStrandSession(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	s, err := rt.OpenSession(t.Context(), SessionOptions{ID: "panic", OnClosed: func(Outcome) { panic("callback") }})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.CloseSession(t.Context(), s.ID(), SessionCloseCompleted); err == nil || !strings.Contains(err.Error(), "callback panicked") {
		t.Fatalf("close=%v", err)
	}
	if _, err := rt.OpenSession(t.Context(), SessionOptions{ID: "panic"}); err != nil {
		t.Fatalf("identity leaked: %v", err)
	}
}
