package output

import (
	"context"
	"time"
)

type toolCallIDKey struct{}

func ContextWithCallID(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, toolCallIDKey{}, callID)
}

func CallIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(toolCallIDKey{}).(string); ok {
		return v
	}
	return ""
}

type ToolDataEvent struct {
	Tool      string    `json:"tool"`
	Kind      string    `json:"kind"`
	Target    string    `json:"target,omitempty"`
	Data      any       `json:"data"`
	CallID    string    `json:"call_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

const (
	ToolDataService  = "service"
	ToolDataWeb      = "web"
	ToolDataWeakpass = "weakpass"
	ToolDataVuln     = "vuln"
	// ToolDataProgress carries one raw stdout/stderr line of a foreground
	// command, streamed while it runs.
	ToolDataProgress = "progress"
)
