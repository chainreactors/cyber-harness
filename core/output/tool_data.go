package output

import (
	"context"
	"time"
)

type toolCallIDKey struct{}
type resultSinkKey struct{}

func ContextWithCallID(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, toolCallIDKey{}, callID)
}

func CallIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(toolCallIDKey{}).(string); ok {
		return v
	}
	return ""
}

// ContextWithResultSink installs an optional structured-result receiver for a
// command execution. Transports can collect a result without bypassing the
// normal BashTool/CommandRegistry execution path.
func ContextWithResultSink(ctx context.Context, sink func(*Result)) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, resultSinkKey{}, sink)
}

// PublishResult delivers result to the receiver attached to ctx, if any.
func PublishResult(ctx context.Context, result *Result) {
	if result == nil || ctx == nil {
		return
	}
	if sink, ok := ctx.Value(resultSinkKey{}).(func(*Result)); ok && sink != nil {
		sink(result)
	}
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
)
