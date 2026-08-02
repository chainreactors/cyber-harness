package web

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHubBroadcastAOPReliableSurvivesBackpressure(t *testing.T) {
	hub := NewHub()
	deliveries, unsubscribe := hub.SubscribeAOP("session-1")
	defer unsubscribe()

	for i := int64(1); i <= 64; i++ {
		hub.BroadcastAOP("session-1", &aop.EventDelivery{Cursor: strconv.FormatInt(i, 10), Event: &aop.Event{
			SessionId: "session-1", Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{}},
		}}, false)
	}
	hub.BroadcastAOP("session-1", &aop.EventDelivery{Cursor: "999", Event: &aop.Event{
		SessionId: "session-1", Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{}},
	}}, false)
	hub.BroadcastAOP("session-1", &aop.EventDelivery{Cursor: "1000", Event: &aop.Event{
		SessionId: "session-1", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}},
	}}, true)

	var sawTerminal, sawOverflow bool
	for len(deliveries) > 0 {
		delivery := <-deliveries
		sawTerminal = sawTerminal || delivery.Cursor == "1000"
		sawOverflow = sawOverflow || delivery.Cursor == "999"
	}
	if !sawTerminal {
		t.Fatal("reliable terminal AOP event was dropped")
	}
	if sawOverflow {
		t.Fatal("droppable AOP event displaced buffered data")
	}
}

func TestHubBroadcastScanReliableSurvivesBackpressure(t *testing.T) {
	hub := NewHub()
	events, _, unsubscribe := hub.SubscribeScan("scan-1")
	defer unsubscribe()

	for i := 0; i < 64; i++ {
		hub.BroadcastScan(scanProgressEvent("scan-1", "progress"), false)
	}
	overflow := scanProgressEvent("scan-1", "overflow")
	hub.BroadcastScan(overflow, false)
	terminal := scanFailedEvent("scan-1", "failed", false)
	hub.BroadcastScan(terminal, true)

	var sawTerminal, sawOverflow bool
	for len(events) > 0 {
		event := <-events
		sawTerminal = sawTerminal || event.GetFailed() != nil
		sawOverflow = sawOverflow || event.GetProgress().GetData() == "overflow"
	}
	if !sawTerminal {
		t.Fatal("reliable terminal scan event was dropped")
	}
	if sawOverflow {
		t.Fatal("droppable scan event displaced buffered data")
	}
}

func TestScanSubscriptionReturnsSnapshotSequenceBoundary(t *testing.T) {
	hub := NewHub()
	hub.BroadcastScan(scanProgressEvent("scan-1", "before-subscribe"), false)
	events, sequence, unsubscribe := hub.SubscribeScan("scan-1")
	defer unsubscribe()
	if sequence != 1 {
		t.Fatalf("subscription sequence = %d, want 1", sequence)
	}
	snapshot := scanSnapshot(&types.Scan{Id: "scan-1"}, sequence)
	if snapshot.Sequence != sequence {
		t.Fatalf("snapshot sequence = %d, want %d", snapshot.Sequence, sequence)
	}
	hub.BroadcastScan(scanProgressEvent("scan-1", "after-subscribe"), false)
	select {
	case event := <-events:
		if event.Sequence <= snapshot.Sequence {
			t.Fatalf("live sequence = %d, snapshot = %d", event.Sequence, snapshot.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("missing live scan event")
	}
}

func TestBroadcastAOPEventPersistsCanonicalProtoJSON(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	createStoredSession(t, store, "session-aop")
	event := &aop.Event{
		Id: "event-1", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)),
		SessionId: "session-aop", Emitter: "aiscan", Seq: 7,
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "message-1", Role: "assistant", Content: []*aop.Content{aop.Text("hello")}}},
	}
	service.BroadcastAOPEvent("session-aop", event)

	events, err := store.ListAOPEvents(context.Background(), "session-aop", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !proto.Equal(events[0], event) {
		t.Fatalf("persisted events = %+v, want %+v", events, event)
	}
}

