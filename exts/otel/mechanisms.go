package otel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// These adapters interpret existing Cyber Any payloads. Descriptor lookup keeps
// OTel independent of optional JEV/guardrail runtimes. Unknown payloads remain
// AOP facts; a binary that uses those extensions already registers their types.
func (b *aopBridge) mechanism(event *aop.Event, ref *operationpb.Ref) (bool, error) {
	if status := event.GetStatus(); status != nil {
		switch status.State {
		case types.CompactStateStart, types.CompactStateEnd, types.CompactStateError:
			detail, _, err := types.GetCompactDetail(event)
			if err != nil {
				return true, err
			}
			attrs := []attribute.KeyValue{attribute.String("cyber.mechanism.kind", "compact"), attribute.Int64("cyber.compact.tokens_before", int64(detail.TokensBefore)), attribute.Int64("cyber.compact.tokens_after", int64(detail.TokensAfter)), attribute.Int64("cyber.compact.kept_messages", int64(detail.KeptMessages))}
			return true, b.statusScope(event, "compact", event.TurnId, status.State, detail.Error, attrs)
		case types.EvalStateStart, types.EvalStateEnd, types.EvalStateError:
			detail, _, err := types.GetEvalDetail(event)
			if err != nil {
				return true, err
			}
			attrs := []attribute.KeyValue{attribute.String("cyber.mechanism.kind", "eval"), attribute.Int("cyber.eval.round", int(detail.Round)), attribute.Int("cyber.eval.max_rounds", int(detail.MaxRounds)), attribute.Bool("cyber.eval.pass", detail.Pass)}
			if b.capture {
				attrs = append(attrs, attribute.String("cyber.eval.reason", clip(detail.Reason)))
			}
			return true, b.statusScope(event, "eval", fmt.Sprintf("%s/%d", event.TurnId, detail.Round), status.State, detail.Error, attrs)
		}
		if status.State == "token_budget_warning" {
			detail := new(types.BudgetWarning)
			if _, err := aop.FindTypedExtension(event, detail); err != nil {
				return true, err
			}
			b.fact(event, ref, attribute.Int64("cyber.context.tokens", int64(detail.ContextTokens)), attribute.Int64("cyber.token_budget", int64(detail.TokenBudget)))
			return true, nil
		}
	}
	payload := event.GetExtension()
	if payload == nil {
		return false, nil
	}
	switch string(payload.MessageName()) {
	case "cyber.jev.RuntimeEvent", "cyber.guardrail.Review":
		value, err := payload.UnmarshalNew()
		if errors.Is(err, protoregistry.NotFound) {
			return false, nil
		}
		if err != nil {
			return true, err
		}
		if string(payload.MessageName()) == "cyber.jev.RuntimeEvent" {
			return true, b.jev(event, value.ProtoReflect())
		}
		return true, b.review(event, ref, value.ProtoReflect())
	default:
		return false, nil
	}
}
func (b *aopBridge) statusScope(event *aop.Event, kind, id, state, failure string, attrs []attribute.KeyValue) error {
	key := scopeKey{event.SessionId, kind, id}
	// Status lifecycles have no request IDs. Multiple compactions in one Turn
	// reuse this identity only after the preceding lifecycle has completed.
	if strings.HasSuffix(state, "_start") {
		b.endedScopes.forget(key)
		b.startScope(event, key, "agent."+kind, "CHAIN", stamp(event), scopeKey{}, false, attrs...)
		return nil
	}
	current := b.scopes[key]
	if current == nil {
		attrs = append(attrs, attribute.Bool("cyber.start_missing", true))
		current = b.startScope(event, key, "agent."+kind, "CHAIN", stamp(event), scopeKey{}, false, attrs...)
	}
	if current == nil {
		return nil
	}
	current.span.SetAttributes(attrs...)
	if failure != "" {
		current.span.SetStatus(codes.Error, failure)
	}
	b.endScope(key, stamp(event))
	return nil
}

