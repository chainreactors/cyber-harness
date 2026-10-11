package otel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/types"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
)

func sessionAttributes(id string, state *session) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String("cyber.session.id", id), attribute.String("session.id", id), attribute.String("cyber.emitter", state.emitter), attribute.String("gen_ai.request.model", state.model), attribute.String("cyber.parent.session.id", state.parent), attribute.String("cyber.parent.call.id", state.call), attribute.String("cyber.lifecycle.source", "aop_projection")}
	if d := state.delegation; d != nil {
		attrs = append(attrs, attribute.String("cyber.agent.id", d.AgentId), attribute.String("cyber.agent.name", d.AgentName), attribute.String("cyber.agent.type", d.AgentType), attribute.String("cyber.agent.run_mode", d.RunMode), attribute.String("cyber.agent.context_mode", d.ContextMode))
	}
	return attrs
}
func (b *aopBridge) sessionStarted(id string, state *session) {
	s := b.signals
	if s == nil {
		return
	}
	if s.sessions != nil {
		s.sessions.Add(state.ctx, 1, metric.WithAttributes(attribute.String("cyber.lifecycle.state", "started")))
		s.sessionsActive.Add(state.ctx, 1)
	}
	s.emit(state.ctx, "agent.session.started", state.start, otellog.SeverityInfo, sessionAttributes(id, state)...)
}
func (b *aopBridge) sessionEnded(id string, state *session, reason string, at time.Time) {
	s := b.signals
	if s == nil {
		return
	}
	phase := "completed"
	severity := otellog.SeverityInfo
	if reason == "incomplete" {
		phase = "incomplete"
		severity = otellog.SeverityError
	}
	duration := max(0, at.Sub(state.start).Seconds())
	if s.sessions != nil {
		labels := []attribute.KeyValue{attribute.String("cyber.lifecycle.state", phase)}
		s.sessions.Add(state.ctx, 1, metric.WithAttributes(labels...))
		s.sessionsActive.Add(state.ctx, -1)
		s.sessionDuration.Record(state.ctx, duration, metric.WithAttributes(labels...))
	}
	s.emit(state.ctx, "agent.session."+phase, at, severity, append(sessionAttributes(id, state), attribute.String("cyber.stop_reason", reason), attribute.Float64("cyber.duration.seconds", duration))...)
}

