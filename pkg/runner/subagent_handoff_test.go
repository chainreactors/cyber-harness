package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	ext "github.com/chainreactors/aiscan/pkg/types/extensions"
	"github.com/chainreactors/ioa/protocols"
)

type handoffClient struct {
	mu         sync.Mutex
	spaceCalls int
	bodies     []protocols.SendMessage
}

func (c *handoffClient) NodeID() string { return "parent-node" }
func (c *handoffClient) RegisterNode(context.Context, string, string, map[string]any) (protocols.Node, error) {
	return protocols.Node{ID: c.NodeID()}, nil
}
func (c *handoffClient) Space(context.Context, string, string, ...string) (protocols.SpaceInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spaceCalls++
	return protocols.SpaceInfo{ID: "space-1", Name: "test"}, nil
}
func (c *handoffClient) Send(_ context.Context, spaceID string, body protocols.SendMessage) (protocols.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies = append(c.bodies, body)
	return protocols.Message{ID: "message-" + string(rune('0'+len(c.bodies))), SpaceID: spaceID}, nil
}
func (c *handoffClient) Read(context.Context, string, protocols.ReadOptions) ([]protocols.Message, error) {
	return nil, nil
}

func (c *handoffClient) snapshot() (int, []protocols.SendMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spaceCalls, append([]protocols.SendMessage(nil), c.bodies...)
}

func waitHandoffBodies(t *testing.T, client *handoffClient, count int) (int, []protocols.SendMessage) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		spaceCalls, bodies := client.snapshot()
		if len(bodies) >= count {
			return spaceCalls, bodies
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, bodies := client.snapshot()
	t.Fatalf("messages = %d, want %d", len(bodies), count)
	return 0, bodies
}

func handoffEvent(t *testing.T, sessionID, agentName string, event *aop.Event) *aop.Event {
	t.Helper()
	event.SessionId = sessionID
	event.Emitter = agentName
	return event
}

func TestIOAHandoffFromAOPBus(t *testing.T) {
	client := &handoffClient{}
	bus := eventbus.New[*aop.Event]()
	cancel := subscribeIOAHandoffContext(context.Background(), bus, client, "test", nil)
	defer cancel()

	start := handoffEvent(t, "child-session", "worker", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{
		Model:            "test-model",
		ParentSessionId:  "parent-session",
		ParentToolCallId: "spawn-1",
	}}})
	if err := ext.SetDelegation(start, ext.DelegationDetail{
		Task:      "inspect target",
		AgentName: "worker",
		RunMode:   ext.DelegationRunForeground,
	}); err != nil {
		t.Fatal(err)
	}
	bus.Emit(start)

	bus.Emit(handoffEvent(t, "child-session", "worker", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{
		Id: "m-1", Role: "assistant", Content: []*aop.Content{aop.Text("inspection complete")},
	}}}))
	bus.Emit(handoffEvent(t, "child-session", "worker", &aop.Event{Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}}))

	spaceCalls, bodies := waitHandoffBodies(t, client, 2)
	if spaceCalls != 1 {
		t.Fatalf("space calls = %d, want 1", spaceCalls)
	}
	for i, body := range bodies {
		if body.ContentType != "handoff" {
			t.Fatalf("message %d content_type = %q", i, body.ContentType)
		}
		if len(body.Content) != 2 || body.Content["title"] == nil || body.Content["message"] == nil {
			t.Fatalf("message %d content = %#v, want native handoff title/message", i, body.Content)
		}
	}
	delegate, returned := bodies[0], bodies[1]
	if delegate.Refs != nil {
		t.Fatalf("delegate refs = %#v, want nil", delegate.Refs)
	}
	meta, ok := delegate.Meta["subagent"].(map[string]any)
	if !ok {
		t.Fatalf("delegate meta = %#v", delegate.Meta)
	}
	if meta["phase"] != "delegate" || meta["parent_tool_call_id"] != "spawn-1" || meta["mode"] != "sync" {
		t.Fatalf("delegate meta = %#v", meta)
	}
	if delegate.Content["message"] != "inspect target" {
		t.Fatalf("delegate message = %#v", delegate.Content["message"])
	}
	retMeta, ok := returned.Meta["subagent"].(map[string]any)
	if !ok || retMeta["phase"] != "return" || retMeta["status"] != "completed" {
		t.Fatalf("return meta = %#v", returned.Meta)
	}
	if returned.Content["message"] != "inspection complete" {
		t.Fatalf("return message = %#v", returned.Content["message"])
	}
	refs := returned.Refs
	if refs == nil || len(refs.Messages) != 1 || refs.Messages[0] != "message-1" {
		t.Fatalf("return refs = %#v, want delegation message %q", refs, "message-1")
	}
}

func TestIOAHandoffFailedRun(t *testing.T) {
	client := &handoffClient{}
	bus := eventbus.New[*aop.Event]()
	cancel := subscribeIOAHandoffContext(context.Background(), bus, client, "test", nil)
	defer cancel()

	start := handoffEvent(t, "child-session", "worker", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{
		ParentSessionId: "parent-session", ParentToolCallId: "spawn-2",
	}}})
	if err := ext.SetDelegation(start, ext.DelegationDetail{
		Task:      "inspect target",
		AgentName: "worker",
		RunMode:   ext.DelegationRunBackground,
	}); err != nil {
		t.Fatal(err)
	}
	bus.Emit(start)
	bus.Emit(handoffEvent(t, "child-session", "worker", &aop.Event{Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{
		StopReason: "error", Error: &aop.ProtocolError{Message: "boom"},
	}}}))

	_, bodies := waitHandoffBodies(t, client, 2)
	retMeta, ok := bodies[1].Meta["subagent"].(map[string]any)
	if !ok || retMeta["status"] != "failed" || retMeta["mode"] != "async" {
		t.Fatalf("return meta = %#v", bodies[1].Meta)
	}
	if bodies[1].Content["message"] != "boom" {
		t.Fatalf("return message = %#v", bodies[1].Content["message"])
	}
}

func TestIOAHandoffIgnoresNonDelegationSessions(t *testing.T) {
	client := &handoffClient{}
	bus := eventbus.New[*aop.Event]()
	cancel := subscribeIOAHandoffContext(context.Background(), bus, client, "test", nil)
	defer cancel()

	bus.Emit(handoffEvent(t, "root-session", "aiscan", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: "test-model"}}}))
	bus.Emit(handoffEvent(t, "root-session", "aiscan", &aop.Event{Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}}))

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, bodies := client.snapshot()
		if len(bodies) > 0 {
			t.Fatalf("unexpected handoff messages: %#v", bodies)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
