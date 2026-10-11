package otel

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"
)

type signals struct {
	logger                                                            otellog.Logger
	events, drops, sessions, turns, scopes, requests, tokens, missing metric.Int64Counter
	sessionsActive, turnsActive, scopesActive                         metric.Int64UpDownCounter
	sessionDuration, turnDuration, scopeDuration, modelDuration       metric.Float64Histogram
	tokenUsage                                                        metric.Int64Histogram
}

func newSignals(logs *sdklog.LoggerProvider, metrics *sdkmetric.MeterProvider) (*signals, error) {
	s := new(signals)
	if logs != nil {
		s.logger = logs.Logger(instrumentationName)
	}
	if metrics == nil {
		return s, nil
	}
	meter := metrics.Meter(instrumentationName)
	var errs []error
	counter := func(name, unit, description string) metric.Int64Counter {
		value, err := meter.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(description))
		errs = append(errs, err)
		return value
	}
	active := func(name, description string) metric.Int64UpDownCounter {
		value, err := meter.Int64UpDownCounter(name, metric.WithUnit("{scope}"), metric.WithDescription(description))
		errs = append(errs, err)
		return value
	}
	duration := func(name, description string) metric.Float64Histogram {
		value, err := meter.Float64Histogram(name, metric.WithUnit("s"), metric.WithDescription(description), metric.WithExplicitBucketBoundaries(.001, .005, .01, .05, .1, .5, 1, 5, 10, 30, 60, 300))
		errs = append(errs, err)
		return value
	}
	s.events = counter("cyber.aop.events", "{event}", "Accepted AOP events, deduplicated within the correlation window")
	s.drops = counter("cyber.otel.events.dropped", "{event}", "AOP subscription drops observed at Flush or Close")
	s.sessions = counter("cyber.agent.sessions", "{session}", "Observed Agent session lifecycle transitions")
	s.turns = counter("cyber.agent.turns", "{turn}", "Completed Agent turns, including incomplete turns")
	s.scopes = counter("cyber.scope.completed", "{scope}", "Completed execution scopes by kind and outcome")
	s.requests = counter("cyber.model.requests", "{request}", "Observed model requests; retry accounting and compiler summaries excluded")
	s.tokens = counter("cyber.model.tokens", "{token}", "Reported input/output tokens, counted once per available accounting fact")
	s.missing = counter("cyber.model.usage.missing", "{request}", "Model requests without complete reported usage")
	s.sessionsActive = active("cyber.agent.sessions.active", "Sessions with observed start and no observed end")
	s.turnsActive = active("cyber.agent.turns.active", "Agent turns currently observed as active")
	s.scopesActive = active("cyber.scope.active", "Execution scopes currently observed as active")
	s.sessionDuration = duration("cyber.agent.session.duration", "Observed Agent session lifetime")
	s.turnDuration = duration("cyber.agent.turn.duration", "Observed Agent turn duration")
	s.scopeDuration = duration("cyber.scope.duration", "Observed execution scope duration, including mechanisms")
	s.modelDuration = duration("gen_ai.client.operation.duration", "Observed model request duration; estimated endpoints marked in trace/logs")
	var err error
	s.tokenUsage, err = meter.Int64Histogram("gen_ai.client.token.usage", metric.WithUnit("{token}"), metric.WithDescription("Reported input/output token usage; summaries excluded"), metric.WithExplicitBucketBoundaries(1, 10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000))
	errs = append(errs, err)
	return s, errors.Join(errs...)
}

