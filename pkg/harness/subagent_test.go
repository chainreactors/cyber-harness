//go:build e2e

package harness

import "testing"

func TestAgentSubagentSync(t *testing.T) {
	h := New(t)
	r := h.Agent("Use the subagent tool to create a sync subagent with prompt 'echo sub_sync_ok using bash and report the output'. Report the subagent result.")
	Verify(t, r).
		OK().
		ToolUsed("subagent").
		ToolResultContains("subagent", "sub_sync_ok").
		OutputContains("sub_sync_ok").
		Done()
}

func TestAgentSubagentAsync(t *testing.T) {
	h := New(t)
	r := h.Agent("Create an async subagent with prompt 'Run echo async_marker_99 in bash'. Wait for its completion notification and report its result.")
	Verify(t, r).
		OK().
		ToolUsed("subagent").
		OutputContains("async_marker_99").
		MinTurns(2).
		Done()
}

func TestAgentSubagentSyncTimeout(t *testing.T) {
	h := New(t)
	r := h.Agent("Create a sync subagent with timeout '2s' and prompt 'Run sleep 30 in bash'. Report what happened (it should timeout).")
	Verify(t, r).
		OK().
		ToolUsed("subagent").
		ToolResultContains("subagent", "timed out").
		Done()
}

func TestAgentSubagentList(t *testing.T) {
	h := New(t)
	r := h.Agent("Create an async subagent named 'worker1' with prompt 'sleep 5'. Then immediately use subagent list action to show running subagents. Report the list.")
	Verify(t, r).
		OK().
		ToolUsed("subagent").
		ToolArgsContain("subagent", "list").
		SubagentCreated("worker1").
		Done()
}

func TestAgentMultiSubagentFanOut(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"You have 3 independent tasks. Use the subagent tool to create 3 SEPARATE async subagents, one for each:\n" +
			"1. Subagent named 'host-info': run 'uname -a' in bash and report.\n" +
			"2. Subagent named 'user-info': run 'whoami' in bash and report.\n" +
			"3. Subagent named 'dir-info': run 'pwd' in bash and report.\n" +
			"Create all 3 subagents, then wait for all completion notifications. " +
			"Summarize all 3 results together.",
	)
	Verify(t, r).
		OK().
		MinSubagentCreates(3).
		SubagentCreated("host-info").
		SubagentCreated("user-info").
		SubagentCreated("dir-info").
		MinTurns(2).
		Done()
}

func TestAgentSubagentWithBashAndReport(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Create 2 async subagents:\n" +
			"1. Named 'counter': run 'seq 1 5' in bash.\n" +
			"2. Named 'greeter': run 'echo hello_from_subagent' in bash.\n" +
			"Wait for both to complete. Then report both outputs in your final answer.",
	)
	Verify(t, r).
		OK().
		MinSubagentCreates(2).
		OutputContains("hello_from_subagent").
		Done()
}

func TestAgentSubagentChain(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Step 1: Create a sync subagent that runs 'echo chain_step_1' in bash and returns the output.\n" +
			"Step 2: After you receive the result from step 1, create another sync subagent " +
			"that runs 'echo chain_step_2' in bash.\n" +
			"Report both results to confirm the chain completed.",
	)
	Verify(t, r).
		OK().
		MinSubagentCreates(2).
		SubagentResultContains("chain_step_1").
		SubagentResultContains("chain_step_2").
		Done()

	results := r.SubagentResults()
	step1Idx, step2Idx := -1, -1
	for i, res := range results {
		if contains(res, "chain_step_1") && step1Idx == -1 {
			step1Idx = i
		}
		if contains(res, "chain_step_2") && step2Idx == -1 {
			step2Idx = i
		}
	}
	if step1Idx >= 0 && step2Idx >= 0 && step1Idx >= step2Idx {
		t.Fatalf("chain order wrong: step1 at %d, step2 at %d", step1Idx, step2Idx)
	}
}

func TestAgentSubagentMessage(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Create an async subagent named 'listener' with prompt: " +
			"'Wait for a message. When you receive one, run echo GOT_MESSAGE in bash and report.'\n" +
			"After creating it, use the subagent message action to send a message " +
			"'hello from parent' to the 'listener' subagent.\n" +
			"Wait for the listener to complete and report its result.",
	)
	Verify(t, r).
		OK().
		SubagentCreated("listener").
		ToolArgMatch("subagent", func(args string) bool {
			return contains(args, `"message"`) && contains(args, "listener")
		}).
		AnyResultContains("GOT_MESSAGE").
		Done()
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
