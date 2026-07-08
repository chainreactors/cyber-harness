//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/chainreactors/aiscan/pkg/agent"
)

// ---- wire types (host <-> wasm) --------------------------------------------
//
// Field names match the CyberHub Copilot host (src/offscreen/wasm-agent.ts):
// camelCase, `task` for the new user message, a nested `context` blob passed
// verbatim to the tool bridge, and a `{output, messages, turns, stop}` result.

// runPayload is the JSON argument to runAgent.
type runPayload struct {
	Task         string              `json:"task"`
	SystemPrompt string              `json:"systemPrompt"`
	Model        string              `json:"model"`
	MaxTurns     int                 `json:"maxTurns"`
	Messages     []agent.ChatMessage `json:"messages"`
	Tools        []toolSchema        `json:"tools"`
	// context is threaded, untouched, to __aiscanTool so the host can target the
	// right tab and observe the right abort signal. Kept raw to avoid re-shaping.
	Context json.RawMessage `json:"context"`
	// Optional knobs (not sent by the current host; supported for completeness).
	MaxParallelTools int       `json:"maxParallelTools"`
	MaxTokens        int       `json:"maxTokens"`
	Temperature      *float64  `json:"temperature"`
	TokenBudget      int       `json:"tokenBudget"`
	Eval             *evalSpec `json:"eval"`
}

// runContext is the subset of `context` the wasm itself needs (the run id, used
// as the cancellation key and session id).
type runContext struct {
	RunID string `json:"runId"`
}

// toolSchema is a host-executed tool advertised to the LLM. The wasm holds only
// the schema; execution is delegated to the JS host via __aiscanTool.
type toolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

func (t toolSchema) definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Type: "function",
		Function: agent.FunctionDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		},
	}
}

// evalSpec enables the evaluator (Goal) loop when Criteria is non-empty.
type evalSpec struct {
	Criteria  string `json:"criteria"`
	MaxRounds int    `json:"maxRounds"`
}

// runResult is the JSON the run Promise resolves with. Matches what
// WasmAgentConversation.send() reads: output / messages / turns / stop.
type runResult struct {
	Output   string              `json:"output"`
	Messages []agent.ChatMessage `json:"messages"`
	Turns    int                 `json:"turns"`
	Stop     string              `json:"stop"`
	Usage    agent.Usage         `json:"usage"`
	Error    string              `json:"error,omitempty"`
}

// mapStop projects aiscan's richer StopReason set onto the host's vocabulary
// ("completed" | "terminated" | "max_turns" | "error"). A user-cancelled run is
// detected host-side via the abort signal, so its exact stop string is moot.
func mapStop(s agent.StopReason) string {
	switch s {
	case agent.StopReasonStopped, agent.StopReasonBudget:
		return "max_turns"
	default:
		return string(s)
	}
}

func marshalResult(result *agent.Result, runErr error) (string, error) {
	var out runResult
	if result != nil {
		out.Output = result.Output
		out.Messages = result.Messages
		out.Turns = result.Turns
		out.Stop = mapStop(result.Stop)
		out.Usage = result.TotalUsage
		if result.Err != nil {
			out.Error = result.Err.Error()
		}
	}
	if runErr != nil && out.Error == "" {
		out.Error = runErr.Error()
	}
	if out.Error != "" && (out.Stop == "" || out.Stop == "completed") {
		out.Stop = "error"
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(data), nil
}

// ---- promise plumbing (Go <-> JS) ------------------------------------------

// newPromise returns a JS Promise whose executor runs `run` on a goroutine, so
// the loop can block on channels (awaiting LLM/tool promises) while the JS event
// loop keeps turning. A panic in the goroutine rejects the promise rather than
// tearing down the whole wasm instance.
func newPromise(run func(resolve, reject func(any))) js.Value {
	var executor js.Func
	executor = js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			defer executor.Release()
			defer func() {
				if r := recover(); r != nil {
					reject.Invoke(fmt.Sprintf("wasm panic: %v", r))
				}
			}()
			run(
				func(v any) { resolve.Invoke(v) },
				func(v any) { reject.Invoke(v) },
			)
		}()
		return nil
	})
	return js.Global().Get("Promise").New(executor)
}

// awaitValue blocks until a JS thenable settles and returns its value (or
// error). A non-thenable is returned as-is, so a host callback may answer
// synchronously. Honors ctx: on cancel it returns ctx.Err() and releases the
// callbacks once the promise eventually settles (no leak, no goroutine spin).
func awaitValue(ctx context.Context, v js.Value) (js.Value, error) {
	if v.Type() != js.TypeObject || v.Get("then").Type() != js.TypeFunction {
		return v, nil
	}

	type outcome struct {
		val js.Value
		err error
	}
	ch := make(chan outcome, 1)

	var onOK, onErr js.Func
	release := func() {
		onOK.Release()
		onErr.Release()
	}
	onOK = js.FuncOf(func(_ js.Value, args []js.Value) any {
		val := js.Undefined()
		if len(args) > 0 {
			val = args[0]
		}
		ch <- outcome{val: val}
		return nil
	})
	onErr = js.FuncOf(func(_ js.Value, args []js.Value) any {
		ch <- outcome{err: jsError(args)}
		return nil
	})
	v.Call("then", onOK, onErr)

	select {
	case o := <-ch:
		release()
		return o.val, o.err
	case <-ctx.Done():
		// The promise is still pending; drain it whenever it settles so the
		// js.Funcs are released rather than leaked.
		go func() {
			<-ch
			release()
		}()
		return js.Undefined(), ctx.Err()
	}
}

// jsError turns a promise rejection value into a Go error, preferring an Error's
// .message when present.
func jsError(args []js.Value) error {
	if len(args) == 0 {
		return errors.New("promise rejected")
	}
	e := args[0]
	switch e.Type() {
	case js.TypeString:
		return errors.New(e.String())
	case js.TypeObject:
		if msg := e.Get("message"); msg.Type() == js.TypeString {
			return errors.New(msg.String())
		}
	}
	return fmt.Errorf("promise rejected: %v", e)
}
