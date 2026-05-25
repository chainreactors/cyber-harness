//go:build e2e

package harness

import (
	"strings"
	"testing"
)

func TestAgentSubagentSync(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-sync",
		Prompt: "Use the subagent tool to create a sync subagent with prompt 'echo sub_sync_ok using bash and report the output'. Report the subagent result.",
		Steps: Steps(
			Tool("subagent").Action("create").NoError(),
		),
		OutputContains: []string{"sub_sync_ok"},
		MaxTurns:       4,
		JudgeCriteria: "The agent must create a sync subagent. The subagent must execute 'echo sub_sync_ok' via bash. " +
			"The final output must contain 'sub_sync_ok' proving the subagent completed and returned its result.",
	}.Run(t, h)
}

func TestAgentSubagentAsync(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-async",
		Prompt: "Create an async subagent with prompt 'Run echo async_marker_99 in bash'. Wait for its completion notification and report its result.",
		Steps: Steps(
			Tool("subagent").Action("create").NoError(),
		),
		OutputContains: []string{"async_marker_99"},
		MaxTurns:       8,
		JudgeCriteria: "The agent must create an async subagent. It must then wait for the subagent completion notification " +
			"(which arrives via inbox). The final output must contain 'async_marker_99'.",
	}.Run(t, h)
}

func TestAgentSubagentSyncTimeout(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-sync-timeout",
		Prompt: "Create a sync subagent with timeout '2s' and prompt 'Run sleep 30 in bash'. Report what happened (it should timeout).",
		Steps: Steps(
			Tool("subagent").ResultHas("timed out"),
		),
		MaxTurns: 3,
		JudgeCriteria: "The agent must create a sync subagent with a 2s timeout running 'sleep 30'. " +
			"The subagent must timeout. The agent must report the timeout in its output.",
	}.Run(t, h)
}

func TestAgentSubagentList(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-list",
		Prompt: "Create an async subagent named 'worker1' with prompt 'sleep 5'. Then immediately use subagent list action to show running subagents. Report the list.",
		Steps: Steps(
			Tool("subagent").Arg("name", "worker1"),
			Tool("subagent").Action("list"),
		),
		MaxTurns: 6,
		JudgeCriteria: "The agent must: (1) create an async subagent named 'worker1', " +
			"(2) call subagent list to show running subagents, " +
			"(3) the list result should show 'worker1' as running.",
	}.Run(t, h)
}

func TestAgentMultiSubagentFanOut(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-fan-out",
		Prompt: "You have 3 independent tasks. Use the subagent tool to create 3 SEPARATE async subagents, one for each:\n" +
			"1. Subagent named 'host-info': run 'uname -a' in bash and report.\n" +
			"2. Subagent named 'user-info': run 'whoami' in bash and report.\n" +
			"3. Subagent named 'dir-info': run 'pwd' in bash and report.\n" +
			"Create all 3 subagents, then wait for all completion notifications. " +
			"Summarize all 3 results together.",
		Steps: Steps(
			Tool("subagent").Arg("name", "host-info"),
			Tool("subagent").Arg("name", "user-info"),
			Tool("subagent").Arg("name", "dir-info"),
		),
		MaxTurns: 10,
		JudgeCriteria: "The agent must create exactly 3 async subagents (host-info, user-info, dir-info). " +
			"It must wait for all 3 completions. The final output must summarize results from all 3 subagents.",
	}.Run(t, h)
}

func TestAgentSubagentWithBashAndReport(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-bash-report",
		Prompt: "Create 2 async subagents:\n" +
			"1. Named 'counter': run 'seq 1 5' in bash.\n" +
			"2. Named 'greeter': run 'echo hello_from_subagent' in bash.\n" +
			"Wait for both to complete. Then report both outputs in your final answer.",
		Steps: Steps(
			Tool("subagent").Arg("name", "counter"),
			Tool("subagent").Arg("name", "greeter"),
		),
		OutputContains: []string{"hello_from_subagent"},
		MaxTurns:       10,
		JudgeCriteria: "The agent must create 2 subagents and wait for both. " +
			"The final output must include the output from both: the sequence 1-5 and 'hello_from_subagent'.",
	}.Run(t, h)
}

func TestAgentSubagentChain(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-chain",
		Prompt: "Step 1: Create a sync subagent that runs 'echo chain_step_1' in bash and returns the output.\n" +
			"Step 2: After you receive the result from step 1, create another sync subagent " +
			"that runs 'echo chain_step_2' in bash.\n" +
			"Report both results to confirm the chain completed.",
		MaxTurns: 8,
		JudgeCriteria: "The agent must create 2 sync subagents sequentially (not in parallel). " +
			"Step 2 must happen AFTER step 1 completes. " +
			"The final output must contain both 'chain_step_1' and 'chain_step_2'.",
		Check: func(t *testing.T, r *RunResult) {
			results := r.SubagentResults()
			if len(results) < 2 {
				t.Fatalf("expected ≥2 subagent results, got %d", len(results))
			}
			s1, s2 := -1, -1
			for i, res := range results {
				if strings.Contains(res, "chain_step_1") && s1 == -1 {
					s1 = i
				}
				if strings.Contains(res, "chain_step_2") && s2 == -1 {
					s2 = i
				}
			}
			if s1 >= 0 && s2 >= 0 && s1 >= s2 {
				t.Fatalf("chain order wrong: step1 at %d, step2 at %d", s1, s2)
			}
		},
	}.Run(t, h)
}

func TestAgentSubagentMessage(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-message",
		Prompt: "Create an async subagent named 'listener' with prompt: " +
			"'Wait for a message. When you receive one, run echo GOT_MESSAGE in bash and report.'\n" +
			"After creating it, use the subagent message action to send a message " +
			"'hello from parent' to the 'listener' subagent.\n" +
			"Wait for the listener to complete and report its result.",
		Steps: Steps(
			Tool("subagent").Arg("name", "listener"),
			Tool("subagent").Action("message").Arg("name", "listener"),
		),
		Ordered:  true,
		MaxTurns: 10,
		JudgeCriteria: "The agent must: (1) create an async subagent named 'listener', " +
			"(2) send a message to it via the subagent message action, " +
			"(3) the listener must execute 'echo GOT_MESSAGE' after receiving the message, " +
			"(4) the final output must contain 'GOT_MESSAGE' confirming the message was received and processed.",
	}.Run(t, h)
}
