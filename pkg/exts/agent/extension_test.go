package agent_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
	agentext "github.com/chainreactors/cyber/pkg/exts/agent"
)

type loopFunc func(context.Context, agent.Config) (*agent.Result, error)

func TestLoopOnlyInstallationDoesNotRequireSessionHost(t *testing.T) {
	value := agentext.New(loopFunc(func(_ context.Context, config agent.Config) (*agent.Result, error) {
		return &agent.Result{Output: config.SessionID}, nil
	}))
	var loop agent.Loop
	borrow := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		loop, err = extension.Use[agent.Loop](scope)
		return err
	}}
	set, err := extension.New(value, borrow)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(t.Context(), agent.Config{SessionID: "standalone"})
	if err != nil || result.Output != "standalone" {
		t.Fatalf("standalone loop: %v, %v", result, err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("closed loop: %v", err)
	}
}

func (f loopFunc) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	return f(ctx, config)
}

func load(t *testing.T, loop agent.Loop) (*agentext.Loop, *extension.Set) {
	t.Helper()
	value := newLoopExtension(loop)
	set, err := extension.New(value)
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
	return value.Loop(), set
}

func TestAdmissionAndInitializationLifetime(t *testing.T) {
	var calls int
	var loop *agentext.Loop
	value := newLoopExtension(loopFunc(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		calls++
		if config.Loop != loop {
			t.Fatal("derived configs bypass the installed lifecycle")
		}
		return &agent.Result{Output: config.SessionID}, ctx.Err()
	}))
	loop = value.Loop()
	if _, err := loop.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("run before Load: %v", err)
	}
	set, err := extension.New(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	init, cancel := context.WithCancel(t.Context())
	if err := set.Load(init); err != nil {
		t.Fatal(err)
	}
	cancel()
	result, err := loop.Run(t.Context(), agent.Config{SessionID: "first"})
	if err != nil || result.Output != "first" || calls != 1 {
		t.Fatalf("run after init cancellation: %v, %v, calls=%d", result, err, calls)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) || calls != 1 {
		t.Fatalf("run after Close: %v, calls=%d", err, calls)
	}
}

func TestCloseCancelsAndRetainsDependenciesUntilDrain(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	var dependencyClosed atomic.Bool
	value := newLoopExtension(loopFunc(func(ctx context.Context, _ agent.Config) (*agent.Result, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release // Cancellation acknowledgement is not resource completion.
		return nil, ctx.Err()
	}))
	loop := value.Loop()
	set, err := extension.New(
		extension.Func{CloseFunc: func(context.Context) error {
			dependencyClosed.Store(true)
			return nil
		}},
		value,
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
	go func() { _, err := loop.Run(t.Context(), agent.Config{}); runDone <- err }()
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
	if _, err := loop.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
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
			value := newLoopExtension(loop)
			set, err := extension.New(value)
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

func newLoopExtension(loop agent.Loop) *agentext.Extension {
	return agentext.New(loop)
}
