package inbox

import "context"

type contextKey struct{}

// ContextWithInbox scopes asynchronous command notifications to the agent
// session that invoked a tool.
func ContextWithInbox(ctx context.Context, ib Inbox) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, ib)
}

// FromContext returns the session inbox attached to ctx, if any.
func FromContext(ctx context.Context) Inbox {
	if ctx == nil {
		return nil
	}
	ib, _ := ctx.Value(contextKey{}).(Inbox)
	return ib
}
