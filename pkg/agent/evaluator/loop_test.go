package evaluator

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/aop"
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

func TestRunWithEvalPreservesInitialInputNoEcho(t *testing.T) {
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

	bus := eventbus.New[aop.Event]()
	var events []aop.Event
	bus.Subscribe(func(event aop.Event) { events = append(events, event) })
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
		NoEcho: true,
	}

	result, verdict, err := RunWithEval(context.Background(), ag,
		NewLoopConfigWithInput(verdictProvider, "test", nil, input, "finish the task", 1))
	if err != nil {
		t.Fatalf("RunWithEval() error = %v", err)
	}
	if result == nil || verdict == nil || !verdict.Pass {
		t.Fatalf("RunWithEval() result = %+v, verdict = %+v", result, verdict)
	}

	for _, event := range events {
		if event.Type != aop.TypeMessage {
			continue
		}
		data, decodeErr := aop.DecodeData[aop.MessageData](event)
		if decodeErr != nil {
			t.Fatalf("decode message event: %v", decodeErr)
		}
		if data.Role == "user" {
			t.Fatalf("NoEcho eval emitted user message: %+v", event)
		}
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
