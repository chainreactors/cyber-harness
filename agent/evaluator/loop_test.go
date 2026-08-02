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
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}},
	}}
	verdictProvider := &fixedProvider{response: &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: &aop.Message{
			Role: "assistant",
			Content: []*aop.Content{
				{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
					Id:   "verdict-1",
					Name: "verdict",
					Kind: "function",
					Arguments: &aop.EncodedValue{
						Data:      []byte(`{"pass":true,"reason":"done","feedback":"","inherit_context":true}`),
						MediaType: aop.JSONMediaType,
					},
				}}},
			},
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
	input := &aop.Message{
		Role: "user",
		Content: []*aop.Content{
			aop.Text("inspect this"),
			aop.Image("image/png", []byte{0x00}),
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
	var userMessage *aop.Message
	for _, m := range agentProvider.request.Messages {
		if m.Role == "user" {
			userMessage = m
			break
		}
	}
	if userMessage == nil || len(userMessage.Content) != 2 {
		t.Fatalf("agent user message = %+v, want text and image parts", userMessage)
	}
	if text := userMessage.Content[0].GetText(); text == nil || text.Text != "inspect this" {
		t.Fatalf("agent user part[0] = %+v, want original text", userMessage.Content[0])
	}
	if media := userMessage.Content[1].GetMedia(); media == nil || media.Kind != "image" {
		t.Fatalf("agent user part[1] = %+v, want original image", userMessage.Content[1])
	}
}
