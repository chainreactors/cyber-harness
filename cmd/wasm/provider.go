//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/chainreactors/aiscan/pkg/agent"
)

// jsProvider implements agent.Provider by bridging every ChatCompletion to the
// JS global __aiscanLLM(reqJSON, ctxJSON) -> Promise<respJSON>. The host owns
// provider selection, keys, caching and fallback; the brain just asks for a
// completion. ctxJSON is the same per-run context used by tools, so the host can
// bind LLM fetch cancellation to the right run.
type jsProvider struct {
	ctxJSON string
}

func (p *jsProvider) Name() string { return "wasm-host" }

func (p *jsProvider) ChatCompletion(ctx context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal LLM request: %w", err)
	}

	fn := js.Global().Get(globalLLMFn)
	if fn.Type() != js.TypeFunction {
		return nil, fmt.Errorf("host LLM bridge %q is not defined", globalLLMFn)
	}

	val, err := awaitValue(ctx, fn.Invoke(string(reqJSON), p.ctxJSON))
	if err != nil {
		return nil, err
	}
	if val.Type() != js.TypeString {
		return nil, fmt.Errorf("host LLM bridge must resolve a JSON string, got %s", val.Type())
	}

	var resp agent.ChatCompletionResponse
	if err := json.Unmarshal([]byte(val.String()), &resp); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return &resp, nil
}
