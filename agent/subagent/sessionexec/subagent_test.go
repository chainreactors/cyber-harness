package sessionexec

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
)

func TestSubAgentSyncReturnsResult(t *testing.T) {
	parent := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
		Provider:  &scriptedProvider{responses: []*agent.ChatCompletionResponse{chatResponse(newTextMessage("assistant", "child result"))}},
		Tools:     newTestTools(t),
		Model:     "test-model",
		SessionID: "parent-session",
	})

	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(context.Background(), parent.Cfg), operation.Invocation{CallID: "spawn-sync"})

	tool := newSubagentTestTool(t, parent.Cfg)
	result, err := tool.Execute(ctx, `{"action":"create","mode":"sync","label":"worker","prompt":"do the work"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := coretool.ResultText(result); !strings.Contains(got, `label="worker" session_id="`) || !strings.Contains(got, "child result") {
		t.Fatalf("result = %q", got)
	}
}

func TestSubagentMessagesBelongToChild(t *testing.T) {
	bus := coreevents.New()
	var messages []*aop.Event
	bus.Observe(func(ev *aop.Event) {
		if ev.GetMessage() != nil {
			messages = append(messages, ev)
		}
	})
	parent := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{}, Bus: bus, SessionID: "parent", AgentName: "parent",
		Provider: &scriptedProvider{responses: []*agent.ChatCompletionResponse{chatResponse(newTextMessage("assistant", "child result"))}},
	})
	tool := newSubagentTestTool(t, parent.Cfg)
	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
	if _, err := tool.Execute(ctx, `{"mode":"sync","label":"child","prompt":"work"}`); err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 {
		t.Fatal("missing child messages")
	}
	for _, ev := range messages {
		if ev.SessionId == "parent" || ev.Emitter != "child" {
			t.Fatalf("child message uses parent emitter: %v", ev)
		}
	}
}

func TestSubAgentCreateRequiresExecutingAgentContext(t *testing.T) {

	tool := newSubagentTestTool(t, agent.Config{})

	_, err := tool.Execute(context.Background(), `{"action":"create","mode":"sync","label":"worker","prompt":"work"}`)
	if err == nil || err.Error() != "subagent create requires the executing agent context" {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSubAgentCreateRequiresSpawningToolCallID(t *testing.T) {
	parent := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
		Provider: &scriptedProvider{},
		Tools:    newTestTools(t),
		Model:    "test-model",
	})

	tool := newSubagentTestTool(t, parent.Cfg)

	_, err := tool.Execute(agent.ContextWithToolAgentConfig(context.Background(), parent.Cfg), `{"action":"create","mode":"sync","label":"worker","prompt":"work"}`)
	if err == nil || err.Error() != "subagent create requires the spawning tool call id" {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSubAgentUsesExecutingAgentContext(t *testing.T) {
	provider := &scriptedProvider{responses: []*agent.ChatCompletionResponse{
		chatResponse(newTextMessage("assistant", "context result")),
	}}

	activeInbox := inbox.NewBuffered(agent.DefaultInboxCapacity)
	var mu sync.Mutex
	var events []*aop.Event
	bus := coreevents.New()
	bus.Observe(func(event *aop.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	active := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
		Provider:  provider,
		Tools:     newTestTools(t),
		Model:     "test-model",
		SessionID: "active-session",
		Inbox:     activeInbox,
		Bus:       bus,
	})

	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(context.Background(), active.Cfg), operation.Invocation{CallID: "spawn-context"})

	tool := newSubagentTestTool(t, active.Cfg)
	if _, err := tool.Execute(ctx, `{"action":"create","mode":"async","label":"context-worker","prompt":"work"}`); err != nil {
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
		if aop.Kind(event) != "session.started" || event.Emitter != "context-worker" {
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
	llm := &scriptedProvider{responses: []*agent.ChatCompletionResponse{
		chatResponse(newTextMessage("assistant", "background result")),
	}}

	activeInbox := inbox.NewBuffered(agent.DefaultInboxCapacity)
	active := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
		Provider:  llm,
		Tools:     newTestTools(t),
		Model:     "test-model",
		SessionID: "active-session",
		Inbox:     activeInbox,
	})

	ctx, cancel := context.WithCancel(context.Background())
	ctx = operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(ctx, active.Cfg), operation.Invocation{CallID: "spawn-bg"})
	cancel()

	tool := newSubagentTestTool(t, active.Cfg)

	if _, err := tool.Execute(ctx, `{"action":"create","mode":"async","label":"bg-worker","prompt":"work"}`); err != nil {
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
	llm := &scriptedProvider{responses: []*agent.ChatCompletionResponse{
		chatResponse(newTextMessage("assistant", "fork result")),
	}}

	activeInbox := inbox.NewBuffered(agent.DefaultInboxCapacity)
	cfg := agent.Config{Loop: agent.StandardLoop{},
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

	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(context.Background(), cfg), operation.Invocation{CallID: "spawn-fork"})

	tool := newSubagentTestTool(t, cfg)
	if _, err := tool.Execute(ctx, `{"action":"create","mode":"fork","label":"forker","prompt":"continue"}`); err != nil {
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
	llm := &scriptedProvider{responses: []*agent.ChatCompletionResponse{
		chatResponse(newTextMessage("assistant", "child result")),
	}}
	parent := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
		Provider:     llm,
		Tools:        newTestTools(t),
		Model:        "test-model",
		SessionID:    "parent-session",
		SystemPrompt: "PARENT-GUIDANCE",
	})

	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(context.Background(), parent.Cfg), operation.Invocation{CallID: "spawn-prompt"})

	tool := newSubagentTestTool(t, parent.Cfg)
	if _, err := tool.Execute(ctx, `{"action":"create","mode":"sync","label":"worker","prompt":"do the work"}`); err != nil {
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

func TestForkWaitsForWholeParallelToolBatch(t *testing.T) {
	prior := newTextMessage("user", "prior")
	batch := &aop.Message{Role: "assistant", Content: []*aop.Content{
		{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{Id: "a"}}},
		{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{Id: "b"}}},
	}}
	result := func(id string) *aop.Message {
		return &aop.Message{Role: "tool", Content: []*aop.Content{{Value: &aop.Content_ToolResult{ToolResult: &aop.ToolResult{CallId: id}}}}}
	}
	messages := []*aop.Message{prior, batch, result("a")}
	if got := truncateToLastCompleteBoundary(messages); len(got) != 1 {
		t.Fatalf("inherited incomplete batch: %v", got)
	}
	messages = append(messages, result("b"))
	got := truncateToLastCompleteBoundary(messages)
	if len(got) != 4 || got[0] == prior {
		t.Fatal("complete fork was not an isolated snapshot")
	}
}

