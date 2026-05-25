//go:build e2e

package harness

import "testing"

func TestAgentLoopCreateAndList(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "loop-create-list-delete",
		Prompt: "Use the loop tool to create a loop named 'test-loop' with interval '30s' and prompt 'check status'. Then use the loop tool with action 'list' to show all active loops. Then delete the loop named 'test-loop'. Report what you did.",
		Steps: Steps(
			Tool("loop").Action("create").Arg("name", "test-loop").ResultHas("created"),
			Tool("loop").Action("list").ResultHas("test-loop"),
			Tool("loop").Action("delete").Arg("name", "test-loop").ResultHas("deleted"),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 6,
	}.Run(t, h)
}

func TestAgentLoopMultipleCreate(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-multi-create",
		Prompt: "Use the loop tool to create 2 loops:\n" +
			"1. Name 'scanner' interval '30s' prompt 'run gogo scan'\n" +
			"2. Name 'monitor' interval '30s' prompt 'check service health'\n" +
			"After creating both, list loops. Then delete both loops. Report what you did.",
		Steps: Steps(
			Tool("loop").Action("create").Arg("name", "scanner"),
			Tool("loop").Action("create").Arg("name", "monitor"),
			Tool("loop").Action("list"),
			Tool("loop").Action("delete").Arg("name", "scanner"),
			Tool("loop").Action("delete").Arg("name", "monitor"),
		),
		OutputContains: []string{"scanner", "monitor"},
		NoErrors:       true,
		MaxTurns:        8,
	}.Run(t, h)
}

func TestAgentLoopAndSubagent(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-and-subagent",
		Prompt: "Do three things in order:\n" +
			"1. Create a loop named 'poller' with interval '30s' and prompt 'poll targets'.\n" +
			"2. Create a sync subagent with prompt 'Reply with the word SUBAGENT_OK and nothing else.'\n" +
			"3. Delete the loop named 'poller'.\n" +
			"Report all results.",
		Steps: Steps(
			Tool("loop").Action("create").Arg("name", "poller"),
			Tool("subagent"),
			Tool("loop").Action("delete").Arg("name", "poller"),
		),
		Ordered:        true,
		OutputContains: []string{"poller"},
		MaxTurns:       6,
	}.Run(t, h)
}

func TestAgentLoopDuplicateNameRejected(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-duplicate-rejected",
		Prompt: "Use the loop tool to create a loop named 'dup' with interval '30s' and prompt 'test'. " +
			"Then try to create another loop also named 'dup' with interval '1m' and prompt 'test2'. " +
			"Report what happened — the second create should fail. " +
			"Finally delete the loop named 'dup'.",
		Steps: Steps(
			Tool("loop").Action("create").Arg("name", "dup").NoError(),
			Tool("loop").Action("delete").Arg("name", "dup"),
		),
		Ordered:  true,
		MaxTurns: 6,
	}.Run(t, h)
}

func TestAgentLoopIntentSuite(t *testing.T) {
	h := New(t)
	IntentSuite(t, h,
		Intent{
			Name:   "loop-create-only",
			Prompt: "Create a loop named 'quick' with interval '30s' and prompt 'ping'. Then delete it. Report results.",
			Steps: Steps(
				Tool("loop").Action("create").Arg("name", "quick"),
				Tool("loop").Action("delete").Arg("name", "quick"),
			),
			Ordered:  true,
			NoErrors: true,
		},
		Intent{
			Name:   "loop-list-empty",
			Prompt: "Use the loop tool with action 'list' to show all loops. Report the result.",
			Steps: Steps(
				Tool("loop").Action("list").ResultHas("No active"),
			),
			NoErrors: true,
		},
	)
}
