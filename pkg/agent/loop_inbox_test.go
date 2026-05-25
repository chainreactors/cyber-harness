package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

func TestInboxDrainedBeforeFirstTurnLLMCall(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "ack")),
		},
	}
	ib := inbox.NewBuffered(4)
	ib.Push(inbox.NewMessage(inbox.OriginPeer, "user", "[peer] hello"))
	ib.Push(inbox.NewMessage(inbox.OriginPeer, "user", "[peer] status?"))

	result, err := Run(context.Background(), "main task", tools,
		WithProvider(llm),
		WithModel("test"),
		WithSystemPrompt("system"),
		WithInbox(ib),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result != "ack" {
		t.Fatalf("result = %q, want ack", result)
	}

	requests := llm.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	msgs := requests[0].Messages
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4 (system + 2 peer + task): %#v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Fatalf("msg[0].Role = %q, want system", msgs[0].Role)
	}
	if got := contentOf(msgs[1]); !strings.Contains(got, "[peer] hello") {
		t.Fatalf("msg[1] missing peer content: %q", got)
	}
	if got := contentOf(msgs[2]); !strings.Contains(got, "[peer] status?") {
		t.Fatalf("msg[2] missing peer content: %q", got)
	}
	if got := contentOf(msgs[3]); got != "main task" {
		t.Fatalf("msg[3] = %q, want main task", got)
	}
}

func TestInboxClosedDoesNotBlock(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "done")),
		},
	}
	ib := inbox.NewBuffered(4)
	ib.Close()

	result, err := Run(context.Background(), "task", tools,
		WithProvider(llm),
		WithModel("test"),
		WithSystemPrompt("system"),
		WithInbox(ib),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result != "done" {
		t.Fatalf("result = %q, want done", result)
	}
}

type pushingProvider struct {
	inner  provider.Provider
	inbox  *inbox.Buffered
	pushed bool
	push   inbox.Message
}

func (p *pushingProvider) Name() string { return "pushing" }

func (p *pushingProvider) ChatCompletion(ctx context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	if !p.pushed {
		p.pushed = true
		p.inbox.Push(p.push)
	}
	return p.inner.ChatCompletion(ctx, req)
}

func TestInboxDrainedBetweenTurns(t *testing.T) {
	tools := command.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})

	scripted := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:       "call_1",
					Type:     "function",
					Function: provider.FunctionCall{Name: "echo", Arguments: "{}"},
				}},
			}),
			chatResponse(provider.NewTextMessage("assistant", "final")),
		},
	}

	ib := inbox.NewBuffered(4)
	pushing := &pushingProvider{
		inner: scripted,
		inbox: ib,
		push:  inbox.NewMessage(inbox.OriginPeer, "user", "[peer] watch out for example.com"),
	}

	result, err := Run(context.Background(), "scan things", tools,
		WithProvider(pushing),
		WithModel("test"),
		WithSystemPrompt("system"),
		WithInbox(ib),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result != "final" {
		t.Fatalf("result = %q, want final", result)
	}

	requests := scripted.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}

	turn1Msgs := requests[0].Messages
	for _, m := range turn1Msgs {
		if strings.Contains(contentOf(m), "[peer] watch out for example.com") {
			t.Fatalf("turn 1 unexpectedly contains peer message: %#v", turn1Msgs)
		}
	}

	turn2Msgs := requests[1].Messages
	found := false
	for _, m := range turn2Msgs {
		if strings.Contains(contentOf(m), "[peer] watch out for example.com") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("turn 2 missing peer message: %#v", turn2Msgs)
	}
}

func TestRunWaitsWhenKeepAliveIsTrue(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "waiting")),
			chatResponse(provider.NewTextMessage("assistant", "final")),
		},
	}
	ib := inbox.NewBuffered(4)
	var keepAlive atomic.Bool
	keepAlive.Store(true)

	go func() {
		time.Sleep(20 * time.Millisecond)
		ib.Push(inbox.NewMessage(inbox.OriginTask, "user", "<task_completion>scan done</task_completion>"))
		keepAlive.Store(false)
	}()

	result, err := Run(context.Background(), "start background scan", tools,
		WithProvider(llm),
		WithModel("test"),
		WithSystemPrompt("system"),
		WithInbox(ib),
		WithKeepAlive(func() bool { return keepAlive.Load() }),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result != "final" {
		t.Fatalf("result = %q, want final", result)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	found := false
	for _, msg := range requests[1].Messages {
		if strings.Contains(contentOf(msg), "<task_completion>") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("second request missing task completion: %#v", requests[1].Messages)
	}
}

func contentOf(m provider.ChatMessage) string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}
