//go:build e2e

package harness

import (
	"fmt"
	"strings"
	"testing"
)

// Verify provides chainable assertions on a RunResult.
// Every check accumulates failures; call Done() to report them.
//
//	Verify(t, r).
//	    OK().
//	    ToolUsed("bash").
//	    ToolSequence("bash", "task", "bash").
//	    ToolArgMatch("loop", func(args string) bool { return strings.Contains(args, `"create"`) }).
//	    ToolResultMatch("bash", func(res string) bool { return strings.Contains(res, "hello") }).
//	    MinTurns(2).
//	    MinToolCalls(3).
//	    NoToolErrors().
//	    OutputContains("done").
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
	sb.WriteString(fmt.Sprintf("\nresult: exit=%d turns=%d tools=%d duration=%s",
		v.r.ExitCode, v.r.Turns(), len(v.r.ToolCalls()), v.r.Duration))
	v.t.Fatal(sb.String())
}

// --- exit / output ---

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

// --- turns ---

func (v *Verifier) MinTurns(n int) *Verifier {
	if v.r.Turns() < n {
		v.fail(fmt.Sprintf("expected >= %d turns, got %d", n, v.r.Turns()))
	}
	return v
}

// --- tool presence ---

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

func (v *Verifier) MinToolCalls(n int) *Verifier {
	if len(v.r.ToolCalls()) < n {
		v.fail(fmt.Sprintf("expected >= %d tool calls, got %d", n, len(v.r.ToolCalls())))
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

// --- tool sequence ---

// ToolSequence checks that the given tools appear in order (not necessarily
// contiguous). For example ToolSequence("bash", "task", "bash") passes if
// the full sequence is [bash, read, task, bash, write].
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

// --- tool args / results ---

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

// --- errors ---

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

// --- loop-specific ---

func (v *Verifier) LoopCreated(name string) *Verifier {
	for _, e := range v.r.LoopCalls() {
		if strings.Contains(e.Args, `"create"`) && strings.Contains(e.Args, name) {
			return v
		}
	}
	v.fail(fmt.Sprintf("loop %q was not created", name))
	return v
}

func (v *Verifier) LoopDeleted(name string) *Verifier {
	for _, e := range v.r.LoopCalls() {
		if strings.Contains(e.Args, `"delete"`) && strings.Contains(e.Args, name) {
			return v
		}
	}
	v.fail(fmt.Sprintf("loop %q was not deleted", name))
	return v
}

func (v *Verifier) LoopListed() *Verifier {
	for _, e := range v.r.LoopCalls() {
		if strings.Contains(e.Args, `"list"`) {
			return v
		}
	}
	v.fail("loop list was never called")
	return v
}

// --- subagent-specific ---

func (v *Verifier) SubagentCreated(name string) *Verifier {
	for _, e := range v.r.SubagentCalls() {
		if strings.Contains(e.Args, name) &&
			!strings.Contains(e.Args, `"list"`) &&
			!strings.Contains(e.Args, `"kill"`) {
			return v
		}
	}
	v.fail(fmt.Sprintf("subagent %q was not created", name))
	return v
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
