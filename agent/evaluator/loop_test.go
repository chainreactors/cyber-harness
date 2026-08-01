package evaluator

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
)

type fixedProvider struct {
	response *provider.ChatCompletionResponse
	request  *provider.ChatCompletionRequest
}

func (p *fixedProvider) Name() string { return "fixed" }

func (p *fixedProvider) ChatCompletion(_ context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.request = request
	return p.response, nil
}

func TestRunWithEvalPreservesInitialInputAndEmitsCanonicalUserMessage(t *testing.T) {
	agentProvider := &fixedProvider{response: &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.NewTextMessage("assistant", "done")}},
	}}
	verdictProvider := &fixedProvider{response: &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.ChatMessage{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID:   "verdict-1",
				Type: "function",
				Function: provider.FunctionCall{
					Name:      "verdict",
					Arguments: `{"pass":true,"reason":"done","feedback":"","inherit_context":true}`,
				},
			}},
		}}},
	}}

	bus := eventbus.New[*aop.Event]()
	var events []*aop.Event
	bus.Subscribe(func(event *aop.Event) { events = append(events, event) })
	ag := agent.NewAgent(agent.Config{
		Provider:  agentProvider,
		Model:     "test",
		Bus:       bus,
		SessionID: "root-session",
	})
	input := agent.Input{
		Parts: []agent.InputPart{
			{Text: "inspect this"},
			{Image: &agent.InputImage{Base64: "AA==", MediaType: "image/png"}},
		},
	}

	result, verdict, err := RunWithEval(context.Background(), ag,
		NewLoopConfigWithInput(verdictProvider, "test", nil, input, "finish the task", 1))
	if err != nil {
		t.Fatalf("RunWithEval() error = %v", err)
	}
	if result == nil || verdict == nil || !verdict.Pass {
		t.Fatalf("RunWithEval() result = %+v, verdict = %+v", result, verdict)
	}

	var userMessages int
	for _, event := range events {
		if aop.Kind(event) == "message" && event.GetMessage().GetRole() == "user" {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("canonical user messages = %d, want 1", userMessages)
	}

	if agentProvider.request == nil {
		t.Fatal("agent provider received no request")
	}
	var userMessage *provider.ChatMessage
	for i := range agentProvider.request.Messages {
		if agentProvider.request.Messages[i].Role == "user" {
			userMessage = &agentProvider.request.Messages[i]
			break
		}
	}
	if userMessage == nil || len(userMessage.ContentParts) != 2 {
		t.Fatalf("agent user message = %+v, want text and image parts", userMessage)
	}
	if userMessage.ContentParts[0].Text != "inspect this" || userMessage.ContentParts[1].ImageURL == nil {
		t.Fatalf("agent user parts = %+v, want original multimodal input", userMessage.ContentParts)
	}
}
