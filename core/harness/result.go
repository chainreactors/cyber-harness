//go:build e2e

package harness

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
)

type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Events   []AgentEvent
}

type AgentEvent struct {
	Type            string              `json:"type"`
	Turn            int                 `json:"turn,omitempty"`
	ToolName        string              `json:"tool_name,omitempty"`
	ToolCallID      string              `json:"tool_call_id,omitempty"`
	Args            string              `json:"arguments,omitempty"`
	Result          string              `json:"result,omitempty"`
	IsError         bool                `json:"is_error,omitempty"`
	Error           string              `json:"error,omitempty"`
	Stop            string              `json:"stop,omitempty"`
	Message         *agent.ChatMessage  `json:"message,omitempty"`
	ToolResults     []agent.ChatMessage `json:"tool_results,omitempty"`
	Usage           *agent.Usage        `json:"usage,omitempty"`
	ContextTokens   int                 `json:"context_tokens,omitempty"`
	NewMessages     int                 `json:"new_messages,omitempty"`
	RequestModel    string              `json:"request_model,omitempty"`
	RequestMessages int                 `json:"request_messages,omitempty"`
	RequestTools    int                 `json:"request_tools,omitempty"`
}

func (r *RunResult) OK() bool       { return r.ExitCode == 0 }
func (r *RunResult) Output() string { return strings.TrimSpace(r.Stdout) }
func (r *RunResult) Combined() string { return r.Stdout + r.Stderr }

func (r *RunResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Stdout, substr) || strings.Contains(r.Stderr, substr)
}

// ToolCalls returns merged tool call events: arguments come from
// tool_execution_start, results from tool_execution_end, joined by tool_call_id.
func (r *RunResult) ToolCalls() []AgentEvent {
	argsByID := make(map[string]string)
	for _, e := range r.Events {
		if e.Type == "tool_execution_start" && e.ToolCallID != "" {
			argsByID[e.ToolCallID] = e.Args
		}
	}
	var calls []AgentEvent
	for _, e := range r.Events {
		if e.Type == "tool_execution_end" {
			if e.Args == "" && e.ToolCallID != "" {
				e.Args = argsByID[e.ToolCallID]
			}
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

func (r *RunResult) StopReason() string {
	for i := len(r.Events) - 1; i >= 0; i-- {
		if r.Events[i].Type == "agent_end" {
			return r.Events[i].Stop
		}
	}
	return ""
}

func (r *RunResult) TotalTokens() int {
	for i := len(r.Events) - 1; i >= 0; i-- {
		if r.Events[i].Type == "turn_end" && r.Events[i].Usage != nil {
			return r.Events[i].Usage.TotalTokens
		}
	}
	return 0
}

// tool-specific accessors

func (r *RunResult) SubagentCalls() []AgentEvent { return r.ToolCallsNamed("subagent") }

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
	records, err := output.ParseRecordFile(path)
	if err != nil {
		return nil
	}
	var events []AgentEvent
	for _, rec := range records {
		if rec.Type != output.TypeAgent {
			continue
		}
		var e AgentEvent
		if json.Unmarshal(rec.Data, &e) == nil {
			events = append(events, e)
		}
	}
	return events
}
