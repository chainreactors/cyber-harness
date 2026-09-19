package hooks

import (
	"context"
	"log/slog"
)

// Notify dispatches a pure notification. A failing observer is diagnosed but
// cannot turn the already observed operation into a different result.
func Notify[E any](ctx context.Context, r *Registry, point Point[E, struct{}], event E) {
	if _, err := point.Emit(ctx, r, event); err != nil {
		slog.WarnContext(ctx, "hook notification failed", "kind", point.Name(), "error", err)
	}
}
