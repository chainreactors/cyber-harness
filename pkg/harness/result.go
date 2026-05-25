//go:build e2e

package harness

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Events   []AgentEvent
}

type AgentEvent struct {
	Type       string `json:"type"`
	Turn       int    `json:"turn,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Args       string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (r *RunResult) OK() bool       { return r.ExitCode == 0 }
func (r *RunResult) Output() string { return strings.TrimSpace(r.Stdout) }
func (r *RunResult) Combined() string { return r.Stdout + r.Stderr }

func (r *RunResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Stdout, substr) || strings.Contains(r.Stderr, substr)
}

func (r *RunResult) ToolCalls() []AgentEvent {
	var calls []AgentEvent
	for _, e := range r.Events {
		if e.Type == "tool_execution_end" {
			calls = append(calls, e)
		}
	}
	return calls
}

func (r *RunResult) HasToolCall(name string) bool {
	for _, e := range r.ToolCalls() {
		if e.ToolName == name {
			return true
		}
	}
	return false
}

func (r *RunResult) ToolCallsNamed(name string) []AgentEvent {
	var out []AgentEvent
	for _, e := range r.ToolCalls() {
		if e.ToolName == name {
			out = append(out, e)
		}
	}
	return out
}

func (r *RunResult) Turns() int {
	max := 0
	for _, e := range r.Events {
		if e.Turn > max {
			max = e.Turn
		}
	}
	return max
}

func (r *RunResult) ToolCallSequence() []string {
	var names []string
	for _, e := range r.ToolCalls() {
		names = append(names, e.ToolName)
	}
	return names
}

func (r *RunResult) ToolResultContains(toolName, substr string) bool {
	for _, e := range r.ToolCallsNamed(toolName) {
		if strings.Contains(e.Result, substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) ToolArgsContains(toolName, substr string) bool {
	for _, e := range r.ToolCallsNamed(toolName) {
		if strings.Contains(e.Args, substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) AllToolResults() string {
	var sb strings.Builder
	for _, e := range r.ToolCalls() {
		sb.WriteString(e.Result)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (r *RunResult) ErroredToolCalls() []AgentEvent {
	var out []AgentEvent
	for _, e := range r.ToolCalls() {
		if e.IsError {
			out = append(out, e)
		}
	}
	return out
}

// tool-specific accessors

func (r *RunResult) LoopCalls() []AgentEvent    { return r.ToolCallsNamed("loop") }
func (r *RunResult) SubagentCalls() []AgentEvent { return r.ToolCallsNamed("subagent") }

func (r *RunResult) LoopCreateCount() int {
	n := 0
	for _, e := range r.LoopCalls() {
		if strings.Contains(e.Args, `"create"`) {
			n++
		}
	}
	return n
}

func (r *RunResult) SubagentCreateCount() int {
	n := 0
	for _, e := range r.SubagentCalls() {
		if !strings.Contains(e.Args, `"list"`) && !strings.Contains(e.Args, `"kill"`) && !strings.Contains(e.Args, `"message"`) {
			n++
		}
	}
	return n
}

func (r *RunResult) SubagentCreateArgs() []string {
	var args []string
	for _, e := range r.SubagentCalls() {
		if !strings.Contains(e.Args, `"list"`) && !strings.Contains(e.Args, `"kill"`) && !strings.Contains(e.Args, `"message"`) {
			args = append(args, e.Args)
		}
	}
	return args
}

func (r *RunResult) SubagentResults() []string {
	var results []string
	for _, e := range r.SubagentCalls() {
		if !strings.Contains(e.Args, `"list"`) && !strings.Contains(e.Args, `"kill"`) && !strings.Contains(e.Args, `"message"`) {
			results = append(results, e.Result)
		}
	}
	return results
}

func loadEvents(path string) []AgentEvent {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var events []AgentEvent
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e AgentEvent
		if json.Unmarshal([]byte(line), &e) == nil {
			events = append(events, e)
		}
	}
	return events
}
