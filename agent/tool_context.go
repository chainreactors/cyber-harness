package agent

import "context"

type toolAgentContextKey struct{}

func withToolAgentConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, toolAgentContextKey{}, cfg)
}

func toolAgentConfig(ctx context.Context) (Config, bool) {
	cfg, ok := ctx.Value(toolAgentContextKey{}).(Config)
	return cfg, ok
}
