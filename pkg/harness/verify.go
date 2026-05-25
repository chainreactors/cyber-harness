//go:build e2e

package harness

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Verifier provides chainable assertions on a RunResult.
// Accumulates all failures; Done() reports them together.
//
// Two verification layers:
//
// Layer 1 — Structural (tool-level):
//
//	Verify(t, r).
//	    OK().
//	    Expect(Tool("bash").ArgContains("gogo").NoError()).
//	    Expect(Tool("loop").Action("create").Arg("name", "scanner")).
//	    Done()
//
// Layer 2 — Intent (outcome-level):
//
//	Verify(t, r).
//	    OK().
//	    ExpectInOrder(
//	        Tool("loop").Action("create").Arg("name", "scanner"),
//	        Tool("loop").Action("list"),
//	        Tool("loop").Action("delete").Arg("name", "scanner"),
//	    ).
//	    OutputContains("scanner").
//	    NoToolErrors().
//	    MaxTurns(5).
//	    Done()
type Verifier struct {
	t        *testing.T
	r        *RunResult
	failures []string
}

func Verify(t *testing.T, r *RunResult) *Verifier {
	t.Helper()
	return &Verifier{t: t, r: r}
}

func (v *Verifier) fail(msg string) { v.failures = append(v.failures, msg) }

func (v *Verifier) Done() {
	v.t.Helper()
	if len(v.failures) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("verification failed (%d issue(s)):\n", len(v.failures)))
	for i, f := range v.failures {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, f))
	}
	sb.WriteString(fmt.Sprintf("\nresult: exit=%d turns=%d tools=%d duration=%s\n",
		v.r.ExitCode, v.r.Turns(), len(v.r.ToolCalls()), v.r.Duration))
	sb.WriteString(fmt.Sprintf("tool sequence: %v\n", v.r.ToolCallSequence()))
	v.t.Fatal(sb.String())
}

// =====================================================================
// Exit / Output
// =====================================================================

func (v *Verifier) OK() *Verifier {
	if !v.r.OK() {
		v.fail(fmt.Sprintf("exit code %d, expected 0\nstderr: %s", v.r.ExitCode, clip(v.r.Stderr, 500)))
	}
	return v
}

func (v *Verifier) OutputContains(substr string) *Verifier {
	if !v.r.ContainsOutput(substr) {
		v.fail(fmt.Sprintf("output missing %q", substr))
	}
	return v
}

func (v *Verifier) OutputMissing(substr string) *Verifier {
	if v.r.ContainsOutput(substr) {
		v.fail(fmt.Sprintf("output should not contain %q", substr))
	}
	return v
}

// =====================================================================
// Constraints
// =====================================================================

func (v *Verifier) MinTurns(n int) *Verifier {
	if v.r.Turns() < n {
		v.fail(fmt.Sprintf("expected >= %d turns, got %d", n, v.r.Turns()))
	}
	return v
}

func (v *Verifier) MaxTurns(n int) *Verifier {
	if v.r.Turns() > n {
		v.fail(fmt.Sprintf("expected <= %d turns, got %d", n, v.r.Turns()))
	}
	return v
}

func (v *Verifier) MinToolCalls(n int) *Verifier {
	if len(v.r.ToolCalls()) < n {
		v.fail(fmt.Sprintf("expected >= %d tool calls, got %d", n, len(v.r.ToolCalls())))
	}
	return v
}

func (v *Verifier) MaxToolCalls(n int) *Verifier {
	if len(v.r.ToolCalls()) > n {
		v.fail(fmt.Sprintf("expected <= %d tool calls, got %d", n, len(v.r.ToolCalls())))
	}
	return v
}

func (v *Verifier) CompletedWithin(d time.Duration) *Verifier {
	if v.r.Duration > d {
		v.fail(fmt.Sprintf("expected completion within %s, took %s", d, v.r.Duration))
	}
	return v
}

func (v *Verifier) ToolCount(name string, min, max int) *Verifier {
	n := len(v.r.ToolCallsNamed(name))
	if n < min || n > max {
		v.fail(fmt.Sprintf("tool %q called %d times, expected [%d, %d]", name, n, min, max))
	}
	return v
}

// =====================================================================
// Expect — pattern-based tool call verification
// =====================================================================

// Expect verifies that each pattern matches at least one tool call (any order).
func (v *Verifier) Expect(patterns ...ToolPattern) *Verifier {
	result := matchUnordered(patterns, v.r.ToolCalls())
	for _, p := range result.unmatched {
		v.fail(fmt.Sprintf("expected tool call not found: %s", p.describe()))
	}
	return v
}

