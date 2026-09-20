package hooks

import (
	"context"
	"github.com/chainreactors/cyber/agent/inbox"
	aop "github.com/chainreactors/cyber/aop"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/types"
)

// Aliases keep event definitions readable without pulling agent in (that
// would be an import cycle).
type (
	Msg   = aop.Message
	Usage = aop.TokenUsage
)

// StopReason lives here rather than in agent because run_end events carry
// it; agent aliases these back.
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
	Prepend      []*Msg
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
	Turn      int
	Messages  []*Msg
}

// ContextResult replaces the whole message list; nil means unchanged.
type ContextResult struct {
	Messages []*Msg
}

var Context = corehooks.NewPoint[ContextEvent, ContextResult]("context").WithReducer(
	corehooks.Fold(func(acc *ContextResult, ev *ContextEvent, out ContextResult) {
		if out.Messages == nil {
			return
		}
		ev.Messages = out.Messages
		acc.Messages = out.Messages
	}),
)

type RunEndEvent struct {
	SessionID      string
	TurnID         string
	Stop           StopReason
	Output         string
	Messages       []*Msg
	MessageCounter int64
	Usage          *Usage
	Err            error
}

var RunEnd = corehooks.NewPoint[RunEndEvent, struct{}]("run_end")

// SessionEvent lends Deliver only for this execution's lifetime. The owner
// closes admission before SessionEnd; extensions never own or close the Inbox.
type SessionEvent struct {
	ParentToolCallID string
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
