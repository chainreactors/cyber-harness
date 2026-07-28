package webagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
	"github.com/gorilla/websocket"
)

type disconnectChatHandler struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (h *disconnectChatHandler) HandleProtocol(ctx context.Context, msg webproto.Message, _ func(webproto.Message)) bool {
	if msg.Type != webproto.TypeRun {
		return false
	}
	h.once.Do(func() { close(h.started) })
	go func() {
		<-ctx.Done()
		close(h.canceled)
	}()
	return true
}
func (*disconnectChatHandler) HandleUpload(webproto.Message, func(webproto.Message)) {}
func (*disconnectChatHandler) HandleConfigReload(string, func(webproto.Message))     {}

func TestConnectOnceCancelsChatWhenSocketDisconnects(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	handler := &disconnectChatHandler{started: make(chan struct{}), canceled: make(chan struct{})}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		var registration webproto.Message
		if err := conn.ReadJSON(&registration); err != nil {
			t.Errorf("read registration: %v", err)
			return
		}
		if err := conn.WriteJSON(webproto.Message{Type: "connected"}); err != nil {
			t.Errorf("write connected: %v", err)
			return
		}
		payload, _ := json.Marshal(webproto.RunPayload{SessionID: "chat-1"})
		if err := conn.WriteJSON(webproto.Message{Type: webproto.TypeRun, TurnID: "turn-1", Payload: payload}); err != nil {
			t.Errorf("write task: %v", err)
			return
		}
		select {
		case <-handler.started:
		case <-time.After(time.Second):
			t.Error("chat handler did not start")
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := connectOnce(ctx, connectionConfig{
		ServerURL: srv.URL,
		Name:      "worker",
		Registry:  commands.NewRegistry(),
		Chat:      handler,
		Node:      protocols.NodeRef{ID: "worker", Authority: srv.URL},
	}, telemetry.NopLogger())
	if err == nil {
		t.Fatal("connectOnce returned nil after socket disconnect")
	}

	select {
	case <-handler.canceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("chat context remained alive after socket disconnect")
	}
}
