package agent

import (
	"fmt"
	"sync/atomic"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/tool"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	partText                 = "text"
	partReasoning            = "reasoning"
	statusTokenBudgetWarning = "token_budget_warning"
	statusLLMRequest         = "llm_request"
)

// EventEmitter is the narrow event sink an agent needs — callers may wrap a
// bus with stamping/routing middleware (e.g. the runner's sessionEmitter)
// instead of handing over a raw *eventbus.Bus.
type EventEmitter interface {
	Emit(*aop.Event)
}

type aopEmitter struct {
	bus              EventEmitter
	agentName        string
	sessionID        string
	turnID           string
	parentSessionID  string
	parentToolCallID string
	delegation       *types.DelegationDetail
	state            *emitState
}

type emitState struct {
	seq        atomic.Uint64
	messageSeq atomic.Int64
}

func newAOPEmitter(bus EventEmitter, agentName, sessionID, parentSessionID, parentToolCallID string, detail *types.DelegationDetail, msgCounter int64) *aopEmitter {
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

func (e *aopEmitter) emitWithExt(event *aop.Event, value proto.Message) {
	if err := aop.SetTypedExtension(event, value); err == nil {
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
		e.emitWithExt(event, e.delegation)
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

func (e *aopEmitter) turnEnd(stop StopReason, totalUsage *aop.TokenUsage, contextTokens int, runErr error) {
	ended := &aop.TurnEnded{StopReason: string(stop), Usage: totalUsage, ContextTokens: uint64(max(contextTokens, 0))}
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

// messageProto emits an already-built assistant message. The message id is
// assigned by the caller (requestWithRetry) so retries and deltas share it.
func (e *aopEmitter) messageProto(msg *aop.Message) {
	e.emit(&aop.Event{Payload: &aop.Event_Message{Message: msg}})
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

func (e *aopEmitter) toolCall(call *aop.ToolCall) {
	event := &aop.Event{Payload: &aop.Event_ToolCall{ToolCall: call}}
	if detail, ok := delegationFromToolCall(call.Name, decodeToolArguments(call)); ok {
		e.emitWithExt(event, detail)
		return
	}
	e.emit(event)
}

func (e *aopEmitter) toolResult(call *aop.ToolCall, content []*aop.Content, fullResult *tool.Result, terminate, isError bool, durationMs int) {
	result := &aop.ToolResult{
		CallId: call.Id, Name: call.Name, Output: content,
		Terminate: terminate, IsError: isError, DurationMs: uint64(max(durationMs, 0)),
	}
	e.emit(&aop.Event{Payload: &aop.Event_ToolResult{ToolResult: result}})
}

func (e *aopEmitter) usage(usage *aop.TokenUsage, model string) {
	if usage == nil {
		return
	}
	value := proto.CloneOf(usage)
	value.Model = model
	e.emit(&aop.Event{Payload: &aop.Event_Usage{Usage: value}})
}

func (e *aopEmitter) errorEvt(err error, retryable bool) {
	e.emit(&aop.Event{Payload: &aop.Event_Error{Error: &aop.ProtocolError{Message: err.Error(), Retryable: retryable}}})
}

func (e *aopEmitter) providerFrame(frame ProviderRawFrame) {
	direction := aop.Direction_DIRECTION_UNSPECIFIED
	switch frame.Direction {
	case "request":
		direction = aop.Direction_DIRECTION_REQUEST
	case "response":
		direction = aop.Direction_DIRECTION_RESPONSE
	}
	e.emit(&aop.Event{Payload: &aop.Event_ProviderFrame{ProviderFrame: &aop.ProviderFrame{
		Provider: frame.Provider, Protocol: frame.Protocol, EventType: frame.EventType,
		Direction: direction, Transport: frame.Transport, Payload: frame.Payload, MediaType: frame.MediaType,
	}}})
}

func (e *aopEmitter) status(state string, detail proto.Message) {
	event := &aop.Event{Payload: &aop.Event_Status{Status: &aop.Status{State: state}}}
	if detail != nil {
		e.emitWithExt(event, detail)
		return
	}
	e.emit(event)
}
