package api

import (
	"context"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type recordingArtifactArchive struct {
	after  int64
	events []*aop.Event
}

func (a *recordingArtifactArchive) SyncArtifactEvents(_ context.Context, events []*aop.Event, after int64) ([]*aop.EventDelivery, error) {
	a.after = after
	a.events = events
	return []*aop.EventDelivery{{Cursor: "8", Event: events[0]}}, nil
}

func TestArtifactsSyncsRawEventsAfterCursor(t *testing.T) {
	archive := new(recordingArtifactArchive)
	event := apiArtifactEvent(t, "artifact-1")
	response, err := NewArtifacts(archive).SyncArtifacts(t.Context(), &types.SyncArtifactsRequest{
		AfterCursor: "7",
		Artifacts:   []*aop.Event{event},
	})
	if err != nil {
		t.Fatal(err)
	}
	if archive.after != 7 || len(archive.events) != 1 || archive.events[0] != event {
		t.Fatalf("archive input = after:%d events:%+v", archive.after, archive.events)
	}
	if len(response.GetArtifacts()) != 1 || response.GetArtifacts()[0].GetCursor() != "8" {
		t.Fatalf("response = %+v", response)
	}
}

func TestArtifactsRejectsInvalidCursorAndPayload(t *testing.T) {
	service := NewArtifacts(new(recordingArtifactArchive))
	for name, request := range map[string]*types.SyncArtifactsRequest{
		"cursor":  {AfterCursor: "-1"},
		"payload": {Artifacts: []*aop.Event{{Id: "not-artifact", EmittedAt: timestamppb.Now()}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.SyncArtifacts(t.Context(), request)
			if ErrorCode(err) != CodeInvalidArgument {
				t.Fatalf("error = %v, code = %s", err, ErrorCode(err))
			}
		})
	}
}

func TestArtifactsLimitsUploadCount(t *testing.T) {
	_, err := NewArtifacts(new(recordingArtifactArchive)).SyncArtifacts(t.Context(), &types.SyncArtifactsRequest{
		Artifacts: make([]*aop.Event, maxArtifactUploadEvents+1),
	})
	if ErrorCode(err) != CodeResourceExhausted {
		t.Fatalf("error = %v, code = %s", err, ErrorCode(err))
	}
}

func apiArtifactEvent(t *testing.T, id string) *aop.Event {
	t.Helper()
	payload, err := anypb.New(&toolpb.Artifact{Tool: "gogo", Kind: toolpb.ArtifactKindService, Data: []byte(`{"ip":"127.0.0.1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return &aop.Event{Id: id, EmittedAt: timestamppb.Now(), Payload: &aop.Event_Extension{Extension: payload}}
}
