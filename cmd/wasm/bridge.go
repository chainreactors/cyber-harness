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

// runPayload is the JSON argument to aiscanRunAgent. It carries the task, the
// prior transcript to hydrate, and the tool schemas the host can execute.
type runPayload struct {
	RunID            string              `json:"run_id"`
	Prompt           string              `json:"prompt"`
	SystemPrompt     string              `json:"system_prompt"`
	Model            string              `json:"model"`
	Messages         []agent.ChatMessage `json:"messages"`
	Tools            []toolSchema        `json:"tools"`
	MaxTurns         int                 `json:"max_turns"`
	MaxParallelTools int                 `json:"max_parallel_tools"`
	MaxTokens        int                 `json:"max_tokens"`
	Temperature      *float64            `json:"temperature"`
	TokenBudget      int                 `json:"token_budget"`
	Eval             *evalSpec           `json:"eval"`
}

// toolSchema is a host-executed tool advertised to the LLM. The wasm only holds
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
	MaxRounds int    `json:"max_rounds"`
}

// runResult is the JSON the run Promise resolves with.
type runResult struct {
	Output        string              `json:"output"`
	Messages      []agent.ChatMessage `json:"messages"`
	NewMessages   []agent.ChatMessage `json:"new_messages,omitempty"`
	Turns         int                 `json:"turns"`
	Stop          string              `json:"stop"`
	Usage         agent.Usage         `json:"usage"`
	ContextTokens int                 `json:"context_tokens,omitempty"`
	Error         string              `json:"error,omitempty"`
}

func marshalResult(result *agent.Result, runErr error) (string, error) {
	var out runResult
	if result != nil {
		out.Output = result.Output
		out.Messages = result.Messages
		out.NewMessages = result.NewMessages
		out.Turns = result.Turns
		out.Stop = string(result.Stop)
		out.Usage = result.TotalUsage
		out.ContextTokens = result.ContextTokens
		if result.Err != nil {
			out.Error = result.Err.Error()
		}
	}
	if runErr != nil && out.Error == "" {
		out.Error = runErr.Error()
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
