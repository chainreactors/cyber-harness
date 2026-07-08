//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"strings"
	"syscall/js"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
)

// jsTool is a host-dispatched tool: the wasm advertises the schema to the LLM
// but delegates execution to the JS global __aiscanTool(name, argsJSON, ctxJSON)
// -> Promise<result>. This is the seam that keeps browser drivers (go-rod /
// playwright) out of the wasm — browser tools run in the host on the user's real
// tab; the brain only decides which tool to call. ctxJSON is the run's context
// blob ({tabId, url, runId}) threaded verbatim so the host hits the right tab.
type jsTool struct {
	def     agent.ToolDefinition
	ctxJSON string
}

func (t *jsTool) Name() string                        { return t.def.Function.Name }
func (t *jsTool) Description() string                 { return t.def.Function.Description }
func (t *jsTool) Definition() commands.ToolDefinition { return t.def }

func (t *jsTool) Execute(ctx context.Context, arguments string) (commands.ToolResult, error) {
	fn := js.Global().Get(globalToolFn)
	if fn.Type() != js.TypeFunction {
		return commands.ErrorResult("host tool bridge " + globalToolFn + " is not defined"), nil
	}
	val, err := awaitValue(ctx, fn.Invoke(t.Name(), arguments, t.ctxJSON))
	if err != nil {
		// A ctx cancel or host rejection: surface to the loop, which cancels
		// cleanly (canceled ctx) or heals the dangling tool_call.
		return commands.ToolResult{}, err
	}
	return toolResultFromJS(val), nil
}

// toolEnvelope is the host's __aiscanTool reply shape. Pointers distinguish
// "field present" from "zero value" so a bare JSON blob isn't mistaken for it.
type toolEnvelope struct {
	Content      *string `json:"content"`
	Text         *string `json:"text"`
	IsError      *bool   `json:"isError"`
	IsErrorSnake *bool   `json:"is_error"`
	Terminate    *bool   `json:"terminate"`
}

// toolResultFromJS parses the host reply. The extension returns a JSON string
// {content, isError, terminate}; a simpler host may return a plain string or an
// object — all three are accepted.
func toolResultFromJS(v js.Value) commands.ToolResult {
	switch v.Type() {
	case js.TypeString:
		return parseToolEnvelope(v.String())
	case js.TypeObject:
		return toolResultFromObject(v)
	case js.TypeUndefined, js.TypeNull:
		return commands.TextResult("")
	default:
		return commands.TextResult(v.String())
	}
}

func parseToolEnvelope(s string) commands.ToolResult {
	if strings.HasPrefix(strings.TrimSpace(s), "{") {
		var env toolEnvelope
		if json.Unmarshal([]byte(s), &env) == nil &&
			(env.Content != nil || env.Text != nil || env.IsError != nil || env.IsErrorSnake != nil || env.Terminate != nil) {
			return env.result()
		}
	}
	// Not the envelope — treat the whole string as the result text.
	return commands.TextResult(s)
}

func (env toolEnvelope) result() commands.ToolResult {
	content := ""
	switch {
	case env.Content != nil:
		content = *env.Content
	case env.Text != nil:
		content = *env.Text
	}
	res := commands.TextResult(content)
	switch {
	case env.IsError != nil:
		res.IsError = *env.IsError
	case env.IsErrorSnake != nil:
		res.IsError = *env.IsErrorSnake
	}
	if env.Terminate != nil {
		res.Terminate = *env.Terminate
	}
	return res
}

func toolResultFromObject(v js.Value) commands.ToolResult {
	get := func(k string) js.Value { return v.Get(k) }
	content := ""
	if c := get("content"); c.Type() == js.TypeString {
		content = c.String()
	} else if t := get("text"); t.Type() == js.TypeString {
		content = t.String()
	}
	res := commands.TextResult(content)
	if e := get("isError"); e.Type() == js.TypeBoolean {
		res.IsError = e.Bool()
	} else if e := get("is_error"); e.Type() == js.TypeBoolean {
		res.IsError = e.Bool()
	}
	if tm := get("terminate"); tm.Type() == js.TypeBoolean {
		res.Terminate = tm.Bool()
	}
	return res
}
