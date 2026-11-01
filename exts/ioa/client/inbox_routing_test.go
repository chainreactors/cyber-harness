package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	service "github.com/chainreactors/cyber/tools/ioa"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestRouterAdmissionFailureRemainsRetryable(t *testing.T) {
	var attempts atomic.Int32
	var admitted inbox.Message
	connection := New(service.Config{})
	adapter := NewCollaboration(CollaborationOptions{Receive: func(_ context.Context, message inbox.Message) error {
		if attempts.Add(1) == 1 {
			return inbox.ErrInboxFull
		}
		admitted = message
		return nil
	}})
	hosttest.Load(t, t.Context(), extension.Provided[*hooks.Registry](hooks.New()), promptext.New(),
		extension.Provided[telemetry.Logger](telemetry.NopLogger()), connection, adapter)
	if _, err := adapter.sessionStart(t.Context(), agenthooks.SessionEvent{SessionID: "receiver", Deliver: func(context.Context, inbox.Message) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	message := protocols.Message{ID: "retry-id", Sender: "peer", Content: map[string]any{"text": "work"},
		Meta: map[string]any{"target_session_id": "receiver", "source_session_id": "sender-session", "interrupt": true}}
	if err := adapter.deliver(t.Context(), message); !errors.Is(err, inbox.ErrInboxFull) {
		t.Fatalf("first admission=%v", err)
	}
	for range 2 {
		if err := adapter.deliver(t.Context(), message); err != nil {
			t.Fatal(err)
		}
	}
	if attempts.Load() != 2 || !admitted.Interrupt || admitted.Meta["message_id"] != message.ID || admitted.Meta["target_session_id"] != "receiver" {
		t.Fatalf("attempts=%d admitted=%#v", attempts.Load(), admitted)
	}
}

func TestRouterDrainsEventsBeforeReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	handler := ioaserver.NewHTTPHandler(ioaserver.NewService(store, "test"))
	var feeds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sse") {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if feeds.Add(1) != 1 {
			<-r.Context().Done()
			return
		}
		for i := range 12 {
			data, _ := json.Marshal(protocols.Message{ID: fmt.Sprintf("event-%02d", i), Sender: "external-monitor",
				Content: map[string]any{"text": "observation"}})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
		}
		// The SDK reports a decode error and closes both channels with the
		// preceding twelve events potentially still buffered.
		fmt.Fprint(w, "event: message\ndata: invalid-json\n\n")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	accepted := make(chan string, 12)
	connection := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://test@", 1), Space: "drain", AutoRegister: true})
	adapter := NewCollaboration(CollaborationOptions{Receive: func(_ context.Context, message inbox.Message) error {
		accepted <- message.Meta["message_id"].(string)
		return nil
	}})
	set := hosttest.Load(t, ctx, extension.Provided[*hooks.Registry](hooks.New()), promptext.New(),
		extension.Provided[telemetry.Logger](telemetry.NopLogger()), connection, adapter)
	defer set.Close(context.Background())
	for i := range 12 {
		select {
		case got := <-accepted:
			if want := fmt.Sprintf("event-%02d", i); got != want {
				t.Fatalf("stream order=%q want=%q", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("buffered event %d lost on stream closure", i)
		}
	}
}

func TestSubscriptionRetriesRouterBackpressure(t *testing.T) {
	for _, cancelPending := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover", true: "shutdown"}[cancelPending], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			var attempts atomic.Int32
			entered := make(chan struct{}, 1)
			accepted := make(chan string, 2)
			connection := New(service.Config{Space: "backpressure", AutoRegister: true})
			adapter := NewCollaboration(CollaborationOptions{Receive: func(_ context.Context, message inbox.Message) error {
				if attempts.Add(1) == 1 || cancelPending {
					select {
					case entered <- struct{}{}:
					default:
					}
					return inbox.ErrInboxFull
				}
				accepted <- message.Meta["message_id"].(string)
				return nil
			}})
			registry := hooks.New()
			set := hosttest.Load(t, ctx, extension.Provided[*hooks.Registry](registry), promptext.New(),
				extension.Provided[telemetry.Logger](telemetry.NopLogger()), connection, adapter)
			if _, err := agenthooks.SessionStart.Emit(ctx, registry, agenthooks.SessionEvent{SessionID: "receiver",
				Deliver: func(context.Context, inbox.Message) error { return nil }}); err != nil {
				t.Fatal(err)
			}
			space, err := connection.Service().ReadySpace(ctx)
			if err != nil {
				t.Fatal(err)
			}
			client := connection.Service().Client()
			var ids []string
			for range 2 {
				message, err := client.Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": "monitor event"},
					Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}, Meta: map[string]any{"target_session_id": "receiver"}})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, message.ID)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("subscription never attempted admission")
			}
			if !cancelPending {
				for _, id := range ids {
					select {
					case got := <-accepted:
						if got != id {
							t.Fatalf("admission order=%s want=%s", got, id)
						}
					case <-ctx.Done():
						t.Fatal("event lost after router backpressure")
					}
				}
				if attempts.Load() != 3 {
					t.Fatalf("attempts=%d want=3", attempts.Load())
				}
			}
			closeCtx, stop := context.WithTimeout(ctx, 500*time.Millisecond)
			defer stop()
			if err := set.Close(closeCtx); err != nil {
				t.Fatalf("shutdown blocked on admission retry: %v", err)
			}
		})
	}
}
