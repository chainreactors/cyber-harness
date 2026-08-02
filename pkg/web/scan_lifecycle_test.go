package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func waitScanStatus(t *testing.T, store *SQLiteStore, id string, want types.ScanStatus) *types.Scan {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		scan, err := store.Get(context.Background(), id)
		if err == nil && scan.Status == want {
			return scan
		}
		time.Sleep(10 * time.Millisecond)
	}
	scan, err := store.Get(context.Background(), id)
	t.Fatalf("scan %s status = %+v, err = %v; want %s", id, scan, err, want)
	return nil
}

func TestCancelRemoteScanStopsAgentAndPreservesCanceledStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(ServiceConfig{Store: store, MaxConcurrent: 1, ScanTimeout: time.Minute})
	pool := NewAgentPool(svc.Hub())
	svc.SetAgentPool(pool)

	srv, _ := setupTestServerWithPool(t, svc, pool)
	conn := dialAgent(t, srv, "scan-agent", []string{"scan"})
	t.Cleanup(func() { _ = conn.Close() })
	waitAgents(t, pool, 1)

	scan, err := svc.SubmitScan(context.Background(), "127.0.0.1", "quick", false, false, false)
	if err != nil {
		t.Fatal(err)
	}

	callEnvelope := readHubEnvelope(t, conn)
	if callEnvelope.GetId() != scan.Id {
		t.Fatalf("scan dispatch = %+v", callEnvelope)
	}
	if message := unwrapEnvelope(t, callEnvelope); message.(*toolpb.ProtocolMessage).GetCall() == nil {
		t.Fatalf("scan dispatch = %+v", message)
	}
	waitScanStatus(t, store, scan.Id, types.ScanStatus_SCAN_STATUS_RUNNING)

	if err := svc.CancelScan(scan.Id); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	cancel := unwrapEnvelope(t, readHubEnvelope(t, conn))
	if cancel.(*aop.ProtocolMessage).GetCancelOperation().GetTargetId() != scan.Id {
		t.Fatalf("cancel envelope = %+v", cancel)
	}

	waitScanStatus(t, store, scan.Id, types.ScanStatus_SCAN_STATUS_CANCELED)
	deadline := time.Now().Add(time.Second)
	for len(svc.sem) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(svc.sem); got != 0 {
		t.Fatalf("scan concurrency slot still occupied after cancellation: %d", got)
	}

	// A result that races with cancellation must not resurrect the scan.
	pool.handleAgentEnvelope(pool.Pick().(*remoteAgent), wrapMessage(t, generateID(), scan.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: &aop.Event{
		SessionId: scan.Id, TurnId: scan.Id, Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{CallId: scan.Id}},
	}}}))
	time.Sleep(20 * time.Millisecond)
	if got, err := store.Get(context.Background(), scan.Id); err != nil || got.Status != types.ScanStatus_SCAN_STATUS_CANCELED {
		t.Fatalf("late result changed canceled scan: scan=%+v err=%v", got, err)
	}
}

