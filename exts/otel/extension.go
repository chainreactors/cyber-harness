// Package otel exports the existing AOP stream as OTel traces, logs and metrics.
package otel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/protobuf/proto"
)

type Options struct {
	// Endpoint is the OTLP/HTTP base URL, e.g. http://127.0.0.1:24318.
	Endpoint    string
	ServiceName string
	Project     string
	// CaptureContent includes bounded input/output and event payloads. Off by default.
	CaptureContent bool
	Queue          int
	// Exporter optionally supplies an application-owned destination. The extension
	// owns its lifecycle once loaded; omit to use the OTLP/HTTP exporter.
	Exporter sdktrace.SpanExporter
	// Endpoint enables all three signals. With no Endpoint, only explicitly
	// supplied exporters are enabled. The extension owns every exporter.
	LogExporter                 sdklog.Exporter
	MetricExporter              sdkmetric.Exporter
	DisableLogs, DisableMetrics bool
	// MetricInterval defaults to 10 seconds; Flush/Close also export metrics.
	MetricInterval time.Duration
	// ResourceAttributes apply equally to all signals, e.g. service.instance.id.
	ResourceAttributes []attribute.KeyValue
}

type Extension struct {
	options        Options
	lifecycle      sync.Mutex
	sub            *eventbus.Subscription[*aop.Event]
	provider       *sdktrace.TracerProvider
	exporter       *checkedExporter
	logs           *sdklog.LoggerProvider
	metrics        *sdkmetric.MeterProvider
	failures       exportErrors
	signals        *signals
	reportedDrops  uint64
	bridge         *aopBridge
	loaded, closed bool
}

var _ extension.Extension = (*Extension)(nil)

func New(options Options) (*Extension, error) {
	if options.Exporter == nil || options.Endpoint != "" {
		u, err := url.Parse(options.Endpoint)
		if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("OTel requires an OTLP/HTTP endpoint URL")
		}
	}
	if options.ServiceName == "" {
		options.ServiceName = "cyber"
	}
	if options.Project == "" {
		options.Project = options.ServiceName
	}
	if options.Queue <= 0 {
		options.Queue = 512
	}
	if options.MetricInterval <= 0 {
		options.MetricInterval = 10 * time.Second
	}
	options.ResourceAttributes = append([]attribute.KeyValue(nil), options.ResourceAttributes...)
	return &Extension{options: options}, nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if e.closed {
		return fmt.Errorf("OTel extension is closed")
	}
	if e.loaded {
		return nil
	}
	stream, err := extension.Use[*events.Stream](scope)
	if err != nil {
		return err
	}
	exporter := e.options.Exporter
	if exporter == nil {
		exporter, err = otlptracehttp.New(scope.Init(), otlptracehttp.WithEndpointURL(otlpURL(e.options.Endpoint, "traces")), otlptracehttp.WithTimeout(5*time.Second))
		if err != nil {
			return err
		}
	}
	e.exporter = &checkedExporter{SpanExporter: exporter}
	attrs := append([]attribute.KeyValue{attribute.String("service.name", e.options.ServiceName), attribute.String("service.instance.id", aop.EnvelopeID()), attribute.String("openinference.project.name", e.options.Project)}, e.options.ResourceAttributes...)
	res := resource.NewSchemaless(attrs...)
	e.provider = sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(e.exporter, sdktrace.WithMaxQueueSize(2048), sdktrace.WithMaxExportBatchSize(512), sdktrace.WithBatchTimeout(100*time.Millisecond), sdktrace.WithExportTimeout(5*time.Second), sdktrace.WithBlocking()),
	)
	if err := e.loadSignals(scope.Init(), res); err != nil {
		return errors.Join(err, e.shutdown(context.WithoutCancel(scope.Init())))
	}
	tracer := e.provider.Tracer(instrumentationName)
	e.bridge = newAOPBridge(signalTracer{Tracer: tracer, signals: e.signals}, e.options.CaptureContent)
	e.bridge.signals = e.signals
	e.sub, err = stream.Consume(eventbus.SubscribeOptions[*aop.Event]{
		Buffer: e.options.Queue, MaxBytes: 16 << 20,
		Filter: func(event *aop.Event) bool {
			if event == nil {
				return false
			}
			switch event.Payload.(type) {
			case *aop.Event_MessageDelta, *aop.Event_ToolCallDelta, *aop.Event_ProviderFrame:
				return false
			}
			return true
		},
		Size:  func(event *aop.Event) int64 { return int64(proto.Size(event)) },
		Clone: func(event *aop.Event) *aop.Event { return proto.CloneOf(event) },
	}, e.bridge.consume)
	if err != nil {
		return errors.Join(err, e.shutdown(context.WithoutCancel(scope.Init())))
	}
	e.loaded = true
	return nil
}

func (e *Extension) Flush(ctx context.Context) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if e.sub == nil || e.closed {
		return nil
	}
	if err := e.sub.Flush(ctx); err != nil {
		return err
	}
	e.recordDrops()
	var logErr, metricErr error
	if e.logs != nil {
		logErr = e.logs.ForceFlush(ctx)
	}
	if e.metrics != nil {
		metricErr = e.metrics.ForceFlush(ctx)
	}
	return errors.Join(e.subscriptionError(), e.provider.ForceFlush(ctx), logErr, metricErr, e.exporter.Err(), e.failures.Err())
}

func (e *Extension) Close(ctx context.Context) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if e.closed {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.sub != nil {
		if err := e.sub.Close(ctx); err != nil {
			return err
		}
	}
	if e.provider == nil {
		e.closed = true
		return nil
	}
	e.bridge.finishOpen()
	e.recordDrops()
	err := e.shutdown(ctx)
	e.closed = true
	return errors.Join(err, e.subscriptionError(), e.exporter.Err(), e.failures.Err())
}

func (e *Extension) subscriptionError() error {
	if e.sub == nil {
		return nil
	}
	var overflow error
	if dropped := e.sub.Dropped(); dropped > 0 {
		overflow = fmt.Errorf("OTel signals incomplete: %d AOP events dropped", dropped)
	}
	return errors.Join(e.sub.Err(), overflow)
}

// Batch exporters report asynchronous failures through the SDK error handler.
// Retain them as well so Flush/Close cannot claim a successful export after loss.
type checkedExporter struct {
	sdktrace.SpanExporter
	mu  sync.Mutex
	err error
}

func (e *checkedExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		e.mu.Lock()
		if e.err == nil {
			e.err = fmt.Errorf("export OTel spans: %w", err)
		}
		e.mu.Unlock()
	}
	return err
}

func (e *checkedExporter) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

func clip(value string) string {
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "") + "…"
}
