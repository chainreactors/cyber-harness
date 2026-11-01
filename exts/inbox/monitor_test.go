package inbox_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	peerclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestContinuousMonitorIOARouting(t *testing.T) {
	interval := 100 * time.Millisecond
	if value := os.Getenv("CYBER_INBOX_MONITOR_INTERVAL"); value != "" {
		var err error
		interval, err = time.ParseDuration(value)
		if err != nil || interval <= 0 {
			t.Fatal("CYBER_INBOX_MONITOR_INTERVAL must be a positive duration")
		}
	}
	model := modelFunc(func(_ context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if request.Messages[len(request.Messages)-1].Role == "tool" {
			return response("routed"), nil
		}
		input := lastInput(request)
		switch {
		case strings.Contains(input, "investigate the payment retry race"):
			return response("", "investigator"), nil
		case strings.Contains(input, "documentation migration guide"):
			return response("", "writer"), nil
		case strings.Contains(input, "Investigation result"):
			return response("", "coordinator"), nil
		default:
			return nil, fmt.Errorf("unexpected routing input")
		}
	})
	continuousMonitorScenario(t, model, interval)
}

// Notify only after IOA has installed its subscription and flushed headers.
type readyStreamWriter struct {
	http.ResponseWriter
	ready chan struct{}
}

func (w *readyStreamWriter) Flush() {
	w.ResponseWriter.(http.Flusher).Flush()
	if w.ready != nil {
		close(w.ready)
		w.ready = nil
	}
}

// Both variants exercise the actual HTTP IOA server, SDK subscription, router
// Agent, tool dispatch, and Session mailboxes. Only the routing provider varies.
func continuousMonitorScenario(t *testing.T, routingModel provider.Provider, interval time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	started := time.Now()
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	handler := ioaserver.NewHTTPHandler(ioaserver.NewService(store, "monitor-test"))
	drop := make(chan struct{})
	var connections atomic.Int32
	reconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sse") {
			switch connections.Add(1) {
			case 1:
				feedCtx, stop := context.WithCancel(r.Context())
				defer stop()
				go func() {
					select {
					case <-drop:
						stop()
					case <-feedCtx.Done():
					}
				}()
				r = r.WithContext(feedCtx)
			case 2:
				w = &readyStreamWriter{ResponseWriter: w, ready: reconnected}
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://monitor-test@", 1)
	connection := ioaclient.New(ioatools.Config{URL: endpoint, NodeName: "router-node", Space: "monitoring", AutoRegister: true})
	router := inboxext.New(inboxext.Config{Instructions: "The investigator investigates source defects. The writer prepares documentation. The coordinator integrates investigation results. Monitoring events about the payment retry race belong to the investigator; route follow-up events to that same investigator."})
	adapter := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: router.Router().Deliver})
	sessions := sessionext.New(session.Config{PrimarySessionID: "coordinator"})
	var calls atomic.Int32
	var researchCalls atomic.Int32
	busy := make(chan struct{}, 1)
	workerInputs := make(chan string, 16)
	completed := make(chan *aop.Event, 32)
	stream := events.New()
	observer := stream.Observe(func(event *aop.Event) {
		if event.GetTurnEnded() != nil {
			completed <- event
		}
	})
	defer observer.Close(ctx)
	p := modelFunc(func(callCtx context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if request.SessionID == router.Router().SessionID() {
			calls.Add(1)
			return routingModel.ChatCompletion(callCtx, request)
		}
		if request.SessionID == "investigator" && researchCalls.Add(1) == 1 {
			busy <- struct{}{}
			<-callCtx.Done()
			return nil, callCtx.Err()
		}
		workerInputs <- request.SessionID + ":" + lastInput(request)
		return response("receiver completed"), nil
	})
	f := apptest.NewFixture(t, nil, stream)
	f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), router, connection, adapter, sessions)
	set := hosttest.Load(t, ctx, entries...)
	// Join sessions and subscriptions before closing their HTTP server, including
	// failure paths. hosttest's cleanup is deliberately idempotent.
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := set.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	var writer *session.Session
	for _, id := range []string{"coordinator", "investigator", "writer"} {
		s, err := sessions.Runtime().OpenSession(ctx, session.SessionOptions{ID: id, AgentName: id})
		if err != nil {
			t.Fatal(err)
		}
		if id == "writer" {
			writer = s
		}
	}
	space, err := connection.Service().ReadySpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.EnsureRegistered(ctx, "external-monitor", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := monitor.GetSpaceInfo(ctx, space); err != nil {
		t.Fatal(err)
	}
	send := func(text, target string, interrupt bool) protocols.Message {
		t.Helper()
		message, err := monitor.Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": text},
			Refs: &protocols.Ref{Nodes: []string{connection.Service().Client().NodeID()}},
			Meta: map[string]any{"source_session_id": "payment-monitor", "target_session_id": target, "interrupt": interrupt}})
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	waitTurns := func(ids ...string) {
		t.Helper()
		want := make(map[string]bool)
		for _, id := range ids {
			want[id] = true
		}
		for len(want) > 0 {
			event := waitFor(t, ctx, completed)
			if want[event.SessionId] {
				if event.GetTurnEnded().Error != nil {
					t.Fatalf("turn failed in %s: %s", event.SessionId, event.GetTurnEnded().Error.Message)
				}
				delete(want, event.SessionId)
			}
		}
	}
	idle := func() {
		t.Helper()
		before := calls.Load()
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if calls.Load() != before {
			t.Fatal("idle router called the model without new input")
		}
	}
	send("Monitor event M1: investigate the payment retry race; keep investigating while subsequent monitoring events arrive.", "", false)
	routingDone := false