func TestSubagentModelOverrideAndPanicUseSessionCleanup(t *testing.T) {
	for _, panicLoop := range []bool{false, true} {
		parent := agent.NewAgent(agent.Config{SessionID: "parent", Model: "parent-model", Provider: &scriptedProvider{}, Loop: taskLoop(func(_ context.Context, cfg agent.Config) (*agent.Result, error) {
			if cfg.Model != "child-model" {
				t.Errorf("model = %s", cfg.Model)
			}
			if panicLoop {
				panic("child failed")
			}
			return &agent.Result{Output: "done", Stop: agent.StopReasonCompleted}, nil
		})})
		tool := newSubagentTestTool(t, parent.Cfg)
		_, registerErr := tool.registry.Add(subagent.Subagent{Name: "worker", Prepare: func(_ context.Context, cfg agent.Config, _ subagent.Input) (agent.Config, string, error) {
			cfg.Model = "child-model"
			return cfg, "task", nil
		}})
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
		_, err := tool.Execute(ctx, `{"mode":"sync","name":"worker","prompt":"task"}`)
		if (err != nil) != panicLoop {
			t.Fatalf("panic=%v error=%v", panicLoop, err)
		}
		if got := tool.list(); got != "No subagents running." {
			t.Fatalf("session leaked: %s", got)
		}
		if parent.Model() != "parent-model" {
			t.Fatal("child changed parent model")
		}
	}
}

func TestSubagentKillAndParentCloseUseSessionState(t *testing.T) {
	for _, kill := range []bool{false, true} {
		started := make(chan struct{})
		parent := agent.NewAgent(agent.Config{SessionID: "parent", Provider: &scriptedProvider{}, Loop: taskLoop(func(ctx context.Context, _ agent.Config) (*agent.Result, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})})
		tool := newSubagentTestTool(t, parent.Cfg)
		ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
		if _, err := tool.Execute(ctx, `{"mode":"async","label":"child","prompt":"task"}`); err != nil {
			t.Fatal(err)
		}
		<-started
		if !strings.Contains(tool.list(), "child") {
			t.Fatal("active session missing")
		}
		if kill {
			tool.mu.Lock()
			var id string
			for sessionID := range tool.runs {
				id = sessionID
			}
			tool.mu.Unlock()
			if _, err := tool.kill(id); err != nil {
				t.Fatal(err)
			}
			wait, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if !parent.Cfg.Inbox.Wait(wait) {
				t.Fatal("no kill completion")
			}
		} else if err := tool.runtime.CloseSession(t.Context(), parent.SessionID(), session.SessionCloseCanceled); err != nil {
			t.Fatal(err)
		}
		if tool.list() != "No subagents running." {
			t.Fatal("child survived parent closure/cancel")
		}
		if len(parent.Cfg.Inbox.Drain()) != 1 {
			t.Fatal("completion must be delivered exactly once")
		}
	}
}
