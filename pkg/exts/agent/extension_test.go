package agent_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	agentext "github.com/chainreactors/cyber/pkg/exts/agent"
	"github.com/chainreactors/cyber/pkg/toolset"
)

type loopFunc func(context.Context, agent.Config) (*agent.Result, error)

func TestLoopOnlyInstallationDoesNotRequireSessionHost(t *testing.T) {
	value, err := agentext.New(agentext.Config{Loop: loopFunc(func(_ context.Context, config agent.Config) (*agent.Result, error) {
		return &agent.Result{Output: config.SessionID}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(extension.Entry{ID: "agent", Extension: value})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime := value.Runtime()
	result, err := runtime.Run(t.Context(), agent.Config{SessionID: "standalone"})
	if err != nil || result.Output != "standalone" {
		t.Fatalf("standalone loop: %v, %v", result, err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("closed loop: %v", err)
	}
}

func (f loopFunc) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	return f(ctx, config)
}

func load(t *testing.T, loop agent.Loop) (*agentext.Runtime, *extension.Set) {
	t.Helper()
	value, application := newLoopExtension(loop)
	set, err := extension.New(
		extension.Entry{ID: "application", Extension: application},
		extension.Entry{ID: "agent", DependsOn: []string{"application"}, Extension: value},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := set.Close(ctx); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	return value.Runtime(), set
}

func TestAdmissionAndInitializationLifetime(t *testing.T) {
	var calls int
	var runtime *agentext.Runtime
	value, application := newLoopExtension(loopFunc(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		calls++
		if config.Loop != runtime {
			t.Fatal("derived configs bypass the installed lifecycle")
		}
		return &agent.Result{Output: config.SessionID}, ctx.Err()
	}))
	runtime = value.Runtime()
	if _, err := runtime.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("run before Load: %v", err)
	}
	set, err := extension.New(
		extension.Entry{ID: "application", Extension: application},
		extension.Entry{ID: "agent", DependsOn: []string{"application"}, Extension: value},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	init, cancel := context.WithCancel(t.Context())
	if err := set.Load(init); err != nil {
		t.Fatal(err)
	}
	cancel()
	result, err := runtime.Run(t.Context(), agent.Config{SessionID: "first"})
	if err != nil || result.Output != "first" || calls != 1 {
		t.Fatalf("run after init cancellation: %v, %v, calls=%d", result, err, calls)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) || calls != 1 {
		t.Fatalf("run after Close: %v, calls=%d", err, calls)
	}
}

func TestCloseCancelsAndRetainsDependenciesUntilDrain(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	var dependencyClosed atomic.Bool
	value, application := newLoopExtension(loopFunc(func(ctx context.Context, _ agent.Config) (*agent.Result, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release // Cancellation acknowledgement is not resource completion.
		return nil, ctx.Err()
	}))
	runtime := value.Runtime()
	set, err := extension.New(
		extension.Entry{ID: "resource", Extension: extension.Func{CloseFunc: func(context.Context) error {
			dependencyClosed.Store(true)
			return nil
		}}},
		extension.Entry{ID: "application", DependsOn: []string{"resource"}, Extension: application},
		extension.Entry{ID: "agent", DependsOn: []string{"application"}, Extension: value},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblock.Do(func() { close(release) })
		_ = set.Close(context.Background())
	})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { _, err := runtime.Run(t.Context(), agent.Config{}); runDone <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("loop did not start")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := set.Close(deadline); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("close with work in flight: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("extension did not cancel the loop")
	}
	if dependencyClosed.Load() {
		t.Fatal("released a dependency before its loop drained")
	}
	if _, err := runtime.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("accepted work while draining: %v", err)
	}
	unblock.Do(func() { close(release) })
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("run result: %v", err)
	}
	if err := set.Close(t.Context()); err != nil || !dependencyClosed.Load() {
		t.Fatalf("close retry: %v, dependency closed=%v", err, dependencyClosed.Load())
	}
}

func TestCallerCancellationDoesNotStopOtherSessions(t *testing.T) {
	started := make(chan string, 2)
	value, set := load(t, loopFunc(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		started <- config.SessionID
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	first, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := value.Run(first, agent.Config{SessionID: "first"}); firstDone <- err }()
	go func() { _, err := value.Run(t.Context(), agent.Config{SessionID: "second"}); secondDone <- err }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("sessions did not run concurrently")
		}
	}
	cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v", err)
	}
	select {
	case err := <-secondDone:
		t.Fatalf("caller cancellation stopped another session: %v", err)
	default:
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("second run: %v", err)
	}
}

func TestIndependentInstallationCanCloseWithoutStoppingAnother(t *testing.T) {
	loop := loopFunc(func(_ context.Context, config agent.Config) (*agent.Result, error) {
		return &agent.Result{Output: config.SessionID}, nil
	})
	first, firstSet := load(t, loop)
	second, _ := load(t, loop)
	if err := firstSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("closed installation still admits calls: %v", err)
	}
	result, err := second.Run(t.Context(), agent.Config{SessionID: "independent"})
	if err != nil || result.Output != "independent" {
		t.Fatalf("closing another installation stopped this one: %v, %v", result, err)
	}
}

func TestFailedLoadAndPanickingLoopReleaseOwnership(t *testing.T) {
	for name, loop := range map[string]agent.Loop{"typed nil loop": loopFunc(nil)} {
		t.Run(name, func(t *testing.T) {
			value, application := newLoopExtension(loop)
			set, err := extension.New(
				extension.Entry{ID: "application", Extension: application},
				extension.Entry{ID: "agent", DependsOn: []string{"application"}, Extension: value},
			)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close(context.Background())
			if err := set.Load(t.Context()); err == nil {
				t.Fatal("installed an implicit default loop")
			}
			if err := set.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("panic", func(t *testing.T) {
		value, set := load(t, loopFunc(func(context.Context, agent.Config) (*agent.Result, error) {
			panic("loop failed")
		}))
		func() {
			defer func() {
				if recover() == nil {
					t.Error("loop panic was hidden")
				}
			}()
			_, _ = value.Run(t.Context(), agent.Config{})
		}()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := set.Close(ctx); err != nil {
			t.Fatalf("panicking call did not drain: %v", err)
		}
	})
}

func newLoopExtension(loop agent.Loop) (*agentext.Extension, *apppkg.Resource) {
	hookRegistry := hooks.New()
	application, err := apppkg.New(apppkg.Config{SkipEngines: true}, apppkg.AppServices{
		Hooks: hookRegistry, Events: events.New(),
		Commands: commands.NewRegistry(hookRegistry), Tools: toolset.NewRegistry(hookRegistry),
	})
	if err != nil {
		panic(err)
	}
	value, err := agentext.New(agentext.Config{Loop: loop})
	if err != nil {
		panic(err)
	}
	return value, application
}
