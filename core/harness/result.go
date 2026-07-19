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
	AOPType    string         `json:"aop_type"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Result     any            `json:"result,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
	Content    string         `json:"content,omitempty"`
	Role       string         `json:"role,omitempty"`
	Delta      bool           `json:"delta,omitempty"`
	Stop       string         `json:"stop,omitempty"`
	Turn       int            `json:"turn,omitempty"`
	Turns      int            `json:"turns,omitempty"`

	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

func (r *RunResult) OK() bool         { return r.ExitCode == 0 }
func (r *RunResult) Output() string   { return strings.TrimSpace(r.Stdout) }
func (r *RunResult) Combined() string { return r.Stdout + r.Stderr }

func (r *RunResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Stdout, substr) || strings.Contains(r.Stderr, substr)
}

func (r *RunResult) ToolCalls() []AgentEvent {
	argsByID := make(map[string]map[string]any)
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
		if strings.Contains(e.ResultText(), substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) ToolArgsContains(toolName, substr string) bool {
	for _, e := range r.ToolCallsNamed(toolName) {
		if strings.Contains(e.ArgsText(), substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) AllToolResults() string {
	var sb strings.Builder
	for _, e := range r.ToolCalls() {
		sb.WriteString(e.ResultText())
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
			args = append(args, e.ArgsText())
		}
	}
	return args
}

func (r *RunResult) SubagentResults() []string {
	var results []string
	for _, e := range r.SubagentCalls() {
		if isSubagentCreate(e) {
			results = append(results, e.ResultText())
		}
	}
	return results
}

func flattenAOPEvent(ev aop.Event) (AgentEvent, error) {
	ae := AgentEvent{AOPType: ev.Type}

	switch ev.Type {
	case aop.TypeText:
		var d aop.TextData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.Content = d.Content
		ae.Role = d.Role
		ae.Delta = d.Delta
	case aop.TypeToolCall:
		var d aop.ToolCallData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.ToolName = d.ToolName
		ae.ToolCallID = d.ToolCallID
		args, ok := d.Args.(map[string]any)
		if !ok {
			return ae, fmt.Errorf("AOP %s args must be an object", ev.Type)
		}
		ae.Args = args
	case aop.TypeToolResult:
		var d aop.ToolResultData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.ToolName = d.ToolName
		ae.ToolCallID = d.ToolCallID
		ae.IsError = d.IsError
		ae.Result = d.Content
	case aop.TypeUsage:
		var d aop.UsageData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.InputTokens = d.InputTokens
		ae.OutputTokens = d.OutputTokens
		ae.TotalTokens = d.TotalTokens
	case aop.TypeSessionEnd:
		var d aop.SessionEndData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.Stop = d.Stop
		ae.Turns = d.Turns
	case aop.TypeTurnStart, aop.TypeTurnEnd:
		var d aop.TurnData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.Turn = d.Turn
	case aop.TypeSessionStart:
		var d aop.SessionStartData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
	case aop.TypeError:
		var d aop.ErrorData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.Content = d.Message
		ae.IsError = true
	case aop.TypeStatus:
		var d aop.StatusData
		if err := decodeAOPData(ev, &d); err != nil {
			return ae, err
		}
		ae.Content = d.State
	default:
		return ae, fmt.Errorf("unsupported AOP event type %q", ev.Type)
	}
	return ae, nil
}

func decodeAOPData(ev aop.Event, target any) error {
	if err := json.Unmarshal(ev.Data, target); err != nil {
		return fmt.Errorf("decode AOP %s data: %w", ev.Type, err)
	}
	return nil
}

func eventValueText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func (e AgentEvent) ArgsText() string   { return eventValueText(e.Args) }
func (e AgentEvent) ResultText() string { return eventValueText(e.Result) }

func isSubagentCreate(event AgentEvent) bool {
	action, _ := event.Args["action"].(string)
	return action != "list" && action != "kill" && action != "message"
}
