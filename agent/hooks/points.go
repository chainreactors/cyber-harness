package hooks

import (
	aop "github.com/chainreactors/aiscan/aop"
	corehooks "github.com/chainreactors/aiscan/core/hooks"
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

var BeforeRun = corehooks.Point[RunStartEvent, RunStartResult]{
	Kind: "before_run",
	Reduce: corehooks.Fold(func(acc *RunStartResult, ev *RunStartEvent, out RunStartResult) {
		if out.SystemPrompt != nil {
			// Fold into the event so the next handler edits the new prompt.
			ev.SystemPrompt = *out.SystemPrompt
			acc.SystemPrompt = out.SystemPrompt
		}
		acc.Prepend = append(acc.Prepend, out.Prepend...)
	}),
}

type ContextEvent struct {
	SessionID string
	Turn      int
	Messages  []*Msg
}

// ContextResult replaces the whole message list; nil means unchanged.
type ContextResult struct {
	Messages []*Msg
}

var Context = corehooks.Point[ContextEvent, ContextResult]{
	Kind: "context",
	Reduce: corehooks.Fold(func(acc *ContextResult, ev *ContextEvent, out ContextResult) {
		if out.Messages == nil {
			return
		}
		ev.Messages = out.Messages
		acc.Messages = out.Messages
	}),
}

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

var RunEnd = corehooks.Point[RunEndEvent, struct{}]{Kind: "run_end"}

type SessionEvent struct {
	SessionID string
	ParentID  string
	AgentName string
	Model     string
	Reason    string
}

var (
	SessionStart = corehooks.Point[SessionEvent, struct{}]{Kind: "session_start"}
	SessionEnd   = corehooks.Point[SessionEvent, struct{}]{Kind: "session_end"}
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

var BeforeCompact = corehooks.Point[CompactEvent, CancelResult]{
	Kind:   "before_compact",
	Reduce: corehooks.StopWhen[CompactEvent](func(r CancelResult) bool { return r.Cancel }),
}
