package commands

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
)

type inboxContextKey struct{}

// ContextWithInbox scopes asynchronous command notifications to the agent
// session that invoked the tool.
func ContextWithInbox(ctx context.Context, ib inbox.Inbox) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, inboxContextKey{}, ib)
}

func inboxFromContext(ctx context.Context) inbox.Inbox {
	if ctx != nil {
		if ib, ok := ctx.Value(inboxContextKey{}).(inbox.Inbox); ok && ib != nil {
			return ib
		}
	}
	return nil
}
