package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentpkg "github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/truncate"
)

const (
	defaultMaxRetries = 3
	maxResultPreview  = 200
	maxOutputPreview  = 3000
	maxTraceSize      = 16000
)

type Config struct {
	Provider      provider.Provider
	Model         string
	MaxRetries    int
	ContextWindow int
	Logger        telemetry.Logger
	Prompts       prompt.Resolver
}

// Verdict is the evaluator's answer for one round. Pass ends the loop as a
// success; when Pass is false, Continue is the evaluator's own call on whether
// another round is still worth running. There is no fixed round budget the
// verdict has to fit into — the loop keeps going while the evaluator asks for
// it, so Continue=false is the ordinary way an unfinished goal stops.
type Verdict struct {
	Pass           bool   `json:"pass"`
	Continue       bool   `json:"continue"`
	Reason         string `json:"reason"`
	Feedback       string `json:"feedback"`
	InheritContext bool   `json:"inherit_context"`
}

// Round records a verdict the evaluator already returned. The loop feeds the
// previous rounds back in so the evaluator can see whether its own feedback
// moved anything, which is what makes an open-ended loop safe to run.
type Round struct {
	Number   int
	Pass     bool
	Reason   string
	Feedback string
}

// Request is one evaluation: the goal, the trace of the round that just ran,
// and the rounds that came before it.
type Request struct {
	Goal          string
	Criteria      string
	Messages      []*aop.Message
	Output        string
	Turns         int
	ContextTokens int
	Round         int
	Ceiling       int
	Guidance      string
	History       []Round
}

type Evaluator struct {
	cfg Config
}

func New(cfg Config) *Evaluator {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = agentpkg.ModelContextWindow(cfg.Model)
	}
	if cfg.Logger == nil {
		cfg.Logger = telemetry.NopLogger()
	}
	return &Evaluator{cfg: cfg}
}