func TestCancelQueuedScanDoesNotWaitForConcurrencySlot(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(ServiceConfig{Store: store, MaxConcurrent: 1, ScanTimeout: time.Minute})
	pool := NewAgentPool(svc.Hub())
	svc.SetAgentPool(pool)
	srv, _ := setupTestServerWithPool(t, svc, pool)
	conn := dialAgent(t, srv, "queue-agent", []string{"scan"})
	t.Cleanup(func() { _ = conn.Close() })
	waitAgents(t, pool, 1)

	running, err := svc.SubmitScan(context.Background(), "127.0.0.1", "quick", false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = readHubEnvelope(t, conn)
	waitScanStatus(t, store, running.Id, types.ScanStatus_SCAN_STATUS_RUNNING)

	queued, err := svc.SubmitScan(context.Background(), "127.0.0.2", "quick", false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	waitScanStatus(t, store, queued.Id, types.ScanStatus_SCAN_STATUS_QUEUED)
	if err := svc.CancelScan(queued.Id); err != nil {
		t.Fatal(err)
	}
	waitScanStatus(t, store, queued.Id, types.ScanStatus_SCAN_STATUS_CANCELED)

	if err := svc.CancelScan(running.Id); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_ = readHubEnvelope(t, conn)
	waitScanStatus(t, store, running.Id, types.ScanStatus_SCAN_STATUS_CANCELED)
}

type controlledDeadlineContext struct {
	context.Context
	done chan struct{}
}

func newControlledDeadlineContext() *controlledDeadlineContext {
	return &controlledDeadlineContext{Context: context.Background(), done: make(chan struct{})}
}

func (c *controlledDeadlineContext) Done() <-chan struct{} { return c.done }

func (c *controlledDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *controlledDeadlineContext) expire() { close(c.done) }

func TestRemoteScanTimeoutCancelsAgentAndFailsScan(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(ServiceConfig{Store: store, MaxConcurrent: 1})
	pool := NewAgentPool(svc.Hub())
	svc.SetAgentPool(pool)
	agent := newFakeAgent("timeout-agent", 1)
	pool.register(agent)

	scan := &types.Scan{
		Id: "timeout-scan", Target: "127.0.0.1", Mode: "quick",
		Status: types.ScanStatus_SCAN_STATUS_RUNNING, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}
	scanID := scan.Id

	ctx := newControlledDeadlineContext()
	done := make(chan struct{})
	go func() {
		svc.runScanViaAgent(ctx, scan)
		close(done)
	}()

	var call *aop.Envelope
	select {
	case call = <-agent.sendCh:
	case <-time.After(time.Second):
		t.Fatal("agent did not receive scan dispatch")
	}
	if call.GetId() != scanID {
		t.Fatalf("scan dispatch = %+v", call)
	}

	ctx.expire()
	var cancel *aop.Envelope
	select {
	case cancel = <-agent.sendCh:
	case <-time.After(time.Second):
		t.Fatal("agent did not receive timeout cancellation")
	}
	cancelMessage, err := aop.Unwrap(cancel)
	if err != nil {
		t.Fatal(err)
	}
	if cancelMessage.(*aop.ProtocolMessage).GetCancelOperation().GetTargetId() != scanID {
		t.Fatalf("timeout cancel envelope = %+v", cancelMessage)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed-out remote scan did not return")
	}
	failed := waitScanStatus(t, store, scanID, types.ScanStatus_SCAN_STATUS_FAILED)
	if failed.Error != "scan timed out" {
		t.Fatalf("timeout error = %q", failed.Error)
	}
}

func TestRemoteScanExpiredBeforeDispatchFailsScan(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(ServiceConfig{Store: store, MaxConcurrent: 1})
	pool := NewAgentPool(svc.Hub())
	svc.SetAgentPool(pool)
	agent := newFakeAgent("timeout-agent", 1)
	pool.register(agent)

	scan := &types.Scan{
		Id: "expired-scan", Target: "127.0.0.1", Mode: "quick",
		Status: types.ScanStatus_SCAN_STATUS_RUNNING, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}
	ctx := newControlledDeadlineContext()
	ctx.expire()
	svc.runScanViaAgent(ctx, scan)

	failed := waitScanStatus(t, store, scan.Id, types.ScanStatus_SCAN_STATUS_FAILED)
	if failed.Error != "scan timed out" {
		t.Fatalf("timeout error = %q", failed.Error)
	}
	select {
	case msg := <-agent.sendCh:
		t.Fatalf("expired scan was dispatched: %+v", msg)
	default:
	}
}

func setupTestServerWithPool(t *testing.T, svc *Service, pool *AgentPool) (*httptest.Server, *AgentPool) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/aop/ws", func(w http.ResponseWriter, r *http.Request) {
		HandleAOPWebSocket(svc, pool, w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, pool
}

func TestCancelTaskQueuesBehindFullSendChannel(t *testing.T) {
	pool := NewAgentPool(NewHub())
	remote := newFakeAgent("agent-1", 1)
	remote.toolCalls = map[string]struct{}{"scan-1": {}}
	remote.tasks["scan-1"] = make(chan taskResult, 1)
	remote.sendCh <- aop.MustWrap("busy", "", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Request{Request: &types.ReloadRequest{}}}) // saturate the buffer
	pool.agents[remote.nodeID] = remote

	canceled := make(chan error, 1)
	go func() { canceled <- pool.CancelTask(remote.nodeID, "scan-1") }()
	select {
	case <-canceled:
		t.Fatal("cancellation bypassed the full send channel")
	case <-time.After(50 * time.Millisecond):
	}
	if first := <-remote.sendCh; first.GetId() != "busy" {
		t.Fatalf("first envelope = %+v", first)
	}
	if err := <-canceled; err != nil {
		t.Fatal(err)
	}
	select {
	case envelope := <-remote.sendCh:
		message, err := aop.Unwrap(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if message.(*aop.ProtocolMessage).GetCancelOperation().GetTargetId() != "scan-1" {
			t.Fatalf("queued cancellation = %+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation was not queued on the send channel")
	}
}

func TestCancelTaskWaitsForSaturatedSendChannel(t *testing.T) {
	pool := NewAgentPool(NewHub())
	remote := newFakeAgent("agent-1", 1)
	remote.toolCalls = map[string]struct{}{"scan-1": {}}
	resultCh := make(chan taskResult, 1)
	remote.tasks["scan-1"] = resultCh
	remote.sendCh <- aop.MustWrap("reload", "", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Request{Request: &types.ReloadRequest{}}})
	pool.agents[remote.nodeID] = remote

	canceled := make(chan error, 1)
	go func() { canceled <- pool.CancelTask(remote.nodeID, "scan-1") }()
	select {
	case _, ok := <-resultCh:
		if ok {
			t.Fatal("canceled result channel remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not converge the pending task")
	}

	<-remote.sendCh // drain the reload so the cancel can enqueue
	if err := <-canceled; err != nil {
		t.Fatal(err)
	}
	select {
	case envelope := <-remote.sendCh:
		message, err := aop.Unwrap(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if message.(*aop.ProtocolMessage).GetCancelOperation().GetTargetId() != "scan-1" {
			t.Fatalf("queued cancellation = %+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation was dropped under send-channel backpressure")
	}
}

func TestCompleteScanCannotOverwriteCanceledScan(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	scan := &types.Scan{Id: "scan-canceled", Target: "127.0.0.1", Mode: "quick", Status: types.ScanStatus_SCAN_STATUS_CANCELED, CreatedAt: nowProto(), UpdatedAt: nowProto()}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}

	svc := NewService(ServiceConfig{Store: store})
	changed, err := svc.completeScan(context.Background(), scan)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("completeScan() completed a canceled scan")
	}
	stored, err := store.Get(context.Background(), scan.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != types.ScanStatus_SCAN_STATUS_CANCELED || strings.TrimSpace(stored.Report) != "" {
		t.Fatalf("canceled scan was mutated: %+v", stored)
	}
}

func TestCancelCompletedScanReturnsConflictAndPreservesStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	scan := &types.Scan{
		Id:        "scan-completed",
		Target:    "127.0.0.1",
		Mode:      "quick",
		Status:    types.ScanStatus_SCAN_STATUS_COMPLETED,
		CreatedAt: nowProto(),
		UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}

	response, err := newScanServiceCore(NewService(ServiceConfig{Store: store})).CancelScan(context.Background(), &types.CancelScanRequest{
		RequestId: "cancel-completed", ScanId: scan.Id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetRejected().GetCode() != "FAILED_PRECONDITION" {
		t.Fatalf("CancelScan rejection = %+v; want FAILED_PRECONDITION", response.GetRejected())
	}
	stored, err := store.Get(context.Background(), scan.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != types.ScanStatus_SCAN_STATUS_COMPLETED {
		t.Fatalf("completed scan status = %s; want COMPLETED", stored.Status)
	}
}

func TestCancelMissingScanReturnsNotFound(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	response, err := newScanServiceCore(NewService(ServiceConfig{Store: store})).CancelScan(context.Background(), &types.CancelScanRequest{
		RequestId: "cancel-missing", ScanId: "missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetRejected().GetCode() != "NOT_FOUND" {
		t.Fatalf("CancelScan rejection = %+v; want NOT_FOUND", response.GetRejected())
	}
}
