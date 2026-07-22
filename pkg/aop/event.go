// Package aop implements Agent Output Protocol — a language-neutral JSONL
// event protocol for AI coding agents.
package aop

import "encoding/json"

// Event is the stable hand-written AOP envelope. Data and extension namespaces
// stay raw until a consumer explicitly decodes them, so bridges can forward
// unknown protocol additions without rewriting them.
type Event struct {
	Type      string                     `json:"type"`
	TS        string                     `json:"ts"`
	SessionID string                     `json:"session_id"`
	Agent     string                     `json:"agent"`
	Seq       int                        `json:"seq,omitempty"`
	Data      json.RawMessage            `json:"data"`
	Ext       map[string]json.RawMessage `json:"ext,omitempty"`
}

func (e Event) Valid() bool {
	return e.Type != "" && e.TS != "" && e.SessionID != "" && e.Agent != "" && len(e.Data) > 0
}

const (
	TypeSessionStart = "session.start"
	TypeSessionEnd   = "session.end"
	TypeMessage      = "message"
	TypeMessageDelta = "message.delta"
	TypeToolCall     = "tool.call"
	TypeToolResult   = "tool.result"
	TypeUsage        = "usage"
	TypeTurnStart    = "turn.start"
	TypeTurnEnd      = "turn.end"
	TypeError        = "error"
	TypeStatus       = "status"
)

const (
	PartText      = "text"
	PartReasoning = "reasoning"
	PartImage     = "image"
)

const (
	NSAOP = "aop"

	StatusTokenBudgetWarning = "token_budget_warning"
	StatusLLMRequest         = "llm_request"
)

// ToolResultContent is the structured Content variant used when a tool result
// contains images alongside its text.
type ToolResultContent struct {
	Content string        `json:"content"`
	Images  []ImageSource `json:"images,omitempty"`
}