waitingForInvestigator:
	for {
		select {
		case <-busy:
			break waitingForInvestigator
		case event := <-completed:
			if failure := event.GetTurnEnded().Error; failure != nil {
				t.Fatalf("initial routing failed: %s", failure.Message)
			}
			routingDone = event.SessionId == router.Router().SessionID()
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if !routingDone {
		waitTurns(router.Router().SessionID())
	}
	// Direct external ingress interleaves with the IOA stream while the
	// investigator's actual Agent Run remains blocked.
	submit(t, ctx, router.Router(), mailbox.NewUserMessage("Independently prepare the documentation migration guide, revision 1."), "writer")
	if input := waitFor(t, ctx, workerInputs); !strings.HasPrefix(input, "writer:") {
		t.Fatalf("independent work blocked: %s", input)
	}
	waitTurns(router.Router().SessionID(), "writer")
	idle()
	for i := 2; i <= 4; i++ {
		if i == 3 {
			close(drop)
			waitFor(t, ctx, reconnected)
			idle()
		}
		send(fmt.Sprintf("Monitor event M%d: investigate the payment retry race; new evidence batch %d.", i, i), "", false)
		// Routing completes despite a busy receiver; no task completion is awaited.
		waitTurns(router.Router().SessionID())
		idle()
	}
	if researchCalls.Load() != 1 {
		t.Fatal("busy investigator unexpectedly finished before interruption")
	}
	send("Monitor event M5: investigate the payment retry race with two concurrent retries. This corrects all earlier evidence.", "investigator", true)
	input := waitFor(t, ctx, workerInputs)
	if !strings.HasPrefix(input, "investigator:") || !strings.Contains(input, "two concurrent retries") || !strings.Contains(input, "payment-monitor") {
		t.Fatalf("monitor interruption lost source or content: %s", input)
	}
	waitTurns(router.Router().SessionID(), "investigator")
	idle()
	send("Independently prepare the documentation migration guide, revision 2, incorporating the earlier revision.", "writer", false)
	if input := waitFor(t, ctx, workerInputs); !strings.HasPrefix(input, "writer:") || !strings.Contains(input, "revision 2") {
		t.Fatalf("idle session did not resume its history: %s", input)
	}
	waitTurns(router.Router().SessionID(), "writer")
	if len(writer.MessagesSnapshot()) < 4 {
		t.Fatal("writer lost prior turn history")
	}
	result := send("Investigation result: two concurrent retries both observed the unpaid state. Send this finding to the owner integrating the review.", "", false)
	if input := waitFor(t, ctx, workerInputs); !strings.HasPrefix(input, "coordinator:") {
		t.Fatalf("result not integrated: %s", input)
	}
	waitTurns(router.Router().SessionID(), "coordinator")
	ack, err := connection.Service().Client().Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": "review received the monitoring result"},
		Refs: &protocols.Ref{Nodes: []string{monitor.NodeID()}, Messages: []string{result.ID}},
		Meta: map[string]any{"source_session_id": "coordinator"}})
	if err != nil {
		t.Fatal(err)
	}
	replies, err := monitor.Read(ctx, space, protocols.ReadOptions{All: true, After: result.ID})
	if err != nil || len(replies) != 1 || replies[0].ID != ack.ID {
		t.Fatalf("IOA result acknowledgment missing: %v %v", replies, err)
	}
	idle()
	closeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if err := set.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Router().Submit(ctx, mailbox.NewUserMessage("after shutdown")); !errors.Is(err, inboxext.ErrUnavailable) {
		t.Fatalf("closed listener admitted new input: %v", err)
	}
	t.Logf("duration=%s routed_messages=8 monitor_events=5 model_calls=%d subscriptions=%d; idle calls=0, IOA acknowledgment received", time.Since(started).Round(time.Millisecond), calls.Load(), connections.Load())
}
