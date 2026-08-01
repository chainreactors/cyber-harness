package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	chatpb "github.com/chainreactors/aiscan/aop/aiscan/chat"
	"github.com/chainreactors/aiscan/aop/aiscan/chat/chatconnect"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
)

func TestUploadConnectRPCRejectsMissingSession(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "upload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := httptest.NewServer(NewHandler(NewService(ServiceConfig{Store: store}), nil, nil, nil, nil, ""))
	defer server.Close()
	client := chatconnect.NewSessionServiceClient(server.Client(), server.URL, connect.WithProtoJSON())
	response, err := client.UploadSessionFile(context.Background(), connect.NewRequest(&chatpb.UploadSessionFileRequest{
		RequestId: "upload-1", SessionId: "missing", Filename: "note.txt", Data: []byte("hello"),
	}))
	if err != nil || response.Msg.GetRejected().GetCode() != "NOT_FOUND" {
		t.Fatalf("UploadSessionFile = %v, %v", response, err)
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

	var upload *transport.ServerFrame
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
	taskID := upload.GetFileUpload().GetTaskId()
	_, pending := remote.tasks[taskID]
	remote.mu.Unlock()
	if pending {
		t.Fatal("canceled upload remained in the agent task map")
	}
	select {
	case msg := <-remote.controlCh:
		if msg.GetCancelTurn().GetTurnId() != taskID {
			t.Fatalf("upload cancel frame = %+v", msg)
		}
	default:
		t.Fatal("upload cancellation was not sent to the agent")
	}
}
