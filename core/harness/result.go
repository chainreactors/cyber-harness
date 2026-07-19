//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
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
	AOPType    string
	ToolName   string
	ToolCallID string
	Args       map[string]json.RawMessage
	Result     string
	IsError    bool
	Content    string
	Role       string
	Delta      bool
	Stop       string
	Turn       int
	Turns      int

	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

func (r *RunResult) OK() bool         { return r.ExitCode == 0 }
func (r *RunResult) Output() string   { return strings.TrimSpace(r.Stdout) }
func (r *RunResult) Combined() string { return r.Stdout + r.Stderr }

func (r *RunResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Stdout, substr) || strings.Contains(r.Stderr, substr)
}

func (r *RunResult) ToolCalls() []AgentEvent {
	argsByID := make(map[string]map[string]json.RawMessage)
	for _, e := range r.Events {
		if e.AOPType == aop.TypeToolCall && e.ToolCallID != "" {
			argsByID[e.ToolCallID] = e.Args
		}
	}
	var calls []AgentEvent
	for _, e := range r.Events {
		if e.AOPType == aop.TypeToolResult {
			if e.Args == nil && e.ToolCallID != "" {
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
		if strings.Contains(argsText(e.Args), substr) {
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
		if isSubagentCreate(e) {
			n++
		}
	}
	return n
}

func (r *RunResult) SubagentCreateArgs() []string {
	var args []string
	for _, e := range r.SubagentCalls() {
		if isSubagentCreate(e) {
			args = append(args, argsText(e.Args))
		}
	}
	return args
}

func (r *RunResult) SubagentResults() []string {
	var results []string
	for _, e := range r.SubagentCalls() {
		if isSubagentCreate(e) {
			results = append(results, e.Result)
		}
	}
	return results
}

func flattenAOPEvent(ev aop.Event) (AgentEvent, error) {
	ae := AgentEvent{AOPType: ev.Type}

	switch ev.Type {
	case aop.TypeText:
		d, err := decodeAOPData[aop.TextData](ev)
		if err != nil {
			return ae, err
		}
		ae.Content = d.Content
		ae.Role = d.Role
		ae.Delta = d.Delta
	case aop.TypeToolCall:
		d, err := decodeAOPData[struct {
			ToolCallID string                     `json:"tool_call_id"`
			ToolName   string                     `json:"tool_name"`
			Args       map[string]json.RawMessage `json:"args"`
		}](ev)
		if err != nil {
			return ae, err
		}
		ae.ToolName = d.ToolName
		ae.ToolCallID = d.ToolCallID
		if d.Args == nil {
			return ae, fmt.Errorf("AOP %s args must be an object", ev.Type)
		}
		ae.Args = d.Args
	case aop.TypeToolResult:
		d, err := decodeAOPData[struct {
			ToolCallID string `json:"tool_call_id"`
			ToolName   string `json:"tool_name"`
			Content    string `json:"content"`
			IsError    bool   `json:"is_error"`
		}](ev)
		if err != nil {
			return ae, err
		}
		ae.ToolName = d.ToolName
		ae.ToolCallID = d.ToolCallID
		ae.IsError = d.IsError
		ae.Result = d.Content
	case aop.TypeUsage:
		d, err := decodeAOPData[aop.UsageData](ev)
		if err != nil {
			return ae, err
		}
		ae.InputTokens = d.InputTokens
		ae.OutputTokens = d.OutputTokens
		ae.TotalTokens = d.TotalTokens
	case aop.TypeSessionEnd:
		d, err := decodeAOPData[aop.SessionEndData](ev)
		if err != nil {
			return ae, err
		}
		ae.Stop = d.Stop
		ae.Turns = d.Turns
	case aop.TypeTurnStart, aop.TypeTurnEnd:
		d, err := decodeAOPData[aop.TurnData](ev)
		if err != nil {
			return ae, err
		}
		ae.Turn = d.Turn
	case aop.TypeSessionStart:
		if _, err := decodeAOPData[aop.SessionStartData](ev); err != nil {
			return ae, err
		}
	case aop.TypeError:
		d, err := decodeAOPData[aop.ErrorData](ev)
		if err != nil {
			return ae, err
		}
		ae.Content = d.Message
		ae.IsError = true
	case aop.TypeStatus:
		d, err := decodeAOPData[aop.StatusData](ev)
		if err != nil {
			return ae, err
		}
		ae.Content = d.State
	default:
		return ae, fmt.Errorf("unsupported AOP event type %q", ev.Type)
	}
	return ae, nil
}

func decodeAOPData[T any](ev aop.Event) (T, error) {
	var data T
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		return data, fmt.Errorf("decode AOP %s data: %w", ev.Type, err)
	}
	return data, nil
}

func isSubagentCreate(event AgentEvent) bool {
	action := string(event.Args["action"])
	return action != `"list"` && action != `"kill"` && action != `"message"`
}

func argsText(args map[string]json.RawMessage) string {
	if len(args) == 0 {
		return "{}"
	}
	encoded, _ := json.Marshal(args)
	return string(encoded)
}
