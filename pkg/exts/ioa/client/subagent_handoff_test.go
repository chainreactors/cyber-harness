package client

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/types"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	service "github.com/chainreactors/cyber/tools/ioa"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestHandoffAndSessionRouting(t *testing.T) {
	for _, backend := range []string{"memory", "http"} {
		t.Run(backend, func(t *testing.T) {
			config := service.Config{Space: "work", AutoRegister: true}
			if backend == "http" {
				store := ioaserver.NewMemoryStore()
				defer store.Close()
				server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "test-key")))
				defer server.Close()
				config.URL = strings.Replace(server.URL, "http://", "http://test-key@", 1)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			reg := hooks.New()
			connection := New(config)
			e := NewCollaboration(CollaborationOptions{})
			set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NopLogger()), connection, e)
			if err != nil {
				t.Fatal(err)
			}
			if err = set.Load(ctx); err != nil {
				t.Fatal(err)
			}
			defer set.Close(context.Background())
			got := make(chan inbox.Message, 4)
			ev := agenthooks.SessionEvent{SessionID: "child", ParentID: "parent", ParentToolCallID: "call", AgentName: "worker", Model: "test", Input: "skill + task", Delegation: &types.DelegationDetail{Task: "task", RunMode: types.DelegationRunForeground}, Deliver: func(_ context.Context, m inbox.Message) error { got <- m; return nil }}
			if _, err = agenthooks.SessionStart.Emit(ctx, reg, ev); err != nil {
				t.Fatal(err)
			}
			client := e.service.Client()
			space := e.Service().ReceiveSpace()
			history, err := client.Read(ctx, space, protocols.ReadOptions{All: true})
			if err != nil || len(history) != 1 {
				t.Fatalf("dispatch: %v %v", history, err)
			}
			if history[0].Content["input"] != "skill + task" || history[0].Content["message"] != "task" {
				t.Fatalf("input: %#v", history[0])
			}
			msg, err := client.Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": "sibling input"}, Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}, Meta: map[string]any{"source_session_id": "sibling", "target_session_id": "child"}})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case m := <-got:
				if m.Meta["message_id"] != msg.ID || m.Origin != inbox.OriginPeer {
					t.Fatalf("input: %#v", m)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err = e.deliver(ctx, msg); err != nil {
				t.Fatal(err)
			}
			select {
			case m := <-got:
				t.Fatalf("duplicate: %#v", m)
			default:
			}
			ev.Output = "result"
			ev.Stop = agenthooks.StopReasonCompleted
			if _, err = agenthooks.SessionEnd.Emit(ctx, reg, ev); err != nil {
				t.Fatal(err)
			}
			if _, err = agenthooks.SessionEnd.Emit(ctx, reg, ev); err != nil {
				t.Fatal(err)
			}
			history, err = client.Read(ctx, space, protocols.ReadOptions{All: true})
			if err != nil || len(history) != 3 {
				t.Fatalf("history: %v %v", history, err)
			}
			if history[2].Refs.Messages[0] != history[0].ID || history[2].Content["message"] != "result" {
				t.Fatalf("return: %#v", history[2])
			}
			msg.ID = "late"
			if err = e.deliver(ctx, msg); err == nil {
				t.Fatal("closed task still receives")
			}
		})
	}
}

func TestRoutingRejectsOtherNodesAndAmbiguousDefaults(t *testing.T) {
	connection := New(service.Config{})
	e := NewCollaboration(CollaborationOptions{})
	reg := hooks.New()
	set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NopLogger()), connection, e)
	if err != nil {
		t.Fatal(err)
	}
	if err = set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	got := make(chan inbox.Message, 2)
	for _, id := range []string{"one", "two"} {
		_, err = agenthooks.SessionStart.Emit(t.Context(), reg, agenthooks.SessionEvent{SessionID: id, Deliver: func(_ context.Context, m inbox.Message) error { got <- m; return nil }})
		if err != nil {
			t.Fatal(err)
		}
	}
	msg := protocols.Message{ID: "foreign", Sender: "peer", Refs: protocols.Ref{Nodes: []string{"other-node"}}, Meta: map[string]any{"target_session_id": "one"}}
	if err = e.deliver(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
		t.Fatal("wrong node delivered")
	default:
	}
	msg.Refs = protocols.Ref{}
	msg.Meta = nil
	if err = e.deliver(t.Context(), msg); err == nil {
		t.Fatal("ambiguous broadcast accepted")
	}
}

func TestSpaceSwitchMovesSubscriptionButKeepsDispatchReference(t *testing.T) {
	reg := hooks.New()
	connection := New(service.Config{RegisterCommands: true})
	commands := coretool.NewCommandRegistry()
	e := NewCollaboration(CollaborationOptions{})
	set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NopLogger()), commands, connection, e)
	if err != nil {
		t.Fatal(err)
	}
	if err = set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	got := make(chan inbox.Message, 1)
	ev := agenthooks.SessionEvent{SessionID: "child", Delegation: &types.DelegationDetail{Task: "task"}, Deliver: func(_ context.Context, m inbox.Message) error { got <- m; return nil }}
	if _, err = agenthooks.SessionStart.Emit(t.Context(), reg, ev); err != nil {
		t.Fatal(err)
	}
	oldSpace := e.Service().ReceiveSpace()
	var output bytes.Buffer
	if _, err = commands.Run(t.Context(), []string{"ioa", "space", "new-space", "new work"}, &coretool.Execution{Stdout: &output}); err != nil {
		t.Fatal(err)
	}
	space := e.Service().ReceiveSpace()
	if space == oldSpace {
		t.Fatal("space unchanged")
	}
	client := e.service.Client()
	if _, err = client.Send(t.Context(), space, protocols.SendMessage{Content: map[string]any{"text": "new"}, Meta: map[string]any{"target_session_id": "child"}, Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("subscription did not follow binding")
	}
	ev.Output = "done"
	ev.Stop = agenthooks.StopReasonCompleted
	if _, err = agenthooks.SessionEnd.Emit(t.Context(), reg, ev); err != nil {
		t.Fatal(err)
	}
	records, err := client.Read(t.Context(), oldSpace, protocols.ReadOptions{All: true})
	if err != nil || len(records) != 2 || records[1].Refs.Messages[0] != records[0].ID {
		t.Fatalf("lost dispatch space: %v %v", records, err)
	}
}

func TestPeerInputExposesReplyAddress(t *testing.T) {
	msg := protocols.Message{Content: map[string]any{"text": "offer"}, Meta: map[string]any{"source_session_id": "sibling-a", "target_session_id": "sibling-b"}}
	text := formatIOAMessage(msg)
	if !strings.Contains(text, "source_session_id=sibling-a") || !strings.Contains(text, "target_session_id=sibling-b") || !strings.HasSuffix(text, "offer") {
		t.Fatalf("peer cannot address reply: %s", text)
	}
}