// eventLog preserves every accepted non-streaming AOP fact. Projection lifecycle
// logs distinguish observed scope completions from duplicate input facts.
func (b *aopBridge) eventLog(event *aop.Event, conversionErr error) {
	s := b.signals
	if s == nil {
		return
	}
	kind := aop.Kind(event)
	if s.events != nil {
		s.events.Add(context.Background(), 1, metric.WithAttributes(attribute.String("aop.event.kind", kind)))
	}
	if s.logger == nil {
		return
	}
	attrs := b.factAttributes(event)
	ctx := context.Background()
	if run := b.turns[turnKey{event.SessionId, event.TurnId}]; run != nil {
		ctx = run.ctx
		if status := event.GetStatus(); status != nil && status.State == "llm_request" && run.modelCtx != nil {
			ctx = run.modelCtx
		}
		if event.GetUsage() != nil && run.modelCtx != nil {
			ctx = run.modelCtx
		}
		if message := event.GetMessage(); message != nil && message.Role == "assistant" && run.modelCtx != nil {
			ctx = run.modelCtx
		}
	} else if previous, ok := b.endedTurns.get(turnKey{event.SessionId, event.TurnId}); ok {
		ctx = previous
	} else if state := b.session(event.SessionId); state != nil && state.ctx != nil {
		ctx = state.ctx
	}
	ref := new(operationpb.Ref)
	_, _ = aop.FindTypedExtension(event, ref)
	if ref.OperationId != "" {
		attrs = append(attrs, attribute.String("cyber.operation.id", ref.OperationId), attribute.String("cyber.operation.parent_id", ref.ParentOperationId), attribute.String("cyber.call.id", ref.CallId), attribute.String("cyber.resource.id", ref.ResourceId))
		if operation, ok := b.scopeContext(scopeKey{event.SessionId, "operation", ref.OperationId}); ok {
			ctx = operation
		}
	}
	severity := otellog.SeverityInfo
	if status := event.GetStatus(); status != nil {
		attrs = append(attrs, attribute.String("cyber.status.state", status.State))
		var key scopeKey
		switch status.State {
		case types.CompactStateStart, types.CompactStateEnd, types.CompactStateError:
			key = scopeKey{event.SessionId, "compact", event.TurnId}
		case types.EvalStateStart, types.EvalStateEnd, types.EvalStateError:
			detail, _, _ := types.GetEvalDetail(event)
			key = scopeKey{event.SessionId, "eval", fmt.Sprintf("%s/%d", event.TurnId, detail.Round)}
		}
		if key.id != "" {
			if mechanism, ok := b.scopeContext(key); ok {
				ctx = mechanism
			}
		}
	}
	if usage := event.GetUsage(); usage != nil {
		attrs = append(attrs, usageSummary(usage)...)
		attrs = append(attrs, attribute.Bool("cyber.usage.missing", usage.Detail["usage_missing"] > 0))
	}
	if ended := event.GetTurnEnded(); ended != nil {
		attrs = append(attrs, attribute.String("cyber.stop_reason", ended.StopReason))
		if ended.Error != nil {
			severity = otellog.SeverityError
		}
	}
	if event.GetError() != nil {
		severity = otellog.SeverityError
	}
	if call := event.GetToolCall(); call != nil {
		attrs = append(attrs, attribute.String("cyber.call.id", call.Id), attribute.String("tool.name", call.Name))
	}
	if result := event.GetToolResult(); result != nil {
		if call, ok := b.calls.get(turnKey{event.SessionId, result.CallId}); ok {
			ctx = call
		}
		attrs = append(attrs, attribute.String("cyber.call.id", result.CallId), attribute.String("tool.name", result.Name), attribute.Bool("cyber.tool.is_error", result.IsError))
		if result.IsError {
			severity = otellog.SeverityError
		}
	}
	if payload := event.GetExtension(); payload != nil {
		if payload.MessageIs(new(operationpb.Completed)) {
			completed := new(operationpb.Completed)
			if payload.UnmarshalTo(completed) == nil {
				attrs = append(attrs, attribute.String("cyber.operation.kind", completed.Kind))
				if completed.Failure != nil {
					severity = otellog.SeverityError
					attrs = append(attrs, attribute.String("cyber.failure.kind", completed.Failure.Kind.String()))
				}
			}
		}
		if string(payload.MessageName()) == "cyber.jev.RuntimeEvent" {
			if value, err := payload.UnmarshalNew(); err == nil {
				runtime := value.ProtoReflect()
				attrs = append(attrs, attribute.Bool("cyber.background", boolField(runtime, "background")))
				if oneof := runtime.Descriptor().Oneofs().ByName("payload"); oneof != nil {
					if selected := runtime.WhichOneof(oneof); selected != nil {
						detail := runtime.Get(selected).Message()
						id := textField(detail, "request_id")
						attrs = append(attrs, attribute.String("cyber.jev.event_kind", string(selected.Name())), attribute.String("cyber.request.id", id))
						if mechanism, ok := b.scopeContext(scopeKey{event.SessionId, "jev", id}); ok {
							ctx = mechanism
						}
					}
				}
			}
		}
		if string(payload.MessageName()) == "cyber.guardrail.Review" {
			if value, err := payload.UnmarshalNew(); err == nil {
				attrs = append(attrs, attribute.String("cyber.guardrail.state", enumField(value.ProtoReflect(), "state")))
				if mechanism, ok := b.scopeContext(scopeKey{event.SessionId, "guardrail", ref.OperationId}); ok {
					ctx = mechanism
				}
			}
		}
	}
	if conversionErr != nil {
		severity = otellog.SeverityError
		attrs = append(attrs, attribute.String("cyber.conversion.error", clip(conversionErr.Error())))
	}
	name := kind
	if !strings.HasPrefix(name, "aop.") {
		name = "aop." + kind
	}
	s.emit(ctx, name, stamp(event), severity, attrs...)
}
