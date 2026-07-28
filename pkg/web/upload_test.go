package web

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func newMultipartUploadRequest(t *testing.T, filename string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestReadMultipartUploadEnforcesExactFileLimit(t *testing.T) {
	for _, size := range []int{7, 8} {
		req := newMultipartUploadRequest(t, "note.txt", bytes.Repeat([]byte("x"), size))
		filename, data, err := readMultipartUpload(httptest.NewRecorder(), req, 8)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if filename != "note.txt" || len(data) != size {
			t.Fatalf("size %d: filename=%q bytes=%d", size, filename, len(data))
		}
	}

	req := newMultipartUploadRequest(t, "large.txt", bytes.Repeat([]byte("x"), 9))
	if _, _, err := readMultipartUpload(httptest.NewRecorder(), req, 8); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("size 9 error = %v, want ErrUploadTooLarge", err)
	}
}

func TestReadMultipartUploadRejectsMalformedBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/upload", bytes.NewBufferString("not multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
	if _, _, err := readMultipartUpload(httptest.NewRecorder(), req, 8); err == nil || errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("malformed multipart error = %v", err)
	}
}

func TestUploadReturnsNotFoundForMissingSession(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "upload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	handler := NewHandler(NewService(ServiceConfig{Store: store}), nil, nil, nil, nil, "")
	req := newMultipartUploadRequest(t, "note.txt", []byte("hello"))
	req.URL.Path = "/api/chat/sessions/missing/upload"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s; want 404", recorder.Code, recorder.Body.String())
	}
}

func TestHandleFileUploadCancellationRemovesPendingAgentTask(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "upload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "upload-session")
	session, err := store.GetSession(context.Background(), "upload-session")
	if err != nil {
		t.Fatal(err)
	}
	session.AgentID = "upload-agent"
	if _, err := store.db.Exec(`UPDATE chat_sessions SET agent_id = ? WHERE id = ?`, session.AgentID, session.ID); err != nil {
		t.Fatal(err)
	}

	pool := NewAgentPool(NewHub())
	remote := newFakeAgent(session.AgentID, 1)
	pool.register(remote)
	svc := NewService(ServiceConfig{Store: store, AgentPool: pool})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.HandleFileUpload(ctx, session.ID, "note.txt", []byte("hello"))
		done <- err
	}()

	var upload webproto.Message
	select {
	case upload = <-remote.sendCh:
	case <-time.After(time.Second):
		t.Fatal("upload was not dispatched")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("upload cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("upload did not return after request cancellation")
	}

	remote.mu.Lock()
	_, pending := remote.tasks[upload.TaskID]
	remote.mu.Unlock()
	if pending {
		t.Fatal("canceled upload remained in the agent task map")
	}
	select {
	case msg := <-remote.controlCh:
		if msg.Type != webproto.TypeRunCancel || msg.TurnID != upload.TaskID {
			t.Fatalf("upload cancel frame = %+v", msg)
		}
	default:
		t.Fatal("upload cancellation was not sent to the agent")
	}
}
