package telemetry

import (
	"bytes"
	"strings"
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

	if got := buf.String(); got != "[debug] visible\n" {
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
	if !strings.Contains(got, "[error] visible error") {
		t.Fatalf("error log missing after suppression: %q", got)
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
	if !strings.Contains(got, "[error] error") {
		t.Fatalf("error log missing: %q", got)
	}
}
