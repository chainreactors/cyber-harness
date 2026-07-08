//go:build js && wasm

// Command wasm is the js/wasm entry point that runs aiscan's agent-core as the
// "brain" embedded in a browser extension (CyberHub Copilot), per RFC #189
// (方案 A). The Go loop/evaluator make every decision; the JS host owns every
// side effect. Three seams connect them, all crossing the boundary as JSON:
//
//	brain (this wasm)          seam (syscall/js)              hand (JS host)
//	----------------           -----------------              --------------
//	agent.Config.Provider  ->  __aiscanLLM(reqJSON)       ->  real OpenAI/Anthropic provider
//	commands.Registry      ->  __aiscanTool(name, args)   ->  chrome.scripting on the real tab
//	eventbus.Bus[Event]    ->  onEvent(eventJSON)         ->  UI / progress stream
//
// The wasm never imports go-rod / playwright / os-exec and never touches the
// DOM: browser tools are registered as host-dispatched stubs (schema only,
// execution delegated to __aiscanTool). See README.md for the wire protocol.
//
// Exported globals:
//
//	aiscanAgentReady : bool         // set true once exports are installed
//	aiscanRunAgent(payloadJSON, onEvent?) -> Promise<resultJSON>
//	aiscanCancelAgent(runId) -> bool  // aborts an in-flight run by run_id
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"syscall/js"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	"github.com/chainreactors/aiscan/pkg/commands"
)

// Names of the JS globals the host installs for the LLM and tool seams. The
// event seam is passed per-run as the second argument to aiscanRunAgent.
const (
	globalLLMFn   = "__aiscanLLM"
	globalToolFn  = "__aiscanTool"
	globalReadyCB = "__aiscanOnReady"
)

// cancels tracks the CancelFunc for every in-flight run keyed by run_id so the
// host can abort a specific run across the JS/WASM boundary.
var (
	cancelMu sync.Mutex
	cancels  = map[string]context.CancelFunc{}
)

func registerCancel(runID string, cancel context.CancelFunc) {
	if runID == "" {
		return
	}
	cancelMu.Lock()
	cancels[runID] = cancel
	cancelMu.Unlock()
}

func unregisterCancel(runID string) {
	if runID == "" {
		return
	}
	cancelMu.Lock()
	delete(cancels, runID)
	cancelMu.Unlock()
}

// cancelRun implements aiscanCancelAgent(runId) -> bool.
func cancelRun(_ js.Value, args []js.Value) any {
	if len(args) == 0 || args[0].Type() != js.TypeString {
		return false
	}
	cancelMu.Lock()
	cancel := cancels[args[0].String()]
	cancelMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// runAgent implements aiscanRunAgent(payloadJSON, onEvent?) -> Promise<resultJSON>.
// It returns immediately with a Promise; the loop runs on a goroutine so the JS
// event loop stays free to resolve the LLM/tool promises the loop awaits.
func runAgent(_ js.Value, args []js.Value) any {
	payloadJSON := ""
	if len(args) > 0 && args[0].Type() == js.TypeString {
		payloadJSON = args[0].String()
	}
	var onEvent js.Value
	if len(args) > 1 {
		onEvent = args[1]
	}
	return newPromise(func(resolve, reject func(any)) {
		out, err := doRun(payloadJSON, onEvent)
		if err != nil {
			reject(err.Error())
			return
		}
		resolve(out)
	})
}

// doRun builds an agent-core from the payload, wires the three JS seams, runs it
// (plain loop or evaluator loop), and returns the result as a JSON string. A run
// that errors mid-flight still resolves with a result carrying the error and any
// partial transcript; only a malformed payload rejects.
func doRun(payloadJSON string, onEvent js.Value) (string, error) {
	var p runPayload
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		return "", fmt.Errorf("parse payload: %w", err)
	}

	reg := commands.NewRegistry()
	for _, ts := range p.Tools {
		reg.RegisterTool(&jsTool{def: ts.definition()})
	}

	bus := eventbus.New[agent.Event]()
	if onEvent.Type() == js.TypeFunction {
		unsub := bus.Subscribe(func(ev agent.Event) {
			data, err := json.Marshal(ev)
			if err != nil {
				return
			}
			onEvent.Invoke(string(data))
		})
		defer unsub()
	}

	cfg := agent.Config{
		Provider:         &jsProvider{},
		Tools:            reg,
		Model:            p.Model,
		SystemPrompt:     p.SystemPrompt,
		Messages:         p.Messages,
		MaxTokens:        p.MaxTokens,
		Temperature:      p.Temperature,
		MaxTurns:         p.MaxTurns,
		MaxParallelTools: p.MaxParallelTools,
		TokenBudget:      p.TokenBudget,
		Bus:              bus,
		SessionID:        p.RunID,
		// The JS provider is non-streaming; the loop falls back to
		// ChatCompletion (retry.go) automatically, but be explicit.
		Stream: false,
	}

	ctx, cancel := context.WithCancel(context.Background())
	registerCancel(p.RunID, cancel)
	defer unregisterCancel(p.RunID)
	defer cancel()

	ag := agent.NewAgent(cfg)

	var (
		result *agent.Result
		runErr error
	)
	if p.Eval != nil && p.Eval.Criteria != "" {
		// Goal mode: judge the natural-language criteria each round and re-drive
		// with feedback, reusing the same host-backed provider as the judge.
		evalCfg := evaluator.EvalLoopConfig{
			Evaluator: evaluator.New(evaluator.Config{
				Provider: cfg.Provider,
				Model:    cfg.Model,
			}),
			MaxEvalRounds: p.Eval.MaxRounds,
			Goal:          p.Prompt,
			Criteria:      p.Eval.Criteria,
			Bus:           bus,
		}
		result, _, runErr = evaluator.RunWithEval(ctx, ag, evalCfg)
	} else {
		result, runErr = ag.Run(ctx, p.Prompt)
	}

	return marshalResult(result, runErr)
}

func main() {
	js.Global().Set("aiscanRunAgent", js.FuncOf(runAgent))
	js.Global().Set("aiscanCancelAgent", js.FuncOf(cancelRun))
	js.Global().Set("aiscanAgentReady", js.ValueOf(true))
	if cb := js.Global().Get(globalReadyCB); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	// Keep the instance alive so the exported funcs remain callable. With
	// registered js.Funcs pending, the Go wasm runtime yields to the JS event
	// loop here instead of reporting a deadlock.
	select {}
}
