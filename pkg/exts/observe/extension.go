// Package observe converts public execution hooks into typed AOP observations.
package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	ptyext "github.com/chainreactors/cyber/pkg/exts/pty"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Kind string

const (
	Tools     Kind = "tools"
	Commands  Kind = "commands"
	Processes Kind = "processes"
	Files     Kind = "files"
	HTTP      Kind = "http"
)

type Options struct {
	Kinds  []Kind
	File   FileOptions
	Logger telemetry.Logger
}

type Extension struct {
	hooks  *corehooks.Registry
	events *coreevents.Stream
	kinds  map[Kind]bool

	mu        sync.RWMutex
	file      FileOptions
	subs      []*corehooks.Subscription
	snapshots map[string]Snapshot
	loaded    bool
	closing   bool
	closed    bool
	logger    telemetry.Logger
}

var _ extension.Extension = (*Extension)(nil)

func New(registry *corehooks.Registry, stream *coreevents.Stream, options Options) (*Extension, error) {
	if registry == nil || stream == nil {
		return nil, fmt.Errorf("observe requires shared hooks and AOP stream")
	}
	kinds := make(map[Kind]bool, len(options.Kinds))
	for _, kind := range options.Kinds {
		switch kind {
		case Tools, Commands, Processes, Files, HTTP:
			if kinds[kind] {
				return nil, fmt.Errorf("duplicate observation kind %q", kind)
			}
			kinds[kind] = true
		default:
			return nil, fmt.Errorf("unknown observation kind %q", kind)
		}
	}
	fileOptions := options.File
	if fileOptions.MaxEntries == 0 && fileOptions.Ignore == nil && !fileOptions.Enabled {
		fileOptions = defaultFileOptions()
	}
	logger := options.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Extension{
		hooks: registry, events: stream, kinds: kinds, file: fileOptions,
		snapshots: make(map[string]Snapshot), logger: logger,
	}, nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.closing {
		return fmt.Errorf("observe is closed")
	}
	if e.loaded {
		return nil
	}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	e.loaded = true
	const source = "observe"
	if e.kinds[Tools] {
		e.subs = append(e.subs,
			toolhooks.Started.On(e.hooks, source, e.toolStarted),
			toolhooks.Completed.On(e.hooks, source, e.toolCompleted),
		)
	}
	if e.kinds[Commands] {
		e.subs = append(e.subs,
			toolhooks.CommandStarted.On(e.hooks, source, e.commandStarted),
			toolhooks.CommandCompleted.On(e.hooks, source, e.commandCompleted),
		)
	}
	if e.kinds[Processes] || e.kinds[Files] {
		e.subs = append(e.subs,
			toolhooks.ProcessStarting.On(e.hooks, source, e.processStarting),
			toolhooks.ProcessStartedObserved.On(e.hooks, source, e.processStarted),
			toolhooks.ProcessCompleted.On(e.hooks, source, e.processCompleted),
		)
	}
	if e.kinds[Files] {
		e.subs = append(e.subs, toolhooks.FileAccessObserved.On(e.hooks, source, e.fileAccess))
	}
	if e.kinds[HTTP] {
		e.subs = append(e.subs, toolhooks.FlowCompletedObserved.On(e.hooks, source, e.flowCompleted))
	}
	return nil
}

func eventFor(ctx context.Context, payload proto.Message, correlation *operationpb.Ref, sidecars ...proto.Message) (*aop.Event, error) {
	encoded, err := anypb.New(payload)
	if err != nil {
		return nil, err
	}
	invocation := operation.InvocationFromContext(ctx)
	event := &aop.Event{
		SessionId: invocation.SessionID, TurnId: invocation.TurnID, Emitter: invocation.Emitter,
		Payload: &aop.Event_Extension{Extension: encoded},
	}
	if correlation != nil {
		if err := aop.SetTypedExtension(event, correlation); err != nil {
			return nil, err
		}
	}
	for _, sidecar := range sidecars {
		if sidecar != nil {
			if err := aop.SetTypedExtension(event, sidecar); err != nil {
				return nil, err
			}
		}
	}
	return event, nil
}

