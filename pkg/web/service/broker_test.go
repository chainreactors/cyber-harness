package service

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	types "github.com/chainreactors/cyber/core/types"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	scanpb "github.com/chainreactors/cyber/pkg/web/scan"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRoutedChildEventsShareParentTimelineSequence(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "parent")
	svc := NewService(ServiceConfig{Store: store})
	defer svc.Close(context.Background())
	for i, origin := range []string{"parent", "child", "parent"} {
		svc.BroadcastAOPEvent("parent", &aop.Event{
			Id: strconv.Itoa(i), SessionId: origin, TurnId: "turn", Seq: 50,
			Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}},
		})
	}
	events, err := store.ListAOPEvents(t.Context(), "parent", 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%v, error=%v", events, err)
	}
	if events[0].Seq != 1 || events[1].Seq != 2 || events[1].SessionId != "child" {
		t.Fatalf("routed timeline lost ordering or origin: %v", events)
	}
	// A new service must continue the destination timeline even for a child origin.
	restarted := NewService(ServiceConfig{Store: store})
	defer restarted.Close(context.Background())
	restarted.BroadcastAOPEvent("parent", &aop.Event{Id: "after-restart", SessionId: "child",
		Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text("next")}}},
	})
	events, err = store.ListAOPEvents(t.Context(), "parent", 10)
	if err != nil || len(events) != 3 || events[2].Seq != 3 {
		t.Fatalf("restarted timeline=%v, error=%v", events, err)
	}
}

func TestWebTimelineConcurrentOrderingAndRestartDedup(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "timeline")
	svc := NewService(ServiceConfig{Store: store})
	defer svc.Close(context.Background())
	deliveries, stop := svc.hub.SubscribeAOP("timeline")
	defer stop()
	var writers sync.WaitGroup
	for i := 0; i < 20; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			event := &aop.Event{Id: strconv.Itoa(i), SessionId: "timeline", Seq: 900,
				Payload: &aop.Event_Message{Message: &aop.Message{Id: strconv.Itoa(i), Role: "assistant", Content: []*aop.Content{aop.Text("text")}}}}
			svc.BroadcastAOPEvent("timeline", event)
			if event.Seq != 900 {
				t.Error("source event mutated")
			}
		}()
	}
	writers.Wait()
	stored, err := store.ListAOPEvents(t.Context(), "timeline", 100)
	if err != nil || len(stored) != 20 {
		t.Fatalf("events=%d error=%v", len(stored), err)
	}
	for seq := uint64(1); seq <= 20; seq++ {
		select {
		case delivery := <-deliveries:
			if delivery.Event.Seq != seq {
				t.Fatalf("wire order=%d expected=%d", delivery.Event.Seq, seq)
			}
		default:
			t.Fatal("missing delivery")
		}
	}
	restarted := NewService(ServiceConfig{Store: store})
	defer restarted.Close(context.Background())
	replay, cancelReplay := restarted.hub.SubscribeAOP("timeline")
	defer cancelReplay()
	restarted.BroadcastAOPEvent("timeline", stored[0])
	select {
	case <-replay:
		t.Fatal("durable duplicate was republished after restart")
	default:
	}
	restarted.BroadcastAOPEvent("timeline", &aop.Event{Id: "new", SessionId: "timeline", Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text("new")}}}})
	if got := (<-replay).Event.Seq; got != 21 {
		t.Fatalf("restart sequence=%d", got)
	}
}

