package toolnode

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"google.golang.org/protobuf/proto"
)

type CallHandler struct {
	Executor coretool.Executor
	Progress *eventbus.Bus[*toolpb.Progress]
	Emitter  string
	Logger   telemetry.Logger
	Send     func(string, proto.Message)
	Publish  func(*aop.Event)
	mu       sync.Mutex
	calls    map[string]*call
	closed   bool
	workers  sync.WaitGroup
}

type call struct {
	ctx    context.Context
	cancel context.CancelFunc
	sealed bool
}

func (h *CallHandler) Handle(ctx context.Context, envelope *aop.Envelope, message proto.Message, _ aop.SendFunc) error {
	value, ok := message.(*toolpb.ProtocolMessage)
	if !ok || value.GetCall() == nil || value.GetCall().Call == nil {
		return fmt.Errorf("tool call is required")
	}
	request := proto.CloneOf(value.GetCall())
	id := envelope.GetId()
	if id == "" || request.Call.Id != "" && request.Call.Id != id {
		return fmt.Errorf("tool call ID must match envelope ID")
	}
	request.Call.Id = id
	if h.Publish == nil {
		return fmt.Errorf("tool call event publisher is required")
	}
	h.mu.Lock()
	if h.closed || ctx.Err() != nil {
		h.mu.Unlock()
		return aop.ErrNamespaceUnavailable
	}
	if h.calls == nil {
		h.calls = make(map[string]*call)
	}
	if _, exists := h.calls[id]; exists {
		h.mu.Unlock()
		h.Send(id, aop.NewProtocolError("DUPLICATE_OPERATION", "operation ID was already accepted"))
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	state := &call{ctx: ctx, cancel: cancel}
	h.calls[id] = state
	h.workers.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.workers.Done()
		defer cancel()
		defer h.seal(id)
		defer func() {
			if recovered := recover(); recovered != nil {
				h.seal(id)
				if h.Logger != nil {
					h.Logger.Errorf("tool operation panic operation_id=%s panic=%v\n%s", id, recovered, debug.Stack())
				}
				h.Send(id, aop.NewProtocolError("OPERATION_FAILED", "tool operation failed unexpectedly"))
			}
		}()
		ctx = operation.ContextWithInvocation(ctx, operation.Invocation{Emitter: h.Emitter})
		event, err := coretool.ExecuteToolRequest(ctx, id, request, h.Executor, h.Progress)
		h.seal(id)
		if err != nil {
			h.Send(id, aop.NewProtocolError("INVALID_PAYLOAD", err.Error()))
			return
		}
		h.Publish(event)
	}()
	return nil
}

func (h *CallHandler) seal(id string) {
	h.mu.Lock()
	if state := h.calls[id]; state != nil {
		state.sealed = true
	}
	h.mu.Unlock()
}

// Forward holds the artifact gate through the FIFO write. Sealing therefore
// waits for every admitted artifact before the terminal is published.
func (h *CallHandler) Forward(event *aop.Event) {
	if event == nil {
		return
	}
	id := event.GetToolResult().GetCallId()
	if extension := event.GetExtension(); extension != nil && extension.MessageIs(new(toolpb.Artifact)) {
		ref := new(operationpb.Ref)
		if found, err := aop.FindTypedExtension(event, ref); err == nil && found {
			id = ref.CallId
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if state := h.calls[id]; state != nil && (state.sealed || state.ctx != nil && state.ctx.Err() != nil) {
			return
		}
	}
	h.Send(id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: proto.CloneOf(event)}})
}

func (h *CallHandler) ForwardProgress(progress *toolpb.Progress) {
	if progress == nil || strings.TrimSpace(progress.Text) == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if state := h.calls[progress.CallId]; state != nil && (state.sealed || state.ctx != nil && state.ctx.Err() != nil) {
		return
	}
	value := proto.CloneOf(progress)
	value.Text = strings.ToValidUTF8(value.Text, "\uFFFD")
	h.Send(value.CallId, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Progress{Progress: value}})
}

// Cancel handles connection-level tool cancellation before namespace dispatch.
func (h *CallHandler) Cancel(envelope *aop.Envelope) bool {
	if envelope == nil || envelope.Payload == nil || !envelope.Payload.MessageIs(&aop.ProtocolMessage{}) {
		return false
	}
	value := new(aop.ProtocolMessage)
	if envelope.Payload.UnmarshalTo(value) != nil || value.GetCancelOperation() == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if state := h.calls[value.GetCancelOperation().TargetId]; state != nil {
		state.cancel()
	}
	return true
}

// Close revokes admission, cancels accepted calls, and waits for their cleanup.
func (h *CallHandler) Close() {
	h.mu.Lock()
	h.closed = true
	for _, state := range h.calls {
		state.cancel()
	}
	h.mu.Unlock()
	h.workers.Wait()
}