// ExpectInOrder verifies that patterns match tool calls in sequence
// (subsequence — other calls may appear between them).
func (v *Verifier) ExpectInOrder(patterns ...ToolPattern) *Verifier {
	result := matchOrdered(patterns, v.r.ToolCalls())
	if len(result.unmatched) > 0 {
		var descs []string
		for _, p := range result.unmatched {
			descs = append(descs, p.describe())
		}
		v.fail(fmt.Sprintf("tool call sequence incomplete, unmatched: [%s]\nactual: %v",
			strings.Join(descs, ", "), v.r.ToolCallSequence()))
	}
	return v
}

// ExpectNone verifies that NO tool call matches the pattern.
func (v *Verifier) ExpectNone(patterns ...ToolPattern) *Verifier {
	for _, p := range patterns {
		for _, e := range v.r.ToolCalls() {
			if p.Match(e) {
				v.fail(fmt.Sprintf("unexpected tool call matched: %s", p.describe()))
				break
			}
		}
	}
	return v
}

// =====================================================================
// Legacy tool checks (still useful for simple cases)
// =====================================================================

func (v *Verifier) ToolUsed(name string) *Verifier {
	if !v.r.HasToolCall(name) {
		v.fail(fmt.Sprintf("tool %q was never called", name))
	}
	return v
}

func (v *Verifier) ToolNotUsed(name string) *Verifier {
	if v.r.HasToolCall(name) {
		v.fail(fmt.Sprintf("tool %q should not have been called", name))
	}
	return v
}

func (v *Verifier) ToolSequence(names ...string) *Verifier {
	seq := v.r.ToolCallSequence()
	idx := 0
	for _, s := range seq {
		if idx < len(names) && s == names[idx] {
			idx++
		}
	}
	if idx < len(names) {
		v.fail(fmt.Sprintf("tool sequence %v not found in %v (matched %d/%d)",
			names, seq, idx, len(names)))
	}
	return v
}

func (v *Verifier) ToolArgMatch(name string, predicate func(string) bool) *Verifier {
	found := false
	for _, e := range v.r.ToolCallsNamed(name) {
		if predicate(e.Args) {
			found = true
			break
		}
	}
	if !found {
		v.fail(fmt.Sprintf("no %q tool call matched arg predicate", name))
	}
	return v
}

func (v *Verifier) ToolResultMatch(name string, predicate func(string) bool) *Verifier {
	found := false
	for _, e := range v.r.ToolCallsNamed(name) {
		if predicate(e.Result) {
			found = true
			break
		}
	}
	if !found {
		v.fail(fmt.Sprintf("no %q tool result matched predicate", name))
	}
	return v
}

func (v *Verifier) ToolArgsContain(name, substr string) *Verifier {
	return v.ToolArgMatch(name, func(args string) bool {
		return strings.Contains(args, substr)
	})
}

func (v *Verifier) ToolResultContains(name, substr string) *Verifier {
	return v.ToolResultMatch(name, func(res string) bool {
		return strings.Contains(res, substr)
	})
}

func (v *Verifier) AnyResultContains(substr string) *Verifier {
	all := v.r.AllToolResults()
	if !strings.Contains(all, substr) && !v.r.ContainsOutput(substr) {
		v.fail(fmt.Sprintf("no tool result or output contains %q", substr))
	}
	return v
}

// =====================================================================
// Errors
// =====================================================================

func (v *Verifier) NoToolErrors() *Verifier {
	errs := v.r.ErroredToolCalls()
	if len(errs) > 0 {
		names := make([]string, len(errs))
		for i, e := range errs {
			names[i] = fmt.Sprintf("%s(%s)", e.ToolName, clip(e.Result, 80))
		}
		v.fail(fmt.Sprintf("%d tool call(s) errored: %s", len(errs), strings.Join(names, ", ")))
	}
	return v
}

// =====================================================================
// Loop / Subagent shortcuts (built on Expect)
// =====================================================================

func (v *Verifier) LoopCreated(name string) *Verifier {
	return v.Expect(Tool("loop").Action("create").Arg("name", name))
}

func (v *Verifier) LoopDeleted(name string) *Verifier {
	return v.Expect(Tool("loop").Action("delete").Arg("name", name))
}

func (v *Verifier) LoopListed() *Verifier {
	return v.Expect(Tool("loop").Action("list"))
}

func (v *Verifier) SubagentCreated(name string) *Verifier {
	return v.Expect(Tool("subagent").Arg("name", name))
}

func (v *Verifier) MinSubagentCreates(n int) *Verifier {
	if v.r.SubagentCreateCount() < n {
		v.fail(fmt.Sprintf("expected >= %d subagent creates, got %d", n, v.r.SubagentCreateCount()))
	}
	return v
}

func (v *Verifier) SubagentResultContains(substr string) *Verifier {
	for _, res := range v.r.SubagentResults() {
		if strings.Contains(res, substr) {
			return v
		}
	}
	v.fail(fmt.Sprintf("no subagent result contains %q", substr))
	return v
}
