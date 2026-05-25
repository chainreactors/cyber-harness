//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolPattern describes an expected tool call. Built with the Tool() function
// and refined with chainable methods.
//
//	Tool("loop").Action("create").Arg("name", "scanner").ResultHas("created")
//	Tool("bash").ArgContains("gogo").NoError()
//	Tool("subagent").Action("create").Arg("name", "worker").Arg("mode", "async")
type ToolPattern struct {
	tool       string
	action     string
	argChecks  []argCheck
	resultHas  []string
	resultNot  []string
	noError    bool
	isError    bool
	label      string
}

type argCheck struct {
	key      string
	contains string
}

func Tool(name string) ToolPattern {
	return ToolPattern{tool: name, label: name}
}

func (p ToolPattern) Action(action string) ToolPattern {
	p.action = action
	p.label = fmt.Sprintf("%s/%s", p.tool, action)
	return p
}

func (p ToolPattern) Arg(key, contains string) ToolPattern {
	p.argChecks = append(p.argChecks, argCheck{key: key, contains: contains})
	return p
}

func (p ToolPattern) ArgContains(substr string) ToolPattern {
	p.argChecks = append(p.argChecks, argCheck{contains: substr})
	return p
}

func (p ToolPattern) ResultHas(substr string) ToolPattern {
	p.resultHas = append(p.resultHas, substr)
	return p
}

func (p ToolPattern) ResultNot(substr string) ToolPattern {
	p.resultNot = append(p.resultNot, substr)
	return p
}

func (p ToolPattern) NoError() ToolPattern {
	p.noError = true
	return p
}

func (p ToolPattern) IsError() ToolPattern {
	p.isError = true
	return p
}

func (p ToolPattern) Label() string { return p.label }

func (p ToolPattern) Match(e AgentEvent) bool {
	if e.ToolName != p.tool {
		return false
	}
	if p.action != "" && !argsContainAction(e.Args, p.action) {
		return false
	}
	for _, ac := range p.argChecks {
		if ac.key != "" {
			if !argsFieldContains(e.Args, ac.key, ac.contains) {
				return false
			}
		} else {
			if !strings.Contains(e.Args, ac.contains) {
				return false
			}
		}
	}
	for _, s := range p.resultHas {
		if !strings.Contains(e.Result, s) {
			return false
		}
	}
	for _, s := range p.resultNot {
		if strings.Contains(e.Result, s) {
			return false
		}
	}
	if p.noError && e.IsError {
		return false
	}
	if p.isError && !e.IsError {
		return false
	}
	return true
}

func (p ToolPattern) describe() string {
	var parts []string
	parts = append(parts, p.tool)
	if p.action != "" {
		parts = append(parts, fmt.Sprintf("action=%s", p.action))
	}
	for _, ac := range p.argChecks {
		if ac.key != "" {
			parts = append(parts, fmt.Sprintf("arg[%s]~%q", ac.key, ac.contains))
		} else {
			parts = append(parts, fmt.Sprintf("args~%q", ac.contains))
		}
	}
	for _, s := range p.resultHas {
		parts = append(parts, fmt.Sprintf("result~%q", s))
	}
	return strings.Join(parts, " ")
}

func argsContainAction(argsJSON, action string) bool {
	return strings.Contains(argsJSON, fmt.Sprintf("%q", action))
}

func argsFieldContains(argsJSON, key, contains string) bool {
	var m map[string]any
	if json.Unmarshal([]byte(argsJSON), &m) != nil {
		return strings.Contains(argsJSON, contains)
	}
	val, ok := m[key]
	if !ok {
		return false
	}
	s := fmt.Sprintf("%v", val)
	return strings.Contains(s, contains)
}

// matchResult holds the result of matching expectations against actual tool calls.
type matchResult struct {
	matched   []matchPair
	unmatched []ToolPattern
}

type matchPair struct {
	pattern ToolPattern
	event   AgentEvent
	index   int
}

// matchUnordered finds a matching event for each pattern (greedy, unordered).
func matchUnordered(patterns []ToolPattern, events []AgentEvent) matchResult {
	used := make([]bool, len(events))
	var matched []matchPair
	var unmatched []ToolPattern

	for _, p := range patterns {
		found := false
		for i, e := range events {
			if used[i] {
				continue
			}
			if p.Match(e) {
				matched = append(matched, matchPair{pattern: p, event: e, index: i})
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			unmatched = append(unmatched, p)
		}
	}
	return matchResult{matched: matched, unmatched: unmatched}
}

// matchOrdered finds matching events in order (subsequence match).
func matchOrdered(patterns []ToolPattern, events []AgentEvent) matchResult {
	var matched []matchPair
	pi := 0
	for i, e := range events {
		if pi >= len(patterns) {
			break
		}
		if patterns[pi].Match(e) {
			matched = append(matched, matchPair{pattern: patterns[pi], event: e, index: i})
			pi++
		}
	}
	var unmatched []ToolPattern
	for _, p := range patterns[pi:] {
		unmatched = append(unmatched, p)
	}
	return matchResult{matched: matched, unmatched: unmatched}
}
