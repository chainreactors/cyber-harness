package session

import (
	"context"
	"errors"
	"testing"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/hooks"
)

func TestSessionHookFailureRollsBack(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	rt.hooks = hooks.New()
	failure := errors.New("denied")
	ends := 0
	agenthooks.SessionStart.On(rt.hooks, "fail", func(context.Context, agenthooks.SessionEvent) (struct{}, error) { return struct{}{}, failure })
	agenthooks.SessionEnd.On(rt.hooks, "cleanup", func(context.Context, agenthooks.SessionEvent) (struct{}, error) { ends++; return struct{}{}, nil })
	if _, err := rt.OpenSession(t.Context(), SessionOptions{ID: "failed"}); !errors.Is(err, failure) {
		t.Fatalf("open: %v", err)
	}
	rt.mu.RLock()
	remaining := len(rt.sessions)
	rt.mu.RUnlock()
	if ends != 1 || remaining != 0 {
		t.Fatalf("ends=%d sessions=%d", ends, remaining)
	}
}
func TestSessionReceiverCannotReachReplacement(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	rt.hooks = hooks.New()
	var first func(context.Context, inbox.Message) error
	agenthooks.SessionStart.On(rt.hooks, "borrow", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
		if first == nil {
			first = ev.Deliver
		}
		return struct{}{}, nil
	})
	if _, err := rt.OpenSession(t.Context(), SessionOptions{ID: "first", LogicalID: "chat"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.CloseSession(t.Context(), "chat", SessionCloseCleared); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.OpenSession(t.Context(), SessionOptions{ID: "second", LogicalID: "chat"}); err != nil {
		t.Fatal(err)
	}
	if err := first(t.Context(), inbox.NewUserMessage("late")); !errors.Is(err, inbox.ErrInboxClosed) {
		t.Fatalf("old receiver: %v", err)
	}
}
