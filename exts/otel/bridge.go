package otel

import (
	"context"
	"time"

	"github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
)

// aopBridge consumes only published AOP facts. It has no Agent/provider hooks
// and does not require OTel identities or new payloads from AOP producers.
// The subscription serializes access; completed correlation is bounded.
type aopBridge struct {
	tracer        trace.Tracer
	capture       bool
	signals       *signals
	turns         map[turnKey]*turn
	sessions      map[string]*session
	scopes        map[scopeKey]*scopeSpan
	seen          *recent[turnKey, bool]
	endedTurns    *recent[turnKey, context.Context]
	endedScopes   *recent[scopeKey, context.Context]
	endedSessions *recent[string, *session]
	calls         *recent[turnKey, context.Context]
}

type turnKey struct{ session, turn string }
type scopeKey struct{ session, kind, id string }
type session struct {
	model, parent, call string
	delegation          *types.DelegationDetail
	emitter             string
	start, last         time.Time
	ctx                 context.Context
}
type turn struct {
	ctx      context.Context
	modelCtx context.Context
	model    string
	span     trace.Span
	requests int
	llm      []*requestSpan
	input    string
	last     time.Time
}
type requestSpan struct {
	span       trace.Span
	start, end time.Time
}
type scopeSpan struct {
	ctx         context.Context
	span        trace.Span
	start, last time.Time
	children    bool
}

// A FIFO retains identities after End so late events cannot resurrect a span.
// It also retains parent contexts after export, without retaining span payloads.
type recent[K comparable, V any] struct {
	values map[K]V
	keys   []K
	next   int
}

func newRecent[K comparable, V any]() *recent[K, V] { return &recent[K, V]{values: make(map[K]V)} }
func (r *recent[K, V]) get(key K) (V, bool)         { value, ok := r.values[key]; return value, ok }
func (r *recent[K, V]) put(key K, value V) {
	if _, ok := r.values[key]; ok {
		r.values[key] = value
		return
	}
	const limit = 4096
	if len(r.keys) < limit {
		r.keys = append(r.keys, key)
	} else {
		delete(r.values, r.keys[r.next])
		r.keys[r.next] = key
		r.next = (r.next + 1) % limit
	}
	r.values[key] = value
}

