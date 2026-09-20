package telemetry_test

import (
	"bytes"

	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/telemetry"
)

func TestLoggerRefCanBeRetargeted(t *testing.T) {
	var first, second bytes.Buffer
	app := telemetry.NewLoggerRef(nil)
	app.Set(telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &first}))
	logger := app

	logger.Infof("before")
	app.Set(telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &second}))
	logger.Infof("after")

	if !strings.Contains(first.String(), "before") {
		t.Fatalf("initial logger missing: %q", first.String())
	}
	if strings.Contains(first.String(), "after") {
		t.Fatalf("retargeted log went to old writer: %q", first.String())
	}
	if !strings.Contains(second.String(), "after") {
		t.Fatalf("retargeted logger missing: %q", second.String())
	}
}
