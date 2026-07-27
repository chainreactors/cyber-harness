package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

func waitScanStatus(t *testing.T, store *SQLiteStore, id string, want ScanStatus) *ScanJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := store.Get(context.Background(), id)
		if err == nil && job.Status == want {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, err := store.Get(context.Background(), id)
	t.Fatalf("scan %s status = %+v, err = %v; want %s", id, job, err, want)
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

	srv, _ := setupTestServerWithPool(t, pool)
	conn := dialAgent(t, srv, "scan-agent", []string{"scan"})
	t.Cleanup(func() { _ = conn.Close() })
	waitAgents(t, pool, 1)

	job, err := svc.SubmitScan(context.Background(), "127.0.0.1", "quick", false, false, false)
	if err != nil {
		t.Fatal(err)
	}

	var call webproto.Message
	if err := conn.ReadJSON(&call); err != nil {
		t.Fatal(err)
	}
	if call.Type != webproto.TypeAOP || call.TaskID != job.ID {
		t.Fatalf("scan dispatch = %+v", call)
	}
	waitScanStatus(t, store, job.ID, StatusRunning)

	if err := svc.CancelScan(job.ID); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var cancel webproto.Message
	if err := conn.ReadJSON(&cancel); err != nil {
		t.Fatalf("agent did not receive scan cancellation: %v", err)
	}
	if cancel.Type != "cancel" || cancel.TaskID != job.ID {
		t.Fatalf("cancel frame = %+v", cancel)
	}

	waitScanStatus(t, store, job.ID, StatusCanceled)
	deadline := time.Now().Add(time.Second)
	for len(svc.sem) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(svc.sem); got != 0 {
		t.Fatalf("scan concurrency slot still occupied after cancellation: %d", got)
	}

	// A result that races with cancellation must not resurrect the scan.
	resultJSON, _ := json.Marshal(&output.Result{})
	pool.handleAgentMessage(pool.Pick(), webproto.Message{
		Type: "complete", TaskID: job.ID, Payload: resultJSON,
	})
	time.Sleep(20 * time.Millisecond)
	if got, err := store.Get(context.Background(), job.ID); err != nil || got.Status != StatusCanceled {
		t.Fatalf("late result changed canceled scan: job=%+v err=%v", got, err)
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
	srv, _ := setupTestServerWithPool(t, pool)
	conn := dialAgent(t, srv, "queue-agent", []string{"scan"})
	t.Cleanup(func() { _ = conn.Close() })
	waitAgents(t, pool, 1)

	running, err := svc.SubmitScan(context.Background(), "127.0.0.1", "quick", false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var call webproto.Message
	if err := conn.ReadJSON(&call); err != nil {
		t.Fatal(err)
	}
	waitScanStatus(t, store, running.ID, StatusRunning)

	queued, err := svc.SubmitScan(context.Background(), "127.0.0.2", "quick", false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	waitScanStatus(t, store, queued.ID, StatusQueued)
	if err := svc.CancelScan(queued.ID); err != nil {
		t.Fatal(err)
	}
	waitScanStatus(t, store, queued.ID, StatusCanceled)

	if err := svc.CancelScan(running.ID); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var cancel webproto.Message
	if err := conn.ReadJSON(&cancel); err != nil {
		t.Fatal(err)
	}
	waitScanStatus(t, store, running.ID, StatusCanceled)
}

func TestRemoteScanTimeoutCancelsAgentAndFailsScan(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewService(ServiceConfig{Store: store, MaxConcurrent: 1, ScanTimeout: 50 * time.Millisecond})
	pool := NewAgentPool(svc.Hub())
	svc.SetAgentPool(pool)
	srv, _ := setupTestServerWithPool(t, pool)
	conn := dialAgent(t, srv, "timeout-agent", []string{"scan"})
	t.Cleanup(func() { _ = conn.Close() })
	waitAgents(t, pool, 1)

	job, err := svc.SubmitScan(context.Background(), "127.0.0.1", "quick", false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var call webproto.Message
	if err := conn.ReadJSON(&call); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var cancel webproto.Message
	if err := conn.ReadJSON(&cancel); err != nil {
		t.Fatalf("agent did not receive timeout cancellation: %v", err)
	}
	if cancel.Type != "cancel" || cancel.TaskID != job.ID {
		t.Fatalf("timeout cancel frame = %+v", cancel)
	}
	failed := waitScanStatus(t, store, job.ID, StatusFailed)
	if failed.Error != "scan timed out" {
		t.Fatalf("timeout error = %q", failed.Error)
	}
}

func setupTestServerWithPool(t *testing.T, pool *AgentPool) (*httptest.Server, *AgentPool) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/ws", pool.HandleWS)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, pool
}

func TestCancelTaskUsesControlChannelWhenTaskQueueIsFull(t *testing.T) {
	pool := NewAgentPool(NewHub())
	remote := newFakeAgent("agent-1", 1)
	remote.toolCalls = map[string]struct{}{"scan-1": {}}
	remote.tasks["scan-1"] = make(chan taskResult, 1)
	remote.sendCh <- webproto.Message{Type: "busy"}
	pool.agents[remote.id] = remote

	if err := pool.CancelTask(remote.id, "scan-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-remote.controlCh:
		if msg.Type != "cancel" || msg.TaskID != "scan-1" {
			t.Fatalf("control cancellation = %+v", msg)
		}
	default:
		t.Fatal("cancellation was not queued on the control channel")
	}
}

func TestDecodeScanResultRejectsInvalidEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "empty"},
		{name: "null", raw: json.RawMessage("null")},
		{name: "malformed", raw: json.RawMessage("{")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeScanResult(tc.raw); err == nil {
				t.Fatal("decodeScanResult() accepted an invalid result")
			}
		})
	}

	result, err := decodeScanResult(json.RawMessage("{}"))
	if err != nil || result == nil {
		t.Fatalf("decodeScanResult({}) = %+v, %v", result, err)
	}
}

func TestCompleteJobCannotOverwriteCanceledScan(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now()
	job := &ScanJob{ID: "scan-canceled", Target: "127.0.0.1", Mode: "quick", Status: StatusCanceled, CreatedAt: now, UpdatedAt: now}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	svc := NewService(ServiceConfig{Store: store})
	changed, err := svc.completeJob(context.Background(), job, "", &output.Result{})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("completeJob() completed a canceled scan")
	}
	stored, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusCanceled || strings.TrimSpace(stored.Report) != "" {
		t.Fatalf("canceled scan was mutated: %+v", stored)
	}
}

func TestCancelCompletedScanReturnsConflictAndPreservesStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now()
	job := &ScanJob{
		ID:        "scan-completed",
		Target:    "127.0.0.1",
		Mode:      "quick",
		Status:    StatusCompleted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(NewService(ServiceConfig{Store: store}), nil, nil, nil, nil, "")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/scans/"+job.ID, nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("DELETE completed scan status = %d, body = %s; want %d", recorder.Code, recorder.Body.String(), http.StatusConflict)
	}
	stored, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusCompleted {
		t.Fatalf("completed scan status = %s; want %s", stored.Status, StatusCompleted)
	}
}

func TestCancelMissingScanReturnsNotFound(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	handler := NewHandler(NewService(ServiceConfig{Store: store}), nil, nil, nil, nil, "")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/scans/missing", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing scan status = %d, body = %s; want %d", recorder.Code, recorder.Body.String(), http.StatusNotFound)
	}
}