func (r *recent[K, V]) forget(key K) {
	delete(r.values, key)
	// Preserve FIFO ordering when a lifecycle without a request ID is reused.
	ordered := append(append([]K(nil), r.keys[r.next:]...), r.keys[:r.next]...)
	r.keys = r.keys[:0]
	for _, existing := range ordered {
		if existing != key {
			r.keys = append(r.keys, existing)
		}
	}
	r.next = 0
}
func newAOPBridge(tracer trace.Tracer, capture bool) *aopBridge {
	return &aopBridge{tracer: tracer, capture: capture,
		turns: make(map[turnKey]*turn), sessions: make(map[string]*session), scopes: make(map[scopeKey]*scopeSpan),
		seen: newRecent[turnKey, bool](), endedTurns: newRecent[turnKey, context.Context](),
		endedScopes: newRecent[scopeKey, context.Context](), endedSessions: newRecent[string, *session](), calls: newRecent[turnKey, context.Context]()}
}
func stamp(event *aop.Event) time.Time {
	if event.EmittedAt != nil {
		return event.EmittedAt.AsTime()
	}
	return time.Now()
}
func attributes(event *aop.Event) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("cyber.session.id", event.SessionId), attribute.String("session.id", event.SessionId),
		attribute.String("cyber.turn.id", event.TurnId), attribute.String("cyber.emitter", event.Emitter),
		attribute.String("aop.event.id", event.Id), attribute.Int64("aop.event.seq", int64(event.Seq)),
	}
}
func (b *aopBridge) session(id string) *session {
	if state := b.sessions[id]; state != nil {
		return state
	}
	state, _ := b.endedSessions.get(id)
	return state
}
func (b *aopBridge) ensureTurn(event *aop.Event) *turn {
	key := turnKey{event.SessionId, event.TurnId}
	if key.session == "" || key.turn == "" {
		return nil
	}
	if _, ended := b.endedTurns.get(key); ended {
		return nil
	}
	if existing := b.turns[key]; existing != nil {
		if at := stamp(event); at.After(existing.last) {
			existing.last = at
		}
		return existing
	}
	ctx := context.Background()
	attrs := append(attributes(event), attribute.String("openinference.span.kind", "AGENT"), attribute.Bool("cyber.start_missing", event.GetTurnStarted() == nil))
	opts := []trace.SpanStartOption{trace.WithTimestamp(stamp(event))}
	if state := b.session(event.SessionId); state != nil {
		attrs = append(attrs, attribute.String("cyber.parent.session.id", state.parent), attribute.String("cyber.parent.call.id", state.call))
		background := false
		if d := state.delegation; d != nil {
			background = d.RunMode == types.DelegationRunBackground
			attrs = append(attrs, attribute.String("cyber.agent.id", d.AgentId), attribute.String("cyber.agent.name", d.AgentName), attribute.String("cyber.agent.type", d.AgentType), attribute.String("cyber.agent.run_mode", d.RunMode), attribute.String("cyber.agent.context_mode", d.ContextMode))
			if b.capture {
				attrs = append(attrs, attribute.String("cyber.agent.task", clip(d.Task)))
			}
		}
		if state.call != "" {
			if parent, ok := b.calls.get(turnKey{state.parent, state.call}); ok {
				if background {
					opts = append(opts, trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(parent)}))
					attrs = append(attrs, sourceAttributes(parent)...)
				} else {
					ctx = parent
				}
			} else {
				attrs = append(attrs, attribute.Bool("cyber.parent.missing", true))
			}
		}
	}
	opts = append(opts, trace.WithAttributes(attrs...))
	ctx, span := b.tracer.Start(ctx, "agent.turn", opts...)
	if state := b.sessions[event.SessionId]; state != nil {
		state.ctx = ctx
	}
	run := &turn{ctx: ctx, span: span, last: stamp(event)}
	b.turns[key] = run
	return run
}
func (b *aopBridge) scopeContext(key scopeKey) (context.Context, bool) {
	if state := b.scopes[key]; state != nil {
		return state.ctx, true
	}
	return b.endedScopes.get(key)
}
func (b *aopBridge) startScope(event *aop.Event, key scopeKey, name, kind string, at time.Time, parent scopeKey, background bool, attrs ...attribute.KeyValue) *scopeSpan {
	if existing := b.scopes[key]; existing != nil {
		return existing
	}
	if _, ended := b.endedScopes.get(key); ended {
		return nil
	}
	ctx := context.Background()
	opts := []trace.SpanStartOption{trace.WithTimestamp(at)}
	attrs = append(attributes(event), attrs...)
	attrs = append(attrs, attribute.String("openinference.span.kind", kind))
	if parent.id != "" {
		if known, ok := b.scopeContext(parent); ok {
			ctx = known
		} else {
			attrs = append(attrs, attribute.Bool("cyber.parent.missing", true))
		}
	}
	if !trace.SpanContextFromContext(ctx).IsValid() {
		key := turnKey{event.SessionId, event.TurnId}
		if source, closed := b.endedTurns.get(key); closed {
			opts = append(opts, trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(source)}))
			attrs = append(attrs, sourceAttributes(source)...)
			attrs = append(attrs, attribute.Bool("cyber.source.turn_ended", true))
		} else if background {
			if run := b.turns[key]; run != nil {
				opts = append(opts, trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(run.ctx)}))
				attrs = append(attrs, sourceAttributes(run.ctx)...)
			} else {
				attrs = append(attrs, attribute.Bool("cyber.source.missing", true))
			}
		} else if run := b.ensureTurn(event); run != nil {
			ctx = run.ctx
		}
	}
	if kind == "LLM" {
		opts = append(opts, trace.WithSpanKind(trace.SpanKindClient))
	}
	opts = append(opts, trace.WithAttributes(attrs...))
	ctx, span := b.tracer.Start(ctx, name, opts...)
	state := &scopeSpan{ctx: ctx, span: span, start: at, last: stamp(event)}
	b.scopes[key] = state
	return state
}
func endAt(span trace.Span, start, end time.Time) {
	if end.Before(start) {
		span.SetAttributes(attribute.Bool("cyber.timestamp.invalid", true))
		end = start
	}
	span.End(trace.WithTimestamp(end))
}

