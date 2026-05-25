//go:build e2e

package harness

import "testing"

func TestAgentBackgroundTask(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a background bash task: 'sleep 1 && echo bg_done'. Then use the task tool to list running tasks. Wait for it to finish. Report the final output.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		ToolUsed("task").
		ToolArgsContain("bash", "background").
		ToolResultContains("bash", "Started").
		AnyResultContains("bg_done").
		ToolSequence("bash", "task").
		NoToolErrors().
		Done()
}

func TestAgentTaskPeek(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'for i in 1 2 3; do echo line_$i; sleep 0.5; done' as a background task. Use task peek to check its output, then wait for completion and report all lines.")
	Verify(t, r).
		OK().
		ToolUsed("task").
		ToolArgsContain("task", "peek").
		Done()
}

func TestAgentTaskKill(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a background task 'sleep 300' named 'sleeper'. List tasks to confirm it's running. Kill it. List again to confirm it's gone or killed. Report status.")
	Verify(t, r).
		OK().
		ToolUsed("task").
		ToolCount("task", 2, 10).
		ToolArgsContain("task", "kill").
		Done()
}
