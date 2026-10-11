package otel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

const instrumentationName = "github.com/chainreactors/cyber/exts/otel"

func otlpURL(endpoint, signal string) string {
	value, _ := url.JoinPath(endpoint, "v1", signal)
	return value
}

func (e *Extension) loadSignals(ctx context.Context, res *resource.Resource) error {
	if !e.options.DisableLogs && (e.options.Endpoint != "" || e.options.LogExporter != nil) {
		exporter := e.options.LogExporter
		if exporter == nil {
			var err error
			exporter, err = otlploghttp.New(ctx, otlploghttp.WithEndpointURL(otlpURL(e.options.Endpoint, "logs")), otlploghttp.WithTimeout(5*time.Second))
			if err != nil {
				return err
			}
		}
		e.logs = sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(&checkedLogExporter{Exporter: exporter, failures: &e.failures}, sdklog.WithMaxQueueSize(2048), sdklog.WithExportMaxBatchSize(512), sdklog.WithExportInterval(100*time.Millisecond), sdklog.WithExportTimeout(5*time.Second))))
	}
	if !e.options.DisableMetrics && (e.options.Endpoint != "" || e.options.MetricExporter != nil) {
		exporter := e.options.MetricExporter
		if exporter == nil {
			var err error
			exporter, err = otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(otlpURL(e.options.Endpoint, "metrics")), otlpmetrichttp.WithTimeout(5*time.Second))
			if err != nil {
				return err
			}
		}
		e.metrics = sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(&checkedMetricExporter{Exporter: exporter, failures: &e.failures}, sdkmetric.WithInterval(e.options.MetricInterval), sdkmetric.WithTimeout(5*time.Second))))
	}
	var err error
	e.signals, err = newSignals(e.logs, e.metrics)
	return err
}

func (e *Extension) shutdown(ctx context.Context) error {
	var errs []error
	if e.provider != nil {
		errs = append(errs, e.provider.Shutdown(ctx))
	}
	if e.logs != nil {
		errs = append(errs, e.logs.Shutdown(ctx))
	}
	if e.metrics != nil {
		errs = append(errs, e.metrics.Shutdown(ctx))
	}
	return errors.Join(errs...)
}
func (e *Extension) recordDrops() {
	if e.sub == nil || e.signals == nil {
		return
	}
	dropped := uint64(e.sub.Dropped())
	if dropped > e.reportedDrops {
		e.signals.dropped(context.Background(), dropped-e.reportedDrops)
		e.reportedDrops = dropped
	}
}

type exportErrors struct {
	mu  sync.Mutex
	err error
}

func (e *exportErrors) record(signal string, err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err == nil {
		e.err = fmt.Errorf("export OTel %s: %w", signal, err)
	}
}
func (e *exportErrors) Err() error { e.mu.Lock(); defer e.mu.Unlock(); return e.err }

type checkedLogExporter struct {
	sdklog.Exporter
	failures *exportErrors
}

func (e *checkedLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	err := e.Exporter.Export(ctx, records)
	e.failures.record("logs", err)
	return err
}

type checkedMetricExporter struct {
	sdkmetric.Exporter
	failures *exportErrors
}

func (e *checkedMetricExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	err := e.Exporter.Export(ctx, data)
	e.failures.record("metrics", err)
	return err
}