func (e *Extension) emit(ctx context.Context, payload proto.Message, correlation *operationpb.Ref, sidecars ...proto.Message) {
	event, err := eventFor(ctx, payload, correlation, sidecars...)
	if err != nil {
		e.logger.Warnf("observe encode: %v", err)
		return
	}
	// Publication is synchronous at the hook boundary. Cancel prevents new
	// callback admission; callbacks already admitted are drained by Close and
	// must still publish their observation while that drain is in progress.
	e.events.Publish(event)
}

func (e *Extension) toolStarted(ctx context.Context, event toolhooks.CallEvent) (struct{}, error) {
	e.emit(ctx, &operationpb.Started{Kind: "tool", Name: event.Call.GetName()}, event.Operation)
	return struct{}{}, nil
}

func (e *Extension) toolCompleted(ctx context.Context, event toolhooks.Completion) (struct{}, error) {
	e.emitCompleted(ctx, "tool", event.Call.GetName(), event.Lifecycle)
	return struct{}{}, nil
}

func (e *Extension) commandStarted(ctx context.Context, event toolhooks.CommandEvent) (struct{}, error) {
	e.emit(ctx, &operationpb.Started{Kind: "command", Name: event.Name}, event.Operation)
	return struct{}{}, nil
}

func (e *Extension) commandCompleted(ctx context.Context, event toolhooks.CommandCompletion) (struct{}, error) {
	e.emitCompleted(ctx, "command", event.Command.Name, event.Lifecycle)
	return struct{}{}, nil
}

func (e *Extension) processStarting(ctx context.Context, event toolhooks.ProcessEvent) (struct{}, error) {
	// Snapshot preparation is deliberately synchronous with the real pre-start
	// boundary; no work is performed when file observation is not selected.
	if e.kinds[Files] {
		e.processSnapshot(ctx, event)
	}
	return struct{}{}, nil
}

func (e *Extension) processStarted(ctx context.Context, event toolhooks.ProcessEvent) (struct{}, error) {
	if e.kinds[Processes] {
		e.emit(ctx, &operationpb.Started{Kind: "process", Name: event.Command}, event.Operation)
	}
	return struct{}{}, nil
}

func (e *Extension) processCompleted(ctx context.Context, event toolhooks.ProcessCompletion) (struct{}, error) {
	if e.kinds[Files] {
		e.finishSnapshot(ctx, event)
	}
	if e.kinds[Processes] {
		if event.Session != nil {
			e.emitCompleted(ctx, "process", event.Process.Command, event.Lifecycle, ptyext.SessionToProto(event.Session))
		} else {
			e.emitCompleted(ctx, "process", event.Process.Command, event.Lifecycle)
		}
	}
	return struct{}{}, nil
}

func (e *Extension) fileAccess(ctx context.Context, event toolhooks.FileEvent) (struct{}, error) {
	access := &filepb.Access{
		Id: aop.EnvelopeID(), Op: event.Op, Source: event.Source, Path: event.Path,
		WorkDir: event.Directory, Size: event.Size, Bytes: int64(len(event.Data)), Edits: event.Edits,
		Timestamp: timestamppb.Now(),
	}
	if event.Err != nil {
		access.Error = event.Err.Error()
	} else if event.Op == filepb.AccessOp_ACCESS_OP_WRITE || event.Op == filepb.AccessOp_ACCESS_OP_CREATE || event.Op == filepb.AccessOp_ACCESS_OP_EDIT {
		sum := sha256.Sum256(event.Data)
		access.Digest = hex.EncodeToString(sum[:])
	}
	e.emit(ctx, access, event.Operation)
	return struct{}{}, nil
}

func (e *Extension) flowCompleted(ctx context.Context, event toolhooks.FlowEvent) (struct{}, error) {
	// ProxyHub gives this boundary an owned, hydrated copy after FlowStore commit.
	e.emit(ctx, event.Flow, event.Operation)
	return struct{}{}, nil
}

