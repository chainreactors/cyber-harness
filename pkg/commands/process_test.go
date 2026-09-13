package commands

import (
	"context"
	"errors"
	"testing"

	corehooks "github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/operation"
	toolhooks "github.com/chainreactors/aiscan/core/tool/hooks"
)

func TestProcessHooksFollowActualCompletion(t *testing.T) {
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	commands, _ := loadTestRegistry(t, commandGroup("wait", "test", Command{Name: "wait_for_test", Run: func(ctx context.Context, _ *Execution) (any, error) {
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}))
	registry := corehooks.New()
	bash := NewBashTool(t.TempDir(), 30, registry)
	bash.SetCommandRegistry(commands)
	defer bash.Close()
	starting := make(chan toolhooks.ProcessEvent, 1)
	completed := make(chan toolhooks.ProcessCompletion, 1)
	startSub := toolhooks.ProcessStarting.On(registry, "test", func(_ context.Context, event toolhooks.ProcessEvent) (struct{}, error) {
		starting <- event
		return struct{}{}, nil
	})
	completeSub := toolhooks.ProcessCompleted.On(registry, "test", func(_ context.Context, event toolhooks.ProcessCompletion) (struct{}, error) {
		completed <- event
		return struct{}{}, nil
	})
	defer startSub.Close(context.Background())
	defer completeSub.Close(context.Background())

	execution, err := bash.Start(t.Context(), "wait_for_test", BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := <-starting
	if before.Operation.GetOperationId() == "" || before.Directory != bash.workDir {
		t.Fatalf("before-start: %+v", before)
	}
	select {
	case event := <-completed:
		t.Fatalf("tool return mistaken for exit: %+v", event)
	default:
	}
	close(release)
	if err := execution.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := <-completed
	if after.Operation.GetOperationId() != before.Operation.GetOperationId() || after.Err != nil || after.Session == nil {
		t.Fatalf("after-exit: %+v", after)
	}
	bash.Close()
	if _, err := bash.Start(t.Context(), "wait_for_test", BashExecOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("start after Close: %v", err)
	}
}

func TestFailedStartCompletesProcessHook(t *testing.T) {
	registry := corehooks.New()
	bash := NewBashTool(t.TempDir(), 30, registry)
	defer bash.Close()
	starting := make(chan toolhooks.ProcessEvent, 1)
	completed := make(chan toolhooks.ProcessCompletion, 1)
	toolhooks.ProcessStarting.On(registry, "test", func(_ context.Context, event toolhooks.ProcessEvent) (struct{}, error) {
		starting <- event
		return struct{}{}, nil
	})
	toolhooks.ProcessCompleted.On(registry, "test", func(_ context.Context, event toolhooks.ProcessCompletion) (struct{}, error) {
		completed <- event
		return struct{}{}, nil
	})
	if _, err := bash.Start(t.Context(), "", BashExecOptions{}); !errors.Is(err, operation.ErrStartFailed) {
		t.Fatalf("accepted empty command: %v", err)
	}
	before, after := <-starting, <-completed
	if before.Operation.GetOperationId() != after.Operation.GetOperationId() || after.Err == nil || after.Session != nil {
		t.Fatalf("failed start was not paired: %+v %+v", before, after)
	}
}

func TestProcessStartedPolicyCanCancelOnlyCurrentExecution(t *testing.T) {
	registry := corehooks.New()
	bash := NewBashTool(t.TempDir(), 30, registry)
	defer bash.Close()
	want := errors.New("stop this process")
	toolhooks.ProcessStartedControl.On(registry, "policy", func(_ context.Context, event toolhooks.ProcessEvent) (toolhooks.Cancellation, error) {
		if event.Command == "cancel-me" {
			return toolhooks.Cancellation{Cause: want}, nil
		}
		return toolhooks.Cancellation{}, nil
	})
	if _, err := bash.Start(t.Context(), "cancel-me", BashExecOptions{}); !errors.Is(err, want) {
		t.Fatalf("cancellation = %v", err)
	}
}
