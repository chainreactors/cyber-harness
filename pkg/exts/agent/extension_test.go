package agent_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coreagent "github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/extension"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
)

type loopFunc func(context.Context, coreagent.Config) (*coreagent.Result, error)

func (f loopFunc) Run(ctx context.Context, config coreagent.Config) (*coreagent.Result, error) {
	return f(ctx, config)
}

func load(t *testing.T, loop coreagent.Loop) (*agentext.Runtime, *extension.Set) {
	t.Helper()
	owner, err := agentext.New(loop)
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(extension.Entry{ID: "agent", Extension: owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return owner.Runtime(), set
}

func TestExtensionAdmitsLoopOnlyWhileActive(t *testing.T) {
	owner, err := agentext.New(loopFunc(func(_ context.Context, config coreagent.Config) (*coreagent.Result, error) {
		if _, ok := config.Loop.(*agentext.Runtime); !ok {
			t.Fatal("derived run bypassed Agent admission")
		}
		return &coreagent.Result{Output: config.SessionID}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	runtime := owner.Runtime()
	if _, err := runtime.Run(t.Context(), coreagent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("run before Load: %v", err)
	}
	set, err := extension.New(extension.Entry{ID: "agent", Extension: owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(t.Context(), coreagent.Config{SessionID: "one"})
	if err != nil || result.Output != "one" {
		t.Fatalf("active run = %v, %v", result, err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(t.Context(), coreagent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("run after Close: %v", err)
	}
}

func TestCloseCancelsAndDrainsAcceptedRuns(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	runtime, set := load(t, loopFunc(func(ctx context.Context, _ coreagent.Config) (*coreagent.Result, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan error, 1)
	go func() { _, err := runtime.Run(t.Context(), coreagent.Config{}); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := set.Close(ctx); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("Close while run active: %v", err)
	}
	<-canceled
	if _, err := runtime.Run(t.Context(), coreagent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("run admitted during drain: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run result: %v", err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatalf("Close retry: %v", err)
	}
}

func TestIndependentInstallationsDoNotShareLifecycle(t *testing.T) {
	loop := loopFunc(func(_ context.Context, config coreagent.Config) (*coreagent.Result, error) {
		return &coreagent.Result{Output: config.SessionID}, nil
	})
	first, firstSet := load(t, loop)
	second, _ := load(t, loop)
	if err := firstSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Run(t.Context(), coreagent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("closed installation remained active: %v", err)
	}
	result, err := second.Run(t.Context(), coreagent.Config{SessionID: "second"})
	if err != nil || result.Output != "second" {
		t.Fatalf("independent run = %v, %v", result, err)
	}
}

func TestNewRejectsMissingAndTypedNilLoops(t *testing.T) {
	var typedNil loopFunc
	for _, loop := range []coreagent.Loop{nil, typedNil} {
		if _, err := agentext.New(loop); err == nil {
			t.Fatal("nil loop was accepted")
		}
	}
}
