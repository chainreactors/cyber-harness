package agent

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/agent/hooks"
)

// The kernel reaches the typed hook registry only through these helpers. Each
// helper preserves the zero-handler fast path exposed by hooks.Registry.

func runStartHook(ctx context.Context, cfg Config, systemPrompt string) (string, []ChatMessage) {
	if !cfg.Hooks.Has(hooks.BeforeRun.Kind) {
		return systemPrompt, nil
	}
	result, _ := hooks.BeforeRun.Emit(ctx, cfg.Hooks, hooks.RunStartEvent{
		SessionID:    cfg.SessionID,
		TurnID:       cfg.TurnID,
		AgentName:    cfg.AgentName,
		Model:        cfg.Model,
		SystemPrompt: systemPrompt,
		ToolNames:    toolNames(cfg),
	})
	if result.SystemPrompt != nil {
		systemPrompt = *result.SystemPrompt
	}
	return systemPrompt, result.Prepend
}

func toolNames(cfg Config) []string {
	if cfg.Tools == nil {
		return nil
	}
	definitions := cfg.Tools.ToolDefinitions()
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Function.Name)
	}
	return names
}

func transformContextHook(ctx context.Context, cfg Config, messages []ChatMessage, turn int) []ChatMessage {
	if !cfg.Hooks.Has(hooks.Context.Kind) {
		return messages
	}
	result, _ := hooks.Context.Emit(ctx, cfg.Hooks, hooks.ContextEvent{
		SessionID: cfg.SessionID,
		Turn:      turn,
		Messages:  messages,
	})
	if result.Messages != nil {
		return result.Messages
	}
	return messages
}

// beforeTypedToolCall is fail-closed: a handler error means the call was not
// approved and is returned to the model as a tool error.
func beforeTypedToolCall(ctx context.Context, cfg Config, assistantMsg ChatMessage, tc ToolCall) toolExecution {
	if !cfg.Hooks.Has(hooks.ToolCallHook.Kind) {
		return toolExecution{}
	}
	decision, err := hooks.ToolCallHook.Emit(ctx, cfg.Hooks, hooks.ToolCallEvent{
		SessionID:        cfg.SessionID,
		TurnID:           cfg.TurnID,
		AssistantMessage: assistantMsg,
		Call:             tc,
		SystemPrompt:     cfg.SystemPrompt,
		Messages:         cfg.Messages,
	})
	if err != nil {
		return toolExecution{result: fmt.Sprintf("error: %s", err), isError: true, err: err}
	}
	if !decision.Block {
		return toolExecution{}
	}
	reason := decision.Reason
	if reason == "" {
		reason = "tool execution was blocked"
	}
	return toolExecution{result: reason, isError: true}
}

func afterTypedToolCall(ctx context.Context, cfg Config, tc ToolCall, execution toolExecution, durationMs int) toolExecution {
	if !cfg.Hooks.Has(hooks.ToolResult.Kind) {
		return execution
	}
	patch, err := hooks.ToolResult.Emit(ctx, cfg.Hooks, hooks.ToolResultEvent{
		SessionID:  cfg.SessionID,
		TurnID:     cfg.TurnID,
		Call:       tc,
		Content:    execution.result,
		IsError:    execution.isError,
		Terminate:  execution.flow == ToolFlowTerminate,
		DurationMs: durationMs,
		Full:       execution.fullResult,
	})
	if err != nil {
		execution.result = fmt.Sprintf("error: %s", err)
		execution.isError = true
		execution.err = err
		return execution
	}
	if patch.Content != nil {
		execution.result = *patch.Content
	}
	if patch.IsError != nil {
		execution.isError = *patch.IsError
		if !execution.isError {
			execution.err = nil
		}
	}
	if patch.Terminate != nil {
		if *patch.Terminate {
			execution.flow = ToolFlowTerminate
		} else {
			execution.flow = ToolFlowContinue
		}
	}
	return execution
}

func compactCanceled(ctx context.Context, cfg Config, trigger string, contextTokens int) (bool, string) {
	if !cfg.Hooks.Has(hooks.BeforeCompact.Kind) {
		return false, ""
	}
	result, _ := hooks.BeforeCompact.Emit(ctx, cfg.Hooks, hooks.CompactEvent{
		SessionID:     cfg.SessionID,
		Trigger:       trigger,
		ContextTokens: contextTokens,
		ContextWindow: cfg.ContextWindow,
	})
	return result.Cancel, result.Reason
}

func emitRunEnd(ctx context.Context, cfg Config, result *Result) {
	if result == nil || !cfg.Hooks.Has(hooks.RunEnd.Kind) {
		return
	}
	_, _ = hooks.RunEnd.Emit(ctx, cfg.Hooks, hooks.RunEndEvent{
		SessionID:      cfg.SessionID,
		TurnID:         cfg.TurnID,
		Stop:           result.Stop,
		Output:         result.Output,
		Messages:       result.Messages,
		MessageCounter: result.MessageCounter,
		Usage:          result.TotalUsage,
		Err:            result.Err,
	})
}

func emitSessionStart(ctx context.Context, cfg Config) {
	if !cfg.Hooks.Has(hooks.SessionStart.Kind) {
		return
	}
	_, _ = hooks.SessionStart.Emit(ctx, cfg.Hooks, sessionEvent(cfg, ""))
}

func emitSessionEnd(ctx context.Context, cfg Config, reason string) {
	if !cfg.Hooks.Has(hooks.SessionEnd.Kind) {
		return
	}
	_, _ = hooks.SessionEnd.Emit(ctx, cfg.Hooks, sessionEvent(cfg, reason))
}

func sessionEvent(cfg Config, reason string) hooks.SessionEvent {
	return hooks.SessionEvent{
		SessionID: cfg.SessionID,
		ParentID:  cfg.ParentSessionID,
		AgentName: cfg.AgentName,
		Model:     cfg.Model,
		Reason:    reason,
	}
}
