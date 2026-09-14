package session

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/inbox"
)

func TestDeliveryGatesRoutingAndCapacity(t *testing.T) {
	message := inbox.NewMessage(inbox.OriginPeer, "user", "peer input")
	if err := (&Runtime{}).Deliver(t.Context(), message); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unloaded delivery = %v", err)
	}
	rt := newBareRuntime(t, nil, nil)
	if err := rt.Deliver(t.Context(), message); err == nil {
		t.Fatal("delivery created a session")
	}
	first, err := rt.OpenSession(t.Context(), SessionOptions{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	first.state.inbox.setActive(true)
	if err := rt.Deliver(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if first.state.inbox.Len() != 1 {
		t.Fatal("sole session did not receive input")
	}
	_, err = rt.OpenSession(t.Context(), SessionOptions{ID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Deliver(t.Context(), message); err == nil {
		t.Fatal("ambiguous session accepted input")
	}
	primary, err := rt.OpenSession(t.Context(), SessionOptions{ID: rt.primarySessionID})
	if err != nil {
		t.Fatal(err)
	}
	primary.state.inbox.setActive(true)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := rt.Deliver(canceled, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delivery = %v", err)
	}
	for range agent.DefaultInboxCapacity {
		if err := rt.Deliver(t.Context(), message); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.Deliver(t.Context(), message); err == nil {
		t.Fatal("full inbox accepted input")
	}
	if first.state.inbox.Len() != 1 {
		t.Fatal("delivery escaped primary route")
	}
	if err := rt.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := rt.Deliver(t.Context(), message); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed delivery = %v", err)
	}
}

func TestDeliveryAndShutdownShareAdmission(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	session, err := rt.OpenSession(t.Context(), SessionOptions{ID: "only"})
	if err != nil {
		t.Fatal(err)
	}
	session.state.inbox.setActive(true)
	start := make(chan struct{})
	var callers sync.WaitGroup
	for range 32 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			_ = rt.Deliver(context.Background(), inbox.NewSystemMessage("concurrent"))
		}()
	}
	close(start)
	if err := rt.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	callers.Wait()
	if err := rt.Deliver(t.Context(), inbox.NewSystemMessage("late")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("late input = %v", err)
	}
}
