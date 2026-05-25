//go:build e2e

package harness

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Intent describes a complete AI behavior test case declaratively.
// Instead of writing imperative test code, define an Intent and call Run.
//
//	Intent{
//	    Name:   "loop-lifecycle",
//	    Prompt: "Create loop 'scanner', list, then delete it.",
//	    Steps: Steps(
//	        Tool("loop").Action("create").Arg("name", "scanner").ResultHas("created"),
//	        Tool("loop").Action("list").ResultHas("scanner"),
//	        Tool("loop").Action("delete").Arg("name", "scanner"),
//	    ),
//	    Ordered:        true,
//	    OutputContains: []string{"scanner"},
//	    MaxTurns:       6,
//	    NoErrors:       true,
//	}.Run(t, h)
type Intent struct {
	Name      string
	Prompt    string
	ExtraArgs []string
	Timeout   time.Duration

	// Steps describes expected tool calls.
	Steps []ToolPattern

	// Ordered requires steps to appear in sequence (subsequence match).
	// When false, steps can appear in any order.
	Ordered bool

	// OutputContains lists substrings that must appear in stdout/stderr.
	OutputContains []string

	// OutputMissing lists substrings that must NOT appear in output.
	OutputMissing []string

	// NoErrors requires all tool calls to succeed.
	NoErrors bool

	// MaxTurns caps the number of turns (0 = no limit).
	MaxTurns int

	// MaxToolCalls caps total tool invocations (0 = no limit).
	MaxToolCalls int

	// MaxDuration caps wall-clock time (0 = no limit).
	MaxDuration time.Duration

	// JudgeCriteria, when non-empty, enables LLM-as-judge evaluation.
	// The judge receives the intent prompt, this criteria string, and the
	// full execution trace. It returns a pass/fail verdict.
	// Example: "The agent must have created exactly one loop named 'scanner',
	// listed it to confirm it exists, then deleted it."
	JudgeCriteria string

	// Check is an optional custom verification function.
	Check func(t *testing.T, r *RunResult)
}

// Steps is a convenience constructor for []ToolPattern.
func Steps(patterns ...ToolPattern) []ToolPattern { return patterns }

// Run executes the intent against the harness and verifies all expectations.
func (intent Intent) Run(t *testing.T, h *Harness) *RunResult {
	t.Helper()

	var r *RunResult
	if intent.Timeout > 0 {
		r = h.RunWithTimeout(intent.Timeout, intent.buildArgs()...)
	} else {
		r = h.Agent(intent.Prompt, intent.ExtraArgs...)
	}
	intent.verify(t, h, r)
	return r
}

func (intent Intent) buildArgs() []string {
	args := []string{"agent", "-p", intent.Prompt}
	args = append(args, intent.ExtraArgs...)
	return args
}

func (intent Intent) verify(t *testing.T, h *Harness, r *RunResult) {
	t.Helper()

	v := Verify(t, r).OK()

	// structural checks
	if len(intent.Steps) > 0 {
		if intent.Ordered {
			v = v.ExpectInOrder(intent.Steps...)
		} else {
			v = v.Expect(intent.Steps...)
		}
	}
	for _, s := range intent.OutputContains {
		v = v.OutputContains(s)
	}
	for _, s := range intent.OutputMissing {
		v = v.OutputMissing(s)
	}
	if intent.NoErrors {
		v = v.NoToolErrors()
	}
	if intent.MaxTurns > 0 {
		v = v.MaxTurns(intent.MaxTurns)
	}
	if intent.MaxToolCalls > 0 {
		v = v.MaxToolCalls(intent.MaxToolCalls)
	}
	if intent.MaxDuration > 0 {
		v = v.CompletedWithin(intent.MaxDuration)
	}

	// semantic check via LLM judge
	if intent.JudgeCriteria != "" {
		v = v.JudgeWith(h.Judge(), intent.Prompt, intent.JudgeCriteria)
	}

	v.Done()

	if intent.Check != nil {
		intent.Check(t, r)
	}
}

// Describe returns a human-readable summary of the intent for logging.
func (intent Intent) Describe() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Intent: %s\n", intent.Name))
	sb.WriteString(fmt.Sprintf("  Prompt: %s\n", clip(intent.Prompt, 80)))
	if len(intent.Steps) > 0 {
		order := "any order"
		if intent.Ordered {
			order = "in order"
		}
		sb.WriteString(fmt.Sprintf("  Steps (%s):\n", order))
		for i, s := range intent.Steps {
			sb.WriteString(fmt.Sprintf("    %d. %s\n", i+1, s.describe()))
		}
	}
	if len(intent.OutputContains) > 0 {
		sb.WriteString(fmt.Sprintf("  Output must contain: %v\n", intent.OutputContains))
	}
	if intent.MaxTurns > 0 {
		sb.WriteString(fmt.Sprintf("  Max turns: %d\n", intent.MaxTurns))
	}
	if intent.NoErrors {
		sb.WriteString("  No tool errors allowed\n")
	}
	return sb.String()
}

// IntentSuite runs multiple intents as subtests.
func IntentSuite(t *testing.T, h *Harness, intents ...Intent) {
	t.Helper()
	for _, intent := range intents {
		name := intent.Name
		if name == "" {
			name = clip(intent.Prompt, 40)
		}
		t.Run(name, func(t *testing.T) {
			intent.Run(t, h)
		})
	}
}