func (e *Evaluator) Evaluate(ctx context.Context, req Request) (*Verdict, error) {
	trace := buildTrace(req.Messages, req.Output, req.Turns, req.ContextTokens, e.cfg.ContextWindow)
	if e.cfg.Prompts == nil {
		return nil, fmt.Errorf("evaluator prompt resolver is nil")
	}
	input := prompt.Context{Evaluation: prompt.EvaluationContext{
		Goal: req.Goal, Criteria: req.Criteria, Progress: buildProgress(req), Trace: trace,
	}}
	input.Target = prompt.EvaluatorSystem
	systemResult := e.cfg.Prompts.Build(ctx, input)
	input.Target = prompt.EvaluatorRequest
	requestResult := e.cfg.Prompts.Build(ctx, input)
	for _, diagnostic := range append(systemResult.Diagnostics, requestResult.Diagnostics...) {
		e.cfg.Logger.Warnf("prompt contribution=%q section=%q: %s", diagnostic.Contribution, diagnostic.Section, diagnostic.Message)
	}
	requestPrompt := requestResult.Prompt

	var lastErr error
	for attempt := 0; attempt < e.cfg.MaxRetries; attempt++ {
		v, err := e.call(ctx, systemResult.Prompt, requestPrompt)
		if err == nil {
			return v, nil
		}
		lastErr = err
		e.cfg.Logger.Warnf("evaluate attempt %d failed: %s", attempt+1, err)
		if attempt < e.cfg.MaxRetries-1 {
			select {
			case <-time.After(time.Duration(attempt+1) * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, fmt.Errorf("evaluate failed after %d attempts: %w", e.cfg.MaxRetries, lastErr)
}

var verdictTool = func() *aop.ToolDefinition {
	schema, _ := aop.JSONValue(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pass":            map[string]interface{}{"type": "boolean", "description": "task fully achieved"},
			"continue":        map[string]interface{}{"type": "boolean", "description": "when pass=false, whether another round can still make progress"},
			"reason":          map[string]interface{}{"type": "string", "description": "one-sentence summary"},
			"feedback":        map[string]interface{}{"type": "string", "description": "next step if not pass; self-contained when inherit_context=false"},
			"inherit_context": map[string]interface{}{"type": "boolean", "description": "false to discard conversation history for next round"},
		},
		"required": []string{"pass", "continue", "reason", "feedback", "inherit_context"},
	})
	return &aop.ToolDefinition{
		Type:        "function",
		Name:        "verdict",
		Description: "Submit evaluation verdict",
		InputSchema: schema,
	}
}()

func (e *Evaluator) call(ctx context.Context, systemPrompt, userPrompt string) (*Verdict, error) {
	temp := float64(0)
	resp, err := e.cfg.Provider.ChatCompletion(ctx, &provider.ChatCompletionRequest{
		Model: e.cfg.Model,
		Messages: []*aop.Message{
			provider.TextMessage("system", systemPrompt),
			provider.TextMessage("user", userPrompt),
		},
		Tools:       []*aop.ToolDefinition{verdictTool},
		MaxTokens:   2048,
		Temperature: &temp,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM call: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	for _, call := range provider.MessageToolCalls(resp.Choices[0].Message) {
		if call.Name == "verdict" {
			var v Verdict
			if err := json.Unmarshal(call.GetArguments().GetData(), &v); err != nil {
				return nil, fmt.Errorf("unmarshal verdict: %w", err)
			}
			return &v, nil
		}
	}
	return nil, fmt.Errorf("model did not call verdict tool")
}

// buildProgress shows the evaluator what its earlier rounds asked for and what
// came back, so "no progress" is a judgement it can actually make.
func buildProgress(req Request) string {
	if req.Round <= 0 {
		return ""
	}
	var sb strings.Builder
	if req.Guidance != "" {
		fmt.Fprintf(&sb, "User guidance on how long to keep going: %s\n", truncate.Clip(req.Guidance, maxResultPreview))
	}
	if req.Ceiling > 0 {
		fmt.Fprintf(&sb, "Round %d (hard ceiling %d, reached only if you never stop the loop)\n", req.Round, req.Ceiling)
	} else {
		fmt.Fprintf(&sb, "Round %d\n", req.Round)
	}
	for _, past := range req.History {
		fmt.Fprintf(&sb, "  [round %d] pass=%v reason=%s\n", past.Number, past.Pass, truncate.Clip(past.Reason, maxResultPreview))
		if past.Feedback != "" {
			fmt.Fprintf(&sb, "    you asked for: %s\n", truncate.Clip(past.Feedback, maxResultPreview))
		}
	}
	return sb.String()
}

func buildTrace(messages []*aop.Message, output string, turns, contextTokens, contextWindow int) string {
	var sb strings.Builder
	usagePct := float64(contextTokens) / float64(contextWindow) * 100
	fmt.Fprintf(&sb, "Turns: %d | Messages: %d | Context tokens: %d/%d (%.0f%%)\n", turns, len(messages), contextTokens, contextWindow, usagePct)

	toolCallCount := 0
	for _, msg := range messages {
		toolCallCount += len(provider.MessageToolCalls(msg))
	}
	fmt.Fprintf(&sb, "Tool calls: %d\n", toolCallCount)

	sb.WriteString("\nTool call sequence:\n")
	seq := 0
	for _, msg := range messages {
		for _, call := range provider.MessageToolCalls(msg) {
			seq++
			fmt.Fprintf(&sb, "  [%d] %s\n", seq, call.Name)
		}
	}

	sb.WriteString("\nAssistant summaries:\n")
	for _, msg := range messages {
		if msg.Role == "assistant" {
			if text := provider.MessageText(msg); text != "" {
				fmt.Fprintf(&sb, "- %s\n", truncate.Clip(text, maxResultPreview))
			}
		}
	}

	if output = strings.TrimSpace(output); output != "" {
		fmt.Fprintf(&sb, "\nFinal output:\n%s\n", truncate.Clip(output, maxOutputPreview))
	}
	return truncate.Clip(sb.String(), maxTraceSize)
}
