package agent

import (
	"encoding/base64"
	"fmt"
	"sync/atomic"

	aop "github.com/chainreactors/aiscan/aop"
	ext "github.com/chainreactors/aiscan/aop/aiscan/extensions"
	"github.com/chainreactors/aiscan/core/eventbus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	partText                 = "text"
	partReasoning            = "reasoning"
	statusTokenBudgetWarning = "token_budget_warning"
	statusLLMRequest         = "llm_request"
	aopStatusNamespace       = "aop"
)

type aopEmitter struct {
	bus              *eventbus.Bus[*aop.Event]
	agentName        string
	sessionID        string
	turnID           string
	parentSessionID  string
	parentToolCallID string
	delegation       *ext.DelegationDetail
	state            *emitState
}

type emitState struct {
	seq        atomic.Uint64
	messageSeq atomic.Int64
}

func newAOPEmitter(bus *eventbus.Bus[*aop.Event], agentName, sessionID, parentSessionID, parentToolCallID string, detail *ext.DelegationDetail, msgCounter int64) *aopEmitter {
	em := &aopEmitter{
		bus: bus, agentName: agentName, sessionID: sessionID,
		parentSessionID: parentSessionID, parentToolCallID: parentToolCallID,
		delegation: detail, state: &emitState{},
	}
	em.state.messageSeq.Store(msgCounter)
	return em
}

func (e *aopEmitter) turn(turnID string) *aopEmitter {
	return &aopEmitter{
		bus: e.bus, agentName: e.agentName, sessionID: e.sessionID, turnID: turnID,
		parentSessionID: e.parentSessionID, parentToolCallID: e.parentToolCallID,
		delegation: e.delegation, state: e.state,
	}
}

func (e *aopEmitter) emit(event *aop.Event) {
	seq := e.state.seq.Add(1)
	event.Id = fmt.Sprintf("e-%d", seq)
	event.EmittedAt = timestamppb.Now()
	event.SessionId = e.sessionID
	event.TurnId = e.turnID
	event.Emitter = e.agentName
	event.Seq = seq
	e.bus.Emit(event)
}

func (e *aopEmitter) emitWithExt(event *aop.Event, namespace string, value proto.Message) {
	if err := aop.SetProtoExtension(event, namespace, value); err == nil {
		e.emit(event)
	}
}

func (e *aopEmitter) allocMessageID() string {
	return fmt.Sprintf("m-%d", e.state.messageSeq.Add(1))
}

func (e *aopEmitter) messageCounter() int64 { return e.state.messageSeq.Load() }

func (e *aopEmitter) sessionStart(model string) {
	event := &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{
		Model: model, ParentSessionId: e.parentSessionID, ParentToolCallId: e.parentToolCallID,
	}}}
	if e.delegation != nil {
		e.emitWithExt(event, ext.DelegationNamespace, e.delegation)
		return
	}
	e.emit(event)
}

func (e *aopEmitter) sessionEnd(reason string) {
	e.emit(&aop.Event{Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: reason}}})
}

func (e *aopEmitter) turnStart() {
	e.emit(&aop.Event{Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}})
}

func (e *aopEmitter) turnEnd(stop StopReason, totalUsage Usage, contextTokens int, runErr error) {
	ended := &aop.TurnEnded{StopReason: string(stop), Usage: usageData(totalUsage), ContextTokens: uint64(max(contextTokens, 0))}
	if runErr != nil {
		ended.Error = &aop.ProtocolError{Message: runErr.Error()}
	}
	e.emit(&aop.Event{Payload: &aop.Event_TurnEnded{TurnEnded: ended}})
}

func (e *aopEmitter) message(role string, content []*aop.Content) string {
	id := e.allocMessageID()
	e.messageWithID(id, role, content)
	return id
}

func (e *aopEmitter) messageWithID(id, role string, content []*aop.Content) {
	e.messageWithIdentity(id, role, "", content)
}

func (e *aopEmitter) messageWithIdentity(id, role, name string, content []*aop.Content) {
	e.emit(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: id, Role: role, Name: name, Content: content}}})
}

func (e *aopEmitter) messageDelta(messageID string, contentIndex int, partType, delta string) {
	messageDelta := &aop.MessageDelta{
		MessageId: messageID, ContentIndex: uint32(max(contentIndex, 0)), Operation: aop.DeltaOperation_DELTA_OPERATION_APPEND,
	}
	if partType == partReasoning {
		messageDelta.Value = &aop.MessageDelta_Reasoning{Reasoning: delta}
	} else {
		messageDelta.Value = &aop.MessageDelta_Text{Text: delta}
	}
	e.emit(&aop.Event{Payload: &aop.Event_MessageDelta{MessageDelta: messageDelta}})
}

