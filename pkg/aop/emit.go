package aop

import (
	"encoding/json"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
)

// FromAgentEvent converts an aiscan agent.Event into AOP events.
// A single agent.Event may produce 0–2 AOP events (e.g. turn_end → turn.end + usage).
func FromAgentEvent(ev agent.Event, agentName string) []Event {
	ts := ev.EmittedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	tsStr := ts.UTC().Format(time.RFC3339Nano)
	sid := ev.SessionID

	base := func(typ string, data any) Event {
		raw, _ := json.Marshal(data)
		e := Event{
			Type:      typ,
			TS:        tsStr,
			SessionID: sid,
			Agent:     agentName,
			Data:      raw,
		}
		ext := buildExt(ev)
		if len(ext) > 0 {
			e.Ext = map[string]any{agentName: ext}
		}
		return e
	}

	switch ev.Type {
	case agent.EventAgentStart:
		model := ""
		if ev.Request != nil {
			model = ev.Request.Model
		}
		return []Event{base(TypeSessionStart, SessionStartData{
			Model:           model,
			ParentSessionID: ev.ParentSessionID,
		})}

	case agent.EventAgentEnd:
		return []Event{base(TypeSessionEnd, SessionEndData{
			Stop:  string(ev.Stop),
			Turns: ev.Turn,
			Error: errStr(ev.Err),
		})}

	case agent.EventTurnStart:
		return []Event{base(TypeTurnStart, TurnData{Turn: ev.Turn})}

	case agent.EventTurnEnd:
		events := []Event{base(TypeTurnEnd, TurnData{Turn: ev.Turn})}
		if ev.Usage != nil {
			events = append(events, base(TypeUsage, UsageData{
				InputTokens:      ev.Usage.PromptTokens,
				OutputTokens:     ev.Usage.CompletionTokens,
				TotalTokens:      ev.Usage.TotalTokens,
				CacheReadTokens:  ev.Usage.CacheReadTokens,
				CacheWriteTokens: ev.Usage.CacheWriteTokens,
			}))
		}
		return events

	case agent.EventMessageStart:
		// User input is owned by the caller (web/aide), so the agent producer
		// does not echo it back into the output stream.
		return nil

	case agent.EventMessageUpdate:
		if ev.Message.Role != "assistant" {
			return nil
		}
		events := make([]Event, 0, 2)
		if ev.ReasoningDelta != "" {
			events = append(events, base(TypeText, TextData{
				Content: ev.ReasoningDelta,
				Role:    "assistant", Delta: true, Channel: TextChannelReasoning,
			}))
		}
		if ev.ContentDelta != "" {
			events = append(events, base(TypeText, TextData{Content: ev.ContentDelta, Role: "assistant", Delta: true}))
		}
		return events

	case agent.EventMessageEnd:
		if ev.Message.Role != "assistant" {
			return nil
		}
		events := make([]Event, 0, 2)
		if ev.Message.ReasoningContent != nil && *ev.Message.ReasoningContent != "" {
			events = append(events, base(TypeText, TextData{
				Content: *ev.Message.ReasoningContent,
				Role:    "assistant", Channel: TextChannelReasoning,
			}))
		}
		if ev.Message.Content != nil && *ev.Message.Content != "" {
			events = append(events, base(TypeText, TextData{Content: *ev.Message.Content, Role: "assistant"}))
		}
		return events

	case agent.EventToolExecutionStart:
		args := parseArgs(ev.Arguments)
		return []Event{base(TypeToolCall, ToolCallData{
			ToolCallID: ev.ToolCallID,
			ToolName:   ev.ToolName,
			Args:       args,
		})}

	case agent.EventToolExecutionEnd:
		return []Event{base(TypeToolResult, ToolResultData{
			ToolCallID: ev.ToolCallID,
			ToolName:   ev.ToolName,
			Content:    ev.Result,
			IsError:    ev.IsError,
		})}

	default:
		return nil
	}
}

func parseArgs(raw string) any {
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return m
	}
	return raw
}

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func buildExt(ev agent.Event) map[string]any {
	ext := map[string]any{}
	if ev.ContextTokens > 0 {
		ext["context_tokens"] = ev.ContextTokens
	}
	if ev.EvalRound > 0 {
		ext["eval_round"] = ev.EvalRound
		ext["eval_pass"] = ev.EvalPass
		if ev.EvalReason != "" {
			ext["eval_reason"] = ev.EvalReason
		}
		if ev.EvalError != "" {
			ext["eval_error"] = ev.EvalError
		}
	}
	if ev.CompactTokensBefore > 0 {
		ext["compact_tokens_before"] = ev.CompactTokensBefore
		ext["compact_tokens_after"] = ev.CompactTokensAfter
		ext["compact_kept_messages"] = ev.CompactKeptMessages
	}
	if ev.ParentSessionID != "" {
		ext["parent_session_id"] = ev.ParentSessionID
	}
	return ext
}