func logAttribute(attr attribute.KeyValue) otellog.KeyValue {
	key := string(attr.Key)
	switch attr.Value.Type() {
	case attribute.BOOL:
		return otellog.Bool(key, attr.Value.AsBool())
	case attribute.INT64:
		return otellog.Int64(key, attr.Value.AsInt64())
	case attribute.FLOAT64:
		return otellog.Float64(key, attr.Value.AsFloat64())
	case attribute.STRINGSLICE:
		var values []otellog.Value
		for _, value := range attr.Value.AsStringSlice() {
			values = append(values, otellog.StringValue(value))
		}
		return otellog.Slice(key, values...)
	default:
		return otellog.String(key, attr.Value.AsString())
	}
}
func (s *signals) emit(ctx context.Context, name string, at time.Time, severity otellog.Severity, attrs ...attribute.KeyValue) {
	if s == nil || s.logger == nil {
		return
	}
	var record otellog.Record
	record.SetTimestamp(at)
	record.SetObservedTimestamp(time.Now())
	record.SetEventName(name)
	record.SetBody(otellog.StringValue(name))
	record.SetSeverity(severity)
	switch severity {
	case otellog.SeverityError:
		record.SetSeverityText("ERROR")
	case otellog.SeverityWarn:
		record.SetSeverityText("WARN")
	default:
		record.SetSeverityText("INFO")
	}
	record.AddAttributes(otellog.String("cyber.event.name", name), otellog.String("cyber.log.id", aop.EnvelopeID()))
	for _, attr := range attrs {
		record.AddAttributes(logAttribute(attr))
	}
	s.logger.Emit(ctx, record)
}
func (s *signals) dropped(ctx context.Context, count uint64) {
	if s == nil {
		return
	}
	if s.drops != nil {
		s.drops.Add(ctx, int64(count))
	}
	s.emit(ctx, "otel.events.dropped", time.Now(), otellog.SeverityWarn, attribute.Int64("cyber.otel.events.dropped", int64(count)))
}

// This decorator runs at the AOP projection boundary, before trace sampling or
// export. Its local attributes make metrics/logs independent of IsRecording;
// no Agent hooks, globals or exported-span reprocessing are involved.
type signalTracer struct {
	trace.Tracer
	signals *signals
}

func (t signalTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, inner := t.Tracer.Start(ctx, name, opts...)
	s := t.signals
	if s == nil || (s.logger == nil && s.scopes == nil) {
		return ctx, inner
	}
	config := trace.NewSpanStartConfig(opts...)
	at := config.Timestamp()
	if at.IsZero() {
		at = time.Now()
	}
	span := &signalSpan{Span: inner, signals: s, ctx: ctx, name: name, start: at, attrs: make(map[attribute.Key]attribute.Value)}
	for _, attr := range config.Attributes() {
		span.attrs[attr.Key] = attr.Value
	}
	span.kind = scopeKind(name, span.attrs)
	labels := []attribute.KeyValue{attribute.String("cyber.scope.kind", span.kind)}
	if s.scopesActive != nil {
		s.scopesActive.Add(ctx, 1, metric.WithAttributes(labels...))
	}
	if span.kind == "turn" && s.turnsActive != nil {
		s.turnsActive.Add(ctx, 1)
	}
	s.emit(ctx, span.lifecycleName("started"), at, otellog.SeverityInfo, span.logAttributes()...)
	return trace.ContextWithSpan(ctx, span), span
}

type signalSpan struct {
	trace.Span
	signals    *signals
	mu         sync.Mutex
	ctx        context.Context
	name, kind string
	start      time.Time
	attrs      map[attribute.Key]attribute.Value
	status     codes.Code
	ended      bool
}