func (e *aopEmitter) toolCall(toolCallID, toolName string, args any, workDir string) {
	arguments, err := aop.JSONValue(args)
	if err != nil {
		e.errorEvt(err, false)
		return
	}
	call := &aop.ToolCall{Id: toolCallID, Name: toolName, Kind: "function", Arguments: arguments, WorkingDirectory: workDir}
	event := &aop.Event{Payload: &aop.Event_ToolCall{ToolCall: call}}
	if detail, ok := delegationFromToolCall(toolName, args); ok {
		e.emitWithExt(event, ext.DelegationNamespace, &detail)
		return
	}
	e.emit(event)
}

func (e *aopEmitter) toolResult(toolCallID, toolName string, content []*aop.Content, details any, terminate, isError bool, durationMs int) {
	detail, err := aop.JSONValue(details)
	if err != nil {
		e.errorEvt(err, false)
		return
	}
	result := &aop.ToolResult{
		CallId: toolCallID, Name: toolName, Output: content, Detail: detail,
		Terminate: terminate, IsError: isError, DurationMs: uint64(max(durationMs, 0)),
	}
	e.emit(&aop.Event{Payload: &aop.Event_ToolResult{ToolResult: result}})
}

func (e *aopEmitter) usage(usage *Usage, model string) {
	if usage == nil {
		return
	}
	value := usageData(*usage)
	value.Model = model
	e.emit(&aop.Event{Payload: &aop.Event_Usage{Usage: value}})
}

func (e *aopEmitter) errorEvt(err error, retryable bool) {
	e.emit(&aop.Event{Payload: &aop.Event_Error{Error: &aop.ProtocolError{Message: err.Error(), Retryable: retryable}}})
}

func (e *aopEmitter) providerFrame(frame ProviderRawFrame) {
	direction := aop.Direction_DIRECTION_UNSPECIFIED
	if frame.Direction == "request" {
		direction = aop.Direction_DIRECTION_REQUEST
	} else if frame.Direction == "response" {
		direction = aop.Direction_DIRECTION_RESPONSE
	}
	e.emit(&aop.Event{Payload: &aop.Event_ProviderFrame{ProviderFrame: &aop.ProviderFrame{
		Provider: frame.Provider, Protocol: frame.Protocol, EventType: frame.EventType,
		Direction: direction, Transport: frame.Transport, Payload: frame.Payload, MediaType: frame.MediaType,
	}}})
}

func (e *aopEmitter) status(state, namespace string, detail proto.Message) {
	event := &aop.Event{Payload: &aop.Event_Status{Status: &aop.Status{State: state}}}
	if namespace != "" && detail != nil {
		e.emitWithExt(event, namespace, detail)
		return
	}
	e.emit(event)
}

func usageData(usage Usage) *aop.TokenUsage {
	if usage == (Usage{}) {
		return nil
	}
	return &aop.TokenUsage{
		InputTokens: uint64(max(usage.PromptTokens, 0)), OutputTokens: uint64(max(usage.CompletionTokens, 0)),
		TotalTokens: uint64(max(usage.TotalTokens, 0)), Detail: map[string]uint64{
			"cache_read": uint64(max(usage.CacheReadTokens, 0)), "cache_write": uint64(max(usage.CacheWriteTokens, 0)),
		},
	}
}

func messagePartsFromChat(message ChatMessage) []*aop.Content {
	var content []*aop.Content
	if message.ReasoningContent != nil && *message.ReasoningContent != "" {
		content = append(content, aop.Reasoning(*message.ReasoningContent))
	}
	if message.Content != nil && *message.Content != "" {
		content = append(content, aop.Text(*message.Content))
	}
	for _, part := range message.ContentParts {
		switch part.Type {
		case "text":
			if part.Text != "" {
				content = append(content, aop.Text(part.Text))
			}
		case "image_url":
			if part.ImageURL == nil {
				continue
			}
			mediaType, base64Data := ParseDataURI(part.ImageURL.URL)
			data, err := base64.StdEncoding.DecodeString(base64Data)
			if err == nil {
				content = append(content, aop.Image(mediaType, data))
			}
		}
	}
	return content
}
