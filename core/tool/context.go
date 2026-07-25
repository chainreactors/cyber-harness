package tool

import "context"

type invocationContextKey struct{}

// Invocation carries executor-owned context that must not become model-facing
// tool arguments.
type Invocation struct {
	WorkDir string
}

func ContextWithInvocation(ctx context.Context, invocation Invocation) context.Context {
	return context.WithValue(ctx, invocationContextKey{}, invocation)
}

func InvocationFromContext(ctx context.Context) Invocation {
	if ctx == nil {
		return Invocation{}
	}
	invocation, _ := ctx.Value(invocationContextKey{}).(Invocation)
	return invocation
}

func WorkDirFromContext(ctx context.Context, fallback string) string {
	if workDir := InvocationFromContext(ctx).WorkDir; workDir != "" {
		return workDir
	}
	return fallback
}
