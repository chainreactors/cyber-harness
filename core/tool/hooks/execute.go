package hooks

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/tool"
	"google.golang.org/protobuf/proto"
)

// Check converts the result of a fail-closed admission point into the boundary
// error returned to its caller. Handler failures and explicit denial remain
// separately inspectable through errors.Is/errors.As.
func Check(admission Admission, handlerErr error) error {
	if handlerErr != nil {
		return errors.Join(operation.ErrDenied, handlerErr)
	}
	if admission.Deny != nil {
		return errors.Join(operation.ErrDenied, admission.Deny)
	}
	return nil
}

// CancellationCause converts a during-stage control response to the cause that
// should be sent to the local managed operation.
func CancellationCause(cancellation Cancellation, handlerErr error) error {
	if handlerErr != nil && cancellation.Cause != nil {
		return errors.Join(handlerErr, cancellation.Cause)
	}
	if handlerErr != nil {
		return handlerErr
	}
	return cancellation.Cause
}

// Execute is the single tool invocation boundary. Agents, ToolNode and direct
// callers all enter here; it does not publish protocol ToolCall/ToolResult
// events, whose ownership remains with their existing producers.
func Execute(ctx context.Context, registry *corehooks.Registry, name, arguments string, run func(context.Context, string) (*tool.Result, error)) (result *tool.Result, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	invocation := operation.InvocationFromContext(ctx)
	if invocation.CallID == "" {
		invocation.CallID = aop.EnvelopeID()
		ctx = operation.ContextWithInvocation(ctx, invocation)
	}
	ctx, cancel := operation.Begin(ctx, "tool", name)
	defer cancel(nil)

	correlation := operation.Correlation(ctx)
	call := &aop.ToolCall{
		Id:               invocation.CallID,
		Name:             name,
		WorkingDirectory: invocation.WorkDir,
		Arguments:        &aop.EncodedValue{Data: []byte(arguments), MediaType: aop.JSONMediaType},
	}
	var startedAt time.Time

	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.Join(err, operation.PanicError("tool", name))
			slog.ErrorContext(ctx, "tool panicked", "tool", name, "error", recovered, "stack", string(debug.Stack()))
		}
		if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
			err = errors.Join(err, cause)
		}
		if result == nil {
			result = &tool.Result{}
		}
		if err != nil {
			result.IsError = true
			if len(result.Output) == 0 {
				result.Output = []*aop.Content{aop.Text(err.Error())}
			}
		}
		endedAt := time.Now()
		result.CallId = invocation.CallID
		result.Name = name
		if !startedAt.IsZero() {
			result.DurationMs = uint64(endedAt.Sub(startedAt).Milliseconds())
		}
		if Completed.Has(registry) {
			corehooks.Notify(context.WithoutCancel(ctx), registry, Completed, Completion{
				Lifecycle: Lifecycle{Operation: cloneCorrelation(correlation), StartedAt: startedAt, EndedAt: endedAt, Err: err},
				Call:      proto.Clone(call).(*aop.ToolCall),
				Result:    proto.Clone(result).(*tool.Result),
			})
		}
	}()

	if Before.Has(registry) {
		admission, hookErr := Before.Emit(ctx, registry, CallEvent{Call: proto.Clone(call).(*aop.ToolCall), Operation: cloneCorrelation(correlation)})
		if err = Check(admission, hookErr); err != nil {
			return nil, err
		}
	}
	if err = context.Cause(ctx); err != nil {
		return nil, err
	}

	startedAt = time.Now()
	if Started.Has(registry) {
		corehooks.Notify(ctx, registry, Started, CallEvent{Call: proto.Clone(call).(*aop.ToolCall), Operation: cloneCorrelation(correlation)})
	}
	if err = context.Cause(ctx); err != nil {
		return nil, err
	}

	result, err = run(ctx, arguments)
	if result == nil {
		result = &tool.Result{}
	}
	if After.Has(registry) {
		wasError, wasTerminate := result.IsError, result.Terminate
		transformed := proto.Clone(result).(*tool.Result)
		_, hookErr := After.Emit(ctx, registry, ResultEvent{
			Call: proto.Clone(call).(*aop.ToolCall), Operation: cloneCorrelation(correlation), Result: transformed,
		})
		if hookErr == nil {
			// Result transforms are monotonic for terminal state. A policy may
			// fail or terminate success, never disguise an existing failure.
			transformed.IsError = transformed.IsError || wasError
			transformed.Terminate = transformed.Terminate || wasTerminate
			result = transformed
		} else {
			err = errors.Join(err, hookErr)
		}
	}
	return result, err
}

func cloneCorrelation(correlation *operationpb.Ref) *operationpb.Ref {
	if correlation == nil {
		return nil
	}
	return proto.Clone(correlation).(*operationpb.Ref)
}
