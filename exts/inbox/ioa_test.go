package inbox_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	"github.com/chainreactors/ioa/protocols"
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
		Meta:    map[string]any{"target_session_id": "receiver", "source_session_id": "remote-worker", "interrupt": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := waitFor(t, ctx, worker)
	if !strings.Contains(input, "incoming-nonce-7") || !strings.Contains(input, "remote-worker") {
		t.Fatalf("lost IOA envelope: %s", input)
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
			Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}, Meta: map[string]any{"target_session_id": "receiver"}})
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
		if message := waitFor(t, ctx, got); message.Meta["message_id"] != id {
			t.Fatalf("burst lost ordering or duplicated a recipient: got=%v want=%s", message.Meta["message_id"], id)
		}
	}
}
