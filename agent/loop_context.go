package agent

import "context"

type loopSchedulerContextKey struct{}

// ContextWithLoopScheduler scopes direct command execution to one runtime
// session. Agent tool calls carry the scheduler in their Config snapshot.
func ContextWithLoopScheduler(ctx context.Context, scheduler *LoopScheduler) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, loopSchedulerContextKey{}, scheduler)
}

// LoopSchedulerFromContext resolves both direct-command and agent-tool-call
// contexts without exposing the agent's full Config.
func LoopSchedulerFromContext(ctx context.Context) *LoopScheduler {
	if ctx == nil {
		return nil
	}
	if scheduler, _ := ctx.Value(loopSchedulerContextKey{}).(*LoopScheduler); scheduler != nil {
		return scheduler
	}
	if cfg, ok := toolAgentConfig(ctx); ok {
		return cfg.LoopScheduler
	}
	return nil
}
