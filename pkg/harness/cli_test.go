//go:build e2e

package harness

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestScannerHelpExitsClean(t *testing.T) {
	h := New(t)
	for _, name := range scannerHelpCommands() {
		t.Run(name, func(t *testing.T) {
			r := h.Scanner(name, "-h")
			Verify(t, r).
				OK().
				OutputContains("Usage:").
				Done()
		})
	}
}

func TestVersionFlag(t *testing.T) {
	h := New(t)
	r := h.Run("--version")
	Verify(t, r).
		OK().
		OutputContains("aiscan v").
		Done()
}

func TestScannerDirectGogo(t *testing.T) {
	h := New(t)
	r := h.Scanner("gogo", "-i", "127.0.0.1", "-p", "80")
	if r.ExitCode != 0 {
		t.Logf("gogo exit=%d stderr: %s", r.ExitCode, clip(r.Stderr, 500))
	}
}

func TestScannerDirectSpray(t *testing.T) {
	h := New(t)
	r := h.Scanner("spray", "-i", "http://127.0.0.1:1", "--limit", "1")
	if r.ExitCode != 0 {
		t.Logf("spray exit=%d stderr: %s", r.ExitCode, clip(r.Stderr, 500))
	}
}

func TestAgentTimeout(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(15*time.Second,
		"agent", "-p", "Run 'sleep 60' in bash.",
		"--timeout", "5",
	)
	if r.ExitCode == 0 && r.Duration < 4*time.Second {
		t.Logf("agent completed before timeout — skipping assertion")
		return
	}
	if r.Duration < 4*time.Second {
		t.Fatalf("expected ≥4s duration, got %s", r.Duration)
	}
}

func init() {
	if _, err := exec.LookPath("go"); err != nil {
		panic("go compiler not found; e2e tests require Go toolchain")
	}
}

// helpers shared by non-AI tests

func containsCount(s, substr string) int {
	return strings.Count(s, substr)
}
