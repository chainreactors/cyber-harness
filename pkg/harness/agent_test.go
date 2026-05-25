//go:build e2e

package harness

import (
	"strings"
	"testing"
)

func TestAgentSimplePrompt(t *testing.T) {
	h := New(t)
	r := h.Agent("What is 2+2? Reply with just the number.")
	Verify(t, r).
		OK().
		OutputContains("4").
		Done()
}

func TestAgentEmptyReply(t *testing.T) {
	h := New(t)
	r := h.Agent("Reply with the word 'pong' and nothing else.")
	Verify(t, r).OK().Done()
	if !strings.Contains(strings.ToLower(r.Output()), "pong") {
		t.Fatalf("expected 'pong', got: %s", r.Output())
	}
}

func TestAgentBashTool(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'echo hello_e2e' in a shell and tell me the exact output.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		ToolResultContains("bash", "hello_e2e").
		OutputContains("hello_e2e").
		NoToolErrors().
		Done()
}

func TestAgentReadTool(t *testing.T) {
	h := New(t)
	r := h.Agent("Read /etc/hostname and reply with only its contents.")
	Verify(t, r).OK().Done()
	if r.Output() == "" {
		t.Fatal("expected non-empty output")
	}
}

func TestAgentWriteReadRoundtrip(t *testing.T) {
	h := New(t)
	r := h.Agent("Write 'e2e_marker_42' to /tmp/aiscan_e2e_test.txt, then read it back and confirm.")
	Verify(t, r).
		OK().
		OutputContains("e2e_marker_42").
		Done()
}

func TestAgentGlobAndRead(t *testing.T) {
	h := New(t)
	r := h.Agent("List .go files in /mnt/chainreactors/aiscan/pkg/agent/ using glob, then read the first line of defaults.go and tell me the package name.")
	Verify(t, r).
		OK().
		OutputContains("agent").
		Done()
}

func TestAgentMultiStepTask(t *testing.T) {
	h := New(t)
	r := h.Agent("First run 'uname -a' in bash. After you see the result, run 'whoami' in a SEPARATE bash call. Report both results.")
	Verify(t, r).
		OK().
		MinToolCalls(2).
		NoToolErrors().
		Done()
}

func TestAgentMultiTurn(t *testing.T) {
	h := New(t)
	r := h.Agent("Step 1: Create file /tmp/aiscan_multi.txt with content 'step1'. Step 2: Append ' step2' to it. Step 3: Read it and confirm it says 'step1 step2'.")
	Verify(t, r).OK().Done()
	if r.Turns() < 2 {
		t.Logf("warning: expected ≥2 turns, got %d", r.Turns())
	}
}

func TestAgentLargeOutput(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'seq 1 500' in bash. Tell me the last number printed.")
	Verify(t, r).
		OK().
		OutputContains("500").
		NoToolErrors().
		Done()
}

func TestAgentErrorRecovery(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'cat /nonexistent/file' in bash. If it fails, report the error message. Then run 'echo recovered' and report that output.")
	Verify(t, r).
		OK().
		OutputContains("recovered").
		MinToolCalls(2).
		Done()
}
