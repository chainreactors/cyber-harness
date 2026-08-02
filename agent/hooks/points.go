package hooks

import (
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/tool"
)

// Aliases keep event definitions readable without pulling agent in (that
// would be an import cycle).
type (
	Msg      = aop.Message
	ToolCall = aop.ToolCall
	Usage    = aop.TokenUsage
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

var BeforeRun = Point[RunStartEvent, RunStartResult]{
	Kind: "before_run",
	Reduce: Fold(func(acc *RunStartResult, ev *RunStartEvent, out RunStartResult) {
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

var Context = Point[ContextEvent, ContextResult]{
	Kind: "context",
	Reduce: Fold(func(acc *ContextResult, ev *ContextEvent, out ContextResult) {
		if out.Messages == nil {
			return
		}
		ev.Messages = out.Messages
		acc.Messages = out.Messages
	}),
}

type ToolCallEvent struct {
	SessionID        string
	TurnID           string
	AssistantMessage *Msg
	Call             *ToolCall
	SystemPrompt     string
	Messages         []*Msg
}

type ToolCallResult struct {
	Block  bool
	Reason string
}

// ToolCallHook is fail-closed: a handler that errors out cannot be assumed to
// have approved the call, so the caller must treat any error as a denial.
var ToolCallHook = Point[ToolCallEvent, ToolCallResult]{
	Kind:    "tool_call",
	OnError: FailClosed,
	Reduce:  StopWhen[ToolCallEvent](func(r ToolCallResult) bool { return r.Block }),
}

type ToolResultEvent struct {
	SessionID  string
	TurnID     string
	Call       *ToolCall
	Content    string
	IsError    bool
	Terminate  bool
	DurationMs int
	Full       *tool.Result
}

// ToolResultPatch patches individual fields; nil fields are left alone.
type ToolResultPatch struct {
	Content   *string
	IsError   *bool
	Terminate *bool
}

var ToolResult = Point[ToolResultEvent, ToolResultPatch]{
	Kind: "tool_result",
	Reduce: Fold(func(acc *ToolResultPatch, ev *ToolResultEvent, out ToolResultPatch) {
		// Each patch is mirrored onto the event so later handlers see the
		// already-patched result rather than the original.
		if out.Content != nil {
			ev.Content = *out.Content
			acc.Content = out.Content
		}
		if out.IsError != nil {
			ev.IsError = *out.IsError
			acc.IsError = out.IsError
		}
		if out.Terminate != nil {
			ev.Terminate = *out.Terminate
			acc.Terminate = out.Terminate
		}
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

var RunEnd = Point[RunEndEvent, struct{}]{Kind: "run_end"}

type SessionEvent struct {
	SessionID string
	ParentID  string
	AgentName string
	Model     string
	Reason    string
}

var (
	SessionStart = Point[SessionEvent, struct{}]{Kind: "session_start"}
	SessionEnd   = Point[SessionEvent, struct{}]{Kind: "session_end"}
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

var BeforeCompact = Point[CompactEvent, CancelResult]{
	Kind:   "before_compact",
	Reduce: StopWhen[CompactEvent](func(r CancelResult) bool { return r.Cancel }),
}
