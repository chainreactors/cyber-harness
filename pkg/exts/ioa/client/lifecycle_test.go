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

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/types"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
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
			connection := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://test-key@", 1), NodeName: "receiver", Space: "test", AutoRegister: true})
			adapter := NewCollaboration(CollaborationOptions{})
			registry := hooks.New()
			set, err := extension.New(extension.Provided[*hooks.Registry](registry), promptext.New(), extension.Provided(telemetry.NopLogger()), connection, adapter)
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
			if _, err := agenthooks.SessionStart.Emit(ctx, registry, agenthooks.SessionEvent{SessionID: "main", Primary: true, Deliver: func(_ context.Context, message inbox.Message) error { received <- message; return nil }}); err != nil {
				t.Fatal(err)
			}
			send(adapter.service.Client().NodeID(), "self")
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

func TestClientRecordsDuringDependentClose(t *testing.T) {
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "key")))
	defer server.Close()
	reg := hooks.New()
	connection := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://key@", 1), AutoRegister: true, Space: DefaultSpace})
	e := NewCollaboration(CollaborationOptions{})
	ev := agenthooks.SessionEvent{SessionID: "child", ParentID: "parent", ParentToolCallID: "call", AgentName: "worker", Input: "task", Delegation: &types.DelegationDetail{Task: "task"}}
	dependent := extension.Func{
		LoadFunc: func(scope *extension.Scope) error {
			_, err := agenthooks.SessionStart.Emit(scope.Init(), reg, ev)
			return err
		},
		CloseFunc: func(ctx context.Context) error {
			ev.Output = "finished during close"
			ev.Stop = agenthooks.StopReasonCanceled
			_, err := agenthooks.SessionEnd.Emit(ctx, reg, ev)
			return err
		},
	}
	set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NopLogger()), connection, e, dependent)
	if err != nil {
		t.Fatal(err)
	}
	if err = set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	space := e.Service().ReceiveSpace()
	if err = set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, err := store.GetMessages(space, "", 10)
	if err != nil || len(messages) != 2 || messages[1].Refs.Messages[0] != messages[0].ID {
		t.Fatalf("undrained: %v %v", messages, err)
	}
}

func TestRecordFailuresAreSynchronous(t *testing.T) {
	for _, phase := range []string{"delegate", "return"} {
		t.Run(phase, func(t *testing.T) {
			store := ioaserver.NewMemoryStore()
			defer store.Close()
			handler := ioaserver.NewHTTPHandler(ioaserver.NewService(store, "key"))
			var sends atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/messages") {
					n := sends.Add(1)
					if (phase == "delegate" && n == 1) || (phase == "return" && n == 2) {
						http.Error(w, "record unavailable", 503)
						return
					}
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			reg := hooks.New()
			connection := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://key@", 1), AutoRegister: true, Space: DefaultSpace})
			e := NewCollaboration(CollaborationOptions{})
			set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NopLogger()), connection, e)
			if err != nil {
				t.Fatal(err)
			}
			if err = set.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer set.Close(context.Background())
			ev := agenthooks.SessionEvent{SessionID: "child", Delegation: &types.DelegationDetail{Task: "task"}}
			_, startErr := agenthooks.SessionStart.Emit(t.Context(), reg, ev)
			if (phase == "delegate") != (startErr != nil) {
				t.Fatalf("start: %v", startErr)
			}
			ev.Output = "result"
			ev.Stop = agenthooks.StopReasonCompleted
			_, endErr := agenthooks.SessionEnd.Emit(t.Context(), reg, ev)
			if (phase == "return") != (endErr != nil) {
				t.Fatalf("end: %v", endErr)
			}
			e.mu.Lock()
			routes := len(e.routes)
			e.mu.Unlock()
			if routes != 0 {
				t.Fatal("failed record retained route")
			}
			if sends.Load() != map[string]int32{"delegate": 1, "return": 2}[phase] {
				t.Fatal("record silently retried")
			}
		})
	}
}

func TestClientCloseTimeoutRetainsResourceForRetry(t *testing.T) {
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	handler := ioaserver.NewHTTPHandler(ioaserver.NewService(store, "key"))
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/messages") {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	reg := hooks.New()
	connection := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://key@", 1), AutoRegister: true, Space: DefaultSpace})
	e := NewCollaboration(CollaborationOptions{})
	set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NopLogger()), connection, e)
	if err != nil {
		t.Fatal(err)
	}
	if err = set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := agenthooks.SessionStart.Emit(t.Context(), reg, agenthooks.SessionEvent{SessionID: "child", Delegation: &types.DelegationDetail{Task: "task"}})
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("record did not start")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err = set.Close(deadline); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("close: %v", err)
	}
	if err = e.Service().WaitReady(t.Context()); err != nil {
		t.Fatalf("resource released before hook drained: %v", err)
	}
	close(release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if err = set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
