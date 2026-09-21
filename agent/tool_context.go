package agent

import "context"

type toolAgentContextKey struct{}

func ContextWithToolAgentConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, toolAgentContextKey{}, cfg)
}

func ToolAgentConfig(ctx context.Context) (Config, bool) {
	if ctx == nil {
		return Config{}, false
	}
	cfg, ok := ctx.Value(toolAgentContextKey{}).(Config)
	return cfg, ok
}

// LoopSchedulerFromContext reads the same execution snapshot for direct
// commands and model tool calls. A child never falls back to its parent's scheduler.
func LoopSchedulerFromContext(ctx context.Context) *LoopScheduler {
	cfg, _ := ToolAgentConfig(ctx)
	return cfg.LoopScheduler
}
