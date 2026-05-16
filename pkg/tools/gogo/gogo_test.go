package gogo

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	sdkgogo "github.com/chainreactors/sdk/gogo"
)

func TestExecuteInstallsResourceProviderBeforePrepare(t *testing.T) {
	defer gogopkg.ResetResourceProvider()

	var calls atomic.Int32
	engine := sdkgogo.NewEngine(sdkgogo.NewConfig().WithResourceProvider(func(string) []byte {
		calls.Add(1)
		return nil
	}))

	_, err := New(engine).Execute(context.Background(), []string{"-P", "extract"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("resource provider was not called during gogo prepare")
	}
}

func TestExecuteDebugActivatesTelemetryLogger(t *testing.T) {
	var logs bytes.Buffer
	cmd := New(nil).WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logs}))

	if _, err := cmd.Execute(context.Background(), []string{"--debug", "--help"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "[debug] gogo debug enabled") {
		t.Fatalf("debug logs = %q", got)
	}
}
