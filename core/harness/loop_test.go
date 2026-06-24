//go:build e2e

package harness

import (
	"testing"
	"time"
)

func TestAgentLoopCreate(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-create",
		Prompt: "Use bash to run these loop commands in order: " +
			"(1) loop '*/10 * * * *' check system health " +
			"(2) loop list " +
			"(3) loop stop the loop that was just created. " +
			"Report the results and stop.",
		Steps: Steps(
			Tool("bash").ArgContains("loop").NoError(),
			Tool("bash").ArgContains("loop").ArgContains("list").NoError(),
			Tool("bash").ArgContains("loop").ArgContains("stop").NoError(),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 6,
		Timeout:  60 * time.Second,
		JudgeCriteria: "The agent must: (1) create a loop via cron expression, " +
			"(2) list loops, (3) stop the loop. All calls must succeed.",
	}.Run(t, h)
}

func TestAgentLoopLifecycle(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-lifecycle",
		Prompt: "Use bash to run these loop commands in order: " +
			"(1) loop 5m check status " +
			"(2) loop list to confirm the loop exists " +
			"(3) loop stop <name> to stop it " +
			"(4) loop list again to confirm it is gone. " +
			"Report the results after each step and stop.",
		Steps: Steps(
			Tool("bash").ArgContains("loop").NoError(),
			Tool("bash").ArgContains("loop list").NoError(),
			Tool("bash").ArgContains("loop stop").NoError(),
			Tool("bash").ArgContains("loop list").NoError(),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 8,
		Timeout:  90 * time.Second,
		JudgeCriteria: "The agent must: (1) create a loop, (2) list loops showing it exists, " +
			"(3) stop the loop, (4) list loops again confirming it is gone. " +
			"All four commands must succeed without errors.",
	}.Run(t, h)
}
