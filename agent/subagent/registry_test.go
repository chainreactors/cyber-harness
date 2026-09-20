package subagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/registry"
	"github.com/chainreactors/cyber/core/resource"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return r
}

func identity(name string) Subagent {
	return Subagent{Name: name, Prepare: func(_ context.Context, cfg agent.Config, input Input) (agent.Config, string, error) {
		return cfg, input.Prompt, nil
	}}
}

func TestDynamicNamesAndAnonymousPreparation(t *testing.T) {
	r := newRegistry(t)
	if _, err := r.Start(t.Context(), agent.Config{}, Request{Name: "worker", Input: Input{Prompt: "task"}}); !errors.Is(err, registry.ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	var calls int
	worker := identity("worker")
	worker.Prepare = func(_ context.Context, cfg agent.Config, input Input) (agent.Config, string, error) {
		calls++
		cfg.Model = "child-model"
		return cfg, "prepared " + input.Prompt, nil
	}
	h, err := r.Add(worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(worker); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	cfg := agent.Config{Model: "parent-model"}
	run, err := r.Start(t.Context(), cfg, Request{Name: "worker", Label: "instance", Input: Input{Prompt: "task"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || run.Mode != Sync || run.Detail.AgentName != "instance" || run.Detail.AgentType != "worker" || run.Detail.Task != "prepared task" || run.Config.Model != "child-model" || cfg.Model != "parent-model" {
		t.Fatalf("run=%+v calls=%d", run, calls)
	}
	run.Finish()
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.Catalog()) != 0 {
		t.Fatal("withdrawn definition still advertised")
	}
	run, err = r.Start(t.Context(), cfg, Request{Input: Input{Prompt: "anonymous task"}})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Finish()
	if run.Mode != Async || run.Detail.AgentType != "" || calls != 1 || run.Config.Model != cfg.Model {
		t.Fatalf("anonymous=%+v calls=%d", run, calls)
	}
}

func TestWithdrawalCancelsAndDrainsWholeExecution(t *testing.T) {
	r := newRegistry(t)
	h, err := r.Add(identity("worker"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Start(t.Context(), agent.Config{}, Request{Name: "worker", Input: Input{Prompt: "task"}})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Finish()
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := h.Close(deadline); !errors.Is(err, resource.ErrCloseIncomplete) {
		t.Fatalf("released before execution finished: %v", err)
	}
	if run.Context.Err() == nil {
		t.Fatal("withdrawal did not cancel execution")
	}
	if len(r.Catalog()) != 0 {
		t.Fatal("draining worker is advertised")
	}
	if _, err := r.Start(t.Context(), agent.Config{}, Request{Name: "worker"}); !errors.Is(err, registry.ErrUnknown) {
		t.Fatalf("new admission: %v", err)
	}
	if _, err := r.Add(identity("worker")); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("replaced draining definition: %v", err)
	}
	run.Finish()
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(identity("worker")); err != nil {
		t.Fatalf("re-register after drain: %v", err)
	}
}

func TestWithdrawalCancelsPreparation(t *testing.T) {
	r := newRegistry(t)
	entered := make(chan struct{})
	var prepared atomic.Int32
	h, err := r.Add(Subagent{Name: "worker", Prepare: func(ctx context.Context, cfg agent.Config, _ Input) (agent.Config, string, error) {
		prepared.Add(1)
		close(entered)
		<-ctx.Done()
		return cfg, "", ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.Start(t.Context(), agent.Config{}, Request{Name: "worker"}); done <- err }()
	<-entered
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) || prepared.Load() != 1 {
		t.Fatalf("prepared=%d err=%v", prepared.Load(), err)
	}
}

func TestModesTimeoutAndPreparationFailureReleaseAdmission(t *testing.T) {
	r := newRegistry(t)
	_, err := r.Add(Subagent{Name: "panic", Prepare: func(context.Context, agent.Config, Input) (agent.Config, string, error) { panic("prepare") }})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{Name: "panic"}, {Input: Input{Prompt: " "}}, {Mode: "invalid"},
		{Mode: Async, Timeout: time.Second}, {Input: Input{Prompt: "text", Payload: "unsupported"}},
	} {
		if run, err := r.Start(t.Context(), agent.Config{}, request); err == nil {
			run.Finish()
			t.Fatalf("accepted %+v", request)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.Start(ctx, agent.Config{}, Request{Mode: Sync, Input: Input{Prompt: "task"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("sync canceled context: %v", err)
	}
	for _, mode := range []Mode{Async, Fork} {
		run, err := r.Start(ctx, agent.Config{}, Request{Mode: mode, Input: Input{Prompt: "task"}})
		if err != nil {
			t.Fatal(err)
		}
		if run.Context.Err() != nil {
			t.Fatal("background inherited invocation cancellation")
		}
		run.Finish()
	}
	run, err := r.Start(t.Context(), agent.Config{}, Request{Mode: Sync, Input: Input{Prompt: "task"}, Timeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	<-run.Context.Done()
	if !errors.Is(run.Context.Err(), context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", run.Context.Err())
	}
	run.Finish()
	deadline, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := r.Close(deadline); err != nil {
		t.Fatalf("leaked admission: %v", err)
	}
}

func TestCloseAlsoDrainsAnonymousRuns(t *testing.T) {
	r := newRegistry(t)
	run, err := r.Start(t.Context(), agent.Config{}, Request{Input: Input{Prompt: "task"}})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Finish()
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := r.Close(deadline); !errors.Is(err, resource.ErrCloseIncomplete) {
		t.Fatalf("close returned early: %v", err)
	}
	if run.Context.Err() == nil {
		t.Fatal("anonymous execution survived close")
	}
	if _, err := r.Start(t.Context(), agent.Config{}, Request{Input: Input{Prompt: "late"}}); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("late admission: %v", err)
	}
	run.Finish()
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
