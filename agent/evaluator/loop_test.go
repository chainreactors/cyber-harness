package evaluator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
)

type evaluatorTestPrompts struct{}

func (evaluatorTestPrompts) Build(_ context.Context, input prompt.Context) prompt.Result {
	switch input.Target {
	case prompt.EvaluatorSystem:
		return prompt.Result{Prompt: "evaluate the run"}
	case prompt.EvaluatorRequest:
		return prompt.Result{Prompt: strings.Join([]string{
			input.Evaluation.Goal,
			input.Evaluation.Criteria,
			input.Evaluation.Progress,
			input.Evaluation.Trace,
		}, "\n")}
	default:
		return prompt.Result{}
	}
}

type fixedProvider struct {
	response *provider.ChatCompletionResponse
	request  *provider.ChatCompletionRequest
}

func (p *fixedProvider) Name() string { return "fixed" }

func (p *fixedProvider) ChatCompletion(_ context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.request = request
	return p.response, nil
}

func TestRunWithEvalRequiresInitialInput(t *testing.T) {
	ag := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
		Provider: &fixedProvider{},
		Model:    "test",
	})
	_, _, err := RunWithEval(context.Background(), ag, EvalLoopConfig{
		Goal:   "finish the task",
		Budget: Budget{Ceiling: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "initial input is required") {
		t.Fatalf("RunWithEval() error = %v, want missing initial input error", err)
	}
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

	bus := coreevents.New()
	var events []*aop.Event
	bus.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events = append(events, event) }))
	ag := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
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
		NewLoopConfigWithInput(verdictProvider, "test", nil, evaluatorTestPrompts{}, input, "finish the task", "1"))
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

// scriptedProvider answers with a queued response per call, repeating the last
// one once the script runs out.
type scriptedProvider struct {
	responses []*provider.ChatCompletionResponse
	calls     int
	prompts   []string
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) ChatCompletion(_ context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	for _, m := range request.Messages {
		if m.Role == "user" {
			p.prompts = append(p.prompts, provider.MessageText(m))
		}
	}
	index := p.calls
	p.calls++
	if index >= len(p.responses) {
		index = len(p.responses) - 1
	}
	return p.responses[index], nil
}

func verdictResponse(pass, cont bool, reason string) *provider.ChatCompletionResponse {
	args := fmt.Sprintf(`{"pass":%v,"continue":%v,"reason":%q,"feedback":"keep going","inherit_context":true}`, pass, cont, reason)
	return &provider.ChatCompletionResponse{Choices: []provider.Choice{{Message: &aop.Message{
		Role: "assistant",
		Content: []*aop.Content{
			{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
				Id:        "verdict",
				Name:      "verdict",
				Kind:      "function",
				Arguments: &aop.EncodedValue{Data: []byte(args), MediaType: aop.JSONMediaType},
			}}},
		},
	}}}}
}

func evalAgent(p provider.Provider) *agent.Agent {
	return agent.NewAgent(agent.Config{Loop: agent.StandardLoop{}, Provider: p, Model: "test"})
}

// The loop follows the evaluator past the old fixed three-round budget.
func TestRunWithEvalRunsWhileEvaluatorAsksToContinue(t *testing.T) {
	agentProvider := &fixedProvider{response: &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}},
	}}
	verdictProvider := &scriptedProvider{responses: []*provider.ChatCompletionResponse{
		verdictResponse(false, true, "round 1"),
		verdictResponse(false, true, "round 2"),
		verdictResponse(false, true, "round 3"),
		verdictResponse(false, true, "round 4"),
		verdictResponse(true, false, "achieved"),
	}}

	_, verdict, err := RunWithEval(context.Background(), evalAgent(agentProvider),
		NewLoopConfigWithInput(verdictProvider, "test", nil, evaluatorTestPrompts{}, agent.TextInput("scan it"), "finish the task", ""))
	if err != nil {
		t.Fatalf("RunWithEval() error = %v", err)
	}
	if verdict == nil || !verdict.Pass {
		t.Fatalf("verdict = %+v, want pass", verdict)
	}
	if verdictProvider.calls != 5 {
		t.Fatalf("evaluation rounds = %d, want 5", verdictProvider.calls)
	}
	// Later rounds must see the earlier verdicts, or "no progress" is not a
	// judgement the evaluator can make.
	if last := verdictProvider.prompts[len(verdictProvider.prompts)-1]; !strings.Contains(last, "[round 4] pass=false reason=round 4") {
		t.Fatalf("final evaluation prompt missing prior rounds:\n%s", last)
	}
}

