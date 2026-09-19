package api

import (
	"context"
	"strconv"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

const (
	maxArtifactUploadEvents = 100
	maxArtifactUploadBytes  = 50 << 20
)

type ArtifactStore interface {
	SyncArtifactEvents(context.Context, []*aop.Event, int64) ([]*aop.EventDelivery, error)
}

type Artifacts struct {
	store ArtifactStore
}

func NewArtifacts(store ArtifactStore) *Artifacts {
	return &Artifacts{store: store}
}

func (a *Artifacts) SyncArtifacts(ctx context.Context, request *types.SyncArtifactsRequest) (*types.SyncArtifactsResponse, error) {
	if a == nil || a.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "artifact store is unavailable")
	}
	if request == nil {
		request = new(types.SyncArtifactsRequest)
	}
	after, err := parseArtifactCursor(request.GetAfterCursor())
	if err != nil {
		return nil, err
	}
	if len(request.GetArtifacts()) > maxArtifactUploadEvents {
		return nil, Errorf(CodeResourceExhausted, "artifact upload exceeds %d events", maxArtifactUploadEvents)
	}
	total := 0
	for index, event := range request.GetArtifacts() {
		total += proto.Size(event)
		if total > maxArtifactUploadBytes {
			return nil, Errorf(CodeResourceExhausted, "artifact upload exceeds 50 MiB")
		}
		if event == nil || strings.TrimSpace(event.GetId()) == "" {
			return nil, Errorf(CodeInvalidArgument, "artifact event %d requires an id", index)
		}
		if event.GetEmittedAt() == nil || !event.GetEmittedAt().IsValid() {
			return nil, Errorf(CodeInvalidArgument, "artifact event %q has an invalid emitted_at", event.GetId())
		}
		if _, _, found, decodeErr := toolpb.FromEvent(event); decodeErr != nil {
			return nil, NewError(CodeInvalidArgument, decodeErr)
		} else if !found {
			return nil, Errorf(CodeInvalidArgument, "artifact event %q does not contain an aop.tool.Artifact payload", event.GetId())
		}
	}
	deliveries, err := a.store.SyncArtifactEvents(ctx, request.GetArtifacts(), after)
	if err != nil {
		return nil, err
	}
	return &types.SyncArtifactsResponse{Artifacts: deliveries}, nil
}

func parseArtifactCursor(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		return 0, Errorf(CodeInvalidArgument, "after_cursor must be a non-negative integer")
	}
	return cursor, nil
}
