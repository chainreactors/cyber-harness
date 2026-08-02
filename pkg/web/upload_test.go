package web

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
)

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
	session.Session.NodeUri = "upload-agent"
	if err := store.UpdateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}

	pool := NewAgentPool(NewHub())
	remote := newFakeAgent(session.GetSession().GetNodeUri(), 1)
	pool.register(remote)
	svc := NewService(ServiceConfig{Store: store, AgentPool: pool})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.HandleFileUpload(ctx, session.GetSession().GetId(), "note.txt", []byte("hello"))
		done <- err
	}()

	var upload *aop.Envelope
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

	message, err := aop.Unwrap(upload)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := message.(*filepb.ProtocolMessage); !ok {
		t.Fatalf("upload dispatch = %T, want file protocol message", message)
	}
	taskID := upload.GetId()
	remote.mu.Lock()
	_, pending := remote.tasks[taskID]
	remote.mu.Unlock()
	if pending {
		t.Fatal("canceled upload remained in the agent task map")
	}
	select {
	case envelope := <-remote.sendCh:
		message, err := aop.Unwrap(envelope)
		if err != nil {
			t.Fatal(err)
		}
		core, ok := message.(*aop.ProtocolMessage)
		if !ok || core.GetCancelTurnRequest().GetTurnId() != taskID {
			t.Fatalf("upload cancel envelope = %+v", message)
		}
	default:
		t.Fatal("upload cancellation was not sent to the agent")
	}
}