func (e *Extension) emitCompleted(ctx context.Context, kind, name string, lifecycle toolhooks.Lifecycle, sidecars ...proto.Message) {
	completed := &operationpb.Completed{Kind: kind, Name: name}
	if !lifecycle.StartedAt.IsZero() {
		completed.StartedAt = timestamppb.New(lifecycle.StartedAt)
	}
	if lifecycle.Err != nil {
		completed.Failure = failure(lifecycle.Err)
	}
	e.emit(ctx, completed, lifecycle.Operation, sidecars...)
}

func failure(err error) *operationpb.Failure {
	kind := operationpb.FailureKind_FAILURE_KIND_ERROR
	switch {
	case errors.Is(err, operation.ErrDenied):
		kind = operationpb.FailureKind_FAILURE_KIND_DENIED
	case errors.Is(err, operation.ErrStartFailed):
		kind = operationpb.FailureKind_FAILURE_KIND_START_FAILED
	case errors.Is(err, operation.ErrPanicked):
		kind = operationpb.FailureKind_FAILURE_KIND_PANIC
	case errors.Is(err, context.DeadlineExceeded):
		kind = operationpb.FailureKind_FAILURE_KIND_TIMEOUT
	case errors.Is(err, context.Canceled):
		kind = operationpb.FailureKind_FAILURE_KIND_CANCELED
	}
	return &operationpb.Failure{Kind: kind, Message: err.Error()}
}

func (e *Extension) processSnapshot(ctx context.Context, event toolhooks.ProcessEvent) {
	if !e.fileOptions().Enabled {
		return
	}
	id := event.Operation.GetOperationId()
	before, err := TakeSnapshot(event.Directory, e.fileOptions())
	e.mu.Lock()
	if err == nil && !e.closed {
		e.snapshots[id] = before
	}
	e.mu.Unlock()
	if err != nil {
		e.snapshotError(ctx, event.Operation, event.Directory, err)
	}
}

func (e *Extension) finishSnapshot(ctx context.Context, event toolhooks.ProcessCompletion) {
	id := event.Operation.GetOperationId()
	e.mu.Lock()
	before, ok := e.snapshots[id]
	delete(e.snapshots, id)
	e.mu.Unlock()
	if !ok {
		return
	}
	after, err := TakeSnapshot(event.Process.Directory, e.fileOptions())
	if err != nil {
		e.snapshotError(ctx, event.Operation, event.Process.Directory, err)
		return
	}
	for _, change := range DiffSnapshots(before, after) {
		e.emit(ctx, &filepb.Access{
			Id: aop.EnvelopeID(), Op: change.Op, Source: filepb.AccessSource_ACCESS_SOURCE_SNAPSHOT,
			Path: change.Path, WorkDir: event.Process.Directory, Size: change.Size, Timestamp: timestamppb.Now(),
		}, event.Operation)
	}
}

func (e *Extension) snapshotError(ctx context.Context, correlation *operationpb.Ref, directory string, err error) {
	e.emit(ctx, &filepb.Access{
		Id: aop.EnvelopeID(), Source: filepb.AccessSource_ACCESS_SOURCE_SNAPSHOT,
		Path: directory, WorkDir: directory, Error: err.Error(), Timestamp: timestamppb.Now(),
	}, correlation)
}

func (e *Extension) fileOptions() FileOptions {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := e.file
	result.Ignore = append([]string(nil), result.Ignore...)
	return result
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	e.closing = true
	subs := append([]*corehooks.Subscription(nil), e.subs...)
	e.mu.Unlock()
	for _, sub := range subs {
		sub.Cancel()
	}
	var closeErr error
	for _, sub := range subs {
		if err := sub.Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	if closeErr != nil {
		return closeErr
	}
	e.mu.Lock()
	if !e.closed {
		e.closed = true
	}
	pendingSnapshots := len(e.snapshots)
	e.mu.Unlock()
	if pendingSnapshots > 0 {
		closeErr = errors.Join(closeErr, fmt.Errorf("observe incomplete: %d process snapshots unresolved", pendingSnapshots))
	}
	return closeErr
}
