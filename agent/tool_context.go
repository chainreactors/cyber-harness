package agent

import "context"

type toolAgentContextKey struct{}

func ContextWithToolAgentConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, toolAgentContextKey{}, cfg)
}

func ToolAgentConfig(ctx context.Context) (Config, bool) {
	cfg, ok := ctx.Value(toolAgentContextKey{}).(Config)
	return cfg, ok
}