func field(message protoreflect.Message, name string) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(protoreflect.Name(name))
}
func textField(message protoreflect.Message, name string) string {
	if f := field(message, name); f != nil {
		return message.Get(f).String()
	}
	return ""
}
func messageField(message protoreflect.Message, name string) protoreflect.Message {
	if f := field(message, name); f != nil && message.Has(f) {
		return message.Get(f).Message()
	}
	return nil
}
func intField(message protoreflect.Message, name string) int64 {
	if f := field(message, name); f != nil {
		return message.Get(f).Int()
	}
	return 0
}
func uintField(message protoreflect.Message, name string) uint64 {
	if f := field(message, name); f != nil {
		return message.Get(f).Uint()
	}
	return 0
}
func boolField(message protoreflect.Message, name string) bool {
	if f := field(message, name); f != nil {
		return message.Get(f).Bool()
	}
	return false
}
func enumField(message protoreflect.Message, name string) string {
	if f := field(message, name); f != nil {
		if value := f.Enum().Values().ByNumber(message.Get(f).Enum()); value != nil {
			return string(value.Name())
		}
	}
	return ""
}
func copyMessage(message protoreflect.Message, target proto.Message) error {
	data, err := proto.Marshal(message.Interface())
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, target)
}
func reportedUsage(message protoreflect.Message) (*aop.TokenUsage, error) {
	value := messageField(message, "usage")
	if value == nil {
		return nil, nil
	}
	usage := new(aop.TokenUsage)
	err := copyMessage(value, usage)
	return usage, err
}
func (b *aopBridge) jev(event *aop.Event, runtime protoreflect.Message) error {
	attrs := []attribute.KeyValue{attribute.Bool("cyber.background", boolField(runtime, "background")), attribute.Int64("cyber.jev.step", int64(uintField(runtime, "step")))}
	eventKind := "unknown"
	if oneof := runtime.Descriptor().Oneofs().ByName("payload"); oneof != nil {
		if selected := runtime.WhichOneof(oneof); selected != nil {
			eventKind = string(selected.Name())
		}
	}
	attrs = append(attrs, attribute.String("cyber.jev.event_kind", eventKind))
	for _, name := range []string{"task_id", "segment_id", "previous_segment_id", "reflex_id", "call_id", "boundary_id", "claim_id"} {
		attrs = append(attrs, attribute.String("cyber.jev."+name, textField(runtime, name)))
	}
	background := boolField(runtime, "background")
	var detail protoreflect.Message
	var kind string
	var started, finished bool
	if detail = messageField(runtime, "decision_request"); detail != nil {
		kind = "decision"
		started = true
	}
	if result := messageField(runtime, "decision_result"); result != nil {
		detail = result
		kind = "decision"
		finished = true
	}
	if generation := messageField(runtime, "generation"); generation != nil {
		detail = generation
		kind = textField(detail, "kind")
		started = textField(detail, "state") == "started"
		finished = textField(detail, "state") == "finished"
	}
	if detail == nil || (!started && !finished) {
		if fact := messageField(runtime, eventKind); fact != nil {
			for _, name := range []string{"state", "code", "error_stage", "replaced_reflex_id"} {
				if field(fact, name) != nil {
					attrs = append(attrs, attribute.String("cyber.jev."+name, textField(fact, name)))
				}
			}
		}
		if background {
			key := scopeKey{event.SessionId, "jev.fact", event.Id}
			if state := b.startScope(event, key, "jev."+eventKind, "CHAIN", stamp(event), scopeKey{}, true, attrs...); state != nil {
				// Keep opt-in evidence and extension type on background facts too.
				b.addFact(state.span, event, attrs)
				b.endScope(key, stamp(event))
			}
		} else {
			b.fact(event, nil, attrs...)
		}
		return nil
	}
	id := textField(detail, "request_id")
	if id == "" {
		b.fact(event, nil, append(attrs, attribute.Bool("cyber.request.id_missing", true))...)
		return nil
	}
	key := scopeKey{event.SessionId, "jev", id}
	parent := scopeKey{event.SessionId, "jev", textField(detail, "parent_request_id")}
	attrs = append(attrs, attribute.String("cyber.mechanism.kind", "jev"), attribute.String("cyber.jev.kind", kind), attribute.String("cyber.request.id", id), attribute.String("cyber.request.parent_id", parent.id), attribute.String("cyber.jev.purpose", textField(detail, "purpose")), attribute.Int64("cyber.jev.attempt", int64(uintField(detail, "attempt"))), attribute.String("cyber.jev.phase", textField(detail, "phase")), attribute.String("cyber.jev.error_stage", textField(detail, "error_stage")), attribute.String("gen_ai.request.reasoning_effort", textField(detail, "requested_effort")))
	spanKind := "CHAIN"
	if kind == "decision" || kind == "compiler_round" || strings.HasSuffix(kind, "_llm") {
		spanKind = "LLM"
	}
	// reflex_llm is a compilation summary, including in older records without
	// atomic rounds. Never bill it as a leaf: rounds can arrive after its End.
	if kind == "reflex_llm" {
		spanKind = "CHAIN"
	}
	at := stamp(event)
	if finished && b.scopes[key] == nil {
		elapsed := intField(detail, "elapsed_ms")
		if elapsed > 0 {
			at = at.Add(-time.Duration(elapsed) * time.Millisecond)
		}
		attrs = append(attrs, attribute.Bool("cyber.start_missing", true))
	}
	state := b.startScope(event, key, "jev."+kind, spanKind, at, parent, background, attrs...)
	if state == nil {
		return nil
	}
	if kind == "compiler_round" && parent.id != "" {
		if owner := b.scopes[parent]; owner != nil {
			owner.children = true
		}
	}
	if !finished {
		return nil
	}
	state.span.SetAttributes(attrs...)
	if failure := textField(detail, "error"); failure != "" {
		state.span.SetStatus(codes.Error, failure)
	}
	usage, err := reportedUsage(detail)
	if err != nil {
		return err
	}
	if kind == "reflex_llm" {
		state.span.SetAttributes(attribute.String("openinference.span.kind", "CHAIN"), attribute.String("cyber.usage.scope", "summary"), attribute.Bool("cyber.usage.leaf_observed_at_end", state.children))
		if usage != nil {
			state.span.SetAttributes(usageSummary(usage)...)
		}
	} else if spanKind == "LLM" {
		state.span.SetAttributes(attribute.String("openinference.span.kind", "LLM"), attribute.String("cyber.usage.scope", "logical_request"))
		if usage != nil {
			setUsage(state.span, usage)
		} else {
			state.span.SetAttributes(attribute.Bool("cyber.usage.missing", true))
		}
	}
	if b.capture {
		state.span.SetAttributes(attribute.String("output.value", clip(textField(detail, "output"))))
	}
	b.endScope(key, stamp(event))
	return nil
}
func (b *aopBridge) review(event *aop.Event, ref *operationpb.Ref, detail protoreflect.Message) error {
	if ref.OperationId == "" {
		if value := messageField(detail, "operation"); value != nil {
			if err := copyMessage(value, ref); err != nil {
				return err
			}
		}
	}
	stateName := enumField(detail, "state")
	attrs := []attribute.KeyValue{attribute.String("cyber.mechanism.kind", "guardrail"), attribute.String("cyber.guardrail.state", stateName), attribute.String("cyber.operation.id", ref.OperationId), attribute.String("cyber.guardrail.resolution_source", textField(detail, "resolution_source"))}
	if decision := messageField(detail, "decision"); decision != nil {
		attrs = append(attrs, attribute.String("cyber.guardrail.action", enumField(decision, "action")))
		if b.capture {
			attrs = append(attrs, attribute.String("cyber.guardrail.reason", clip(textField(decision, "reason"))))
		}
	}
	if ref.OperationId == "" {
		b.fact(event, ref, attrs...)
		return nil
	}
	key := scopeKey{event.SessionId, "guardrail", ref.OperationId}
	if _, ended := b.endedScopes.get(key); ended {
		return nil
	}
	if stateName == "REVIEW_STATE_PENDING" {
		// Review precedes the tool's Started event; parent it to the caller's
		// scope, rather than fabricating an unstarted tool execution.
		b.startScope(event, key, "guardrail.review", "CHAIN", stamp(event), scopeKey{event.SessionId, "operation", ref.ParentOperationId}, false, attrs...)
		return nil
	}
	state := b.scopes[key]
	if state == nil {
		b.fact(event, ref, attrs...)
		return nil
	}
	state.span.SetAttributes(attrs...)
	// A policy rejection is an outcome, not an exporter/runtime failure.
	b.endScope(key, stamp(event))
	return nil
}
