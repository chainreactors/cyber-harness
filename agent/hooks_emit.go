package agent

import (
	"context"

	"github.com/chainreactors/cyber/agent/hooks"
	aop "github.com/chainreactors/cyber/aop"
)

// The kernel reaches the typed hook registry only through these helpers. Each
// helper preserves the zero-handler fast path exposed by hooks.Registry.

func runStartHook(ctx context.Context, cfg Config, systemPrompt string) (string, []*aop.Message) {
	if !hooks.BeforeRun.Has(cfg.Hooks) {
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
		names = append(names, definition.Name)
	}
	return names
}

func transformContextHook(ctx context.Context, cfg Config, messages []*aop.Message, turn int) []*aop.Message {
	if !hooks.Context.Has(cfg.Hooks) {
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

func compactCanceled(ctx context.Context, cfg Config, trigger string, contextTokens int) (bool, string) {
	if !hooks.BeforeCompact.Has(cfg.Hooks) {
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
	if result == nil || !hooks.RunEnd.Has(cfg.Hooks) {
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
