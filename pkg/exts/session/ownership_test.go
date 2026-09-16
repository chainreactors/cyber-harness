package session_test

import (
	"context"
	"github.com/chainreactors/cyber/agent"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	"testing"
)

type loopFunc func(context.Context, agent.Config) (*agent.Result, error)

func (f loopFunc) Run(ctx context.Context, c agent.Config) (*agent.Result, error) { return f(ctx, c) }
func TestSessionCloseDoesNotCloseBorrowedLoop(t *testing.T) {
	l := loopext.New(loopFunc(func(context.Context, agent.Config) (*agent.Result, error) { return &agent.Result{Output: "alive"}, nil }))
	s, err := sessionext.New(agentsession.Config{Loop: l.Runtime()})
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(l, s)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Runtime().OpenSession(t.Context(), agentsession.SessionOptions{ID: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if result, err := l.Runtime().Run(t.Context(), agent.Config{}); err != nil || result.Output != "alive" {
		t.Fatalf("borrowed loop: %v %v", result, err)
	}
}
