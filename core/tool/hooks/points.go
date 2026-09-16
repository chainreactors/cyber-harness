// Package hooks defines typed tool and managed-operation boundaries. It has no
// dependency on agents, concrete tools, output implementations or observation extensions.
package hooks

import (
	"time"

	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	trafficpb "github.com/chainreactors/cyber/aop/traffic"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/utils/pty"
)

type Admission struct {
	Deny error
}

type Cancellation struct {
	Cause error
}

type Lifecycle struct {
	Operation *operationpb.Ref
	StartedAt time.Time
	EndedAt   time.Time
	Err       error
}

type CallEvent struct {
	Call      *aop.ToolCall
	Operation *operationpb.Ref
}

type ResultEvent struct {
	Call      *aop.ToolCall
	Operation *operationpb.Ref
	Result    *tool.Result
}

type Completion struct {
	Lifecycle
	Call   *aop.ToolCall
	Result *tool.Result
}

var Before = corehooks.NewPoint[CallEvent, Admission]("tool.before").
	WithErrorPolicy(corehooks.FailClosed).
	WithReducer(corehooks.StopWhen[CallEvent](func(a Admission) bool { return a.Deny != nil }))

var Started = corehooks.NewPoint[CallEvent, struct{}]("tool.started")

// After receives one private copy of the complete returned result. Handlers may
// transform that value directly and in registration order. If a handler fails,
// Execute discards the copy and reports the failure; terminal error and stop
// flags can only become stricter when the copy is committed.
var After = corehooks.NewPoint[ResultEvent, struct{}]("tool.after").WithErrorPolicy(corehooks.FailClosed)

var Completed = corehooks.NewPoint[Completion, struct{}]("tool.completed")

type CommandEvent struct {
	Operation *operationpb.Ref
	Name      string
	Args      []string
	Directory string
}

type CommandCompletion struct {
	Lifecycle
	Command CommandEvent
}

var BeforeCommand = corehooks.NewPoint[CommandEvent, Admission]("command.before").
	WithErrorPolicy(corehooks.FailClosed).
	WithReducer(corehooks.StopWhen[CommandEvent](func(a Admission) bool { return a.Deny != nil }))

var CommandStarted = corehooks.NewPoint[CommandEvent, struct{}]("command.started")
var CommandCompleted = corehooks.NewPoint[CommandCompletion, struct{}]("command.completed")

type ProcessEvent struct {
	Operation *operationpb.Ref
	Directory string
	Command   string
}

type ProcessCompletion struct {
	Lifecycle
	Process ProcessEvent
	Session *pty.Info
}

var BeforeProcess = corehooks.NewPoint[ProcessEvent, Admission]("process.before").
	WithErrorPolicy(corehooks.FailClosed).
	WithReducer(corehooks.StopWhen[ProcessEvent](func(a Admission) bool { return a.Deny != nil }))

// ProcessStarting runs after admission and before OS creation. Observers use it
// for preparation such as filesystem snapshots. Completion pairs it even when
// process creation fails.
var ProcessStarting = corehooks.NewPoint[ProcessEvent, struct{}]("process.starting")

// ProcessStartedControl is the synchronous cancellation boundary. A policy can
// request cancellation, but cannot undo effects that happened before startup.
var ProcessStartedControl = corehooks.NewPoint[ProcessEvent, Cancellation]("process.started.control").
	WithErrorPolicy(corehooks.FailClosed).
	WithReducer(corehooks.StopWhen[ProcessEvent](func(c Cancellation) bool { return c.Cause != nil }))

var ProcessStartedObserved = corehooks.NewPoint[ProcessEvent, struct{}]("process.started")
var ProcessCompleted = corehooks.NewPoint[ProcessCompletion, struct{}]("process.completed")

// FileEvent.Data is valid until synchronous dispatch returns. Consumers
// retaining it must copy it. Expensive digesting only belongs in an installed
// observer.
type FileEvent struct {
	Operation *operationpb.Ref
	Op        filepb.AccessOp
	Source    filepb.AccessSource
	Path      string
	Directory string
	Data      []byte
	Size      int64
	Edits     uint32
	Err       error
}

var FileAccessControl = corehooks.NewPoint[FileEvent, Cancellation]("file.access.control").
	WithErrorPolicy(corehooks.FailClosed).
	WithReducer(corehooks.StopWhen[FileEvent](func(c Cancellation) bool { return c.Cause != nil }))

var FileAccessObserved = corehooks.NewPoint[FileEvent, struct{}]("file.access")

type FlowEvent struct {
	Operation *operationpb.Ref
	Flow      *trafficpb.Flow
}

var FlowCompletedControl = corehooks.NewPoint[FlowEvent, Cancellation]("http.completed.control").
	WithErrorPolicy(corehooks.FailClosed).
	WithReducer(corehooks.StopWhen[FlowEvent](func(c Cancellation) bool { return c.Cause != nil }))

var FlowCompletedObserved = corehooks.NewPoint[FlowEvent, struct{}]("http.completed")
