package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/aop/x/delegation"
	"github.com/chainreactors/ioa/protocols"
)

type handoffClient struct {
	spaceCalls int
	bodies     []protocols.SendMessage
}

func (c *handoffClient) NodeID() string { return "parent-node" }
func (c *handoffClient) RegisterNode(context.Context, string, string, map[string]any) (protocols.Node, error) {
	return protocols.Node{ID: c.NodeID()}, nil
}
func (c *handoffClient) Space(context.Context, string, string, ...string) (protocols.SpaceInfo, error) {
	c.spaceCalls++
	return protocols.SpaceInfo{ID: "space-1", Name: "test"}, nil
}
func (c *handoffClient) Send(_ context.Context, spaceID string, body protocols.SendMessage) (protocols.Message, error) {
	c.bodies = append(c.bodies, body)
	return protocols.Message{ID: "message-" + string(rune('0'+len(c.bodies))), SpaceID: spaceID}, nil
}
func (c *handoffClient) Read(context.Context, string, protocols.ReadOptions) ([]protocols.Message, error) {
	return nil, nil
}

func handoffEvent(t *testing.T, typ, sessionID, agentName string, data any) aop.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return aop.Event{Type: typ, SessionID: sessionID, Agent: agentName, Data: raw}
}

func TestIOAHandoffFromAOPBus(t *testing.T) {
	client := &handoffClient{}
	bus := eventbus.New[aop.Event]()
	subscribeIOAHandoff(bus, client, "test", nil)

	start := handoffEvent(t, aop.TypeSessionStart, "child-session", "worker", aop.SessionStartData{
		Model:            "test-model",
		ParentSessionID:  "parent-session",
		ParentToolCallID: "spawn-1",
	})
	if err := delegation.Set(&start, delegation.DelegationDetail{
		Task:      "inspect target",
		AgentName: "worker",
		RunMode:   delegation.DelegationDetailRunModeForeground,
	}); err != nil {
		t.Fatal(err)
	}
	bus.Emit(start)

	bus.Emit(handoffEvent(t, aop.TypeMessage, "child-session", "worker", aop.MessageData{
		MessageID: "m-1", Role: "assistant",
		Parts: []aop.MessagePart{{Type: aop.PartText, Text: "inspection complete"}},
	}))
	bus.Emit(handoffEvent(t, aop.TypeSessionEnd, "child-session", "worker", aop.SessionEndData{Stop: "completed"}))

	deadline := time.Now().Add(2 * time.Second)
	for len(client.bodies) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if client.spaceCalls != 1 {
		t.Fatalf("space calls = %d, want 1", client.spaceCalls)
	}
	if len(client.bodies) != 2 {
		t.Fatalf("messages = %d, want 2", len(client.bodies))
	}
	for i, body := range client.bodies {
		if body.ContentType != "handoff" {
			t.Fatalf("message %d content_type = %q", i, body.ContentType)
		}
		if len(body.Content) != 2 || body.Content["title"] == nil || body.Content["message"] == nil {
			t.Fatalf("message %d content = %#v, want native handoff title/message", i, body.Content)
		}
	}
	delegate, returned := client.bodies[0], client.bodies[1]
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
	bus := eventbus.New[aop.Event]()
	subscribeIOAHandoff(bus, client, "test", nil)

	start := handoffEvent(t, aop.TypeSessionStart, "child-session", "worker", aop.SessionStartData{
		ParentSessionID:  "parent-session",
		ParentToolCallID: "spawn-2",
	})
	if err := delegation.Set(&start, delegation.DelegationDetail{
		Task:      "inspect target",
		AgentName: "worker",
		RunMode:   delegation.DelegationDetailRunModeBackground,
	}); err != nil {
		t.Fatal(err)
	}
	bus.Emit(start)
	bus.Emit(handoffEvent(t, aop.TypeSessionEnd, "child-session", "worker", aop.SessionEndData{Stop: "error", Error: "boom"}))

	deadline := time.Now().Add(2 * time.Second)
	for len(client.bodies) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(client.bodies) != 2 {
		t.Fatalf("messages = %d, want 2", len(client.bodies))
	}
	retMeta, ok := client.bodies[1].Meta["subagent"].(map[string]any)
	if !ok || retMeta["status"] != "failed" || retMeta["mode"] != "async" {
		t.Fatalf("return meta = %#v", client.bodies[1].Meta)
	}
	if client.bodies[1].Content["message"] != "boom" {
		t.Fatalf("return message = %#v", client.bodies[1].Content["message"])
	}
}

func TestIOAHandoffIgnoresNonDelegationSessions(t *testing.T) {
	client := &handoffClient{}
	bus := eventbus.New[aop.Event]()
	subscribeIOAHandoff(bus, client, "test", nil)

	bus.Emit(handoffEvent(t, aop.TypeSessionStart, "root-session", "aiscan", aop.SessionStartData{Model: "test-model"}))
	bus.Emit(handoffEvent(t, aop.TypeSessionEnd, "root-session", "aiscan", aop.SessionEndData{Stop: "completed"}))

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(client.bodies) > 0 {
			t.Fatalf("unexpected handoff messages: %#v", client.bodies)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
