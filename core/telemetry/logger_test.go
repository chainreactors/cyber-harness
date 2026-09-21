package telemetry

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/chainreactors/logs"
)

func TestActivateDebugUsesTelemetryLoggerAsGlobal(t *testing.T) {
	oldGlobal := logs.Log
	defer func() { logs.Log = oldGlobal }()

	var buf bytes.Buffer
	logger := NewLogger(LogConfig{Output: &buf})
	restore := ActivateDebug(logger)
	logs.Log.Debugf("visible")
	restore()

	if got := buf.String(); got != "● visible\n" {
		t.Fatalf("debug output = %q", got)
	}
	if logs.Log != oldGlobal {
		t.Fatal("global logger was not restored")
	}
}

func TestSuppressGlobalNonErrorsKeepsOnlyErrors(t *testing.T) {
	oldGlobal := logs.Log
	defer func() { logs.Log = oldGlobal }()

	var buf bytes.Buffer
	GlobalLogger(LogConfig{Output: &buf})
	restore := SuppressGlobalNonErrors()
	logs.Log.Infof("hidden info")
	logs.Log.Warnf("hidden warn")
	logs.Log.Errorf("visible error")
	restore()

	got := buf.String()
	if strings.Contains(got, "hidden") {
		t.Fatalf("non-error logs were not suppressed: %q", got)
	}
	if !strings.Contains(got, "● visible error") {
		t.Fatalf("error log missing after suppression: %q", got)
	}
}

func TestGlobalSDKSettingsCannotSilenceApplicationLogger(t *testing.T) {
	oldGlobal := logs.Log
	defer func() { logs.Log = oldGlobal }()
	var buf bytes.Buffer
	logger := GlobalLogger(LogConfig{Output: &buf})
	logs.Log.SetQuiet(true)
	logs.Log.SetLevel(logs.ErrorLevel)
	logs.Log.Warnf("SDK warning")
	logger.Warnf("application delivery failed")
	if got := buf.String(); got != "● application delivery failed\n" {
		t.Fatalf("application diagnostics lost to SDK settings: %q", got)
	}
}

func TestErrorOnlyLoggerSuppressesNonErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := ErrorOnlyLogger(NewLogger(LogConfig{Output: &buf}))
	logger.Debugf("debug")
	logger.Infof("info")
	logger.Warnf("warn")
	logger.Errorf("error")
	logger.Importantf("important")

	got := buf.String()
	if strings.Contains(got, "debug") || strings.Contains(got, "info") || strings.Contains(got, "warn") || strings.Contains(got, "important") {
		t.Fatalf("non-error logs were not suppressed: %q", got)
	}
	if !strings.Contains(got, "● error") {
		t.Fatalf("error log missing: %q", got)
	}
}

func TestLoggerColorStylesOnlyMarker(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(LogConfig{Debug: true, Output: &buf, Color: true})
	logger.Infof("ready")

	got := buf.String()
	if !strings.Contains(got, "\x1b[0;32m●\x1b[0m ready") {
		t.Fatalf("colored marker missing: %q", got)
	}
	if strings.Contains(got, "\x1b[0;32m● ready") {
		t.Fatalf("entire line appears colored: %q", got)
	}
}

func TestLoggerOutputCanChangeWithoutReplacingLogger(t *testing.T) {
	var first, second bytes.Buffer
	logger := NewLogger(LogConfig{Debug: true, Output: &first})
	consumer := logger
	consumer.Debugf("before")
	logger.SetOutput(&second)
	consumer.Debugf("after")
	logger.SetOutput(nil)
	consumer.Debugf("discarded")
	if first.String() != "● before\n" || second.String() != "● after\n" {
		t.Fatalf("destinations: first=%q second=%q", first.String(), second.String())
	}
}

func TestLoggerOutputChangeDuringLogging(t *testing.T) {
	var first, second bytes.Buffer
	logger := NewLogger(LogConfig{Output: &first})
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 100 {
			logger.SetOutput(&second)
			logger.SetOutput(&first)
		}
	})
	for range 4 {
		workers.Go(func() {
			for range 100 {
				logger.Warnf("record")
			}
		})
	}
	workers.Wait()
	if got := strings.Count(first.String()+second.String(), "● record\n"); got != 400 {
		t.Fatalf("lost or interleaved log records: %d", got)
	}
}
