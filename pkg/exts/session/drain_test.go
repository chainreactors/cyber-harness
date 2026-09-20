package session_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
)

func TestGraphDrainsSessionBeforeSharedLoopAndTools(t *testing.T) {
	f := apptest.NewFixture(t, nil, nil)
	f.Providers.Set(inertProvider{}, agent.ProviderConfig{Model: "test"})
	started, canceled := make(chan string, 2), make(chan string, 2)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	loop := loopext.New(loopFunc(func(ctx context.Context, c agent.Config) (*agent.Result, error) {
		started <- c.SessionID
		<-ctx.Done()
		canceled <- c.SessionID
		<-release
		return nil, ctx.Err()
	}))
	sessions := sessionext.New(session.Config{})
	var dependencyClosed atomic.Bool
	entries := append(apptest.Entries(t, f), promptext.New(), extension.Func{CloseFunc: func(context.Context) error { dependencyClosed.Store(true); return nil }}, loop, sessions)
	set := hosttest.Set(t, entries...)
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := sessions.Runtime().OpenSession(t.Context(), session.SessionOptions{ID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := current.Run(t.Context(), session.RunInput{Message: agent.TextInput("test")})
	if err != nil {
		t.Fatal(err)
	}
	direct := make(chan error, 1)
	go func() { _, err := loop.Loop().Run(t.Context(), agent.Config{SessionID: "direct"}); direct <- err }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("execution did not start")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := set.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close: %v", err)
	}
	select {
	case id := <-canceled:
		if id != "session" {
			t.Fatalf("closed dependency before session: %s", id)
		}
	case <-time.After(time.Second):
		t.Fatal("session not canceled")
	}
	if dependencyClosed.Load() {
		t.Fatal("dependency closed before drain")
	}
	if _, err := sessions.Runtime().OpenSession(t.Context(), session.SessionOptions{ID: "late"}); err == nil {
		t.Fatal("admitted during close")
	}
	once.Do(func() { close(release) })
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-direct; !errors.Is(err, context.Canceled) {
		t.Fatalf("direct: %v", err)
	}
	if !dependencyClosed.Load() {
		t.Fatal("dependency not closed")
	}
	if _, err := loop.Loop().Run(t.Context(), agent.Config{}); !errors.Is(err, loopext.ErrUnavailable) {
		t.Fatalf("late loop: %v", err)
	}
}
