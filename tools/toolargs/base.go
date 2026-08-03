package toolargs

import (
	"context"
	"encoding/json"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/telemetry"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Base struct {
	Logger  telemetry.Logger
	Proxy   string
	WorkDir string
	Events  aop.EventEmitter
}

func (b *Base) SetWorkDir(dir string) { b.WorkDir = dir }
func (b *Base) SetProxy(proxy string) { b.Proxy = proxy }

func (b *Base) InitLogger(logger telemetry.Logger) {
	if logger != nil {
		b.Logger = logger
	} else {
		b.Logger = telemetry.NopLogger()
	}
}

func (b *Base) EmitArtifactCtx(ctx context.Context, tool, kind, target string, data any) {
	if b.Events == nil || data == nil {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		b.Logger.Warnf("marshal %s artifact: %s", tool, err)
		return
	}
	invocation := coretool.InvocationFromContext(ctx)
	artifact := &toolpb.Artifact{
		Tool: tool, Kind: kind, Target: target, Data: raw,
		MediaType: aop.JSONMediaType, Timestamp: timestamppb.New(time.Now()), CallId: invocation.CallID,
	}
	extension, err := anypb.New(artifact)
	if err != nil {
		b.Logger.Warnf("encode %s artifact: %s", tool, err)
		return
	}
	emitter := invocation.Emitter
	if emitter == "" {
		emitter = tool
	}
	b.Events.Emit(&aop.Event{
		SessionId: invocation.SessionID, TurnId: invocation.TurnID, Emitter: emitter,
		Payload: &aop.Event_Extension{Extension: extension},
	})
}
