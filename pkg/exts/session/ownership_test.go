package session_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/apptest"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
)

type loopFunc func(context.Context, agent.Config) (*agent.Result, error)

func (f loopFunc) Run(ctx context.Context, c agent.Config) (*agent.Result, error) { return f(ctx, c) }

type inertProvider struct{}

func (inertProvider) Name() string { return "inert" }
func (inertProvider) ChatCompletion(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return nil, errors.New("unexpected model request")
}

func TestSessionCloseDoesNotCloseBorrowedLoop(t *testing.T) {
	application := &apppkg.App{}
	l := loopext.New(loopFunc(func(context.Context, agent.Config) (*agent.Result, error) { return &agent.Result{Output: "alive"}, nil }))
	s := sessionext.New(agentsession.Config{})
	set, err := extension.New(append(apptest.Entries(t, application), l, s)...)
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
	if result, err := l.Loop().Run(t.Context(), agent.Config{}); err != nil || result.Output != "alive" {
		t.Fatalf("borrowed loop: %v %v", result, err)
	}
}

// The installed order is load-bearing: the managed loop must enter the graph
// before the session resource that borrows it.
func TestLoopPrecedesSessionInCompositionOrder(t *testing.T) {
	managed := make(chan *loopext.Loop, 1)
	selected := loopFunc(func(_ context.Context, config agent.Config) (*agent.Result, error) {
		loop, ok := config.Loop.(*loopext.Loop)
		if !ok {
			return nil, errors.New("session bypassed the managed Agent loop")
		}
		if config.Inbox != nil {
			config.Inbox.Drain()
		}
		managed <- loop
		return &agent.Result{Stop: agent.StopReasonCompleted}, nil
	})
	application := &apppkg.App{}
	application.SetProvider(inertProvider{}, agent.ProviderConfig{})
	loop := loopext.New(selected)
	sessions := sessionext.New(agentsession.Config{Application: application})
	set, err := extension.New(append(apptest.Entries(t, application), loop, sessions)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })

	// The runtime is built during Load, so before that there is nothing to
	// admit work at all -- a stronger guarantee than refusing it.
	if sessions.Runtime() != nil {
		t.Fatal("the session runtime existed before the composition loaded")
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Runtime().OpenSession(t.Context(), agentsession.SessionOptions{ID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(t.Context(), agentsession.RunInput{Message: agent.TextInput("test")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	installed := <-managed
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := installed.Run(t.Context(), agent.Config{}); !errors.Is(err, loopext.ErrUnavailable) {
		t.Fatalf("Agent loop remained available after close: %v", err)
	}
	if _, err := sessions.Runtime().OpenSession(t.Context(), agentsession.SessionOptions{ID: "late"}); err == nil {
		t.Fatal("Session admitted work after close")
	}
}

func TestRuntimeDoesNotExposeResourceLifecycle(t *testing.T) {
	runtime := reflect.TypeFor[*agentsession.Runtime]()
	for _, method := range []string{"Start", "Close"} {
		if _, exists := runtime.MethodByName(method); exists {
			t.Errorf("session Runtime exposes owner method %s", method)
		}
	}
	resource := reflect.TypeFor[*agentsession.Resource]()
	for _, method := range []string{"Start", "Close", "Runtime"} {
		if _, exists := resource.MethodByName(method); !exists {
			t.Errorf("session Resource is missing owner method %s", method)
		}
	}
}
