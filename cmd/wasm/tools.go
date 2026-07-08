//go:build js && wasm

package main

import (
	"context"
	"syscall/js"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
)

// jsTool is a host-dispatched tool: the wasm advertises the schema to the LLM
// but delegates execution to the JS global __aiscanTool(name, argsJSON) ->
// Promise<result>. This is the seam that keeps browser drivers (go-rod /
// playwright) out of the wasm — browser tools run in the host on the user's real
// tab, the brain only decides which tool to call.
type jsTool struct {
	def agent.ToolDefinition
}

func (t *jsTool) Name() string                        { return t.def.Function.Name }
func (t *jsTool) Description() string                 { return t.def.Function.Description }
func (t *jsTool) Definition() commands.ToolDefinition { return t.def }

func (t *jsTool) Execute(ctx context.Context, arguments string) (commands.ToolResult, error) {
	fn := js.Global().Get(globalToolFn)
	if fn.Type() != js.TypeFunction {
		return commands.ErrorResult("host tool bridge " + globalToolFn + " is not defined"), nil
	}
	val, err := awaitValue(ctx, fn.Invoke(t.Name(), arguments))
	if err != nil {
		// A ctx cancel or host rejection: surface as a tool error so the loop
		// can heal the dangling tool_call rather than aborting the whole run.
		return commands.ToolResult{}, err
	}
	return toolResultFromJS(val), nil
}

// toolResultFromJS accepts either a plain string (the result text) or an object
// { text, is_error, terminate } so the host can signal errors and let a tool end
// the run (e.g. a finish/submit action).
func toolResultFromJS(v js.Value) commands.ToolResult {
	switch v.Type() {
	case js.TypeString:
		return commands.TextResult(v.String())
	case js.TypeObject:
		text := ""
		if t := v.Get("text"); t.Type() == js.TypeString {
			text = t.String()
		}
		res := commands.TextResult(text)
		if e := v.Get("is_error"); e.Type() == js.TypeBoolean {
			res.IsError = e.Bool()
		}
		if tm := v.Get("terminate"); tm.Type() == js.TypeBoolean {
			res.Terminate = tm.Bool()
		}
		return res
	case js.TypeUndefined, js.TypeNull:
		return commands.TextResult("")
	default:
		return commands.TextResult(v.String())
	}
}
