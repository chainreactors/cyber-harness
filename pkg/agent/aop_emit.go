package agent

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/aop"
)

// Status states emitted on the AOP status channel. Internal agent semantics
// (eval, compact, token budget, llm request summaries) ride here with detail
// in ext.<agent>.* rather than as first-class event types.
const (
	StatusEvalStart          = "eval_start"
	StatusEvalEnd            = "eval_end"
	StatusEvalError          = "eval_error"
	StatusCompactStart       = "compact_start"
	StatusCompactEnd         = "compact_end"
	StatusCompactError       = "compact_error"
	StatusTokenBudgetWarning = "token_budget_warning"
	StatusLLMRequest         = "llm_request"
)

// aopEmitter is the agent kernel's single event-emission path. Every event
// leaves through it so seq numbering, message_id allocation, and session
// tagging stay consistent per session. Safe for concurrent use.
type aopEmitter struct {
	bus             *eventbus.Bus[aop.Event]
	agentName       string
	sessionID       string
	parentSessionID string
	seq             atomic.Int64
	msgCounter      atomic.Int64
}

func newAOPEmitter(bus *eventbus.Bus[aop.Event], agentName, sessionID, parentSessionID string, msgCounter int64) *aopEmitter {
	em := &aopEmitter{
		bus:             bus,
		agentName:       agentName,
		sessionID:       sessionID,
		parentSessionID: parentSessionID,
	}
	em.msgCounter.Store(msgCounter)
	return em
}

func (e *aopEmitter) emit(typ string, data any, ext map[string]any) {
	raw, err := json.Marshal(data)
	if err != nil {
		raw, _ = json.Marshal(map[string]string{"marshal_error": err.Error()})
	}
	ev := aop.Event{
		Type:      typ,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: e.sessionID,
		Agent:     e.agentName,
		Seq:       int(e.seq.Add(1)),
		Data:      raw,
	}
	if len(ext) > 0 {
		ev.Ext = map[string]any{e.agentName: ext}
	}
	e.bus.Emit(ev)
}

func (e *aopEmitter) allocMessageID() string {
	return fmt.Sprintf("m-%d", e.msgCounter.Add(1))
}

func (e *aopEmitter) messageCounter() int64 {
	return e.msgCounter.Load()
}

func (e *aopEmitter) sessionStart(model string) {
	e.emit(aop.TypeSessionStart, aop.SessionStartData{
		Model:           model,
		ParentSessionID: e.parentSessionID,
	}, nil)
}

func (e *aopEmitter) sessionEnd(stop StopReason, turns int, runErr error) {
	data := aop.SessionEndData{Stop: string(stop), Turns: turns}
	if runErr != nil {
		data.Error = runErr.Error()
	}
	e.emit(aop.TypeSessionEnd, data, nil)
}

func (e *aopEmitter) turnStart(turn int) {
	e.emit(aop.TypeTurnStart, aop.TurnData{Turn: turn}, nil)
}

func (e *aopEmitter) turnEnd(turn int, totalUsage Usage, contextTokens int) {
	e.emit(aop.TypeTurnEnd, aop.TurnData{Turn: turn}, map[string]any{
		"total_input_tokens":  totalUsage.PromptTokens,
		"total_output_tokens": totalUsage.CompletionTokens,
		"total_tokens":        totalUsage.TotalTokens,
		"context_tokens":      contextTokens,
	})
}

// message emits a complete message event, allocating a fresh message_id.
// Returns the allocated id.
func (e *aopEmitter) message(role string, parts []aop.MessagePart) string {
	id := e.allocMessageID()
	e.messageWithID(id, role, parts)
	return id
}

// messageWithID emits a complete message event with a caller-chosen id —
// used when a streaming message's id was allocated before the retry loop so
// deltas and the final message share it across retries.
func (e *aopEmitter) messageWithID(id, role string, parts []aop.MessagePart) {
	e.emit(aop.TypeMessage, aop.MessageData{MessageID: id, Role: role, Parts: parts}, nil)
}

func (e *aopEmitter) messageDelta(messageID string, partIndex int, partType, delta string) {
	e.emit(aop.TypeMessageDelta, aop.MessageDeltaData{
		MessageID: messageID,
		PartIndex: partIndex,
		PartType:  partType,
		Delta:     delta,
	}, nil)
}

func (e *aopEmitter) toolCall(toolCallID, toolName string, args any) {
	e.emit(aop.TypeToolCall, aop.ToolCallData{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       args,
	}, nil)
}

func (e *aopEmitter) toolResult(toolCallID, toolName string, content any, isError bool, durationMs int) {
	e.emit(aop.TypeToolResult, aop.ToolResultData{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    content,
		IsError:    isError,
		DurationMs: durationMs,
	}, nil)
}

func (e *aopEmitter) usage(u *Usage, model string) {
	if u == nil {
		return
	}
	e.emit(aop.TypeUsage, aop.UsageData{
		InputTokens:      u.PromptTokens,
		OutputTokens:     u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		Model:            model,
	}, nil)
}

func (e *aopEmitter) errorEvt(err error, retryable bool) {
	e.emit(aop.TypeError, aop.ErrorData{Message: err.Error(), Retryable: retryable}, nil)
}

func (e *aopEmitter) status(state string, ext map[string]any) {
	e.emit(aop.TypeStatus, aop.StatusData{State: state}, ext)
}

// messagePartsFromChat flattens a ChatMessage into AOP parts for echo/persist.
func messagePartsFromChat(msg ChatMessage) []aop.MessagePart {
	var parts []aop.MessagePart
	if msg.ReasoningContent != nil && *msg.ReasoningContent != "" {
		parts = append(parts, aop.MessagePart{Type: aop.PartReasoning, Text: *msg.ReasoningContent})
	}
	if msg.Content != nil && *msg.Content != "" {
		parts = append(parts, aop.MessagePart{Type: aop.PartText, Text: *msg.Content})
	}
	for _, p := range msg.ContentParts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				parts = append(parts, aop.MessagePart{Type: aop.PartText, Text: p.Text})
			}
		case "image_url":
			if p.ImageURL == nil {
				continue
			}
			mediaType, base64Data := ParseDataURI(p.ImageURL.URL)
			parts = append(parts, aop.MessagePart{
				Type:  aop.PartImage,
				Image: &aop.ImageSource{Base64: base64Data, MediaType: mediaType},
			})
		}
	}
	return parts
}
