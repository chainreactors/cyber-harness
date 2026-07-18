//go:build e2e

package harness

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
)

type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Events   []AgentEvent
}

// AgentEvent is a flattened view of AOP events for test assertions.
type AgentEvent struct {
	AOPType    string `json:"aop_type"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Args       string `json:"args,omitempty"`
	Result     string `json:"result,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	Content    string `json:"content,omitempty"`
	Role       string `json:"role,omitempty"`
	Delta      bool   `json:"delta,omitempty"`
	Stop       string `json:"stop,omitempty"`
	Turn       int    `json:"turn,omitempty"`
	Turns      int    `json:"turns,omitempty"`

	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

func (r *RunResult) OK() bool       { return r.ExitCode == 0 }
func (r *RunResult) Output() string { return strings.TrimSpace(r.Stdout) }
func (r *RunResult) Combined() string { return r.Stdout + r.Stderr }

func (r *RunResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Stdout, substr) || strings.Contains(r.Stderr, substr)
}

func (r *RunResult) ToolCalls() []AgentEvent {
	argsByID := make(map[string]string)
	for _, e := range r.Events {
		if e.AOPType == aop.TypeToolCall && e.ToolCallID != "" {
			argsByID[e.ToolCallID] = e.Args
		}
	}
	var calls []AgentEvent
	for _, e := range r.Events {
		if e.AOPType == aop.TypeToolResult {
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
		if r.Events[i].AOPType == aop.TypeSessionEnd {
			return r.Events[i].Stop
		}
	}
	return ""
}

func (r *RunResult) TotalTokens() int {
	for i := len(r.Events) - 1; i >= 0; i-- {
		if r.Events[i].AOPType == aop.TypeUsage && r.Events[i].TotalTokens > 0 {
			return r.Events[i].TotalTokens
		}
	}
	return 0
}

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

// loadEvents reads AOP JSONL and flattens into AgentEvent for assertions.
func loadEvents(path string) []AgentEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var events []AgentEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
	for scanner.Scan() {
		var ev aop.Event
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ae, ok := flattenAOPEvent(ev); ok {
			events = append(events, ae)
		}
	}
	return events
}

func flattenAOPEvent(ev aop.Event) (AgentEvent, bool) {
	ae := AgentEvent{AOPType: ev.Type}

	switch ev.Type {
	case aop.TypeText:
		var d aop.TextData
		_ = json.Unmarshal(ev.Data, &d)
		ae.Content = d.Content
		ae.Role = d.Role
		ae.Delta = d.Delta
	case aop.TypeToolCall:
		var d aop.ToolCallData
		_ = json.Unmarshal(ev.Data, &d)
		ae.ToolName = d.ToolName
		ae.ToolCallID = d.ToolCallID
		if s, ok := d.Args.(string); ok {
			ae.Args = s
		} else if d.Args != nil {
			raw, _ := json.Marshal(d.Args)
			ae.Args = string(raw)
		}
	case aop.TypeToolResult:
		var d aop.ToolResultData
		_ = json.Unmarshal(ev.Data, &d)
		ae.ToolName = d.ToolName
		ae.ToolCallID = d.ToolCallID
		ae.IsError = d.IsError
		if s, ok := d.Content.(string); ok {
			ae.Result = s
		} else if d.Content != nil {
			raw, _ := json.Marshal(d.Content)
			ae.Result = string(raw)
		}
	case aop.TypeUsage:
		var d aop.UsageData
		_ = json.Unmarshal(ev.Data, &d)
		ae.InputTokens = d.InputTokens
		ae.OutputTokens = d.OutputTokens
		ae.TotalTokens = d.TotalTokens
	case aop.TypeSessionEnd:
		var d aop.SessionEndData
		_ = json.Unmarshal(ev.Data, &d)
		ae.Stop = d.Stop
		ae.Turns = d.Turns
	case aop.TypeTurnStart, aop.TypeTurnEnd:
		var d aop.TurnData
		_ = json.Unmarshal(ev.Data, &d)
		ae.Turn = d.Turn
	case aop.TypeSessionStart:
		// no extra fields needed for assertions
	default:
		return ae, false
	}
	return ae, true
}
