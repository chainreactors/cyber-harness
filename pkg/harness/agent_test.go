//go:build e2e

package harness

import (
	"strings"
	"testing"
)

func TestAgentSimplePrompt(t *testing.T) {
	h := New(t)
	Intent{
		Name:           "simple-prompt",
		Prompt:         "What is 2+2? Reply with just the number.",
		OutputContains: []string{"4"},
		MaxTurns:       2,
		JudgeCriteria:  "The agent must reply with the number 4. No tool calls needed. The answer must be mathematically correct.",
	}.Run(t, h)
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
	Intent{
		Name:   "bash-echo",
		Prompt: "Run 'echo hello_e2e' in a shell and tell me the exact output.",
		Steps: Steps(
			Tool("bash").ArgContains("echo hello_e2e").ResultHas("hello_e2e").NoError(),
		),
		OutputContains: []string{"hello_e2e"},
		NoErrors:       true,
		MaxTurns:       3,
		JudgeCriteria: "The agent must: (1) call the bash tool with a command containing 'echo hello_e2e', " +
			"(2) the bash result must contain 'hello_e2e', " +
			"(3) the final output must report 'hello_e2e' as the result.",
	}.Run(t, h)
}

func TestAgentReadTool(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "read-file",
		Prompt: "Read /etc/hostname and reply with only its contents.",
		Steps: Steps(
			Tool("read").ArgContains("hostname").NoError(),
		),
		NoErrors: true,
		MaxTurns: 3,
		JudgeCriteria: "The agent must use the read tool to read /etc/hostname, and the final output must contain the hostname value " +
			"(not just say 'I read it' — the actual content must appear).",
	}.Run(t, h)
}

func TestAgentWriteReadRoundtrip(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "write-read-roundtrip",
		Prompt: "Write 'e2e_marker_42' to /tmp/aiscan_e2e_test.txt, then read it back and confirm.",
		Steps: Steps(
			Tool("write").ArgContains("e2e_marker_42").NoError(),
			Tool("read").ArgContains("aiscan_e2e_test").NoError(),
		),
		Ordered:        true,
		OutputContains: []string{"e2e_marker_42"},
		NoErrors:       true,
		MaxTurns:       5,
		JudgeCriteria: "The agent must: (1) write the exact string 'e2e_marker_42' to a file, " +
			"(2) read it back and confirm the content matches. Both steps must succeed without errors.",
	}.Run(t, h)
}

func TestAgentGlobAndRead(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "glob-and-read",
		Prompt: "List .go files in /mnt/chainreactors/aiscan/pkg/agent/ using glob, then read the first line of defaults.go and tell me the package name.",
		Steps: Steps(
			Tool("glob").NoError(),
			Tool("read").ArgContains("defaults.go").NoError(),
		),
		Ordered:        true,
		OutputContains: []string{"agent"},
		NoErrors:       true,
		MaxTurns:       4,
		JudgeCriteria: "The agent must: (1) use glob to list .go files in the agent directory, " +
			"(2) read defaults.go, (3) correctly report that the package name is 'agent'.",
	}.Run(t, h)
}

func TestAgentMultiStepTask(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "multi-step-bash",
		Prompt: "First run 'uname -a' in bash. After you see the result, run 'whoami' in a SEPARATE bash call. Report both results.",
		Steps: Steps(
			Tool("bash").ArgContains("uname").NoError(),
			Tool("bash").ArgContains("whoami").NoError(),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 6,
		JudgeCriteria: "The agent must make TWO separate bash calls: one for 'uname -a' and one for 'whoami'. " +
			"Both results must appear in the final output. They must NOT be combined in a single bash call.",
	}.Run(t, h)
}

func TestAgentMultiTurn(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "multi-turn-file-ops",
		Prompt: "Step 1: Create file /tmp/aiscan_multi.txt with content 'step1'. Step 2: Append ' step2' to it. Step 3: Read it and confirm it says 'step1 step2'.",
		NoErrors: true,
		MaxTurns: 8,
		JudgeCriteria: "The agent must perform three sequential file operations: " +
			"(1) create a file with 'step1', (2) append ' step2' to it, (3) read and confirm the content is 'step1 step2'. " +
			"The final output must confirm the combined content.",
	}.Run(t, h)
}

func TestAgentLargeOutput(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "large-output",
		Prompt: "Run 'seq 1 500' in bash. Tell me the last number printed.",
		Steps: Steps(
			Tool("bash").ArgContains("seq").NoError(),
		),
		OutputContains: []string{"500"},
		NoErrors:       true,
		MaxTurns:       8,
		JudgeCriteria:  "The agent must run 'seq 1 500' and correctly identify that the last number is 500.",
	}.Run(t, h)
}

func TestAgentErrorRecovery(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "error-recovery",
		Prompt: "Run 'cat /nonexistent/file' in bash. If it fails, report the error message. Then run 'echo recovered' and report that output.",
		Steps: Steps(
			Tool("bash").ArgContains("nonexistent"),
			Tool("bash").ArgContains("recovered").NoError(),
		),
		Ordered:        true,
		OutputContains: []string{"recovered"},
		MaxTurns:       5,
		JudgeCriteria: "The agent must: (1) attempt to cat a nonexistent file, (2) recognize the error, " +
			"(3) recover by running 'echo recovered', (4) report both the error and the recovery in the final output.",
	}.Run(t, h)
}