func TestEvalMetadataPersistsOnlyInAOP(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	createStoredSession(t, store, "session-eval")
	event := &aop.Event{
		Id: "event-1", EmittedAt: timestamppb.Now(), SessionId: "session-eval", TurnId: "turn-1", Emitter: "aiscan",
		Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}},
	}
	_ = types.SetEvalDetail(event, &types.EvalDetail{Round: 2, Reason: "needs verification"})
	service.BroadcastAOPEvent("session-eval", event)
	events, err := store.ListAOPEvents(context.Background(), "session-eval", 100)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	detail, ok, err := types.GetEvalDetail(events[0])
	if err != nil || !ok || detail.Round != 2 || detail.Reason != "needs verification" {
		t.Fatalf("eval detail = %+v, ok = %v, err = %v", detail, ok, err)
	}
}

func TestServerGeneratedAOPEventContinuesStoredSessionSequence(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "session-seq")
	if err := store.AddAOPEvent(context.Background(), "session-seq", &aop.Event{
		Id: "agent-7", EmittedAt: timestamppb.Now(), SessionId: "session-seq", Emitter: "agent", Seq: 7,
		Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}},
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: store})
	service.broadcastHubError("session-seq", "failed", "failed", nil)
	events, err := store.ListAOPEvents(context.Background(), "session-seq", 10)
	if err != nil || len(events) != 2 || events[1].Seq != 8 || events[1].GetError() == nil {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestScanCompletePersistsTypedAOPExtension(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "session-scan")
	if err := store.Create(context.Background(), &types.Scan{
		Id: "scan-123", Target: "127.0.0.1", Mode: "quick",
		Status: types.ScanStatus_SCAN_STATUS_COMPLETED, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: store})
	service.registerSessionTask("scan-123", "session-scan", "")
	service.broadcastScanComplete("scan-123")

	events, err := store.ListAOPEvents(context.Background(), "session-scan", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	extension := events[0].GetExtension()
	value := new(types.SessionScanEvent)
	if extension == nil || !extension.MessageIs(value) {
		t.Fatalf("extension = %+v", extension)
	}
	if err := extension.UnmarshalTo(value); err != nil {
		t.Fatal(err)
	}
	if value.ScanId != "scan-123" || value.Status != types.ScanStatus_SCAN_STATUS_COMPLETED {
		t.Fatalf("scan extension = %+v", value)
	}
	ids, err := store.SessionScanIDs(context.Background(), "session-scan")
	if err != nil || len(ids) != 1 || ids[0] != "scan-123" {
		t.Fatalf("session scan ids = %v, err = %v", ids, err)
	}
}

func TestWatchScanEventsImmediatelyReturnsTerminalSnapshot(t *testing.T) {
	for _, status := range []types.ScanStatus{types.ScanStatus_SCAN_STATUS_COMPLETED, types.ScanStatus_SCAN_STATUS_FAILED, types.ScanStatus_SCAN_STATUS_CANCELED} {
		t.Run(scanStatusToDB(status), func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			scan := &types.Scan{Id: "terminal-scan", Target: "127.0.0.1", Mode: "quick", Status: status, CreatedAt: nowProto(), UpdatedAt: nowProto()}
			if err := store.Create(context.Background(), scan); err != nil {
				t.Fatal(err)
			}
			service := NewService(ServiceConfig{Store: store})
			var responses []*types.ScanEvent
			err = service.api.Scans.WatchScanEvents(
				&types.WatchScanEventsRequest{ScanId: scan.Id}, context.Background(),
				func(event *types.ScanEvent) error {
					responses = append(responses, event)
					return nil
				},
			)
			if err != nil || len(responses) != 1 || responses[0].GetSnapshot().GetId() != scan.Id {
				t.Fatalf("responses = %+v, err = %v", responses, err)
			}
		})
	}
}
