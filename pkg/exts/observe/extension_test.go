package observe_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	operationpb "github.com/chainreactors/aiscan/aop/operation"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/operation"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	observe "github.com/chainreactors/aiscan/pkg/exts/observe"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/chainreactors/aiscan/tools/files"
)

func TestObservePublishesOneCorrelatedAOPStream(t *testing.T) {
	hookRegistry := hooks.New()
	stream := coreevents.New()
	observer, err := observe.New(hookRegistry, stream, observe.Options{Kinds: []observe.Kind{observe.Tools, observe.Files}})
	if err != nil {
		t.Fatal(err)
	}
	registry := toolset.NewRegistry(hookRegistry)
	fileTools, err := fileext.New(registry, hookRegistry, files.Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(
		extension.Entry{ID: "observe", Extension: observer},
		extension.Entry{ID: "files", DependsOn: []string{"observe"}, Extension: fileTools},
		extension.Entry{ID: "tools", DependsOn: []string{"files"}, Extension: registry},
	)
	if err != nil {
		t.Fatal(err)
	}
	var events []*aop.Event
	sub := stream.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events = append(events, event) }))
	defer sub.Cancel()
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{
		CallID: "call-1", SessionID: "session-1", TurnID: "turn-1", Emitter: "test",
	})
	if _, err := registry.ExecuteTool(ctx, "write", `{"path":"note","content":"committed"}`); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want started, access, completed", len(events))
	}

	refs := make([]*operationpb.Ref, len(events))
	for i, event := range events {
		ref := new(operationpb.Ref)
		if ok, err := aop.FindTypedExtension(event, ref); err != nil || !ok {
			t.Fatalf("event %d operation: ok=%v err=%v", i, ok, err)
		}
		if ref.GetCallId() != "call-1" || ref.GetOperationId() == "" {
			t.Fatalf("event %d operation = %v", i, ref)
		}
		if event.GetSessionId() != "session-1" || event.GetTurnId() != "turn-1" || event.GetEmitter() != "test" {
			t.Fatalf("event %d invocation metadata = %v", i, event)
		}
		refs[i] = ref
	}
	if refs[0].GetOperationId() != refs[2].GetOperationId() {
		t.Fatalf("tool lifecycle operation changed: %v", refs)
	}
	if refs[1].GetOperationId() == refs[0].GetOperationId() || refs[1].GetParentOperationId() != refs[0].GetOperationId() {
		t.Fatalf("file access is not a child of the tool operation: %v", refs)
	}

	started := new(operationpb.Started)
	access := new(filepb.Access)
	completed := new(operationpb.Completed)
	if err := events[0].GetExtension().UnmarshalTo(started); err != nil || started.GetKind() != "tool" || started.GetName() != "write" {
		t.Fatalf("started = %v, %v", started, err)
	}
	if err := events[1].GetExtension().UnmarshalTo(access); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("committed"))
	if access.GetOp() != filepb.AccessOp_ACCESS_OP_CREATE || access.GetDigest() != hex.EncodeToString(digest[:]) {
		t.Fatalf("access = %v", access)
	}
	if err := events[2].GetExtension().UnmarshalTo(completed); err != nil || completed.GetKind() != "tool" || completed.GetStartedAt() == nil || completed.GetFailure() != nil {
		t.Fatalf("completed = %v, %v", completed, err)
	}
}

func TestObserveRejectsInvalidSelection(t *testing.T) {
	stream := coreevents.New()
	if _, err := observe.New(hooks.New(), stream, observe.Options{Kinds: []observe.Kind{"unknown"}}); err == nil {
		t.Fatal("accepted unknown observation kind")
	}
	if _, err := observe.New(hooks.New(), stream, observe.Options{Kinds: []observe.Kind{observe.Files, observe.Files}}); err == nil {
		t.Fatal("accepted duplicate observation kind")
	}
	if _, err := observe.New(nil, stream, observe.Options{}); err == nil {
		t.Fatal("accepted missing hook registry")
	}
	if err := (*observe.Extension)(nil).Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
