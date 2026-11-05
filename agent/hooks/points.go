package hooks

import (
	"context"

	"github.com/chainreactors/cyber/agent/inbox"
	aop "github.com/chainreactors/cyber/aop"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/types"
)

// StopReason lives here rather than in agent because run_end events carry
// it.
type StopReason string

const (
	StopReasonCompleted  StopReason = "completed"
	StopReasonTerminated StopReason = "terminated"
	StopReasonStopped    StopReason = "stopped"
	StopReasonBudget     StopReason = "budget"
	StopReasonError      StopReason = "error"
	StopReasonCanceled   StopReason = "canceled"
)

// RunStartEvent carries the config context a handler needs in flattened form,
// since the event type cannot reference *agent.Config.
type RunStartEvent struct {
	SessionID    string
	TurnID       string
	AgentName    string
	Model        string
	Turn         int
	SystemPrompt string
	ToolNames    []string
}

// RunStartResult replaces the system prompt (nil = keep) and prepends messages
// to the turn.
type RunStartResult struct {
	SystemPrompt *string
	Prepend      []*aop.Message
}

var BeforeRun = corehooks.NewPoint[RunStartEvent, RunStartResult]("before_run").WithReducer(
	corehooks.Fold(func(acc *RunStartResult, ev *RunStartEvent, out RunStartResult) {
		if out.SystemPrompt != nil {
			// Fold into the event so the next handler edits the new prompt.
			ev.SystemPrompt = *out.SystemPrompt
			acc.SystemPrompt = out.SystemPrompt
		}
		acc.Prepend = append(acc.Prepend, out.Prepend...)
	}),
)

type ContextEvent struct {
	SessionID string
	TurnID    string
	Turn      int
	Messages  []*aop.Message
}

// BeforeModel runs once at a decision boundary, outside model retry and request
// serialization. Its result can only append messages to the durable transcript.
// The input is an isolated snapshot; handlers cannot edit the existing prefix.
var BeforeModel = corehooks.NewPoint[ContextEvent, []*aop.Message]("before_model").WithReducer(
	corehooks.Fold(func(acc *[]*aop.Message, _ *ContextEvent, out []*aop.Message) {
		*acc = append(*acc, out...)
	}),
)

// AfterModel observes each accepted, complete model output once, outside retry.
// Messages ends with that assistant output. Handlers receive an isolated snapshot
// and cannot replace the output or dispatch its tool calls.
var AfterModel = corehooks.NewPoint[ContextEvent, struct{}]("after_model")

// ModelRequestPolicy is applied to every request, including retries and streams.
// Restrictions combine monotonically: a handler cannot restore denied tools.
type ModelRequestEvent struct {
	ContextEvent
	Purpose string
}
type ModelPolicy struct {
	Purpose      string
	DisableTools bool
	Deny         error
}

var ModelRequestPolicy = corehooks.NewPoint[ModelRequestEvent, ModelPolicy]("model_request_policy").WithReducer(
	corehooks.Fold(func(acc *ModelPolicy, ev *ModelRequestEvent, out ModelPolicy) {
		acc.DisableTools = acc.DisableTools || out.DisableTools
		if out.Deny != nil {
			acc.Deny = out.Deny
		}
		if out.Purpose != "" {
			acc.Purpose = out.Purpose
			ev.Purpose = out.Purpose
		}
	}),
)

// Context replaces the whole message list; nil means unchanged.
var Context = corehooks.NewPoint[ContextEvent, []*aop.Message]("context").WithReducer(
	corehooks.Fold(func(acc *[]*aop.Message, ev *ContextEvent, out []*aop.Message) {
		if out != nil {
			ev.Messages = out
			*acc = out
		}
	}),
)

type RunEndEvent struct {
	SessionID      string
	TurnID         string
	Stop           StopReason
	Output         string
	Messages       []*aop.Message
	MessageCounter int64
	Usage          *aop.TokenUsage
	Err            error
}

var RunEnd = corehooks.NewPoint[RunEndEvent, struct{}]("run_end")

// InboxWait pairs a cooperative suspension with admission before resuming.
// Resume uses the run context, not the mailbox timeout: timing out does not
// authorize executing beyond the host's concurrency limit.
type InboxWaitEvent struct {
	SessionID string
	Waiting   bool
}

var InboxWait = corehooks.NewPoint[InboxWaitEvent, struct{}]("inbox_wait").WithErrorPolicy(corehooks.FailClosed)

// SessionEvent lends Deliver only for this execution's lifetime. The owner
// closes admission before SessionEnd; extensions never own or close the Inbox.
type SessionEvent struct {
	ParentToolCallID string
	TaskID           string
	Delegation       *types.DelegationDetail
	Input            string
	Primary          bool
	Deliver          func(context.Context, inbox.Message) error
	Output           string
	Stop             StopReason
	Err              error
	SessionID        string
	ParentID         string
	AgentName        string
	Model            string
	Reason           string
}

var (
	SessionStart = corehooks.NewPoint[SessionEvent, struct{}]("session_start").WithErrorPolicy(corehooks.FailClosed)
	SessionEnd   = corehooks.NewPoint[SessionEvent, struct{}]("session_end")
)

type CompactEvent struct {
	SessionID     string
	Trigger       string
	ContextTokens int
	ContextWindow int
}

type CancelResult struct {
	Cancel bool
	Reason string
}

var BeforeCompact = corehooks.NewPoint[CompactEvent, CancelResult]("before_compact").WithReducer(
	corehooks.StopWhen[CompactEvent](func(r CancelResult) bool { return r.Cancel }),
)
