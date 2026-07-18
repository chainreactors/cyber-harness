package telemetry

import (
	"strings"
	"testing"
)

func TestStartupLineUsesTextStatus(t *testing.T) {
	got := StartupOK("llm", "openai/gpt-test")
	if !strings.HasPrefix(got, "llm") {
		t.Fatalf("StartupOK() = %q", got)
	}

	got = StartupLine("ok", "llm", "openai/gpt-test")
	if !strings.HasPrefix(got, "llm") {
		t.Fatalf("StartupLine(ok) = %q", got)
	}

	got = StartupLine("fail", "llm", "unauthorized")
	if !strings.HasPrefix(got, "fail llm") {
		t.Fatalf("StartupLine(fail) = %q", got)
	}
}
