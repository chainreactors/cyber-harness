package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/types"
	service "github.com/chainreactors/cyber/tools/ioa"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestRegistrationSpaceAndSubscriptionRecovery(t *testing.T) {
	for _, failurePath := range []string{"/auth/register", "/spaces"} {
		t.Run(failurePath, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			store := ioaserver.NewMemoryStore()
			defer store.Close()
			handler := ioaserver.NewHTTPHandler(ioaserver.NewService(store, "test-key"))
			var failed atomic.Bool
			var connections atomic.Int32
			subscribed := make(chan int32, 4)
			drop := make(chan struct{})
			feed := make(chan protocols.Message)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && r.URL.Path == failurePath && failed.CompareAndSwap(false, true) {
					http.Error(w, "temporary failure", http.StatusServiceUnavailable)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/sse") {
					number := connections.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					subscribed <- number
					var disconnect <-chan struct{}
					if number == 1 {
						disconnect = drop
					}
					for {
						select {
						case <-r.Context().Done():
							return
						case <-disconnect:
							return
						case message := <-feed:
							data, _ := json.Marshal(message)
							fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
							w.(http.Flusher).Flush()
						}
					}
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			received := make(chan inbox.Message, 4)
			adapter := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://test-key@", 1), NodeName: "receiver", Space: "test", AutoRegister: true}, Dependencies{
				Deliver: func(_ context.Context, message inbox.Message) error { received <- message; return nil },
			})
			set, err := extension.New(adapter)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close(context.Background())
			init, cancelInit := context.WithCancel(ctx)
			if err := set.Load(init); err != nil {
				t.Fatal(err)
			}
			cancelInit()
			waitSubscription := func(want int32) {
				t.Helper()
				select {
				case got := <-subscribed:
					if got != want {
						t.Fatalf("subscription %d, want %d", got, want)
					}
				case <-ctx.Done():
					t.Fatal("subscription did not recover")
				}
			}
			send := func(sender, id string) {
				t.Helper()
				select {
				case feed <- protocols.Message{ID: id, Sender: sender, Content: map[string]any{"text": id}}:
				case <-ctx.Done():
					t.Fatal("send timed out")
				}
			}
			receive := func(id string) {
				t.Helper()
				select {
				case message := <-received:
					if message.Origin != inbox.OriginPeer || message.Meta["message_id"] != id {
						t.Fatalf("unexpected peer message: %#v", message)
					}
				case <-ctx.Done():
					t.Fatal("peer message missing")
				}
			}
			waitSubscription(1)
			send(adapter.resource.Client().NodeID(), "self")
			send("peer", "first")
			receive("first")
			close(drop)
			waitSubscription(2)
			send("peer", "second")
			receive("second")
			if err := set.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestClientDrainsEventsEmittedByDependentClose(t *testing.T) {
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "test-key")))
	defer server.Close()
	stream := events.New()
	adapter := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://test-key@", 1), NodeName: "publisher", Space: "test", AutoRegister: true}, Dependencies{Events: stream})
	agent := extension.Func{CloseFunc: func(context.Context) error {
		start := &aop.Event{SessionId: "child", Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{ParentSessionId: "parent", ParentToolCallId: "spawn"}}}
		setDelegation(t, start)
		stream.Publish(start)
		stream.Publish(&aop.Event{SessionId: "child", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}})
		return nil
	}}
	set, err := extension.New(adapter, agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, err := store.GetMessages(adapter.Runtime().ReceiveSpace(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Refs.Messages[0] != messages[0].ID {
		t.Fatalf("handoffs were not drained: %#v", messages)
	}
}

func TestClientCloseTimeoutRetainsResourceForRetry(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/messages") {
			_, _ = io.Copy(io.Discard, r.Body)
			close(entered)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"space-1"}`)
	}))
	defer server.Close()
	stream := events.New()
	adapter := New(service.Config{URL: server.URL, NodeID: "node-1", Space: "test"}, Dependencies{Events: stream})
	set, err := extension.New(adapter)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	start := &aop.Event{SessionId: "child", Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{ParentToolCallId: "spawn"}}}
	setDelegation(t, start)
	stream.Publish(start)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("send did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := set.Close(ctx); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("close = %v", err)
	}
	if err := adapter.Runtime().WaitReady(t.Context()); err != nil {
		t.Fatalf("resource was released prematurely: %v", err)
	}
	// Cancellation can be reported as an ordinary completed-output error.
	if err := set.Close(t.Context()); errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("retry did not drain: %v", err)
	}
}

func setDelegation(t *testing.T, event *aop.Event) {
	t.Helper()
	if err := types.SetDelegation(event, &types.DelegationDetail{Task: "test", AgentName: "worker", RunMode: types.DelegationRunForeground}); err != nil {
		t.Fatal(err)
	}
}