func (s *signalSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	for _, attr := range attrs {
		s.attrs[attr.Key] = attr.Value
	}
	s.Span.SetAttributes(attrs...)
}
func (s *signalSpan) SetStatus(code codes.Code, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.status = code
	s.Span.SetStatus(code, description)
}
func (s *signalSpan) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.name = name
		s.Span.SetName(name)
	}
}
func scopeKind(name string, attrs map[attribute.Key]attribute.Value) string {
	if name == "agent.turn" {
		return "turn"
	}
	if name == "llm.retry_usage" {
		return "accounting"
	}
	if strings.HasPrefix(name, "llm.") {
		return "llm"
	}
	if kind := attrs["cyber.operation.kind"].AsString(); kind != "" {
		switch kind {
		case "tool", "command", "process":
			return kind
		default:
			return "operation"
		}
	}
	if kind := attrs["cyber.mechanism.kind"].AsString(); kind != "" {
		switch kind {
		case "compact", "eval", "guardrail", "jev":
			return kind
		}
	}
	return "fact"
}
func (s *signalSpan) lifecycleName(phase string) string {
	if s.kind == "turn" {
		return "agent.turn." + phase
	}
	if s.kind == "llm" {
		return "model.request." + phase
	}
	if s.kind == "tool" || s.kind == "command" || s.kind == "process" || s.kind == "operation" {
		return "operation." + phase
	}
	return "scope." + phase
}
func (s *signalSpan) logAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String("cyber.scope.name", s.name), attribute.String("cyber.scope.kind", s.kind), attribute.String("cyber.lifecycle.source", "aop_projection")}
	for key, value := range s.attrs {
		attrs = append(attrs, attribute.KeyValue{Key: key, Value: value})
	}
	return attrs
}
func (s *signalSpan) End(opts ...trace.SpanEndOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	config := trace.NewSpanEndConfig(opts...)
	at := config.Timestamp()
	if at.IsZero() {
		at = time.Now()
	}
	duration := max(0, at.Sub(s.start).Seconds())
	outcome := "success"
	if s.attrs["cyber.incomplete"].AsBool() {
		outcome = "incomplete"
	} else if s.attrs["cyber.stop_reason"].AsString() == "canceled" || s.attrs["cyber.failure.kind"].AsString() == operationpb.FailureKind_FAILURE_KIND_CANCELED.String() {
		outcome = "canceled"
	} else if s.status == codes.Error {
		outcome = "error"
	} else if state := s.attrs["cyber.guardrail.state"].AsString(); state != "" {
		switch state {
		case "REVIEW_STATE_REJECTED":
			outcome = "rejected"
		case "REVIEW_STATE_EXPIRED":
			outcome = "expired"
		case "REVIEW_STATE_CANCELED":
			outcome = "canceled"
		}
	}
	labels := []attribute.KeyValue{attribute.String("cyber.scope.kind", s.kind), attribute.String("cyber.outcome", outcome)}
	metrics := s.signals
	if metrics.scopes != nil {
		metrics.scopes.Add(s.ctx, 1, metric.WithAttributes(labels...))
		metrics.scopeDuration.Record(s.ctx, duration, metric.WithAttributes(labels...))
		metrics.scopesActive.Add(s.ctx, -1, metric.WithAttributes(attribute.String("cyber.scope.kind", s.kind)))
		if s.kind == "turn" {
			metrics.turns.Add(s.ctx, 1, metric.WithAttributes(attribute.String("cyber.outcome", outcome)))
			metrics.turnDuration.Record(s.ctx, duration, metric.WithAttributes(attribute.String("cyber.outcome", outcome)))
			metrics.turnsActive.Add(s.ctx, -1)
		}
		model := s.attrs["gen_ai.request.model"].AsString()
		if model == "" {
			model = s.attrs["llm.model_name"].AsString()
		}
		if model == "" {
			model = "unknown"
		}
		modelLabels := []attribute.KeyValue{attribute.String("gen_ai.operation.name", "chat"), attribute.String("gen_ai.request.model", model)}
		if s.attrs["openinference.span.kind"].AsString() == "LLM" {
			metrics.requests.Add(s.ctx, 1, metric.WithAttributes(append(modelLabels, attribute.String("cyber.outcome", outcome))...))
			metrics.modelDuration.Record(s.ctx, duration, metric.WithAttributes(modelLabels...))
			if s.attrs["cyber.usage.missing"].AsBool() {
				metrics.missing.Add(s.ctx, 1, metric.WithAttributes(modelLabels...))
			}
		}
		if _, known := s.attrs["llm.token_count.total"]; known {
			accounting := s.attrs["cyber.usage.scope"].AsString()
			if accounting == "" {
				accounting = "request"
			}
			for _, token := range []struct{ kind, key string }{{"input", "llm.token_count.prompt"}, {"output", "llm.token_count.completion"}} {
				attrs := append(append([]attribute.KeyValue(nil), modelLabels...), attribute.String("gen_ai.token.type", token.kind), attribute.String("cyber.usage.scope", accounting))
				count := s.attrs[attribute.Key(token.key)].AsInt64()
				metrics.tokens.Add(s.ctx, count, metric.WithAttributes(attrs...))
				metrics.tokenUsage.Record(s.ctx, count, metric.WithAttributes(attrs...))
			}
			metrics.tokens.Add(s.ctx, s.attrs["llm.token_count.total"].AsInt64(), metric.WithAttributes(append(append([]attribute.KeyValue(nil), modelLabels...), attribute.String("gen_ai.token.type", "total"), attribute.String("cyber.usage.scope", accounting))...))
		}
	}
	attrs := append(s.logAttributes(), attribute.String("cyber.outcome", outcome), attribute.Float64("cyber.duration.seconds", duration))
	severity := otellog.SeverityInfo
	if outcome == "error" || outcome == "incomplete" {
		severity = otellog.SeverityError
	}
	phase := "completed"
	if outcome == "incomplete" {
		phase = "incomplete"
	}
	metrics.emit(s.ctx, s.lifecycleName(phase), at, severity, attrs...)
	s.Span.End(opts...)
}
