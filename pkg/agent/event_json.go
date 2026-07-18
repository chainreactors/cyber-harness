package agent

import (
	"encoding/json"
	"time"
)

func (e Event) MarshalJSON() ([]byte, error) {
	ts := e.EmittedAt
	if ts.IsZero() {
		ts = time.Now()
	}

	out := struct {
		Timestamp       string        `json:"ts"`
		Type            EventType     `json:"type"`
		SessionID       string        `json:"session_id,omitempty"`
		ParentSessionID string        `json:"parent_session_id,omitempty"`
		Turn            int           `json:"turn,omitempty"`
		Message         *ChatMessage  `json:"message,omitempty"`
		ToolResults     []ChatMessage `json:"tool_results,omitempty"`
		ToolCallID      string        `json:"tool_call_id,omitempty"`
		ToolName        string        `json:"tool_name,omitempty"`
		Arguments       string        `json:"arguments,omitempty"`
		Result          string        `json:"result,omitempty"`
		IsError         bool          `json:"is_error,omitempty"`
		Error           string        `json:"error,omitempty"`
		Stop            StopReason    `json:"stop,omitempty"`
		NewMessages     int           `json:"new_messages,omitempty"`
		Usage           *Usage        `json:"usage,omitempty"`
		ContextTokens   int           `json:"context_tokens,omitempty"`
		RequestModel    string        `json:"request_model,omitempty"`
		RequestMessages int           `json:"request_messages,omitempty"`
		RequestTools    int           `json:"request_tools,omitempty"`
		// Evaluator-loop verdict fields. Without these the Goal-mode per-round
		// pass/reason never leaves the agent process, so the web UI's eval badge
		// has nothing to render. omitempty keeps them off every non-eval event.
		EvalRound  int    `json:"eval_round,omitempty"`
		EvalPass   bool   `json:"eval_pass,omitempty"`
		EvalReason string `json:"eval_reason,omitempty"`
		EvalError  string `json:"eval_error,omitempty"`

		CompactTokensBefore int `json:"compact_tokens_before,omitempty"`
		CompactTokensAfter  int `json:"compact_tokens_after,omitempty"`
		CompactKeptMessages int `json:"compact_kept_messages,omitempty"`
	}{
		Timestamp:       ts.UTC().Format(time.RFC3339Nano),
		Type:            e.Type,
		SessionID:       e.SessionID,
		ParentSessionID: e.ParentSessionID,
		Turn:            e.Turn,
		ToolCallID:      e.ToolCallID,
		ToolName:        e.ToolName,
		Arguments:       e.Arguments,
		Result:          e.Result,
		IsError:         e.IsError,
		Stop:            e.Stop,
		ContextTokens:   e.ContextTokens,
		EvalRound:       e.EvalRound,
		EvalPass:        e.EvalPass,
		EvalReason:      e.EvalReason,
		EvalError:       e.EvalError,

		CompactTokensBefore: e.CompactTokensBefore,
		CompactTokensAfter:  e.CompactTokensAfter,
		CompactKeptMessages: e.CompactKeptMessages,
	}

	if e.Err != nil {
		out.Error = e.Err.Error()
	}
	if e.Message.Role != "" || e.Message.Content != nil || len(e.Message.ContentParts) > 0 || len(e.Message.ToolCalls) > 0 || e.Message.ToolCallID != "" {
		msg := e.Message
		out.Message = &msg
	}
	if len(e.ToolResults) > 0 {
		out.ToolResults = e.ToolResults
	}
	if len(e.NewMessages) > 0 {
		out.NewMessages = len(e.NewMessages)
	}
	if e.Usage != nil {
		out.Usage = e.Usage
	}
	if e.Request != nil {
		out.RequestModel = e.Request.Model
		out.RequestMessages = len(e.Request.Messages)
		out.RequestTools = len(e.Request.Tools)
	}

	return json.Marshal(out)
}
