package zombie

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func TestExecuteDebugActivatesTelemetryLogger(t *testing.T) {
	var logs bytes.Buffer
	cmd := New(nil).WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logs}))

	if _, err := cmd.Execute(context.Background(), []string{"--debug", "--help"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "[debug] zombie debug enabled") {
		t.Fatalf("debug logs = %q", got)
	}
}