func TestFailedPersistenceDoesNotSealTurn(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "retry")
	svc := NewService(ServiceConfig{Store: store})
	defer svc.Close(context.Background())
	event := &aop.Event{Id: "terminal", SessionId: "retry", TurnId: "turn", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_event BEFORE INSERT ON chat_aop_events BEGIN SELECT RAISE(FAIL, 'storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.acceptAOPEvent("retry", proto.CloneOf(event)); err == nil {
		t.Fatal("persistence failure was hidden")
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_event`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.acceptAOPEvent("retry", proto.CloneOf(event)); err != nil {
		t.Fatal(err)
	}
	stored, err := store.ListAOPEvents(t.Context(), "retry", 10)
	if err != nil || len(stored) != 1 || stored[0].Seq != 1 {
		t.Fatalf("retry events=%v error=%v", stored, err)
	}
}

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
		hub.BroadcastScan(managementapi.ScanProgressEvent("scan-1", "progress"), false)
	}
	overflow := managementapi.ScanProgressEvent("scan-1", "overflow")
	hub.BroadcastScan(overflow, false)
	terminal := managementapi.ScanFailedEvent("scan-1", "failed", false)
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
	hub.BroadcastScan(managementapi.ScanProgressEvent("scan-1", "before-subscribe"), false)
	events, sequence, unsubscribe := hub.SubscribeScan("scan-1")
	defer unsubscribe()
	if sequence != 1 {
		t.Fatalf("subscription sequence = %d, want 1", sequence)
	}
	snapshot := managementapi.ScanSnapshot(&scanpb.Scan{Id: "scan-1"}, sequence)
	if snapshot.Sequence != sequence {
		t.Fatalf("snapshot sequence = %d, want %d", snapshot.Sequence, sequence)
	}
	hub.BroadcastScan(managementapi.ScanProgressEvent("scan-1", "after-subscribe"), false)
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
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	createStoredSession(t, store, "session-aop")
	event := &aop.Event{
		Id: "event-1", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)),
		SessionId: "session-aop", Emitter: "cyber", Seq: 7,
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "message-1", Role: "assistant", Content: []*aop.Content{aop.Text("hello")}}},
	}
	service.BroadcastAOPEvent("session-aop", event)

	events, err := store.ListAOPEvents(context.Background(), "session-aop", 100)
	if err != nil {
		t.Fatal(err)
	}
	expected := proto.CloneOf(event)
	expected.Seq = 1
	if event.Seq != 7 {
		t.Fatal("incoming timeline was mutated")
	}
	if len(events) != 1 || !proto.Equal(events[0], expected) {
		t.Fatalf("persisted events = %+v, want %+v", events, event)
	}
}

func TestBroadcastAOPEventDoesNotFanOutRetryWithSameEventID(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	createStoredSession(t, store, "session-retry")
	deliveries, unsubscribe := service.SubscribeSessionEvents("session-retry")
	defer unsubscribe()
	event := &aop.Event{
		Id: "event-retry", SessionId: "session-retry", Emitter: "cyber",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "message-1", Role: "assistant", Content: []*aop.Content{aop.Text("once")}}},
	}
	service.BroadcastAOPEvent("session-retry", event)
	service.BroadcastAOPEvent("session-retry", proto.Clone(event).(*aop.Event))

	select {
	case <-deliveries:
	case <-time.After(time.Second):
		t.Fatal("first event was not broadcast")
	}
	select {
	case duplicate := <-deliveries:
		t.Fatalf("duplicate event was broadcast: %+v", duplicate)
	case <-time.After(50 * time.Millisecond):
	}
	stored, err := store.ListAOPEvents(context.Background(), "session-retry", 10)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored events = %d, err = %v; want 1", len(stored), err)
	}
}

func TestEvalMetadataPersistsOnlyInAOP(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	createStoredSession(t, store, "session-eval")
	event := &aop.Event{
		Id: "event-1", EmittedAt: timestamppb.Now(), SessionId: "session-eval", TurnId: "turn-1", Emitter: "cyber",
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
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
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
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "session-scan")
	if err := store.Create(context.Background(), &scanpb.Scan{
		Id: "scan-123", Target: "127.0.0.1", Mode: "quick",
		Status: scanpb.ScanStatus_SCAN_STATUS_COMPLETED, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatal(err)
	}
	// The session binds the scan at open time; that durable link — not a
	// fabricated in-flight task — is what the completion fan-out resolves.
	if err := store.LinkScanToSession(context.Background(), "session-scan", "scan-123"); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: store})
	service.broadcastScanStatus("scan-123", scanpb.ScanStatus_SCAN_STATUS_COMPLETED)

	events, err := store.ListAOPEvents(context.Background(), "session-scan", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	extension := events[0].GetExtension()
	value := new(scanpb.SessionScanEvent)
	if extension == nil || !extension.MessageIs(value) {
		t.Fatalf("extension = %+v", extension)
	}
	if err := extension.UnmarshalTo(value); err != nil {
		t.Fatal(err)
	}
	if value.ScanId != "scan-123" || value.Status != scanpb.ScanStatus_SCAN_STATUS_COMPLETED {
		t.Fatalf("scan extension = %+v", value)
	}
	ids, err := store.SessionScanIDs(context.Background(), "session-scan")
	if err != nil || len(ids) != 1 || ids[0] != "scan-123" {
		t.Fatalf("session scan ids = %v, err = %v", ids, err)
	}
}

func TestScanCompleteWithoutSessionBindingEmitsNothing(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "session-unbound")
	service := NewService(ServiceConfig{Store: store})
	// An in-flight task id is not a scan binding. The completion fan-out resolves
	// the durable session_scans relation, so a stray task registration must not
	// leak a result card into a session the scan was never bound to.
	service.registerSessionTask("scan-stray", "session-unbound")
	service.broadcastScanStatus("scan-stray", scanpb.ScanStatus_SCAN_STATUS_COMPLETED)

	events, err := store.ListAOPEvents(context.Background(), "session-unbound", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestWatchScanEventsImmediatelyReturnsTerminalSnapshot(t *testing.T) {
	for _, status := range []scanpb.ScanStatus{scanpb.ScanStatus_SCAN_STATUS_COMPLETED, scanpb.ScanStatus_SCAN_STATUS_FAILED, scanpb.ScanStatus_SCAN_STATUS_CANCELED} {
		t.Run(scanStatusToDB(status), func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"), ScanSchema)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			scan := &scanpb.Scan{Id: "terminal-scan", Target: "127.0.0.1", Mode: "quick", Status: status, CreatedAt: nowProto(), UpdatedAt: nowProto()}
			if err := store.Create(context.Background(), scan); err != nil {
				t.Fatal(err)
			}
			service := NewService(ServiceConfig{Store: store, Scans: &ScanServiceConfig{}})
			var responses []*scanpb.ScanEvent
			err = service.api.Scans.WatchScanEvents(
				&scanpb.WatchScanEventsRequest{ScanId: scan.Id}, context.Background(),
				func(event *scanpb.ScanEvent) error {
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
