package operation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/aiscan/core/operation"
)

func TestNestedOperationsPreserveInvocationAndCancelNearestScope(t *testing.T) {
	base := operation.ContextWithInvocation(t.Context(), operation.Invocation{
		CallID: "call-1", SessionID: "session-1", TurnID: "turn-1", Emitter: "test",
	})
	parent, finishParent := operation.Begin(base, "tool", "write")
	defer finishParent(nil)
	child, finishChild := operation.Begin(parent, "file", "write")
	defer finishChild(nil)
	child = operation.ContextWithResource(child, "resource-1")

	parentRef := operation.Correlation(parent)
	childRef := operation.Correlation(child)
	if parentRef.GetOperationId() == "" || childRef.GetOperationId() == "" || childRef.GetOperationId() == parentRef.GetOperationId() {
		t.Fatalf("operation identities = parent %v, child %v", parentRef, childRef)
	}
	if childRef.GetParentOperationId() != parentRef.GetOperationId() || childRef.GetResourceId() != "resource-1" || childRef.GetCallId() != "call-1" {
		t.Fatalf("child correlation = %v", childRef)
	}

	want := errors.New("stop child")
	if !operation.RequestCancel(child, want) || !errors.Is(context.Cause(child), want) {
		t.Fatalf("child cancellation = %v", context.Cause(child))
	}
	if parent.Err() != nil {
		t.Fatalf("child cancellation escaped to parent: %v", parent.Err())
	}
}

func TestPanicErrorIsStableAndClassifiable(t *testing.T) {
	err := operation.PanicError("tool", "secret")
	if err.Error() != "tool secret failed unexpectedly" || !errors.Is(err, operation.ErrPanicked) {
		t.Fatalf("panic error = %v", err)
	}
}
