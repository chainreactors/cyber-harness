package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	ext "github.com/chainreactors/aiscan/pkg/types/extensions"
)

func TestSubAgentSyncReturnsResult(t *testing.T) {
	parent := NewAgent(Config{
		Provider:  &scriptedProvider{responses: []*ChatCompletionResponse{chatResponse(NewTextMessage("assistant", "child result"))}},
		Tools:     commands.NewRegistry(),
		Model:     "test-model",
		SessionID: "parent-session",
	})
	tool := NewSubAgentTool(nil)

	ctx := output.ContextWithCallID(withToolAgentConfig(context.Background(), parent.Cfg), "spawn-sync")
	result, err := tool.Execute(ctx, `{"action":"create","mode":"sync","name":"worker","prompt":"do the work"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := coretool.ResultText(result); got != `<subagent_result name="worker" type="" status="completed">
child result
</subagent_result>` {
		t.Fatalf("result = %q", got)
	}
}

func TestSubAgentCreateRequiresExecutingAgentContext(t *testing.T) {
	tool := NewSubAgentTool(nil)

	_, err := tool.Execute(context.Background(), `{"action":"create","mode":"sync","name":"worker","prompt":"work"}`)
	if err == nil || err.Error() != "subagent create requires the executing agent context" {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSubAgentCreateRequiresSpawningToolCallID(t *testing.T) {
	parent := NewAgent(Config{
		Provider: &scriptedProvider{},
		Tools:    commands.NewRegistry(),
		Model:    "test-model",
	})
	tool := NewSubAgentTool(nil)

	_, err := tool.Execute(withToolAgentConfig(context.Background(), parent.Cfg), `{"action":"create","mode":"sync","name":"worker","prompt":"work"}`)
	if err == nil || err.Error() != "subagent create requires the spawning tool call id" {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSubAgentUsesExecutingAgentContext(t *testing.T) {
	provider := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(NewTextMessage("assistant", "context result")),
	}}
	tool := NewSubAgentTool(nil)

	activeInbox := inbox.NewBuffered(DefaultInboxCapacity)
	var mu sync.Mutex
	var events []*aop.Event
	bus := eventbus.New[*aop.Event]()
	bus.Subscribe(func(event *aop.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	active := NewAgent(Config{
		Provider:  provider,
		Tools:     commands.NewRegistry(),
		Model:     "test-model",
		SessionID: "active-session",
		Inbox:     activeInbox,
		Bus:       bus,
	})

	ctx := output.ContextWithCallID(withToolAgentConfig(context.Background(), active.Cfg), "spawn-context")
	if _, err := tool.Execute(ctx, `{"action":"create","mode":"async","name":"context-worker","prompt":"work"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for activeInbox.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	completed := activeInbox.Drain()
	if len(completed) != 1 || completed[0].Meta["subagent"] != "context-worker" {
		t.Fatalf("active inbox completion = %#v", completed)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, event := range events {
		if eventKind(event) != "session.started" || event.Emitter != "context-worker" {
			continue
		}
		data := event.GetSessionStarted()
		if data == nil {
			t.Fatal("session.started payload missing")
		}
		if data.ParentSessionId != "active-session" {
			t.Fatalf("parent session = %q, want active-session", data.ParentSessionId)
		}
		if data.ParentToolCallId != "spawn-context" {
			t.Fatalf("parent tool call = %q, want spawn-context", data.ParentToolCallId)
		}
		detail, ok, err := ext.GetDelegation(event)
		if err != nil || !ok {
			t.Fatalf("delegation ext = %#v, %v, %v", detail, ok, err)
		}
		if detail.AgentName != "context-worker" || detail.Task != "work" || detail.RunMode != ext.DelegationRunBackground {
			t.Fatalf("delegation detail = %#v", detail)
		}
		return
	}
	t.Fatal("missing child session.start event")
}

func TestSubAgentToolCallCarriesDelegationExtension(t *testing.T) {
	bus := eventbus.New[*aop.Event]()
	events := make(chan *aop.Event, 1)
	bus.Subscribe(func(event *aop.Event) { events <- event })
	em := newAOPEmitter(bus, "aiscan", "parent-session", "", "", nil, 0)

	em.toolCall(&aop.ToolCall{
		Id:   "spawn-1",
		Name: "subagent",
		Kind: "function",
		Arguments: &aop.EncodedValue{
			Data:      []byte(`{"action":"create","prompt":"inspect the repository","name":"explorer","type":"reviewer","mode":"fork"}`),
			MediaType: aop.JSONMediaType,
		},
	})

	event := <-events
	detail, ok, err := ext.GetDelegation(event)
	if err != nil || !ok {
		t.Fatalf("delegation ext = %#v, %v, %v", detail, ok, err)
	}
	if detail.Task != "inspect the repository" || detail.AgentName != "explorer" || detail.AgentType != "reviewer" {
		t.Fatalf("delegation detail = %#v", detail)
	}
	if detail.RunMode != ext.DelegationRunBackground || detail.ContextMode != ext.DelegationContextFork {
		t.Fatalf("delegation modes = %#v", detail)
	}
}
