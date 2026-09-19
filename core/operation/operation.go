// Package operation owns execution identity and cooperative cancellation.
// It is intentionally independent from agents, concrete tools, observation
// extensions and resource managers.
package operation

import (
	"context"
	"errors"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
)

var (
	ErrDenied      = errors.New("operation denied")
	ErrStartFailed = errors.New("operation start failed")
	ErrPanicked    = errors.New("operation panicked")
)

type panicError struct {
	kind string
	name string
}

func (e panicError) Error() string { return e.kind + " " + e.name + " failed unexpectedly" }
func (e panicError) Unwrap() error { return ErrPanicked }

// PanicError exposes a stable failure without leaking the recovered value or
// stack. The boundary logs those diagnostics privately before returning it.
func PanicError(kind, name string) error { return panicError{kind: kind, name: name} }

type invocationKey struct{}
type operationKey struct{}

// Invocation carries caller-owned correlation that must never become model or
// protocol arguments. Empty SessionID and TurnID are valid for direct calls.
type Invocation struct {
	WorkDir   string
	CallID    string
	SessionID string
	TurnID    string
	Emitter   string
	Progress  func([]byte)
}

func ContextWithInvocation(ctx context.Context, invocation Invocation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationKey{}, invocation)
}

func InvocationFromContext(ctx context.Context) Invocation {
	if ctx == nil {
		return Invocation{}
	}
	invocation, _ := ctx.Value(invocationKey{}).(Invocation)
	return invocation
}

func WorkDirFromContext(ctx context.Context, fallback string) string {
	if workDir := InvocationFromContext(ctx).WorkDir; workDir != "" {
		return workDir
	}
	return fallback
}

// Info identifies one real operation. ResourceID addresses a concrete native
// resource such as a retained PTY session and is distinct from OperationID.
type Info struct {
	OperationID       string
	ParentOperationID string
	ResourceID        string
	Kind              string
	Name              string
	Invocation        Invocation
}

type control struct {
	info   Info
	cancel context.CancelCauseFunc
}

func FromContext(ctx context.Context) Info {
	if ctx != nil {
		if current, ok := ctx.Value(operationKey{}).(control); ok {
			return current.info
		}
	}
	return Info{Invocation: InvocationFromContext(ctx)}
}

// Begin creates a child operation and a cooperative cancellation scope. The
// caller finishes it when the real execution boundary ends; resources retain
// ownership of OS processes, files and network flows.
func Begin(ctx context.Context, kind, name string) (context.Context, context.CancelCauseFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	parent := FromContext(ctx)
	call, cancel := context.WithCancelCause(ctx)
	info := Info{
		OperationID: aop.EnvelopeID(), ParentOperationID: parent.OperationID,
		Kind: kind, Name: name, Invocation: InvocationFromContext(ctx),
	}
	return context.WithValue(call, operationKey{}, control{info: info, cancel: cancel}), cancel
}

// ContextWithResource returns a context whose operation points at the native
// resource created for it without changing the local cancellation handle.
func ContextWithResource(ctx context.Context, resourceID string) context.Context {
	if ctx == nil {
		return context.Background()
	}
	current, ok := ctx.Value(operationKey{}).(control)
	if !ok {
		return ctx
	}
	current.info.ResourceID = resourceID
	return context.WithValue(ctx, operationKey{}, current)
}

// RequestCancel signals only the nearest managed operation. The actual owner
// performs resource-specific cancellation after hook callbacks return.
func RequestCancel(ctx context.Context, cause error) bool {
	if ctx == nil {
		return false
	}
	current, ok := ctx.Value(operationKey{}).(control)
	if !ok || current.cancel == nil {
		return false
	}
	current.cancel(cause)
	return true
}

// Correlation snapshots transport-safe operation identity. It never exposes the
// local cancel handle and never guesses a newer invocation for old activity.
func Correlation(ctx context.Context) *operationpb.Ref {
	current := FromContext(ctx)
	correlation := operationpb.Correlation_CORRELATION_UNATTRIBUTED
	if current.OperationID != "" || current.Invocation.CallID != "" {
		correlation = operationpb.Correlation_CORRELATION_EXPLICIT
	}
	return &operationpb.Ref{
		CallId: current.Invocation.CallID, OperationId: current.OperationID,
		ParentOperationId: current.ParentOperationID, ResourceId: current.ResourceID,
		Correlation: correlation,
	}
}
