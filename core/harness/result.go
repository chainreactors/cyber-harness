//go:build e2e

package harness

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
)

type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Events   []aop.Event
}

// ToolExecution is an on-demand typed view that retains both original AOP
// envelopes. It is never stored in place of the protocol events.
type ToolExecution struct {
	CallEvent   aop.Event
	ResultEvent aop.Event
	Call        aop.ToolCallData
	Result      aop.ToolResultData
}

func (e ToolExecution) Name() string {
	if e.Result.ToolName != "" {
		return e.Result.ToolName
	}
	return e.Call.ToolName
}

func (e ToolExecution) Args() any          { return e.Call.Args }
func (e ToolExecution) ResultText() string { return valueText(e.Result.Content) }
func (e ToolExecution) IsError() bool      { return e.Result.IsError }

func (r *RunResult) OK() bool         { return r.ExitCode == 0 }
func (r *RunResult) Output() string   { return strings.TrimSpace(r.Stdout) }
func (r *RunResult) Combined() string { return r.Stdout + r.Stderr }

func (r *RunResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Stdout, substr) || strings.Contains(r.Stderr, substr)
}

func (r *RunResult) ToolCalls() []ToolExecution {
	calls := make(map[string]struct {
		event aop.Event
		data  aop.ToolCallData
	})
	for _, event := range r.Events {
		if event.Type != aop.TypeToolCall {
			continue
		}
		data, err := aop.DecodeData[aop.ToolCallData](event)
		if err == nil && data.ToolCallID != "" {
			calls[data.ToolCallID] = struct {
				event aop.Event
				data  aop.ToolCallData
			}{event: event, data: data}
		}
	}
	var out []ToolExecution
	for _, event := range r.Events {
		if event.Type != aop.TypeToolResult {
			continue
		}
		data, err := aop.DecodeData[aop.ToolResultData](event)
		if err != nil {
			continue
		}
		call := calls[data.ToolCallID]
		out = append(out, ToolExecution{
			CallEvent: call.event, ResultEvent: event, Call: call.data, Result: data,
		})
	}
	return out
}

func (r *RunResult) HasToolCall(name string) bool {
	return len(r.ToolCallsNamed(name)) > 0
}

func (r *RunResult) ToolCallsNamed(name string) []ToolExecution {
	var out []ToolExecution
	for _, execution := range r.ToolCalls() {
		if execution.Name() == name {
			out = append(out, execution)
		}
	}
	return out
}

func (r *RunResult) Turns() int {
	max := 0
	for _, event := range r.Events {
		turn := 0
		switch event.Type {
		case aop.TypeTurnStart:
			if data, err := aop.DecodeData[aop.TurnData](event); err == nil {
				turn = data.Turn
			}
		case aop.TypeTurnEnd:
			if data, err := aop.DecodeData[aop.TurnEndData](event); err == nil {
				turn = data.Turn
			}
		default:
			continue
		}
		if turn > max {
			max = turn
		}
	}
	return max
}

func (r *RunResult) ToolCallSequence() []string {
	var names []string
	for _, execution := range r.ToolCalls() {
		names = append(names, execution.Name())
	}
	return names
}

func (r *RunResult) ToolResultContains(toolName, substr string) bool {
	for _, execution := range r.ToolCallsNamed(toolName) {
		if strings.Contains(execution.ResultText(), substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) ToolArgsContains(toolName, substr string) bool {
	for _, execution := range r.ToolCallsNamed(toolName) {
		if strings.Contains(argsText(execution.Args()), substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) AllToolResults() string {
	var sb strings.Builder
	for _, execution := range r.ToolCalls() {
		sb.WriteString(execution.ResultText())
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (r *RunResult) ErroredToolCalls() []ToolExecution {
	var out []ToolExecution
	for _, execution := range r.ToolCalls() {
		if execution.IsError() {
			out = append(out, execution)
		}
	}
	return out
}

func (r *RunResult) StopReason() string {
	for i := len(r.Events) - 1; i >= 0; i-- {
		if r.Events[i].Type != aop.TypeSessionEnd {
			continue
		}
		data, err := aop.DecodeData[aop.SessionEndData](r.Events[i])
		if err == nil {
			return data.Stop
		}
	}
	return ""
}

func (r *RunResult) TotalTokens() int {
	for i := len(r.Events) - 1; i >= 0; i-- {
		if r.Events[i].Type != aop.TypeUsage {
			continue
		}
		data, err := aop.DecodeData[aop.UsageData](r.Events[i])
		if err == nil && data.TotalTokens > 0 {
			return data.TotalTokens
		}
	}
	return 0
}

func (r *RunResult) SubagentCalls() []ToolExecution { return r.ToolCallsNamed("subagent") }

func (r *RunResult) SubagentCreateCount() int {
	n := 0
	for _, execution := range r.SubagentCalls() {
		if isSubagentCreate(execution) {
			n++
		}
	}
	return n
}

func (r *RunResult) SubagentCreateArgs() []string {
	var args []string
	for _, execution := range r.SubagentCalls() {
		if isSubagentCreate(execution) {
			args = append(args, argsText(execution.Args()))
		}
	}
	return args
}

func (r *RunResult) SubagentResults() []string {
	var results []string
	for _, execution := range r.SubagentCalls() {
		if isSubagentCreate(execution) {
			results = append(results, execution.ResultText())
		}
	}
	return results
}

func isSubagentCreate(execution ToolExecution) bool {
	args, ok := execution.Args().(map[string]any)
	if !ok {
		return true
	}
	action, _ := args["action"].(string)
	return action != "list" && action != "kill" && action != "message"
}

func argsText(args any) string { return valueText(args) }

func valueText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
