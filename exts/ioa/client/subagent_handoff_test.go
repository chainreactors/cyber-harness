package client

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harness "github.com/chainreactors/cyber/core/collaboration"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/types"
	promptext "github.com/chainreactors/cyber/exts/prompt"
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
			ev := agenthooks.SessionEvent{SessionID: "child", TaskID: "durable-task", ParentID: "parent", ParentToolCallID: "call", AgentName: "worker", Model: "test", Input: "skill + task", Delegation: &types.DelegationDetail{Task: "task", RunMode: types.DelegationRunForeground}, Deliver: func(_ context.Context, m inbox.Message) error { got <- m; return nil }}
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
			delegation, err := harness.Parse(history[0].Meta)
			if err != nil || delegation == nil || delegation.Event != harness.SubagentDelegate || delegation.Target.SessionID != "child" || delegation.Source.SessionID != "parent" || delegation.Target.TaskID != "durable-task" {
				t.Fatalf("delegate record: %#v", history[0].Meta)
			}
			msg, err := client.Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": "sibling input"}, Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "sibling"}, Target: harness.Address{SessionID: "child"}}.Meta()})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case m := <-got:
				if m.Message.Id != msg.ID || m.Meta.MessageID != msg.ID || m.Origin != inbox.OriginPeer || m.Interrupt {
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
			urgent, err := client.Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": "correct course"}, Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "sibling"}, Target: harness.Address{SessionID: "worker"}, Interrupt: true}.Meta()})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case m := <-got:
				if !m.Interrupt || m.Message.Id != urgent.ID || m.Meta.MessageID != urgent.ID {
					t.Fatalf("interrupt mapping: %#v", m)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			ev.Stop = agenthooks.StopReasonCompleted
			if _, err = agenthooks.SessionEnd.Emit(ctx, reg, ev); err != nil {
				t.Fatal(err)
			}
			if _, err = agenthooks.SessionEnd.Emit(ctx, reg, ev); err != nil {
				t.Fatal(err)
			}
			history, err = client.Read(ctx, space, protocols.ReadOptions{All: true})
			if err != nil || len(history) != 4 {
				t.Fatalf("history: %v %v", history, err)
			}
			if history[3].Refs.Messages[0] != history[0].ID || history[3].Content["message"] != "result" {
				t.Fatalf("return: %#v", history[3])
			}
			returned, err := harness.Parse(history[3].Meta)
			if err != nil || returned == nil || returned.Event != harness.SubagentReturn || returned.Status != "completed" || returned.Source.SessionID != "child" || returned.Source.TaskID != "durable-task" {
				t.Fatalf("return record: %#v", history[3].Meta)
			}
			msg.ID = "late"
			if err = e.deliver(ctx, msg); err == nil {
				t.Fatal("closed task still receives")
			}
			urgent.ID = "late-name"
			if err = e.deliver(ctx, urgent); err == nil {
				t.Fatal("closed name still receives")
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
		_, err = agenthooks.SessionStart.Emit(t.Context(), reg, agenthooks.SessionEvent{SessionID: id, AgentName: "worker", Deliver: func(_ context.Context, m inbox.Message) error { got <- m; return nil }})
		if err != nil {
			t.Fatal(err)
		}
	}
	msg := protocols.Message{ID: "foreign", Sender: "peer", Refs: protocols.Ref{Nodes: []string{"other-node"}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Target: harness.Address{SessionID: "one"}}.Meta()}
	if err = e.deliver(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
		t.Fatal("wrong node delivered")
	default:
	}
	msg.Refs = protocols.Ref{}
	msg.Meta = harness.Envelope{Version: harness.Version, Event: harness.Message}.Meta()
	if err = e.deliver(t.Context(), msg); err == nil {
		t.Fatal("ambiguous broadcast accepted")
	}
	msg.Meta = harness.Envelope{Version: harness.Version, Event: harness.Message, Target: harness.Address{SessionID: "worker"}}.Meta()
	if err = e.deliver(t.Context(), msg); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous name accepted: %v", err)
	}
	msg.Meta = harness.Envelope{Version: harness.Version, Event: harness.Message, Target: harness.Address{SessionID: "one"}}.Meta()
	e.mu.Lock()
	e.routes["two"].start.AgentName = "one"
	e.mu.Unlock()
	if err = e.deliver(t.Context(), msg); err != nil {
		t.Fatalf("ID must take precedence over another session's name: %v", err)
	}
	<-got
	if _, err = agenthooks.SessionEnd.Emit(t.Context(), reg, agenthooks.SessionEvent{SessionID: "one"}); err != nil {
		t.Fatal(err)
	}
	msg.ID = "remaining-name"
	if err = e.deliver(t.Context(), msg); err != nil {
		t.Fatalf("remaining unique name: %v", err)
	}
	<-got
}

