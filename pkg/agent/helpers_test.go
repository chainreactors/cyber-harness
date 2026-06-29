package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

func testBus(handler func(Event)) *eventbus.Bus[Event] {
	b := eventbus.New[Event]()
	if handler != nil {
		b.Subscribe(handler)
	}
	return b
}

type recordingTool struct {
	name   string
	output string

	mu    sync.Mutex
	calls []string
}

func (t *recordingTool) Name() string { return t.name }

func (t *recordingTool) Description() string { return "recording tool" }

func (t *recordingTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.name,
			Description: t.Description(),
			Parameters:  map[string]any{"type": "object"},
		},
	}
}

func (t *recordingTool) Execute(_ context.Context, arguments string) (commands.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, arguments)
	if strings.Contains(arguments, "fail") {
		return commands.ToolResult{}, fmt.Errorf("failed")
	}
	return commands.TextResult(t.output), nil
}

func (t *recordingTool) callsSnapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

type scriptedProvider struct {
	mu                 sync.Mutex
	responses          []*ChatCompletionResponse
	err                error
	streamEvents       []ChatCompletionStreamEvent
	streamEventBatches [][]ChatCompletionStreamEvent
	requests           []*ChatCompletionRequest
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) ChatCompletion(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, cloneRequest(req))
	if p.err != nil {
		return nil, p.err
	}
	if len(p.responses) == 0 {
		return nil, fmt.Errorf("no scripted response left")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *scriptedProvider) ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	events := append([]ChatCompletionStreamEvent(nil), p.streamEvents...)
	if len(p.streamEventBatches) > 0 {
		events = append([]ChatCompletionStreamEvent(nil), p.streamEventBatches[0]...)
		p.streamEventBatches = p.streamEventBatches[1:]
	}
	p.mu.Unlock()

	ch := make(chan ChatCompletionStreamEvent)
	go func() {
		defer close(ch)
		for _, event := range events {
			select {
			case ch <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (p *scriptedProvider) requestsSnapshot() []*ChatCompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*ChatCompletionRequest, 0, len(p.requests))
	for _, req := range p.requests {
		out = append(out, cloneRequest(req))
	}
	return out
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once

	mu       sync.Mutex
	requests []*ChatCompletionRequest
}

func (p *blockingProvider) Name() string { return "blocking" }

func (p *blockingProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	p.mu.Unlock()
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return chatResponse(NewTextMessage("assistant", "done")), nil
}

type callbackProvider struct {
	fn func(context.Context, *ChatCompletionRequest) (*ChatCompletionResponse, error)
}

func (p *callbackProvider) Name() string { return "callback" }

func (p *callbackProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return p.fn(ctx, req)
}

type retryableTimeoutError struct{}

func (retryableTimeoutError) Error() string   { return "timeout awaiting response headers" }
func (retryableTimeoutError) Timeout() bool   { return true }
func (retryableTimeoutError) Temporary() bool { return true }

type imageErrorProvider struct {
	imagesDisabled atomic.Bool
	callCount      atomic.Int32
}

func (p *imageErrorProvider) Name() string { return "image-error" }

func (p *imageErrorProvider) DisableImages() {
	p.imagesDisabled.Store(true)
}

func (p *imageErrorProvider) ChatCompletion(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	p.callCount.Add(1)
	if p.imagesDisabled.Load() || !messagesContainImages(req.Messages) {
		return chatResponse(NewTextMessage("assistant", "success without images")), nil
	}
	return nil, &APIError{StatusCode: 400, Message: "Invalid parameter: messages[5].content[1].type is not supported, unknown type: image_url"}
}

func messagesContainImages(msgs []ChatMessage) bool {
	for _, m := range msgs {
		for _, p := range m.ContentParts {
			if p.Type == "image_url" {
				return true
			}
		}
	}
	return false
}

type pushingProvider struct {
	inner  Provider
	inbox  *inbox.Buffered
	pushed bool
	push   inbox.Message
}

func (p *pushingProvider) Name() string { return "pushing" }

func (p *pushingProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if !p.pushed {
		p.pushed = true
		p.inbox.Push(p.push)
	}
	return p.inner.ChatCompletion(ctx, req)
}

type stubPseudoCommand struct {
	name   string
	output string
}

func (c *stubPseudoCommand) Name() string  { return c.name }
func (c *stubPseudoCommand) Usage() string { return c.name }
func (c *stubPseudoCommand) Execute(_ context.Context, _ []string) error {
	fmt.Fprint(commands.Output, c.output)
	return nil
}

func chatResponse(msg ChatMessage) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		Choices: []Choice{{Message: msg}},
	}
}

