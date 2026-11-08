package inbox_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	harness "github.com/chainreactors/cyber/core/collaboration"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	nativeext "github.com/chainreactors/cyber/exts/native"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	peerclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestIOAIncomingMessageTraversesRouterAndRealSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	f := apptest.NewFixture(t, nil, nil)
	router := inboxext.New(inboxext.Config{})
	connection := ioaclient.New(ioatools.Config{Space: "inbox-routing-test", AutoRegister: true})
	collaboration := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: router.Router().Deliver})
	sessions := sessionext.New(session.Config{})
	worker := make(chan string, 1)
	p := modelFunc(func(_ context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if request.SessionID == router.Router().SessionID() {
			if request.Messages[len(request.Messages)-1].Role == "tool" {
				return response("routed"), nil
			}
			if !strings.Contains(lastInput(request), "incoming-nonce-7") {
				return nil, errors.New("router never received actual IOA message")
			}
			return response("", "receiver"), nil
		}
		worker <- lastInput(request)
		return response("done"), nil
	})
	f.Providers.Set(p, provider.ProviderConfig{Model: "scenario"})
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), router, connection, collaboration, sessions)
	hosttest.Load(t, t.Context(), entries...)
	if _, err := sessions.Runtime().OpenSession(ctx, session.SessionOptions{ID: "receiver", AgentName: "receiver"}); err != nil {
		t.Fatal(err)
	}
	client := connection.Service().Client()
	_, err := client.Send(ctx, connection.Service().ReceiveSpace(), protocols.SendMessage{
		Content: map[string]any{"text": "incoming-nonce-7"},
		Refs:    &protocols.Ref{Nodes: []string{client.NodeID()}},
		Meta:    harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "remote-worker"}, Target: harness.Address{SessionID: "receiver"}, Interrupt: true}.Meta(),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := waitFor(t, ctx, worker)
	if !strings.Contains(input, "incoming-nonce-7") || !strings.Contains(input, "remote-worker") {
		t.Fatalf("lost IOA envelope: %s", input)
	}
}

func TestIOARecordsCompleteRouterWorkerPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "path-graph")))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://path-graph@", 1)
	f := apptest.NewFixture(t, nil, nil)
	connection := ioaclient.New(ioatools.Config{URL: endpoint, Space: "agent-path", AutoRegister: true})
	var router *inboxext.Router
	collaboration := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: func(ctx context.Context, message mailbox.Message) error {
		return router.Deliver(ctx, message)
	}})
	results := make(chan protocols.Message, 1)
	config := inboxext.Config{StateDir: t.TempDir(), Record: collaboration.RecordInboxHandoff, Notify: func(ctx context.Context, message mailbox.Message) error {
		if message.Meta.ReplyNode == "" {
			return nil
		}
		refs := &protocols.Ref{Nodes: []string{message.Meta.ReplyNode}}
		if message.Meta.ReplyMessage != "" {
			refs.Messages = []string{message.Meta.ReplyMessage}
		}
		record, err := connection.Service().Client().Send(ctx, message.Meta.Space, protocols.SendMessage{
			Content: map[string]any{"text": provider.MessageText(message.Message)}, Refs: refs,
			Meta: harness.Envelope{Version: harness.Version, Event: harness.TaskResult, ID: message.Message.Id, Source: harness.Address{TaskID: message.Meta.TaskID, SessionID: message.Meta.ExecutionID}, Chain: message.Meta.FeedbackChain}.Meta(),
		})
		if err == nil {
			results <- record
		}
		return err
	}}
	inbox := inboxext.New(config)
	router = inbox.Router()
	sessions := sessionext.New(session.Config{})
	p := modelFunc(func(ctx context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, isRouter := activeDecision(request); isRouter {
			return result, nil
		}
		return response("worker reached verified conclusion"), nil
	})
	f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), nativeext.New(), connection, sessions, subagentext.NewTools(), collaboration, inbox)
	set := hosttest.Load(t, ctx, entries...)
	defer closeActive(t, set)
	space, err := connection.Service().ReadySpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = peer.EnsureRegistered(ctx, "path-graph-producer", "", nil); err != nil {
		t.Fatal(err)
	}
	source, err := peer.Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": "ROLE=path-audit. Finish the assigned audit."},
		Refs: &protocols.Ref{Nodes: []string{connection.Service().Client().NodeID()}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "external-review"}}.Meta()})
	if err != nil {
		t.Fatal(err)
	}
	result := waitFor(t, ctx, results)
	resultRecord, err := harness.Parse(result.Meta)
	if err != nil || resultRecord == nil || resultRecord.Event != harness.TaskResult || resultRecord.Source.TaskID == "" {
		t.Fatalf("missing terminal task result: %#v", result.Meta)
	}
	taskID := resultRecord.Source.TaskID
	var messages []protocols.Message
	for len(messages) < 5 {
		messages, err = peer.Read(ctx, space, protocols.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) >= 5 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("IOA path did not settle: messages=%d: %v", len(messages), ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	var route, delegate, returned protocols.Message
	delegateID := ""
	for _, message := range messages {
		if message.ContentType != "handoff" {
			continue
		}
		record, parseErr := harness.Parse(message.Meta)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if record == nil {
			continue
		}
		switch record.Event {
		case harness.InboxRoute:
			route = message
		case harness.SubagentDelegate:
			delegate, delegateID = message, message.ID
		case harness.SubagentReturn:
			returned = message
		}
	}
	if route.ID == "" || delegate.ID == "" || returned.ID == "" {
		t.Fatalf("incomplete IOA path: route=%s delegate=%s return=%s", route.ID, delegate.ID, returned.ID)
	}
	routeRecord, err := harness.Parse(route.Meta)
	if err != nil {
		t.Fatal(err)
	}
	delegateRecord, err := harness.Parse(delegate.Meta)
	if err != nil {
		t.Fatal(err)
	}
	returnRecord, err := harness.Parse(returned.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if routeRecord.Source.NodeID != peer.NodeID() || routeRecord.ID == "" || len(route.Refs.Messages) != 1 || route.Refs.Messages[0] != source.ID || routeRecord.Target.TaskID != taskID || delegateRecord.Target.TaskID != taskID {
		t.Fatalf("router-to-worker path lacks task correlation: route=%#v delegate=%#v", routeRecord, delegateRecord)
	}
	if returnRecord.Source.TaskID != taskID || returnRecord.Source.SessionID != delegateRecord.Target.SessionID || resultRecord.Source.SessionID != returnRecord.Source.SessionID || len(returned.Refs.Messages) != 1 || returned.Refs.Messages[0] != delegateID || len(result.Refs.Messages) != 1 || result.Refs.Messages[0] != source.ID {
		t.Fatalf("IOA path is broken: return=%#v result=%#v", returnRecord, resultRecord)
	}
}

func TestIOABurstWaitsForRouterCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	f := apptest.NewFixture(t, nil, nil)
	router := inboxext.New(inboxext.Config{Capacity: 1})
	connection := ioaclient.New(ioatools.Config{Space: "burst-routing", AutoRegister: true})
	full := make(chan struct{}, 1)
	collaboration := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: func(ctx context.Context, message mailbox.Message) error {
		err := router.Router().Deliver(ctx, message)
		if errors.Is(err, mailbox.ErrInboxFull) {
			select {
			case full <- struct{}{}:
			default:
			}
		}
		return err
	}})
	started, release := make(chan struct{}, 1), make(chan struct{})
	first := true // The router serializes provider calls.
	p := modelFunc(func(callCtx context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if request.Messages[len(request.Messages)-1].Role == "tool" {
			return response("routed"), nil
		}
		if first {
			first = false
			started <- struct{}{}
			select {
			case <-release:
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		}
		return response("", "receiver"), nil
	})
	f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), router, connection, collaboration)
	hosttest.Load(t, ctx, entries...)
	const count = 10
	got := make(chan mailbox.Message, count)
	if _, err := agenthooks.SessionStart.Emit(ctx, f.Hooks, agenthooks.SessionEvent{SessionID: "receiver",
		Deliver: func(_ context.Context, message mailbox.Message) error { got <- message; return nil }}); err != nil {
		t.Fatal(err)
	}
	client := connection.Service().Client()
	var ids []string
	for i := range count {
		message, err := client.Send(ctx, connection.Service().ReceiveSpace(), protocols.SendMessage{Content: map[string]any{"text": "monitor observation"},
			Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Target: harness.Address{SessionID: "receiver"}}.Meta()})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, message.ID)
		if i == 0 {
			waitFor(t, ctx, started)
		}
	}
	waitFor(t, ctx, full)
	close(release)
	for _, id := range ids {
		if message := waitFor(t, ctx, got); message.Message.Id != id {
			t.Fatalf("burst lost ordering or duplicated a recipient: got=%v want=%s", message.Message.Id, id)
		}
	}
}