func TestInboxHandoffUsesMessageIDAsStableHandoffID(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	registry := hooks.New()
	connection := New(service.Config{Space: "work"})
	collaboration := NewCollaboration(CollaborationOptions{})
	set, err := extension.New(extension.Provided[*hooks.Registry](registry), promptext.New(),
		extension.Provided(telemetry.NopLogger()), connection, collaboration)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	space := collaboration.Service().ReceiveSpace()
	source, err := collaboration.Service().Client().Send(ctx, space, protocols.SendMessage{Content: map[string]any{"text": "source input"}})
	if err != nil {
		t.Fatal(err)
	}
	message := inbox.NewMessage(inbox.OriginSystem, "user", "routed input with attached evidence")
	message.Message.Id = "stable-handoff-id"
	message.Meta = inbox.Metadata{Kind: "inbox_handoff", MessageID: source.ID, Space: space,
		Record: &harness.Envelope{Version: harness.Version, Event: harness.InboxRoute, ID: message.Message.Id, Status: "routed", Target: harness.Address{SessionID: "worker-session"}, Agent: &harness.Agent{Name: "worker", Type: "audit"}}}
	if err := collaboration.RecordInboxHandoff(ctx, message); err != nil {
		t.Fatal(err)
	}
	records, err := collaboration.Service().Client().Read(ctx, space, protocols.ReadOptions{All: true})
	if err != nil || len(records) != 2 {
		t.Fatalf("handoff records: %v %v", records, err)
	}
	record, err := harness.Parse(records[1].Meta)
	if err != nil || record == nil || record.ID != message.Message.Id || len(records[1].Refs.Messages) != 1 || records[1].Refs.Messages[0] != source.ID {
		t.Fatalf("handoff identity/reference: %#v", records[1])
	}
	if records[1].Content["message"] != "routed input with attached evidence" {
		t.Fatalf("handoff lost readable input: %#v", records[1].Content)
	}
	if record.Event != harness.InboxRoute || record.Status != "routed" || record.Target.SessionID != "worker-session" || record.Source.SessionID != "" || record.CallID != "" {
		t.Fatalf("router handoff record lost provenance: %#v", record)
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
	if _, err = client.Send(t.Context(), space, protocols.SendMessage{Content: map[string]any{"text": "new"}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Target: harness.Address{SessionID: "child"}}.Meta(), Refs: &protocols.Ref{Nodes: []string{client.NodeID()}}}); err != nil {
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
	msg := protocols.Message{Content: map[string]any{"text": "offer"}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "sibling-a"}, Target: harness.Address{SessionID: "sibling-b"}}.Meta()}
	record, err := harness.Parse(msg.Meta)
	if err != nil {
		t.Fatal(err)
	}
	text := formatIOAMessage(msg, record)
	if !strings.Contains(text, "source_session_id=sibling-a") || !strings.Contains(text, "target_session_id=sibling-b") || !strings.HasSuffix(text, "offer") {
		t.Fatalf("peer cannot address reply: %s", text)
	}
}

func TestApplicationEnvelopeIsValidatedBeforeAdmission(t *testing.T) {
	reg := hooks.New()
	connection := New(service.Config{})
	e := NewCollaboration(CollaborationOptions{})
	set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NopLogger()), connection, e)
	if err != nil {
		t.Fatal(err)
	}
	if err = set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	got := make(chan inbox.Message, 4)
	_, err = agenthooks.SessionStart.Emit(t.Context(), reg, agenthooks.SessionEvent{SessionID: "receiver", Primary: true, Deliver: func(_ context.Context, m inbox.Message) error { got <- m; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	message := protocols.Message{ID: "same-transport-id", Sender: "peer", ContentType: "handoff", Content: map[string]any{"text": "ordinary handoff content"}, Meta: map[string]any{"version": 2, "event": "message"}}
	if err := e.deliver(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if e.Service().Status().LastError == "" {
		t.Fatal("invalid application data was not reported")
	}
	select {
	case <-got:
		t.Fatal("invalid protocol reached an agent")
	default:
	}
	message.Meta = map[string]any{"other.app": map[string]any{"event": "message"}, "target_session_id": "receiver"}
	if err := e.deliver(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
		t.Fatal("foreign application or obsolete fields executed as Harness work")
	default:
	}
	message.Meta = map[string]any{"cyber.harness": harness.Envelope{Version: harness.Version, Event: harness.Message, Target: harness.Address{SessionID: "receiver"}}}
	if err := e.deliver(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
		t.Fatal("removed namespace format executed as work")
	default:
	}
	// Validation failure must not mark the transport ID seen; corrected input is
	// deliverable. Generic handoff content never implies an internal lifecycle.
	message.Meta = harness.Envelope{Version: harness.Version, Event: harness.Message, Target: harness.Address{SessionID: "receiver"}}.Meta()
	if err := e.deliver(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if m := <-got; m.Message.Id != message.ID {
		t.Fatalf("wrong input: %v", m)
	}
	for _, record := range []harness.Envelope{
		{Version: harness.Version, Event: harness.InboxRoute, ID: "route", Target: harness.Address{TaskID: "task"}},
		{Version: harness.Version, Event: harness.SubagentDelegate, ID: "delegate", Target: harness.Address{SessionID: "receiver"}},
		{Version: harness.Version, Event: harness.SubagentReturn, ID: "return", Source: harness.Address{SessionID: "receiver"}},
	} {
		message.ID = record.ID
		message.ContentType = "text"
		message.Meta = record.Meta()
		if err := e.deliver(t.Context(), message); err != nil {
			t.Fatal(err)
		}
		select {
		case <-got:
			t.Fatalf("record executed as input: %s", record.Event)
		default:
		}
	}
}
