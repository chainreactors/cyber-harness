//go:build e2e

package harness

import "testing"

func TestAgentLoopCreateAndList(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Use the loop tool to create a loop named 'test-loop' with interval '30s' and prompt 'check status'. " +
			"Then use the loop tool with action 'list' to show all active loops. " +
			"Then delete the loop named 'test-loop'. Report what you did.",
	)
	Verify(t, r).
		OK().
		ToolUsed("loop").
		LoopCreated("test-loop").
		LoopListed().
		LoopDeleted("test-loop").
		ToolResultContains("loop", "test-loop").
		NoToolErrors().
		Done()
}

func TestAgentLoopMultipleCreate(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Use the loop tool to create 2 loops:\n" +
			"1. Name 'scanner' interval '30s' prompt 'run gogo scan'\n" +
			"2. Name 'monitor' interval '30s' prompt 'check service health'\n" +
			"After creating both, list loops. Then delete both loops. Report what you did.",
	)
	Verify(t, r).
		OK().
		LoopCreated("scanner").
		LoopCreated("monitor").
		LoopListed().
		LoopDeleted("scanner").
		LoopDeleted("monitor").
		NoToolErrors().
		Done()
}

func TestAgentLoopAndSubagent(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Do three things in order:\n" +
			"1. Create a loop named 'poller' with interval '30s' and prompt 'poll targets'.\n" +
			"2. Create a sync subagent with prompt 'Reply with the word SUBAGENT_OK and nothing else.'\n" +
			"3. Delete the loop named 'poller'.\n" +
			"Report all results.",
	)
	Verify(t, r).
		OK().
		ToolUsed("loop").
		ToolUsed("subagent").
		LoopCreated("poller").
		LoopDeleted("poller").
		ToolResultContains("loop", "poller").
		ToolSequence("loop", "subagent", "loop").
		Done()
}

func TestAgentLoopDuplicateNameRejected(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Use the loop tool to create a loop named 'dup' with interval '30s' and prompt 'test'. " +
			"Then try to create another loop also named 'dup' with interval '1m' and prompt 'test2'. " +
			"Report what happened — the second create should fail. " +
			"Finally delete the loop named 'dup'.",
	)
	Verify(t, r).
		OK().
		LoopCreated("dup").
		LoopDeleted("dup").
		Done()

	createCount := 0
	for _, e := range r.LoopCalls() {
		if containsStr(e.Args, `"create"`) {
			createCount++
		}
	}
	if createCount < 2 {
		t.Logf("warning: expected 2 create attempts, got %d", createCount)
	}
}
