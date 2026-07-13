package telemetry

import (
	"strings"
	"testing"
)

func TestStartupLineUsesTextStatus(t *testing.T) {
	got := StartupOK("llm", "openai/gpt-test")
	if !strings.HasPrefix(got, "ok   llm") {
		t.Fatalf("StartupOK() = %q", got)
	}

	got = StartupLine("fail", "llm", "unauthorized")
	if !strings.HasPrefix(got, "fail llm") {
		t.Fatalf("StartupLine(fail) = %q", got)
	}
}
