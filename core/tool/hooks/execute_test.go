package hooks

import (
	"context"
	"errors"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/operation"
	"github.com/chainreactors/aiscan/core/tool"
)

func TestExecutionCompletion(t *testing.T) {
	for _, mode := range []string{"success", "denied", "before-panic", "panic", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r := hooks.New()
			calls, completions := 0, 0
			Before.On(r, "policy", func(ctx context.Context, _ CallEvent) (Admission, error) {
				if mode == "before-panic" {
					panic("private")
				}
				if mode == "denied" {
					return Admission{Deny: errors.New("nope")}, nil
				}
				return Admission{}, nil
			})
			Completed.On(r, "observe", func(ctx context.Context, c Completion) (struct{}, error) {
				completions++
				if !c.StartedAt.IsZero() != (calls != 0) {
					t.Error("incorrect execution status")
				}
				if c.Operation.GetOperationId() == "" || c.Result.CallId == "" {
					t.Error("missing identity")
				}
				if (c.Err == nil) != (mode == "success") {
					t.Errorf("completion error: %v", c.Err)
				}
				return struct{}{}, nil
			})
			result, err := Execute(t.Context(), r, "test", "{}", func(ctx context.Context, _ string) (*tool.Result, error) {
				calls++
				switch mode {
				case "panic":
					panic("private")
				case "error":
					return nil, errors.New("failed")
				case "cancel":
					operation.RequestCancel(ctx, errors.New("policy canceled"))
				}
				return tool.TextResult("ok"), nil
			})
			if completions != 1 {
				t.Fatalf("completions = %d", completions)
			}
			if mode == "denied" || mode == "before-panic" {
				if calls != 0 || !errors.Is(err, operation.ErrDenied) {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
			}
			if result.IsError != (mode != "success") {
				t.Fatal("incorrect terminal error status")
			}
		})
	}
}

func TestAfterPreservesContentAndCannotEraseCancellation(t *testing.T) {
	r := hooks.New()
	After.On(r, "transform", func(_ context.Context, event ResultEvent) (struct{}, error) {
		event.Result.IsError = false
		event.Result.Terminate = true
		return struct{}{}, nil
	})
	want := errors.New("canceled by policy")
	result, err := Execute(t.Context(), r, "test", "{}", func(ctx context.Context, _ string) (*tool.Result, error) {
		operation.RequestCancel(ctx, want)
		return &tool.Result{Output: []*aop.Content{aop.Text("committed before cancellation"), aop.Text("second block")}}, nil
	})
	if !errors.Is(err, want) || !result.IsError || !result.Terminate || len(result.Output) != 2 {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestBeforeCannotModifyInvocationArguments(t *testing.T) {
	r := hooks.New()
	Before.On(r, "observer", func(_ context.Context, e CallEvent) (Admission, error) {
		e.Call.Arguments.Data[0] = '!'
		e.Call.Name = "other"
		return Admission{}, nil
	})
	_, err := Execute(t.Context(), r, "test", "{}", func(_ context.Context, args string) (*tool.Result, error) {
		if args != "{}" {
			t.Fatal("arguments were mutated")
		}
		return tool.TextResult("ok"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
