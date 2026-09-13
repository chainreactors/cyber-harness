// Package hooks defines typed tool and managed-operation boundaries. It has no
// dependency on agents, concrete tools, output implementations or observation extensions.
package hooks

import (
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	operationpb "github.com/chainreactors/aiscan/aop/operation"
	trafficpb "github.com/chainreactors/aiscan/aop/traffic"
	corehooks "github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/tool"
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

var Before = corehooks.Point[CallEvent, Admission]{
	Kind:    "tool.before",
	OnError: corehooks.FailClosed,
	Reduce:  corehooks.StopWhen[CallEvent](func(a Admission) bool { return a.Deny != nil }),
}

var Started = corehooks.Point[CallEvent, struct{}]{Kind: "tool.started"}

// After receives one private copy of the complete returned result. Handlers may
// transform that value directly and in registration order. If a handler fails,
// Execute discards the copy and reports the failure; terminal error and stop
// flags can only become stricter when the copy is committed.
var After = corehooks.Point[ResultEvent, struct{}]{
	Kind:    "tool.after",
	OnError: corehooks.FailClosed,
}

var Completed = corehooks.Point[Completion, struct{}]{Kind: "tool.completed"}

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

var BeforeCommand = corehooks.Point[CommandEvent, Admission]{
	Kind:    "command.before",
	OnError: corehooks.FailClosed,
	Reduce:  corehooks.StopWhen[CommandEvent](func(a Admission) bool { return a.Deny != nil }),
}

var CommandStarted = corehooks.Point[CommandEvent, struct{}]{Kind: "command.started"}
var CommandCompleted = corehooks.Point[CommandCompletion, struct{}]{Kind: "command.completed"}

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

var BeforeProcess = corehooks.Point[ProcessEvent, Admission]{
	Kind:    "process.before",
	OnError: corehooks.FailClosed,
	Reduce:  corehooks.StopWhen[ProcessEvent](func(a Admission) bool { return a.Deny != nil }),
}

// ProcessStarting runs after admission and before OS creation. Observers use it
// for preparation such as filesystem snapshots. Completion pairs it even when
// process creation fails.
var ProcessStarting = corehooks.Point[ProcessEvent, struct{}]{Kind: "process.starting"}

// ProcessStartedControl is the synchronous cancellation boundary. A policy can
// request cancellation, but cannot undo effects that happened before startup.
var ProcessStartedControl = corehooks.Point[ProcessEvent, Cancellation]{
	Kind:    "process.started.control",
	OnError: corehooks.FailClosed,
	Reduce:  corehooks.StopWhen[ProcessEvent](func(c Cancellation) bool { return c.Cause != nil }),
}

var ProcessStartedObserved = corehooks.Point[ProcessEvent, struct{}]{Kind: "process.started"}
var ProcessCompleted = corehooks.Point[ProcessCompletion, struct{}]{Kind: "process.completed"}

// FileEvent.Data is borrowed until synchronous dispatch returns. Consumers
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

var FileAccessControl = corehooks.Point[FileEvent, Cancellation]{
	Kind:    "file.access.control",
	OnError: corehooks.FailClosed,
	Reduce:  corehooks.StopWhen[FileEvent](func(c Cancellation) bool { return c.Cause != nil }),
}

var FileAccessObserved = corehooks.Point[FileEvent, struct{}]{Kind: "file.access"}

type FlowEvent struct {
	Operation *operationpb.Ref
	Flow      *trafficpb.Flow
}

var FlowCompletedControl = corehooks.Point[FlowEvent, Cancellation]{
	Kind:    "http.completed.control",
	OnError: corehooks.FailClosed,
	Reduce:  corehooks.StopWhen[FlowEvent](func(c Cancellation) bool { return c.Cause != nil }),
}

var FlowCompletedObserved = corehooks.Point[FlowEvent, struct{}]{Kind: "http.completed"}
