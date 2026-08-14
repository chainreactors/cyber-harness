package toolargs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	b.EmitArtifactResultCtx(ctx, ArtifactResultID(tool, kind, target, data), tool, kind, target, data)
}

// ArtifactResultID returns a stable identity for one scanner-native record.
// The same value is carried by its aop.tool.Loot marker.
func ArtifactResultID(tool, kind, target string, data any) string {
	raw, _ := json.Marshal(data)
	digest := sha256.Sum256(append([]byte(tool+"\x00"+kind+"\x00"+target+"\x00"), raw...))
	return fmt.Sprintf("%x", digest[:16])
}

func (b *Base) EmitArtifactResultCtx(ctx context.Context, resultID, tool, kind, target string, data any) {
	if b.Events == nil || data == nil {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		b.Logger.Warnf("marshal %s artifact: %s", tool, err)
		return
	}
	// One artifact event is one control-plane frame. A record that outgrows the
	// frame is trimmed to fit here, at the sole point every tool's artifact is
	// built, rather than dropped whole by a frame guard downstream. resultID stays
	// keyed on the untrimmed data above, so a record's identity is stable whether
	// or not its transport representation had to shed bulk.
	if bounded := boundArtifactData(raw); len(bounded) < len(raw) {
		b.Logger.Warnf("artifact budget: %s %s for %.200s trimmed %d → %d bytes", tool, kind, target, len(raw), len(bounded))
		raw = bounded
	}
	invocation := coretool.InvocationFromContext(ctx)
	artifact := &toolpb.Artifact{
		Tool: tool, Kind: kind, Target: target, Data: raw,
		MediaType: aop.JSONMediaType, Timestamp: timestamppb.New(time.Now()), CallId: invocation.CallID,
		ResultId: resultID,
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

func (b *Base) EmitLootCtx(
	ctx context.Context,
	resultID, tool, kind, target, priority, description, verificationStatus string,
	tags []string,
) {
	if b.Events == nil || resultID == "" {
		return
	}
	invocation := coretool.InvocationFromContext(ctx)
	loot := &toolpb.Loot{
		ResultId:           resultID,
		Tool:               tool,
		Kind:               kind,
		Target:             target,
		Priority:           priority,
		Tags:               append([]string(nil), tags...),
		Description:        description,
		VerificationStatus: verificationStatus,
		CallId:             invocation.CallID,
	}
	extension, err := anypb.New(loot)
	if err != nil {
		b.Logger.Warnf("encode %s loot: %s", tool, err)
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