func cloneRequest(req *ChatCompletionRequest) *ChatCompletionRequest {
	cloned := *req
	cloned.Messages = append([]ChatMessage(nil), req.Messages...)
	cloned.Tools = append([]ToolDefinition(nil), req.Tools...)
	return &cloned
}

func hasToolMessage(messages []ChatMessage, toolCallID, contains string) bool {
	for _, msg := range messages {
		if msg.Role != "tool" || msg.ToolCallID != toolCallID || msg.Content == nil {
			continue
		}
		if strings.Contains(*msg.Content, contains) {
			return true
		}
	}
	return false
}

func containsEvent(events []EventType, want EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, 0, len(events))
	for _, event := range events {
		out = append(out, event.Type)
	}
	return out
}

func lastEvent(events []Event) Event {
	if len(events) == 0 {
		return Event{}
	}
	return events[len(events)-1]
}

func strPtr(s string) *string {
	return &s
}

func contentOf(m ChatMessage) string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func bashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}

func scannerBashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}

func assertToolResult(t *testing.T, req *ChatCompletionRequest, toolCallID, contains string) {
	t.Helper()
	if !hasToolMessage(req.Messages, toolCallID, contains) {
		var actual string
		for _, msg := range req.Messages {
			if msg.Role == "tool" && msg.ToolCallID == toolCallID && msg.Content != nil {
				actual = *msg.Content
				break
			}
		}
		t.Fatalf("tool result for %s missing %q, got: %q", toolCallID, contains, actual)
	}
}

func buildTestSystemPrompt(tools *commands.CommandRegistry, ss []skills.Skill) string {
	var sb strings.Builder
	sb.WriteString("You are a test agent.\n\n## Available Tools\n\n")
	if tools != nil {
		for _, t := range tools.Tools() {
			sb.WriteString("### " + t.Name() + "\n" + t.Description() + "\n\n")
		}
		if docs := tools.UsageDocs(); docs != "" {
			sb.WriteString("## Pseudo-Commands\n\n" + docs + "\n\n")
		}
	}
	if skillPrompt := skills.FormatForPrompt(ss); skillPrompt != "" {
		sb.WriteString(skillPrompt)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func buildTmuxTestPrompt(registry *commands.CommandRegistry) string {
	var sb strings.Builder
	sb.WriteString("You are a test agent. You have one tool: bash.\n\n## Tool: bash\n")
	for _, tool := range registry.Tools() {
		sb.WriteString(tool.Description())
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Pseudo-Commands (use via bash tool)\n\ntmux is a pseudo-command built into the bash tool. Call it like:\n  bash tool call with {\"command\": \"tmux new -d -s myname \\\"sh\\\"\"}\n  bash tool call with {\"command\": \"tmux send -t myname \\\"echo hi\\\" Enter\"}\n  bash tool call with {\"command\": \"tmux capture-pane -t myname --new\"}\n  bash tool call with {\"command\": \"tmux ls\"}\n  bash tool call with {\"command\": \"tmux kill -t myname\"}\n\ntmux usage:\n")
	sb.WriteString(registry.UsageDocs())

	sb.WriteString("\n## Rules\n\n1. Execute ONE bash call per step. Do not combine multiple steps.\n2. After send-keys, always sleep briefly (sleep 0.3) before capture-pane.\n3. Use capture-pane with --new for incremental output.\n4. Report observations at the end.\n")
	return sb.String()
}

func skipUnlessLive(t *testing.T) (*ProviderConfig, Provider) {
	t.Helper()
	apiKey := os.Getenv("TEST_API_KEY")
	baseURL := os.Getenv("TEST_BASE_URL")
	model := os.Getenv("TEST_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("set TEST_API_KEY, TEST_BASE_URL, TEST_MODEL to run live tests")
	}
	cfg := &ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: 60,
	}
	cfg, err := ResolveProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	prov, err := NewProviderFromResolved(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, prov
}

func truncateOutput(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