// Also expose source IDs as ordinary attributes for backends whose query API
// does not expose native OTel links (including the tested Phoenix version).
func sourceAttributes(ctx context.Context) []attribute.KeyValue {
	span := trace.SpanContextFromContext(ctx)
	return []attribute.KeyValue{attribute.String("cyber.source.trace_id", span.TraceID().String()), attribute.String("cyber.source.span_id", span.SpanID().String())}
}
func (b *aopBridge) endScope(key scopeKey, at time.Time) {
	if state := b.scopes[key]; state != nil {
		endAt(state.span, state.start, at)
		b.endedScopes.put(key, trace.ContextWithSpanContext(context.Background(), state.span.SpanContext()))
		delete(b.scopes, key)
	}
}
func (b *aopBridge) factAttributes(event *aop.Event, extra ...attribute.KeyValue) []attribute.KeyValue {
	attrs := append(attributes(event), attribute.String("aop.event.kind", aop.Kind(event)))
	attrs = append(attrs, extra...)
	if payload := event.GetExtension(); payload != nil {
		attrs = append(attrs, attribute.String("aop.extension.type", string(payload.MessageName())))
	}
	var extensionTypes []string
	for _, value := range event.Extensions {
		extensionTypes = append(extensionTypes, string(value.MessageName()))
	}
	if len(extensionTypes) > 0 {
		attrs = append(attrs, attribute.StringSlice("aop.extension.types", extensionTypes))
	}
	if b.capture {
		if data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(event); err == nil {
			attrs = append(attrs, attribute.String("aop.event.payload", clip(string(data))))
		}
	}
	return attrs
}

func (b *aopBridge) addFact(span trace.Span, event *aop.Event, attrs []attribute.KeyValue) {
	span.AddEvent(aop.Kind(event), trace.WithTimestamp(stamp(event)), trace.WithAttributes(b.factAttributes(event, attrs...)...))
}

func (b *aopBridge) fact(event *aop.Event, ref *operationpb.Ref, extra ...attribute.KeyValue) {
	attrs := b.factAttributes(event, extra...)
	if ref != nil {
		key := scopeKey{event.SessionId, "operation", ref.OperationId}
		if state := b.scopes[key]; state != nil {
			state.last = stamp(event)
			state.span.AddEvent(aop.Kind(event), trace.WithTimestamp(stamp(event)), trace.WithAttributes(attrs...))
			return
		}
	}
	if run := b.ensureTurn(event); run != nil {
		run.span.AddEvent(aop.Kind(event), trace.WithTimestamp(stamp(event)), trace.WithAttributes(attrs...))
		return
	}
	// A late/background fact remains observable without extending a closed Turn.
	ctx := context.Background()
	opts := []trace.SpanStartOption{trace.WithTimestamp(stamp(event)), trace.WithAttributes(append(attrs, attribute.String("openinference.span.kind", "CHAIN"))...)}
	if ref != nil {
		if parent, ok := b.scopeContext(scopeKey{event.SessionId, "operation", ref.OperationId}); ok {
			ctx = parent
		}
	}
	if !trace.SpanContextFromContext(ctx).IsValid() {
		if source, ok := b.endedTurns.get(turnKey{event.SessionId, event.TurnId}); ok {
			opts = append(opts, trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(source)}))
			opts = append(opts, trace.WithAttributes(sourceAttributes(source)...))
		}
	}
	_, span := b.tracer.Start(ctx, "aop."+aop.Kind(event), opts...)
	span.End(trace.WithTimestamp(stamp(event)))
}
func (b *aopBridge) finishOpen() {
	for key, state := range b.scopes {
		state.span.SetStatus(codes.Error, "scope completion was not observed")
		state.span.SetAttributes(attribute.Bool("cyber.incomplete", true))
		b.endScope(key, state.last)
	}
	for key, run := range b.turns {
		for _, request := range run.llm {
			if request.end.IsZero() {
				request.span.SetStatus(codes.Error, "request completion was not observed")
			}
		}
		b.finishRequests(run, nil, run.last)
		run.span.SetStatus(codes.Error, "turn completion was not observed")
		run.span.SetAttributes(attribute.Bool("cyber.incomplete", true))
		run.span.End(trace.WithTimestamp(run.last))
		delete(b.turns, key)
	}
	for id, state := range b.sessions {
		b.sessionEnded(id, state, "incomplete", state.last)
		delete(b.sessions, id)
	}
}
