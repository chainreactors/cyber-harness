package sessionexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
)

type taskLoop func(context.Context, agent.Config) (*agent.Result, error)

func (f taskLoop) Run(ctx context.Context, cfg agent.Config) (*agent.Result, error) {
	return f(ctx, cfg)
}

func TestTaskLifecycleOrdering(t *testing.T) {
	for _, mode := range []string{"sync", "async", "fork"} {
		t.Run(mode, func(t *testing.T) {
			registry := hooks.New()
			var started, ended atomic.Bool
			var receiver func(context.Context, inbox.Message) error
			agenthooks.SessionStart.On(registry, "record", func(ctx context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
				if ev.ParentToolCallID == "" {
					return struct{}{}, nil
				}
				if ev.ParentID != "parent" || ev.ParentToolCallID != "spawn" || ev.Input != "task" || ev.Delegation.Task != "task" {
					return struct{}{}, fmt.Errorf("invalid task event: %+v", ev)
				}
				receiver = ev.Deliver
				started.Store(true)
				return struct{}{}, ev.Deliver(ctx, inbox.NewMessage(inbox.OriginPeer, "user", "peer"))
			})
			agenthooks.SessionEnd.On(registry, "record", func(ctx context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
				if ev.ParentToolCallID == "" {
					return struct{}{}, nil
				}
				if ev.Output != "result" {
					return struct{}{}, fmt.Errorf("missing final output")
				}
				if err := receiver(ctx, inbox.NewUserMessage("late")); err == nil {
					return struct{}{}, fmt.Errorf("receiver is still open")
				}
				ended.Store(true)
				return struct{}{}, nil
			})
			parent := agent.NewAgent(agent.Config{SessionID: "parent", Hooks: registry, Provider: &scriptedProvider{}, Loop: taskLoop(func(_ context.Context, cfg agent.Config) (*agent.Result, error) {
				if !started.Load() {
					return nil, fmt.Errorf("started before record")
				}
				messages := cfg.Inbox.Drain()
				if len(messages) != 2 || messages[0].Origin != inbox.OriginUser || messages[1].Origin != inbox.OriginPeer {
					return nil, fmt.Errorf("input order: %#v", messages)
				}
				return &agent.Result{Output: "result", Stop: agent.StopReasonCompleted}, nil
			})})

			tool := newSubagentTestTool(t, parent.Cfg)
			defer tool.closeRuntime(context.Background())
			ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
			result, err := tool.Execute(ctx, fmt.Sprintf(`{"mode":%q,"prompt":"task"}`, mode))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "sync" {
				if !ended.Load() || !strings.Contains(coretool.ResultText(result), "result") {
					t.Fatal("returned before record")
				}
			} else {
				wait, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				if !parent.Cfg.Inbox.Wait(wait) {
					t.Fatal("completion missing")
				}
				if !ended.Load() {
					t.Fatal("notified before result record")
				}
			}
		})
	}
}

func TestDispatchFailurePreventsEveryMode(t *testing.T) {
	for _, mode := range []string{"sync", "async", "fork"} {
		t.Run(mode, func(t *testing.T) {
			reg := hooks.New()
			failure := errors.New("record unavailable")
			var ends atomic.Int32
			agenthooks.SessionStart.On(reg, "deny", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
				if ev.ParentToolCallID == "" {
					return struct{}{}, nil
				}
				return struct{}{}, failure
			})
			agenthooks.SessionEnd.On(reg, "cleanup", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
				if ev.ParentToolCallID == "" {
					return struct{}{}, nil
				}
				ends.Add(1)
				return struct{}{}, nil
			})
			var ran atomic.Bool
			parent := agent.NewAgent(agent.Config{Hooks: reg, Provider: &scriptedProvider{}, Loop: taskLoop(func(context.Context, agent.Config) (*agent.Result, error) {
				ran.Store(true)
				return &agent.Result{}, nil
			})})

			tool := newSubagentTestTool(t, parent.Cfg)
			defer tool.closeRuntime(context.Background())
			ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
			if _, err := tool.Execute(ctx, fmt.Sprintf(`{"mode":%q,"prompt":"task"}`, mode)); !errors.Is(err, failure) {
				t.Fatalf("error: %v", err)
			}
			if ran.Load() || ends.Load() != 1 || parent.Cfg.Inbox.ActiveProducers() != 0 {
				t.Fatal("failed dispatch leaked work")
			}
		})
	}
}

func TestTaskLifetimeAndFinalRecordDrain(t *testing.T) {
	reg := hooks.New()
	parentCtx, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()
	entered, release := make(chan struct{}), make(chan struct{})
	agenthooks.SessionEnd.On(reg, "record", func(ctx context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
		if ev.ParentToolCallID == "" {
			return struct{}{}, nil
		}
		if ctx.Err() != nil || ev.Stop != agent.StopReasonCanceled {
			return struct{}{}, fmt.Errorf("invalid final context/stop")
		}
		close(entered)
		<-release
		return struct{}{}, nil
	})
	parent := agent.NewAgent(agent.Config{Lifetime: parentCtx, Hooks: reg, Provider: &scriptedProvider{}, Loop: taskLoop(func(ctx context.Context, _ agent.Config) (*agent.Result, error) { <-ctx.Done(); return nil, ctx.Err() })})
	ctx, cancelCall := context.WithCancel(operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"}))

	tool := newSubagentTestTool(t, parent.Cfg)
	if _, err := tool.Execute(ctx, `{"mode":"async","prompt":"task"}`); err != nil {
		t.Fatal(err)
	}
	cancelCall()
	select {
	case <-entered:
		t.Fatal("call cancellation stopped background task")
	default:
	}
	cancelParent()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("parent lifetime did not cancel child")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := tool.closeRuntime(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close: %v", err)
	}
	if parent.Cfg.Inbox.Len() != 0 {
		t.Fatal("notified before final record")
	}
	close(release)
	if err := tool.closeRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if parent.Cfg.Inbox.Len() != 1 {
		t.Fatal("missing completion")
	}
}

func TestSubagentMessageRemoved(t *testing.T) {

	tool := newSubagentTestTool(t, agent.Config{})
	if _, err := tool.Execute(t.Context(), `{"action":"message","label":"worker","message":"hi"}`); err == nil {
		t.Fatal("direct communication still enabled")
	}
}
