package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/pkg/types"
)

func TestSubAgentSyncReturnsResult(t *testing.T) {
	parent := NewAgent(Config{Loop: StandardLoop{},
		Provider:  &scriptedProvider{responses: []*ChatCompletionResponse{chatResponse(NewTextMessage("assistant", "child result"))}},
		Tools:     newTestTools(t),
		Model:     "test-model",
		SessionID: "parent-session",
	})
	tool := NewSubAgentTool(nil)

	ctx := operation.ContextWithInvocation(withToolAgentConfig(context.Background(), parent.Cfg), operation.Invocation{CallID: "spawn-sync"})
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
	parent := NewAgent(Config{Loop: StandardLoop{},
		Provider: &scriptedProvider{},
		Tools:    newTestTools(t),
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
	bus := coreevents.New()
	bus.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}))
	active := NewAgent(Config{Loop: StandardLoop{},
		Provider:  provider,
		Tools:     newTestTools(t),
		Model:     "test-model",
		SessionID: "active-session",
		Inbox:     activeInbox,
		Bus:       bus,
	})

	ctx := operation.ContextWithInvocation(withToolAgentConfig(context.Background(), active.Cfg), operation.Invocation{CallID: "spawn-context"})
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
		detail, ok, err := types.GetDelegation(event)
		if err != nil || !ok {
			t.Fatalf("delegation ext = %#v, %v, %v", detail, ok, err)
		}
		if detail.AgentName != "context-worker" || detail.Task != "work" || detail.RunMode != types.DelegationRunBackground {
			t.Fatalf("delegation detail = %#v", detail)
		}
		return
	}
	t.Fatal("missing child session.start event")
}

// The tool registry cancels the invocation context the moment Execute returns,
// so a background subagent that inherited it died before its first model call.
// The context is canceled up front here to keep the race out of the test.
func TestSubAgentAsyncOutlivesInvocationContext(t *testing.T) {
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(NewTextMessage("assistant", "background result")),
	}}
	tool := NewSubAgentTool(nil)

	activeInbox := inbox.NewBuffered(DefaultInboxCapacity)
	active := NewAgent(Config{Loop: StandardLoop{},
		Provider:  llm,
		Tools:     newTestTools(t),
		Model:     "test-model",
		SessionID: "active-session",
		Inbox:     activeInbox,
	})

	ctx, cancel := context.WithCancel(context.Background())
	ctx = operation.ContextWithInvocation(withToolAgentConfig(ctx, active.Cfg), operation.Invocation{CallID: "spawn-bg"})
	cancel()

	if _, err := tool.Execute(ctx, `{"action":"create","mode":"async","name":"bg-worker","prompt":"work"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for activeInbox.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	completed := activeInbox.Drain()
	if len(completed) != 1 {
		t.Fatalf("active inbox completion = %#v", completed)
	}
	if got := completed[0].Meta["status"]; got != "completed" {
		t.Fatalf("subagent status = %v, want completed (%q)", got, provider.MessageText(completed[0].Message))
	}
}

// fork promises the parent conversation, which only survives if it is seeded
// into the child's state before Run rebuilds the request from it.
func TestSubAgentForkInheritsParentConversation(t *testing.T) {
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(NewTextMessage("assistant", "fork result")),
	}}
	tool := NewSubAgentTool(nil)

	activeInbox := inbox.NewBuffered(DefaultInboxCapacity)
	cfg := Config{Loop: StandardLoop{},
		Provider:  llm,
		Tools:     newTestTools(t),
		Model:     "test-model",
		SessionID: "active-session",
		Inbox:     activeInbox,
		Messages: []*aop.Message{
			{Role: "user", Content: []*aop.Content{aop.Text("earlier question")}},
			{Role: "assistant", Content: []*aop.Content{aop.Text("earlier answer")}},
		},
	}

	ctx := operation.ContextWithInvocation(withToolAgentConfig(context.Background(), cfg), operation.Invocation{CallID: "spawn-fork"})
	if _, err := tool.Execute(ctx, `{"action":"create","mode":"fork","name":"forker","prompt":"continue"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for activeInbox.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(activeInbox.Drain()); n != 1 {
		t.Fatalf("completion count = %d, want 1", n)
	}

	requests := llm.requestsSnapshot()
	if len(requests) == 0 {
		t.Fatal("subagent made no request")
	}
	var seen []string
	for _, m := range requests[0].Messages {
		seen = append(seen, provider.MessageText(m))
	}
	joined := strings.Join(seen, "\n")
	if !strings.Contains(joined, "earlier question") || !strings.Contains(joined, "earlier answer") {
		t.Fatalf("fork request did not inherit the parent conversation: %q", joined)
	}
}

// Every mode runs the parent's system prompt: it carries the environment, tool
// and skill guidance, and before this only fork saw it, through the copied
// conversation. A sync child never inherits the conversation, so a missing prompt
// left it with nothing but the bare task.
func TestSubAgentInheritsParentSystemPrompt(t *testing.T) {
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(NewTextMessage("assistant", "child result")),
	}}
	parent := NewAgent(Config{Loop: StandardLoop{},
		Provider:     llm,
		Tools:        newTestTools(t),
		Model:        "test-model",
		SessionID:    "parent-session",
		SystemPrompt: "PARENT-GUIDANCE",
	})
	tool := NewSubAgentTool(nil)

	ctx := operation.ContextWithInvocation(withToolAgentConfig(context.Background(), parent.Cfg), operation.Invocation{CallID: "spawn-prompt"})
	if _, err := tool.Execute(ctx, `{"action":"create","mode":"sync","name":"worker","prompt":"do the work"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	requests := llm.requestsSnapshot()
	if len(requests) == 0 {
		t.Fatal("subagent made no request")
	}
	first := requests[0].Messages
	if len(first) == 0 || first[0].Role != "system" || !strings.Contains(provider.MessageText(first[0]), "PARENT-GUIDANCE") {
		t.Fatalf("child first message = %+v, want the parent system prompt", first)
	}
}

func TestSubAgentToolCallCarriesDelegationExtension(t *testing.T) {
	bus := coreevents.New()
	events := make(chan *aop.Event, 1)
	bus.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events <- event }))
	em := newAOPEmitter(bus, "cyber", "parent-session", "", "", nil, 0)

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
	detail, ok, err := types.GetDelegation(event)
	if err != nil || !ok {
		t.Fatalf("delegation ext = %#v, %v, %v", detail, ok, err)
	}
	if detail.Task != "inspect the repository" || detail.AgentName != "explorer" || detail.AgentType != "reviewer" {
		t.Fatalf("delegation detail = %#v", detail)
	}
	if detail.RunMode != types.DelegationRunBackground || detail.ContextMode != types.DelegationContextFork {
		t.Fatalf("delegation modes = %#v", detail)
	}
}
