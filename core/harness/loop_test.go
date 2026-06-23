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
		Prompt: "Use the loop tool to: " +
			"(1) create a loop named 'test-heartbeat' with interval '10m' and prompt 'check system health', " +
			"(2) list all loops to confirm it was created, " +
			"(3) remove the loop 'test-heartbeat' so we clean up. " +
			"Report the results and stop.",
		Steps: Steps(
			Tool("loop").Action("create").Arg("name", "test-heartbeat").NoError(),
			Tool("loop").Action("list").ResultHas("test-heartbeat").NoError(),
			Tool("loop").Action("remove").Arg("name", "test-heartbeat").NoError(),
		),
		Ordered:        true,
		OutputContains: []string{"test-heartbeat"},
		NoErrors:       true,
		MaxTurns:       6,
		Timeout:        60 * time.Second,
		JudgeCriteria: "The agent must: (1) create a loop named 'test-heartbeat', " +
			"(2) list loops confirming it exists, (3) remove it. All calls must succeed.",
	}.Run(t, h)
}

func TestAgentLoopLifecycle(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-lifecycle",
		Prompt: "Use the loop tool to perform these steps in order: " +
			"(1) Create a loop named 'monitor' with interval '10m' and prompt 'check status'. " +
			"(2) List loops to confirm 'monitor' exists. " +
			"(3) Remove the 'monitor' loop. " +
			"(4) List loops again to confirm it is gone. " +
			"Report the results after each step and stop.",
		Steps: Steps(
			Tool("loop").Action("create").Arg("name", "monitor").NoError(),
			Tool("loop").Action("list").ResultHas("monitor").NoError(),
			Tool("loop").Action("remove").Arg("name", "monitor").NoError(),
			Tool("loop").Action("list").NoError(),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 8,
		Timeout:  90 * time.Second,
		JudgeCriteria: "The agent must: (1) create a loop named 'monitor', (2) list loops showing 'monitor' exists, " +
			"(3) remove the 'monitor' loop, (4) list loops again confirming it is gone. " +
			"All four tool calls must succeed without errors.",
	}.Run(t, h)
}