// continue=false is how an unfinished goal ends now.
func TestRunWithEvalStopsWhenEvaluatorDeclinesToContinue(t *testing.T) {
	agentProvider := &fixedProvider{response: &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}},
	}}
	verdictProvider := &scriptedProvider{responses: []*provider.ChatCompletionResponse{
		verdictResponse(false, true, "round 1"),
		verdictResponse(false, false, "target unreachable"),
	}}

	_, verdict, err := RunWithEval(context.Background(), evalAgent(agentProvider),
		NewLoopConfigWithInput(verdictProvider, "test", nil, evaluatorTestPrompts{}, agent.TextInput("scan it"), "finish the task", ""))
	if err != nil {
		t.Fatalf("RunWithEval() error = %v", err)
	}
	if verdict == nil || verdict.Pass || verdict.Continue {
		t.Fatalf("verdict = %+v, want a non-passing stop", verdict)
	}
	if verdictProvider.calls != 2 {
		t.Fatalf("evaluation rounds = %d, want 2", verdictProvider.calls)
	}
}

// The ceiling is the backstop for an evaluator that never stops.
func TestRunWithEvalStopsAtRoundCeiling(t *testing.T) {
	agentProvider := &fixedProvider{response: &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}},
	}}
	verdictProvider := &scriptedProvider{responses: []*provider.ChatCompletionResponse{
		verdictResponse(false, true, "never satisfied"),
	}}

	_, verdict, err := RunWithEval(context.Background(), evalAgent(agentProvider),
		NewLoopConfigWithInput(verdictProvider, "test", nil, evaluatorTestPrompts{}, agent.TextInput("scan it"), "finish the task", "2"))
	if err != nil {
		t.Fatalf("RunWithEval() error = %v", err)
	}
	if verdict == nil || verdict.Pass {
		t.Fatalf("verdict = %+v, want the last non-passing verdict", verdict)
	}
	if verdictProvider.calls != 2 {
		t.Fatalf("evaluation rounds = %d, want the ceiling of 2", verdictProvider.calls)
	}
}

// Natural-language rounds reach the evaluator, which is the only thing that can
// act on them.
func TestRunWithEvalPassesRoundGuidanceToEvaluator(t *testing.T) {
	agentProvider := &fixedProvider{response: &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}},
	}}
	verdictProvider := &scriptedProvider{responses: []*provider.ChatCompletionResponse{
		verdictResponse(true, false, "achieved"),
	}}

	_, _, err := RunWithEval(context.Background(), evalAgent(agentProvider),
		NewLoopConfigWithInput(verdictProvider, "test", nil, evaluatorTestPrompts{}, agent.TextInput("scan it"), "finish the task", "尽量深入，最多三十轮"))
	if err != nil {
		t.Fatalf("RunWithEval() error = %v", err)
	}
	prompt := verdictProvider.prompts[0]
	if !strings.Contains(prompt, "User guidance on how long to keep going: 尽量深入，最多三十轮") {
		t.Fatalf("evaluation prompt missing round guidance:\n%s", prompt)
	}
	// The guidance mentions thirty rounds, so the backstop must not cut in at the
	// default of twenty.
	if !strings.Contains(prompt, "hard ceiling 30") {
		t.Fatalf("evaluation prompt ceiling not raised by guidance:\n%s", prompt)
	}
}
