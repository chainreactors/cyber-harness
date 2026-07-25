package agent

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/aop/x/delegation"
)

// aopEmitter is the agent kernel's single event-emission path. Every event
// leaves through it so seq numbering, message_id allocation, and session
// tagging stay consistent per session. Safe for concurrent use.
type aopEmitter struct {
	bus              *eventbus.Bus[aop.Event]
	agentName        string
	sessionID        string
	turnID           string
	parentSessionID  string
	parentToolCallID string
	delegation       *delegation.DelegationDetail
	state            *emitState
}

type emitState struct {
	seq        atomic.Int64
	messageSeq atomic.Int64
}

func newAOPEmitter(bus *eventbus.Bus[aop.Event], agentName, sessionID, parentSessionID, parentToolCallID string, detail *delegation.DelegationDetail, msgCounter int64) *aopEmitter {
	em := &aopEmitter{
		bus:              bus,
		agentName:        agentName,
		sessionID:        sessionID,
		parentSessionID:  parentSessionID,
		parentToolCallID: parentToolCallID,
		delegation:       detail,
		state:            &emitState{},
	}
	em.state.messageSeq.Store(msgCounter)
	return em
}

func (e *aopEmitter) turn(turnID string) *aopEmitter {
	return &aopEmitter{bus: e.bus, agentName: e.agentName, sessionID: e.sessionID, turnID: turnID, parentSessionID: e.parentSessionID, parentToolCallID: e.parentToolCallID, delegation: e.delegation, state: e.state}
}

func (e *aopEmitter) event(typ string, data any) aop.Event {
	raw, err := json.Marshal(data)
	if err != nil {
		raw, _ = json.Marshal(map[string]string{"marshal_error": err.Error()})
	}
	return aop.Event{
		Type:      typ,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: e.sessionID,
		TurnID:    e.turnID,
		Agent:     e.agentName,
		Seq:       int(e.state.seq.Add(1)),
		Data:      raw,
	}

}

func (e *aopEmitter) emit(typ string, data any) {
	ev := e.event(typ, data)
	e.bus.Emit(ev)
}

func (e *aopEmitter) emitWithExt(typ string, data any, namespace string, ext any) {
	ev := e.event(typ, data)
	if err := aop.SetExt(&ev, namespace, ext); err != nil {
		return
	}
	e.bus.Emit(ev)
}

func (e *aopEmitter) allocMessageID() string {
	return fmt.Sprintf("m-%d", e.state.messageSeq.Add(1))
}

func (e *aopEmitter) messageCounter() int64 {
	return e.state.messageSeq.Load()
}

func (e *aopEmitter) sessionStart(model string) {
	data := aop.SessionStartData{
		Model:            model,
		ParentSessionID:  e.parentSessionID,
		ParentToolCallID: e.parentToolCallID,
	}
	if e.delegation != nil {
		e.emitWithExt(aop.TypeSessionStart, data, delegation.NS, *e.delegation)
		return
	}
	e.emit(aop.TypeSessionStart, data)
}

func (e *aopEmitter) sessionEnd(reason string) {
	e.emit(aop.TypeSessionEnd, aop.SessionEndData{Reason: reason})
}

func (e *aopEmitter) turnStart() {
	e.emit(aop.TypeTurnStart, aop.TurnStartData{})
}

func (e *aopEmitter) turnEnd(stop StopReason, totalUsage Usage, contextTokens int, runErr error) {
	data := aop.TurnEndData{Stop: string(stop), Usage: usageData(totalUsage), ContextTokens: contextTokens}
	if runErr != nil {
		data.Error = runErr.Error()
	}
	e.emit(aop.TypeTurnEnd, data)
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
	e.emit(aop.TypeMessage, aop.MessageData{MessageID: id, Role: role, Parts: parts})
}

func (e *aopEmitter) messageDelta(messageID string, partIndex int, partType, delta string) {
	e.emit(aop.TypeMessageDelta, aop.MessageDeltaData{
		MessageID: messageID,
		PartIndex: partIndex,
		PartType:  partType,
		Delta:     delta,
	})
}

func (e *aopEmitter) toolCall(toolCallID, toolName string, args any, workDir string) {
	data := aop.ToolCallData{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       args,
		WorkDir:    workDir,
	}
	if detail, ok := delegationFromToolCall(toolName, args); ok {
		e.emitWithExt(aop.TypeToolCall, data, delegation.NS, detail)
		return
	}
	e.emit(aop.TypeToolCall, data)
}

func (e *aopEmitter) toolResult(toolCallID, toolName string, content, details any, terminate, isError bool, durationMs int) {
	e.emit(aop.TypeToolResult, aop.ToolResultData{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    content,
		Details:    details,
		Terminate:  terminate,
		IsError:    isError,
		DurationMs: durationMs,
	})
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
	})
}

func (e *aopEmitter) errorEvt(err error, retryable bool) {
	e.emit(aop.TypeError, aop.ErrorData{Message: err.Error(), Retryable: retryable})
}

func (e *aopEmitter) status(state, namespace string, detail any) {
	if detail == nil {
		e.emit(aop.TypeStatus, aop.StatusData{State: state})
		return
	}
	e.emitWithExt(aop.TypeStatus, aop.StatusData{State: state}, namespace, detail)
}

func usageData(u Usage) *aop.UsageData {
	if u == (Usage{}) {
		return nil
	}
	return &aop.UsageData{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens}
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
