package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
)

func TestResolveProviderPresets(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		apiKey       string
		wantProtocol string
		wantBaseURL  string
	}{
		{name: "openai", provider: "openai", apiKey: "key", wantProtocol: "openai", wantBaseURL: "https://api.openai.com/v1"},
		{name: "anthropic", provider: "anthropic", apiKey: "key", wantProtocol: "anthropic", wantBaseURL: "https://api.anthropic.com/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := Resolve(&ProviderConfig{Provider: tt.provider, APIKey: tt.apiKey})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if resolved.Provider != tt.wantProtocol || resolved.BaseURL != tt.wantBaseURL {
				t.Fatalf("Resolve() = provider %q, base_url %q; want %q, %q", resolved.Provider, resolved.BaseURL, tt.wantProtocol, tt.wantBaseURL)
			}
		})
	}
}

func TestResolveRejectsUnsupportedProvider(t *testing.T) {
	for _, name := range []string{"custom", "bogus-vendor"} {
		_, err := Resolve(&ProviderConfig{Provider: name, BaseURL: "https://gateway.example/v1", APIKey: "key"})
		if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
			t.Fatalf("Resolve(%q) error = %v", name, err)
		}
	}
}

func TestResolveVendorAliases(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		baseURL      string
		wantProvider string
		wantBaseURL  string
	}{
		{name: "deepseek default endpoint", provider: "deepseek", wantProvider: "deepseek", wantBaseURL: "https://api.deepseek.com/v1"},
		{name: "deepseek keeps explicit base_url", provider: "deepseek", baseURL: "https://api.deepseek.com", wantProvider: "deepseek", wantBaseURL: "https://api.deepseek.com"},
		{name: "ollama default endpoint", provider: "ollama", wantProvider: "ollama", wantBaseURL: "http://localhost:11434/v1"},
		{name: "openrouter default endpoint", provider: "openrouter", wantProvider: "openrouter", wantBaseURL: "https://openrouter.ai/api/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := Resolve(&ProviderConfig{Provider: tt.provider, BaseURL: tt.baseURL, APIKey: "key"})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if resolved.Provider != tt.wantProvider || resolved.BaseURL != tt.wantBaseURL {
				t.Fatalf("Resolve() = provider %q, base_url %q; want %q, %q", resolved.Provider, resolved.BaseURL, tt.wantProvider, tt.wantBaseURL)
			}
			prov, err := NewProviderFromResolved(resolved)
			if err != nil {
				t.Fatalf("NewProviderFromResolved() error = %v", err)
			}
			if _, ok := prov.(*OpenAIProvider); !ok {
				t.Fatalf("vendor alias resolved to %T, want *OpenAIProvider", prov)
			}
		})
	}
}

func TestResolveUsesBaseURL(t *testing.T) {
	cfg, err := Resolve(&ProviderConfig{
		Provider: "openai",
		BaseURL:  "http://localhost:11434/v1",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if cfg.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestResolveRejectsNegativeModelLimits(t *testing.T) {
	for _, cfg := range []ProviderConfig{
		{MaxTokens: -1},
		{ContextWindow: -1},
	} {
		if _, err := Resolve(&cfg); err == nil {
			t.Fatalf("Resolve(%+v) accepted a negative model limit", cfg)
		}
	}
}

func TestResolvePreservesExplicitBaseURL(t *testing.T) {
	cfg, err := Resolve(&ProviderConfig{
		Provider: "openai",
		BaseURL:  "http://base-url.example/v1",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if cfg.BaseURL != "http://base-url.example/v1" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestInferFromBaseURLDefaultsToOpenAI(t *testing.T) {
	for _, baseURL := range []string{
		"https://api.openai.com/v1",
		"https://api.deepseek.com/v1",
		"https://openrouter.ai/api/v1",
		"http://localhost:11434/v1",
		"https://llm.example.com/v1",
	} {
		if got := InferFromBaseURL(baseURL); got != "openai" {
			t.Fatalf("InferFromBaseURL(%q) = %q, want openai", baseURL, got)
		}
	}
}

func TestResolveExplicitProvider(t *testing.T) {
	cfg, err := Resolve(&ProviderConfig{
		Provider: "anthropic",
		BaseURL:  "https://my-proxy.example.com/v1",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if cfg.Provider != "anthropic" {
		t.Fatalf("Provider = %q, want anthropic", cfg.Provider)
	}
}

func TestNewProviderExplicitAnthropic(t *testing.T) {
	p, err := NewProvider(&ProviderConfig{
		Provider: "anthropic",
		BaseURL:  "https://my-proxy.example.com/v1",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if _, ok := p.(*AnthropicProvider); !ok {
		t.Fatalf("provider type = %T, want *AnthropicProvider", p)
	}
}

func TestAnthropicProviderChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Fatal("missing anthropic-version header")
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization header = %q, want empty", got)
		}

		var body struct {
			Model     string `json:"model"`
			System    string `json:"system"`
			MaxTokens int    `json:"max_tokens"`
			Tools     []struct {
				Type        string                 `json:"type"`
				Name        string                 `json:"name"`
				InputSchema map[string]interface{} `json:"input_schema"`
			} `json:"tools"`
			Messages []struct {
				Role    string                   `json:"role"`
				Content []map[string]interface{} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "claude-test" {
			t.Fatalf("model = %q, want claude-test", body.Model)
		}
		if body.System != "system prompt" {
			t.Fatalf("system = %q, want system prompt", body.System)
		}
		if body.MaxTokens != defaultAnthropicMaxToken {
			t.Fatalf("max_tokens = %d, want %d", body.MaxTokens, defaultAnthropicMaxToken)
		}
		if len(body.Tools) != 1 || body.Tools[0].Name != "bash" {
			t.Fatalf("tools = %#v, want bash tool", body.Tools)
		}
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Fatalf("messages = %#v, want one user message", body.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"scan ready"},{"type":"tool_use","id":"toolu_1","name":"bash","input":{"command":"id"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer server.Close()

	p, err := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic",
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewAnthropicProvider() error = %v", err)
	}

	resp, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "claude-test",
		Messages: []*aop.Message{
			TextMessage("system", "system prompt"),
			TextMessage("user", "scan localhost"),
		},
		Tools: []*aop.ToolDefinition{{
			Type: "function",
			Name: "bash",
			InputSchema: &aop.EncodedValue{
				Data:      []byte(`{"type":"object"}`),
				MediaType: aop.JSONMediaType,
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg.Role != "assistant" || MessageText(msg) != "scan ready" {
		t.Fatalf("message = %#v, want assistant text", msg)
	}
	calls := MessageToolCalls(msg)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(calls))
	}
	if got := string(calls[0].Arguments.Data); got != `{"command":"id"}` {
		t.Fatalf("tool arguments = %q, want command JSON", got)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %#v, want total 15", resp.Usage)
	}
}

func TestAnthropicProviderParsesThinkingBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"internal reasoning"},{"type":"text","text":"visible answer"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":20}}`)
	}))
	defer server.Close()

	p, err := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic",
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewAnthropicProvider() error = %v", err)
	}

	resp, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []*aop.Message{TextMessage("user", "think hard")},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	msg := resp.Choices[0].Message
	if got := MessageText(msg); got != "visible answer" {
		t.Fatalf("content = %q, want 'visible answer'", got)
	}
	if got := MessageReasoning(msg); got != "internal reasoning" {
		t.Fatalf("reasoning = %q, want 'internal reasoning'", got)
	}
}

func TestOpenAIProviderChatCompletionStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"reasoning_content":"think"},"finish_reason":""}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"hel"},"finish_reason":""}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer server.Close()

	p, err := NewOpenAIProvider(&ProviderConfig{
		Provider: "test",
		BaseURL:  server.URL + "/v1",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	ch, err := p.ChatCompletionStream(context.Background(), &ChatCompletionRequest{Model: "test"})
	if err != nil {
		t.Fatalf("ChatCompletionStream() error = %v", err)
	}
	var text string
	var reasoning string
	var done bool
	for event := range ch {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		if delta := event.MessageDelta; delta != nil {
			text += delta.GetText()
			reasoning += delta.GetReasoning()
		}
		if event.Done {
			done = true
		}
	}
	if text != "hello" {
		t.Fatalf("text = %q, want hello", text)
	}
	if reasoning != "think" {
		t.Fatalf("reasoning = %q, want think", reasoning)
	}
	if !done {
		t.Fatal("missing done event")
	}
}

func TestAnthropicProviderChatCompletionStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want text/event-stream", got)
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !body.Stream {
			t.Fatal("stream = false, want true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"usage\":{\"input_tokens\":7}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		fmt.Fprint(w, "event: content_block_start\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"bash\",\"input\":{}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"id\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":5}}\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	p, err := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic",
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewAnthropicProvider() error = %v", err)
	}

	ch, err := p.ChatCompletionStream(context.Background(), &ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []*aop.Message{TextMessage("user", "scan localhost")},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream() error = %v", err)
	}

	var role string
	var text string
	var done bool
	var finishReason string
	var usage *aop.TokenUsage
	type toolCallAcc struct {
		id        string
		name      string
		arguments string
	}
	toolCalls := make(map[uint32]*toolCallAcc)
	for event := range ch {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		if event.Role != "" {
			role = event.Role
		}
		if delta := event.MessageDelta; delta != nil {
			text += delta.GetText()
		}
		for _, delta := range event.ToolDeltas {
			tc := toolCalls[delta.Index]
			if tc == nil {
				tc = &toolCallAcc{}
				toolCalls[delta.Index] = tc
			}
			if delta.CallId != "" {
				tc.id = delta.CallId
			}
			if delta.Name != "" {
				tc.name = delta.Name
			}
			tc.arguments += string(delta.Arguments)
		}
		if event.FinishReason != "" {
			finishReason = event.FinishReason
		}
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.Done {
			done = true
		}
	}
	if role != "assistant" {
		t.Fatalf("role = %q, want assistant", role)
	}
	if text != "hi" {
		t.Fatalf("text = %q, want hi", text)
	}
	if finishReason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", finishReason)
	}
	tc := toolCalls[1]
	if tc == nil || tc.id != "toolu_1" || tc.name != "bash" {
		t.Fatalf("tool call = %#v, want bash tool call", tc)
	}
	if tc.arguments != `{"command":"id"}` {
		t.Fatalf("tool call arguments = %q, want command JSON", tc.arguments)
	}
	if usage == nil || usage.TotalTokens != 12 {
		t.Fatalf("usage = %#v, want total 12", usage)
	}
	if !done {
		t.Fatal("missing done event")
	}
}

func TestAnthropicProviderStreamRejectsPrematureEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n")
		// Anthropic requires message_stop; closing here must be an error.
	}))
	defer server.Close()

	p, err := NewAnthropicProvider(&ProviderConfig{Provider: "anthropic", BaseURL: server.URL + "/v1", Timeout: 5})
	if err != nil {
		t.Fatalf("NewAnthropicProvider() error = %v", err)
	}
	events, err := p.ChatCompletionStream(context.Background(), &ChatCompletionRequest{Model: "test"})
	if err != nil {
		t.Fatalf("ChatCompletionStream() error = %v", err)
	}

	var streamErr error
	var done bool
	for event := range events {
		if event.Err != nil {
			streamErr = event.Err
		}
		done = done || event.Done
	}
	if !errors.Is(streamErr, ErrStreamIncomplete) {
		t.Fatalf("stream error = %v, want ErrStreamIncomplete", streamErr)
	}
	if done {
		t.Fatal("premature EOF was reported as a completed stream")
	}
}

func TestAnthropicErrorToolResultIsMarkedOnWire(t *testing.T) {
	p := &AnthropicProvider{config: &ProviderConfig{BaseURL: "https://api.anthropic.com/v1"}}
	result := ToolResultMessage("call-truncated", &aop.ToolResult{
		Output:  []*aop.Content{aop.Text(truncatedToolResultForTest)},
		IsError: true,
	})
	body, err := p.marshalRequest(&ChatCompletionRequest{
		Model: "test",
		Messages: []*aop.Message{
			{Role: "assistant", Content: []*aop.Content{
				{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
					Id:   "call-truncated",
					Name: "write",
					Kind: "function",
					Arguments: &aop.EncodedValue{
						Data:      []byte(`{}`),
						MediaType: aop.JSONMediaType,
					},
				}}},
			}},
			result,
		},
	})
	if err != nil {
		t.Fatalf("marshalRequest() error = %v", err)
	}

	var payload struct {
		Messages []struct {
			Content []struct {
				Type    string `json:"type"`
				IsError bool   `json:"is_error"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if len(payload.Messages) != 2 || len(payload.Messages[1].Content) != 1 {
		t.Fatalf("messages = %#v", payload.Messages)
	}
	block := payload.Messages[1].Content[0]
	if block.Type != "tool_result" || !block.IsError {
		t.Fatalf("tool result block = %#v, want is_error=true", block)
	}
}

const truncatedToolResultForTest = "model output was truncated"

func TestOpenAIProviderChatCompletionBodyTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":`)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	p, err := NewOpenAIProvider(&ProviderConfig{
		Provider: "test",
		BaseURL:  server.URL + "/v1",
		Timeout:  1,
	})
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	start := time.Now()
	_, err = p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want timeout")
	}
	if !errors.Is(err, ErrCallTimeout) {
		t.Fatalf("ChatCompletion() error = %v, want ErrCallTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("ChatCompletion() took %s, want timeout near 1s", elapsed)
	}
}

func TestOpenAIProviderChatCompletionStreamErrorBodyTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "partial error")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	p, err := NewOpenAIProvider(&ProviderConfig{
		Provider: "test",
		BaseURL:  server.URL + "/v1",
		Timeout:  1,
	})
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	start := time.Now()
	_, err = p.ChatCompletionStream(context.Background(), &ChatCompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("ChatCompletionStream() error = nil, want timeout")
	}
	if !errors.Is(err, ErrCallTimeout) {
		t.Fatalf("ChatCompletionStream() error = %v, want ErrCallTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("ChatCompletionStream() took %s, want timeout near 1s", elapsed)
	}
}

// Compile-time capability parity guard: every optional capability the app
// asserts at runtime must be satisfied by both providers.
var (
	_ interface {
		ListModels(context.Context) ([]string, error)
	} = (*OpenAIProvider)(nil)
	_ interface {
		ListModels(context.Context) ([]string, error)
	} = (*AnthropicProvider)(nil)
	_ StreamingProvider            = (*OpenAIProvider)(nil)
	_ StreamingProvider            = (*AnthropicProvider)(nil)
	_ WebSearchProvider            = (*OpenAIProvider)(nil)
	_ WebSearchProvider            = (*AnthropicProvider)(nil)
	_ interface{ DisableImages() } = (*OpenAIProvider)(nil)
	_ interface{ DisableImages() } = (*AnthropicProvider)(nil)
)

// toolDef builds an aop.ToolDefinition for tests.
func toolDef(name, description string, parameters map[string]interface{}) *aop.ToolDefinition {
	def := &aop.ToolDefinition{Type: "function", Name: name, Description: description}
	if parameters != nil {
		schema, err := aop.JSONValue(parameters)
		if err == nil {
			def.InputSchema = schema
		}
	}
	return def
}

// newToolCall builds an aop.ToolCall for tests.
func newToolCall(id, name, arguments string) *aop.ToolCall {
	return &aop.ToolCall{
		Id:        id,
		Name:      name,
		Kind:      "function",
		Arguments: &aop.EncodedValue{Data: []byte(arguments), MediaType: aop.JSONMediaType},
	}
}

// assistantToolCallMsg builds an assistant aop message carrying tool calls.
func assistantToolCallMsg(calls ...*aop.ToolCall) *aop.Message {
	msg := &aop.Message{Role: "assistant"}
	for _, call := range calls {
		msg.Content = append(msg.Content, &aop.Content{Value: &aop.Content_ToolCall{ToolCall: call}})
	}
	return msg
}

// toolResultMsg builds a tool-role aop message carrying a text tool result.
func toolResultMsg(callID, text string) *aop.Message {
	return ToolResultMessage(callID, &aop.ToolResult{Output: []*aop.Content{aop.Text(text)}})
}

// =============================================================================
// Tests from cache_test.go (original)
// =============================================================================

func TestLiveCacheMetrics(t *testing.T) {
	apiKey := os.Getenv("TEST_API_KEY")
	baseURL := os.Getenv("TEST_BASE_URL")
	model := os.Getenv("TEST_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("set TEST_API_KEY, TEST_BASE_URL, TEST_MODEL to run live cache test")
	}

	cfg := &ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: 60,
	}
	cfg, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	prov, err := NewProviderFromResolved(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Build a substantial system prompt to exceed provider's minimum cache threshold
	systemPrompt := "You are a helpful security analysis assistant. " + strings.Repeat("You have deep expertise in vulnerability assessment, penetration testing, and secure code review. ", 40)

	sysMsg := TextMessage("system", systemPrompt)
	userMsg1 := TextMessage("user", "What is 2+2? Answer in one word.")

	// Turn 1
	req1 := &ChatCompletionRequest{
		Model:          model,
		Messages:       []*aop.Message{sysMsg, userMsg1},
		MaxTokens:      50,
		CacheRetention: CacheShort,
		SessionID:      "test-cache-session-001",
	}

	ctx := context.Background()
	resp1, err := prov.ChatCompletion(ctx, req1)
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}

	t.Logf("=== Turn 1 ===")
	t.Logf("Response: %s", MessageText(resp1.Choices[0].Message))
	logUsage(t, resp1.Usage)

	// Turn 2 — same prefix, new user message
	assistantReply := resp1.Choices[0].Message
	userMsg2 := TextMessage("user", "What is 3+3? Answer in one word.")

	req2 := &ChatCompletionRequest{
		Model:          model,
		Messages:       []*aop.Message{sysMsg, userMsg1, assistantReply, userMsg2},
		MaxTokens:      50,
		CacheRetention: CacheShort,
		SessionID:      "test-cache-session-001",
	}

	resp2, err := prov.ChatCompletion(ctx, req2)
	if err != nil {
		t.Fatalf("turn 2 failed: %v", err)
	}

	t.Logf("=== Turn 2 ===")
	t.Logf("Response: %s", MessageText(resp2.Choices[0].Message))
	logUsage(t, resp2.Usage)

	// Turn 3 — even longer prefix
	assistantReply2 := resp2.Choices[0].Message
	userMsg3 := TextMessage("user", "What is 4+4? Answer in one word.")

	req3 := &ChatCompletionRequest{
		Model:          model,
		Messages:       []*aop.Message{sysMsg, userMsg1, assistantReply, userMsg2, assistantReply2, userMsg3},
		MaxTokens:      50,
		CacheRetention: CacheShort,
		SessionID:      "test-cache-session-001",
	}

	resp3, err := prov.ChatCompletion(ctx, req3)
	if err != nil {
		t.Fatalf("turn 3 failed: %v", err)
	}

	t.Logf("=== Turn 3 ===")
	t.Logf("Response: %s", MessageText(resp3.Choices[0].Message))
	logUsage(t, resp3.Usage)

	// Summary
	t.Logf("\n=== Cache Summary ===")
	for i, resp := range []*ChatCompletionResponse{resp1, resp2, resp3} {
		if resp.Usage != nil {
			ratio := 0.0
			if resp.Usage.InputTokens > 0 {
				ratio = float64(resp.Usage.Detail["cache_read"]) / float64(resp.Usage.InputTokens) * 100
			}
			t.Logf("Turn %d: prompt=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
				i+1, resp.Usage.InputTokens, resp.Usage.Detail["cache_read"], resp.Usage.Detail["cache_write"], ratio)
		}
	}
}

func logUsage(t *testing.T, u *aop.TokenUsage) {
	if u == nil {
		t.Log("Usage: nil")
		return
	}
	raw, _ := json.Marshal(u)
	t.Logf("Usage: %s", raw)
	t.Logf("  prompt=%d completion=%d total=%d cache_read=%d cache_write=%d",
		u.InputTokens, u.OutputTokens, u.TotalTokens, u.Detail["cache_read"], u.Detail["cache_write"])
}

// Also test that the marshalRequest correctly adds cache_control for Anthropic
func TestAnthropicMarshalCacheControl(t *testing.T) {
	cfg := &ProviderConfig{
		Provider: "anthropic",
		BaseURL:  "https://api.anthropic.com/v1",
		APIKey:   "test-key",
		Timeout:  60,
	}
	prov, err := NewAnthropicProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}

	sysMsg := TextMessage("system", "You are a helpful assistant.")
	userMsg := TextMessage("user", "Hello")

	// Without cache
	req := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []*aop.Message{sysMsg, userMsg},
		CacheRetention: CacheNone,
	}
	data, err := prov.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cache_control") {
		t.Error("CacheNone should NOT include cache_control")
	}

	// With cache
	req.CacheRetention = CacheShort
	data, err = prov.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	// Check system prompt has cache_control
	sys, ok := parsed["system"].([]interface{})
	if !ok {
		t.Fatalf("system should be array when cache enabled, got %T", parsed["system"])
	}
	sysBlock := sys[0].(map[string]interface{})
	if _, ok := sysBlock["cache_control"]; !ok {
		t.Error("system prompt block should have cache_control")
	}

	// Check last user message has cache_control
	msgs := parsed["messages"].([]interface{})
	lastMsg := msgs[len(msgs)-1].(map[string]interface{})
	content := lastMsg["content"].([]interface{})
	lastBlock := content[len(content)-1].(map[string]interface{})
	if _, ok := lastBlock["cache_control"]; !ok {
		t.Error("last user message block should have cache_control")
	}

	t.Logf("Marshaled JSON (cache enabled):\n%s", string(data))
}

func TestAnthropicMarshalCacheControlWithTools(t *testing.T) {
	cfg := &ProviderConfig{
		Provider: "anthropic",
		BaseURL:  "https://api.anthropic.com/v1",
		APIKey:   "test-key",
		Timeout:  60,
	}
	prov, err := NewAnthropicProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}

	sysMsg := TextMessage("system", "You are a helpful assistant.")
	userMsg := TextMessage("user", "Hello")

	tools := []*aop.ToolDefinition{
		toolDef("tool_a", "first tool", nil),
		toolDef("tool_b", "second tool", nil),
	}

	req := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []*aop.Message{sysMsg, userMsg},
		Tools:          tools,
		CacheRetention: CacheShort,
	}

	data, err := prov.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	// Check last tool has cache_control, first does not
	toolList := parsed["tools"].([]interface{})
	firstTool := toolList[0].(map[string]interface{})
	lastTool := toolList[len(toolList)-1].(map[string]interface{})

	if _, ok := firstTool["cache_control"]; ok {
		t.Error("first tool should NOT have cache_control")
	}
	if _, ok := lastTool["cache_control"]; !ok {
		t.Error("last tool should have cache_control")
	}

	t.Logf("Tools JSON: %s", mustJSON(toolList))
}

func TestOpenAIMarshalCacheKey(t *testing.T) {
	req := &ChatCompletionRequest{
		Model:          "gpt-4o",
		Messages:       []*aop.Message{TextMessage("user", "Hello")},
		CacheRetention: CacheShort,
		SessionID:      "sess-123",
	}

	data, err := marshalOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed["prompt_cache_key"] != "sess-123" {
		t.Errorf("expected prompt_cache_key=sess-123, got %v", parsed["prompt_cache_key"])
	}
	if _, ok := parsed["prompt_cache_retention"]; ok {
		t.Error("CacheShort should NOT include prompt_cache_retention")
	}

	// CacheLong
	req.CacheRetention = CacheLong
	data, err = marshalOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var parsedLong map[string]interface{}
	json.Unmarshal(data, &parsedLong)
	if parsedLong["prompt_cache_retention"] != "24h" {
		t.Errorf("CacheLong should set prompt_cache_retention=24h, got %v", parsedLong["prompt_cache_retention"])
	}

	// CacheNone
	req.CacheRetention = CacheNone
	data, err = marshalOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var parsedNone map[string]interface{}
	json.Unmarshal(data, &parsedNone)
	if _, ok := parsedNone["prompt_cache_key"]; ok {
		t.Error("CacheNone should NOT include prompt_cache_key")
	}
}

func TestOpenAIStreamRequestIncludesUsage(t *testing.T) {
	req := &ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []*aop.Message{TextMessage("user", "Hello")},
		Stream:   true,
	}

	data, err := marshalOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	opts, ok := parsed["stream_options"].(map[string]interface{})
	if !ok {
		t.Fatalf("stream_options missing: %v", parsed)
	}
	if opts["include_usage"] != true {
		t.Fatalf("include_usage = %v, want true", opts["include_usage"])
	}
}

func TestUsageUnmarshalDeepSeek(t *testing.T) {
	raw := `{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}`
	var u openAIUsage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.CacheReadTokens != 80 {
		t.Errorf("CacheReadTokens: want 80, got %d", u.CacheReadTokens)
	}
	if u.CacheWriteTokens != 20 {
		t.Errorf("CacheWriteTokens: want 20, got %d", u.CacheWriteTokens)
	}
}

func TestUsageUnmarshalOpenAI(t *testing.T) {
	raw := `{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":10}}`
	var u openAIUsage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.CacheReadTokens != 60 {
		t.Errorf("CacheReadTokens: want 60, got %d", u.CacheReadTokens)
	}
	if u.CacheWriteTokens != 10 {
		t.Errorf("CacheWriteTokens: want 10, got %d", u.CacheWriteTokens)
	}
}

func TestUsageUnmarshalNoCacheFields(t *testing.T) {
	raw := `{"prompt_tokens":50,"completion_tokens":10,"total_tokens":60}`
	var u openAIUsage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 50 || u.CompletionTokens != 10 || u.TotalTokens != 60 {
		t.Errorf("basic fields wrong: %+v", u)
	}
	if u.CacheReadTokens != 0 || u.CacheWriteTokens != 0 {
		t.Error("cache tokens should be 0 when not present")
	}
}

func TestConvertAnthropicUsageCacheFields(t *testing.T) {
	u := convertAnthropicUsage(&anthropicUsage{
		InputTokens:              100,
		OutputTokens:             20,
		CacheCreationInputTokens: 50,
		CacheReadInputTokens:     30,
	})
	if u.InputTokens != 180 {
		t.Errorf("InputTokens: want 180, got %d", u.InputTokens)
	}
	if u.Detail["cache_read"] != 30 {
		t.Errorf("cache_read: want 30, got %d", u.Detail["cache_read"])
	}
	if u.Detail["cache_write"] != 50 {
		t.Errorf("cache_write: want 50, got %d", u.Detail["cache_write"])
	}
	fmt.Println("usage:", mustJSON(u))
}

// =============================================================================
// Tests from cache_breakpoint_test.go
// =============================================================================

// TestCacheBreakpointPlacementMultiTurn verifies that cache_control markers
// are placed correctly as the conversation grows across turns, and that the
// prefix is never broken.
func TestCacheBreakpointPlacementMultiTurn(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sysMsg := TextMessage("system", "You are a helpful assistant.")
	tools := []*aop.ToolDefinition{
		toolDef("read", "read file", nil),
		toolDef("write", "write file", nil),
	}

	// --- Turn 1: system + user1 ---
	user1 := TextMessage("user", "Hello turn 1")
	req1 := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []*aop.Message{sysMsg, user1},
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	j1 := mustMarshal(t, prov, req1)
	p1 := mustParse(t, j1)

	t.Log("=== Turn 1 ===")
	assertSystemCached(t, p1)
	assertLastToolCached(t, p1)
	assertLastUserCached(t, p1)
	assertNoCacheOnRole(t, p1, "assistant")
	t.Log(prettyJSON(p1))

	// --- Turn 2: system + user1 + assistant1 + user2 ---
	assistant1 := TextMessage("assistant", "Hi there")
	user2 := TextMessage("user", "Hello turn 2")
	req2 := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []*aop.Message{sysMsg, user1, assistant1, user2},
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	j2 := mustMarshal(t, prov, req2)
	p2 := mustParse(t, j2)

	t.Log("=== Turn 2 ===")
	assertSystemCached(t, p2)
	assertLastToolCached(t, p2)

	msgs2 := p2["messages"].([]interface{})
	// user1 should NOT have cache_control (it's no longer the last user msg)
	firstUserBlocks := msgs2[0].(map[string]interface{})["content"].([]interface{})
	firstUserLastBlock := firstUserBlocks[len(firstUserBlocks)-1].(map[string]interface{})
	if _, ok := firstUserLastBlock["cache_control"]; ok {
		t.Error("Turn 2: user1 should NOT have cache_control (only last user msg gets it)")
	}

	// user2 (last user msg) SHOULD have cache_control
	lastUserMsg := findLastMsgWithRole(msgs2, "user")
	lastUserBlocks := lastUserMsg["content"].([]interface{})
	lastBlock := lastUserBlocks[len(lastUserBlocks)-1].(map[string]interface{})
	if _, ok := lastBlock["cache_control"]; !ok {
		t.Error("Turn 2: user2 (last user) should have cache_control")
	}

	// Verify prefix stability: system and tools are identical between turn 1 and turn 2
	assertPrefixStable(t, "system", p1, p2)
	assertPrefixStable(t, "tools", p1, p2)

	// --- Turn 3: with tool_result (maps to user role) ---
	tc := newToolCall("call_1", "read", `{"path":"test.go"}`)
	assistant2 := assistantToolCallMsg(tc)
	toolResult := toolResultMsg("call_1", "file contents here")
	user3 := TextMessage("user", "Now what?")

	req3 := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []*aop.Message{sysMsg, user1, assistant1, user2, assistant2, toolResult, user3},
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	j3 := mustMarshal(t, prov, req3)
	p3 := mustParse(t, j3)

	t.Log("=== Turn 3 (with tool_result) ===")
	assertSystemCached(t, p3)
	assertLastToolCached(t, p3)

	// After mergeConsecutive, tool_result (user role) and user3 (user role) may merge.
	// The cache_control should be on the LAST content block of the LAST user message.
	msgs3 := p3["messages"].([]interface{})
	lastUser3 := findLastMsgWithRole(msgs3, "user")
	blocks3 := lastUser3["content"].([]interface{})
	finalBlock := blocks3[len(blocks3)-1].(map[string]interface{})
	if _, ok := finalBlock["cache_control"]; !ok {
		t.Error("Turn 3: last content block of last user message should have cache_control")
	}

	// Earlier user messages should NOT have cache_control
	for i := 0; i < len(msgs3)-1; i++ {
		msg := msgs3[i].(map[string]interface{})
		if msg["role"] != "user" {
			continue
		}
		blocks := msg["content"].([]interface{})
		for _, b := range blocks {
			block := b.(map[string]interface{})
			if _, ok := block["cache_control"]; ok {
				t.Errorf("Turn 3: non-last user message at index %d should NOT have cache_control", i)
			}
		}
	}
}

// TestCacheBreakpointSubagentFork simulates what happens in fork mode:
// child inherits parent messages + adds its own prompt.
func TestCacheBreakpointSubagentFork(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sysMsg := TextMessage("system", "You are a security scanner.")
	tools := []*aop.ToolDefinition{
		toolDef("scan", "scan target", nil),
	}

	// Parent conversation: system + user1 + assistant1 + user2 + assistant2
	user1 := TextMessage("user", "Scan target.com")
	assistant1 := TextMessage("assistant", "Starting scan...")
	user2 := TextMessage("user", "Check port 443")
	assistant2 := TextMessage("assistant", "Port 443 is open")

	parentMessages := []*aop.Message{user1, assistant1, user2, assistant2}

	// Fork child: inherits parent messages, adds child prompt as new user message
	childPrompt := TextMessage("user", "Analyze the SSL certificate on port 443")
	childMessages := append([]*aop.Message{sysMsg}, parentMessages...)
	childMessages = append(childMessages, childPrompt)

	// Parent's last request (before forking)
	parentReq := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       append([]*aop.Message{sysMsg}, append(parentMessages, TextMessage("user", "fork a subagent"))...),
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	parentJSON := mustMarshal(t, prov, parentReq)
	parentParsed := mustParse(t, parentJSON)

	// Child's request
	childReq := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       childMessages,
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	childJSON := mustMarshal(t, prov, childReq)
	childParsed := mustParse(t, childJSON)

	t.Log("=== Parent Request ===")
	t.Log(prettyJSON(parentParsed))
	t.Log("=== Child (Fork) Request ===")
	t.Log(prettyJSON(childParsed))

	// System and tools should be identical (cache-friendly prefix)
	assertPrefixStable(t, "system", parentParsed, childParsed)
	assertPrefixStable(t, "tools", parentParsed, childParsed)

	// Both should have system and tools cached
	assertSystemCached(t, parentParsed)
	assertSystemCached(t, childParsed)
	assertLastToolCached(t, parentParsed)
	assertLastToolCached(t, childParsed)

	// Child's shared prefix (system + tools + parent messages) should match parent's prefix
	parentMsgs := parentParsed["messages"].([]interface{})
	childMsgs := childParsed["messages"].([]interface{})

	// The shared prefix is all messages except the last user message in each.
	// Parent has: user1, assistant1, user2, assistant2, user("fork a subagent")
	// Child has:  user1, assistant1, user2, assistant2, user("Analyze the SSL...")
	// Shared prefix: user1, assistant1, user2, assistant2 — these 4 should be identical.
	sharedLen := min(len(parentMsgs), len(childMsgs)) - 1
	for i := 0; i < sharedLen; i++ {
		pMsg := stripCacheControl(parentMsgs[i])
		cMsg := stripCacheControl(childMsgs[i])
		pJSON, _ := json.Marshal(pMsg)
		cJSON, _ := json.Marshal(cMsg)
		if string(pJSON) != string(cJSON) {
			t.Errorf("Shared prefix diverges at message %d:\n  parent: %s\n  child:  %s", i, pJSON, cJSON)
		}
	}
	t.Logf("Shared prefix verified: %d messages identical (excluding cache_control)", sharedLen)

	// Only the LAST user message in each should have cache_control
	parentLastUser := findLastMsgWithRole(parentMsgs, "user")
	childLastUser := findLastMsgWithRole(childMsgs, "user")
	assertBlockHasCacheControl(t, "parent last user", parentLastUser)
	assertBlockHasCacheControl(t, "child last user", childLastUser)
}

// TestCacheNoneProducesNoCacheControl verifies CacheNone never adds markers.
func TestCacheNoneProducesNoCacheControl(t *testing.T) {
	prov := mustAnthropicProvider(t)

	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []*aop.Message{
			TextMessage("system", "system prompt"),
			TextMessage("user", "hello"),
			TextMessage("assistant", "hi"),
			TextMessage("user", "bye"),
		},
		Tools: []*aop.ToolDefinition{
			toolDef("tool1", "t1", nil),
		},
		CacheRetention: CacheNone,
	}
	data := mustMarshal(t, prov, req)
	if strings.Contains(string(data), "cache_control") {
		t.Error("CacheNone request contains cache_control")
		t.Log(string(data))
	}
}

// TestCacheBreakpointToolResultMerge verifies that when tool_result (user role)
// is the last message and gets merged with preceding user messages,
// cache_control lands on the correct final block.
func TestCacheBreakpointToolResultMerge(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sysMsg := TextMessage("system", "system prompt")
	user1 := TextMessage("user", "call the tool")
	tc := newToolCall("c1", "read", `{}`)
	assistant1 := assistantToolCallMsg(tc)
	toolResult := toolResultMsg("c1", "file content here")

	// Case A: tool_result is the LAST message (no user msg after it)
	// tool_result maps to user role → it becomes the "last user message"
	reqA := &ChatCompletionRequest{
		Model:          "test",
		Messages:       []*aop.Message{sysMsg, user1, assistant1, toolResult},
		CacheRetention: CacheShort,
	}
	jA := mustMarshal(t, prov, reqA)
	pA := mustParse(t, jA)
	msgsA := pA["messages"].([]interface{})

	t.Log("=== Case A: tool_result is last ===")
	lastUserA := findLastMsgWithRole(msgsA, "user")
	if lastUserA == nil {
		t.Fatal("no user message found")
	}
	blocksA := lastUserA["content"].([]interface{})
	lastBlockA := blocksA[len(blocksA)-1].(map[string]interface{})
	if _, ok := lastBlockA["cache_control"]; !ok {
		t.Error("Case A: tool_result block (last user msg) should have cache_control")
	}
	t.Logf("  Last user msg has %d blocks, cache_control on last: type=%v",
		len(blocksA), lastBlockA["type"])

	// Case B: tool_result followed by user message → they merge (consecutive user role)
	user2 := TextMessage("user", "now analyze it")
	reqB := &ChatCompletionRequest{
		Model:          "test",
		Messages:       []*aop.Message{sysMsg, user1, assistant1, toolResult, user2},
		CacheRetention: CacheShort,
	}
	jB := mustMarshal(t, prov, reqB)
	pB := mustParse(t, jB)
	msgsB := pB["messages"].([]interface{})

	t.Log("=== Case B: tool_result + user merge ===")
	lastUserB := findLastMsgWithRole(msgsB, "user")
	blocksB := lastUserB["content"].([]interface{})
	lastBlockB := blocksB[len(blocksB)-1].(map[string]interface{})
	if _, ok := lastBlockB["cache_control"]; !ok {
		t.Error("Case B: merged user msg's last block should have cache_control")
	}
	t.Logf("  Merged user msg has %d blocks, cache_control on last: type=%v text=%v",
		len(blocksB), lastBlockB["type"], lastBlockB["text"])

	// Case C: multiple tool calls → multiple tool_results merge into one user message
	tc2 := newToolCall("c2", "write", `{}`)
	assistant2 := assistantToolCallMsg(tc, tc2)
	toolResult1 := toolResultMsg("c1", "result1")
	toolResult2 := toolResultMsg("c2", "result2")

	reqC := &ChatCompletionRequest{
		Model:          "test",
		Messages:       []*aop.Message{sysMsg, user1, assistant2, toolResult1, toolResult2},
		CacheRetention: CacheShort,
	}
	jC := mustMarshal(t, prov, reqC)
	pC := mustParse(t, jC)
	msgsC := pC["messages"].([]interface{})

	t.Log("=== Case C: multiple tool_results merge ===")
	lastUserC := findLastMsgWithRole(msgsC, "user")
	blocksC := lastUserC["content"].([]interface{})
	lastBlockC := blocksC[len(blocksC)-1].(map[string]interface{})
	if _, ok := lastBlockC["cache_control"]; !ok {
		t.Error("Case C: merged tool_results' last block should have cache_control")
	}
	t.Logf("  Merged tool_results has %d blocks, cache_control on last: type=%v",
		len(blocksC), lastBlockC["type"])
	for i, b := range blocksC {
		block := b.(map[string]interface{})
		_, cc := block["cache_control"]
		t.Logf("    block[%d] type=%v cache_control=%v", i, block["type"], cc)
	}
}

// TestCacheBreakpointStabilityAcrossTurns checks that the shared prefix bytes
// between consecutive turns are byte-identical (excluding the moving cache_control).
func TestCacheBreakpointStabilityAcrossTurns(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sys := TextMessage("system", "system prompt here")
	tools := []*aop.ToolDefinition{
		toolDef("tool1", "desc", nil),
	}

	// Build 5 turns of conversation
	msgs := []*aop.Message{sys}
	for turn := 1; turn <= 5; turn++ {
		msgs = append(msgs, TextMessage("user", fmt.Sprintf("question %d", turn)))
		msgs = append(msgs, TextMessage("assistant", fmt.Sprintf("answer %d", turn)))
	}
	msgs = append(msgs, TextMessage("user", "final question"))

	// Marshal the full request
	reqFull := &ChatCompletionRequest{
		Model: "test", Messages: msgs, Tools: tools, CacheRetention: CacheShort,
	}
	jFull := mustMarshal(t, prov, reqFull)
	pFull := mustParse(t, jFull)

	// Marshal a shorter prefix (first 3 turns + new question)
	shortMsgs := append(msgs[:7], TextMessage("user", "different question")) // sys + 3 turns + new user
	reqShort := &ChatCompletionRequest{
		Model: "test", Messages: shortMsgs, Tools: tools, CacheRetention: CacheShort,
	}
	jShort := mustMarshal(t, prov, reqShort)
	pShort := mustParse(t, jShort)

	// System and tools MUST be identical
	assertPrefixStable(t, "system", pFull, pShort)
	assertPrefixStable(t, "tools", pFull, pShort)

	// Shared message prefix (first 6 messages: 3 user+assistant pairs) should be identical
	fullMsgs := pFull["messages"].([]interface{})
	shortMsgs2 := pShort["messages"].([]interface{})

	sharedCount := len(shortMsgs2) - 1 // exclude last (which has cache_control)
	for i := 0; i < sharedCount; i++ {
		fJSON, _ := json.Marshal(stripCacheControl(fullMsgs[i]))
		sJSON, _ := json.Marshal(stripCacheControl(shortMsgs2[i]))
		if string(fJSON) != string(sJSON) {
			t.Errorf("shared message[%d] differs", i)
		}
	}
	t.Logf("Prefix stable across turns: %d shared messages verified", sharedCount)
}

// =============================================================================
// Tests from cache_protocol_test.go
// =============================================================================

// cachedPrefix tracks requests to compute realistic server-side cache behavior.
type cachedPrefix struct {
	mu     sync.Mutex
	cached int // tokens "cached" on server side
}

func (c *cachedPrefix) hit(promptTokens int) (cacheRead, cacheWrite int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached > 0 && c.cached <= promptTokens {
		cacheRead = c.cached
	} else {
		// First request: write to cache (use ~80% of prompt as cache)
		cacheWrite = promptTokens * 80 / 100
		c.cached = cacheWrite
	}
	return
}

// --- OpenAI mock server ---

func newOpenAIMockServer(t *testing.T, cache *cachedPrefix) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", 404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		turn++
		messages := req["messages"].([]interface{})
		promptTokens := len(messages) * 50 // rough estimate

		stream, _ := req["stream"].(bool)

		// Check cache fields arrive correctly
		cacheKey, _ := req["prompt_cache_key"].(string)
		cacheRet, _ := req["prompt_cache_retention"].(string)
		t.Logf("[OpenAI turn %d] messages=%d prompt_cache_key=%q prompt_cache_retention=%q stream=%v",
			turn, len(messages), cacheKey, cacheRet, stream)

		cacheRead, cacheWrite := cache.hit(promptTokens)
		completionTokens := 10

		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)

			// Content chunk
			contentChunk := fmt.Sprintf(`{"choices":[{"delta":{"role":"assistant","content":"response turn %d"},"index":0}]}`, turn)
			fmt.Fprintf(w, "data: %s\n\n", contentChunk)

			// Final chunk with usage
			usageChunk := fmt.Sprintf(
				`{"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d,"cache_write_tokens":%d}}}`,
				promptTokens, completionTokens, promptTokens+completionTokens, cacheRead, cacheWrite)
			fmt.Fprintf(w, "data: %s\n\n", usageChunk)
			fmt.Fprintf(w, "data: [DONE]\n\n")
		} else {
			resp := map[string]interface{}{
				"id":      fmt.Sprintf("chatcmpl-%d", turn),
				"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"role": "assistant", "content": fmt.Sprintf("response turn %d", turn)}, "finish_reason": "stop"}},
				"usage": map[string]interface{}{
					"prompt_tokens":     promptTokens,
					"completion_tokens": completionTokens,
					"total_tokens":      promptTokens + completionTokens,
					"prompt_tokens_details": map[string]interface{}{
						"cached_tokens":      cacheRead,
						"cache_write_tokens": cacheWrite,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
}

// --- Anthropic mock server ---

func newAnthropicMockServer(t *testing.T, cache *cachedPrefix) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, "not found", 404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		turn++
		messages := req["messages"].([]interface{})
		promptTokens := len(messages) * 50

		// Verify cache_control markers are present
		systemVal := req["system"]
		hasSysCacheCtrl := false
		if sysArr, ok := systemVal.([]interface{}); ok {
			for _, s := range sysArr {
				block := s.(map[string]interface{})
				if _, ok := block["cache_control"]; ok {
					hasSysCacheCtrl = true
				}
			}
		}

		hasToolCacheCtrl := false
		if toolsVal, ok := req["tools"].([]interface{}); ok && len(toolsVal) > 0 {
			lastTool := toolsVal[len(toolsVal)-1].(map[string]interface{})
			_, hasToolCacheCtrl = lastTool["cache_control"]
		}

		hasUserCacheCtrl := false
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i].(map[string]interface{})
			if msg["role"] == "user" {
				blocks := msg["content"].([]interface{})
				lastBlock := blocks[len(blocks)-1].(map[string]interface{})
				_, hasUserCacheCtrl = lastBlock["cache_control"]
				break
			}
		}

		isStream, _ := req["stream"].(bool)

		t.Logf("[Anthropic turn %d] messages=%d cache_control: system=%v tools=%v last_user=%v stream=%v",
			turn, len(messages), hasSysCacheCtrl, hasToolCacheCtrl, hasUserCacheCtrl, isStream)

		cacheRead, cacheWrite := cache.hit(promptTokens)
		completionTokens := 10

		if isStream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)

			// message_start with usage
			fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type": "message_start",
				"message": map[string]interface{}{
					"id":   fmt.Sprintf("msg_%d", turn),
					"role": "assistant",
					"usage": map[string]interface{}{
						"input_tokens":                promptTokens,
						"output_tokens":               0,
						"cache_creation_input_tokens": cacheWrite,
						"cache_read_input_tokens":     cacheRead,
					},
				},
			}))

			// content_block_start
			fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]interface{}{"type": "text", "text": ""},
			}))

			// content_block_delta
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]interface{}{"type": "text_delta", "text": fmt.Sprintf("response turn %d", turn)},
			}))

			// content_block_stop
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type": "content_block_stop", "index": 0,
			}))

			// message_delta with final usage
			fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type":  "message_delta",
				"delta": map[string]interface{}{"stop_reason": "end_turn"},
				"usage": map[string]interface{}{"output_tokens": completionTokens},
			}))

			// message_stop
			fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type": "message_stop",
			}))
		} else {
			resp := map[string]interface{}{
				"id":          fmt.Sprintf("msg_%d", turn),
				"type":        "message",
				"role":        "assistant",
				"content":     []interface{}{map[string]interface{}{"type": "text", "text": fmt.Sprintf("response turn %d", turn)}},
				"stop_reason": "end_turn",
				"usage": map[string]interface{}{
					"input_tokens":                promptTokens,
					"output_tokens":               completionTokens,
					"cache_creation_input_tokens": cacheWrite,
					"cache_read_input_tokens":     cacheRead,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
}

// --- Anthropic mock server with tool_use response ---

func newAnthropicToolMockServer(t *testing.T, cache *cachedPrefix) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		turn++
		messages := req["messages"].([]interface{})
		promptTokens := len(messages) * 50
		cacheRead, cacheWrite := cache.hit(promptTokens)
		completionTokens := 15

		// Verify tools have cache_control on last
		if toolsVal, ok := req["tools"].([]interface{}); ok {
			for i, tv := range toolsVal {
				tool := tv.(map[string]interface{})
				_, cc := tool["cache_control"]
				isLast := i == len(toolsVal)-1
				if cc && !isLast {
					t.Errorf("[turn %d] tool[%d] has cache_control but is not last", turn, i)
				}
				if !cc && isLast {
					t.Errorf("[turn %d] last tool missing cache_control", turn)
				}
			}
		}

		t.Logf("[Anthropic-tool turn %d] messages=%d cache_read=%d cache_write=%d", turn, len(messages), cacheRead, cacheWrite)

		if turn == 1 {
			// Return tool_use
			resp := map[string]interface{}{
				"id":   "msg_tool",
				"type": "message",
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "tool_use", "id": "call_abc", "name": "read", "input": map[string]interface{}{"path": "test.go"}},
				},
				"stop_reason": "tool_use",
				"usage": map[string]interface{}{
					"input_tokens":                promptTokens,
					"output_tokens":               completionTokens,
					"cache_creation_input_tokens": cacheWrite,
					"cache_read_input_tokens":     cacheRead,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		} else {
			// Return text
			resp := map[string]interface{}{
				"id":          "msg_final",
				"type":        "message",
				"role":        "assistant",
				"content":     []interface{}{map[string]interface{}{"type": "text", "text": "done with tool"}},
				"stop_reason": "end_turn",
				"usage": map[string]interface{}{
					"input_tokens":                promptTokens,
					"output_tokens":               completionTokens,
					"cache_creation_input_tokens": cacheWrite,
					"cache_read_input_tokens":     cacheRead,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
}

func TestOpenAIProtocol_MultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newOpenAIMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewOpenAIProvider(&ProviderConfig{
		Provider: "openai", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runMultiTurnScenario(t, prov, "OpenAI non-stream")
}

func TestOpenAIProtocol_StreamingMultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newOpenAIMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewOpenAIProvider(&ProviderConfig{
		Provider: "openai", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runStreamingMultiTurnScenario(t, prov, "OpenAI stream")
}

func TestAnthropicProtocol_MultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runMultiTurnScenario(t, prov, "Anthropic non-stream")
}

func TestAnthropicProtocol_StreamingMultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runStreamingMultiTurnScenario(t, prov, "Anthropic stream")
}

func TestAnthropicProtocol_ToolCallCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicToolMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})

	ctx := testContext()
	sys := TextMessage("system", "You are a tool-using assistant.")
	user1 := TextMessage("user", "Read test.go")
	tools := []*aop.ToolDefinition{
		toolDef("read", "read file",
			map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}}),
		toolDef("write", "write file", nil),
	}

	// Turn 1: triggers tool_use
	req1 := &ChatCompletionRequest{
		Messages: []*aop.Message{sys, user1},
		Tools:    tools, CacheRetention: CacheShort, SessionID: "sess-tool",
	}
	resp1, err := prov.ChatCompletion(ctx, req1)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	assertCacheFields(t, "tool turn 1", resp1.Usage)

	// Turn 2: tool_result + follow-up (simulates the agent loop)
	assistant1 := resp1.Choices[0].Message
	toolResult := toolResultMsg("call_abc", "package main...")
	user2 := TextMessage("user", "What does it do?")

	req2 := &ChatCompletionRequest{
		Messages: []*aop.Message{sys, user1, assistant1, toolResult, user2},
		Tools:    tools, CacheRetention: CacheShort, SessionID: "sess-tool",
	}
	resp2, err := prov.ChatCompletion(ctx, req2)
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	assertCacheFields(t, "tool turn 2", resp2.Usage)

	if resp2.Usage.Detail["cache_read"] == 0 {
		t.Error("tool turn 2: expected cache_read > 0")
	}

	t.Logf("\n=== Tool Call Cache Summary ===")
	logTurn(t, 1, resp1.Usage)
	logTurn(t, 2, resp2.Usage)
}

func TestOpenAIProtocol_SubagentForkCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newOpenAIMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewOpenAIProvider(&ProviderConfig{
		Provider: "openai", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runForkScenario(t, prov, "OpenAI fork")
}

func TestAnthropicProtocol_SubagentForkCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runForkScenario(t, prov, "Anthropic fork")
}

// --- Live test against DeepSeek (both protocols) ---

func TestLive_OpenAIProtocol_AllScenarios(t *testing.T) {
	cfg, prov := skipLive(t)
	_ = cfg
	t.Run("MultiTurn", func(t *testing.T) { runMultiTurnScenario(t, prov, "Live-OpenAI") })
	t.Run("Streaming", func(t *testing.T) { runStreamingMultiTurnScenario(t, prov, "Live-OpenAI-Stream") })
	t.Run("Fork", func(t *testing.T) { runForkScenario(t, prov, "Live-OpenAI-Fork") })
}

// =============================================================================
// Shared scenario runners (from cache_protocol_test.go)
// =============================================================================

func runMultiTurnScenario(t *testing.T, prov Provider, label string) {
	t.Helper()
	ctx := testContext()
	sys := TextMessage("system", "You are a helpful assistant. "+strings.Repeat("You have deep expertise in mathematics and always answer with just the numeric result. ", 30))
	user1 := TextMessage("user", "What is 2+2?")

	// Turn 1
	req1 := &ChatCompletionRequest{
		Messages:  []*aop.Message{sys, user1},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-mt",
	}
	resp1, err := prov.ChatCompletion(ctx, req1)
	if err != nil {
		t.Fatalf("%s turn 1: %v", label, err)
	}
	assertCacheFields(t, label+" turn 1", resp1.Usage)

	// Turn 2
	a1 := resp1.Choices[0].Message
	user2 := TextMessage("user", "What is 3+3?")
	req2 := &ChatCompletionRequest{
		Messages:  []*aop.Message{sys, user1, a1, user2},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-mt",
	}
	resp2, err := prov.ChatCompletion(ctx, req2)
	if err != nil {
		t.Fatalf("%s turn 2: %v", label, err)
	}
	assertCacheFields(t, label+" turn 2", resp2.Usage)

	// Turn 3
	a2 := resp2.Choices[0].Message
	user3 := TextMessage("user", "What is 4+4?")
	req3 := &ChatCompletionRequest{
		Messages:  []*aop.Message{sys, user1, a1, user2, a2, user3},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-mt",
	}
	resp3, err := prov.ChatCompletion(ctx, req3)
	if err != nil {
		t.Fatalf("%s turn 3: %v", label, err)
	}
	assertCacheFields(t, label+" turn 3", resp3.Usage)

	// Context should grow
	if resp3.Usage.InputTokens <= resp1.Usage.InputTokens {
		t.Errorf("%s: prompt tokens should grow (turn1=%d turn3=%d)",
			label, resp1.Usage.InputTokens, resp3.Usage.InputTokens)
	}

	// Cache should improve (may be 0 if prompt is below provider's minimum cache threshold)
	if resp2.Usage.Detail["cache_read"] == 0 && resp3.Usage.Detail["cache_read"] == 0 {
		t.Logf("%s: WARNING cache_read=0 in turn 2 and 3 — prompt may be below provider minimum cache threshold", label)
	}

	t.Logf("\n=== %s Multi-Turn Summary ===", label)
	logTurn(t, 1, resp1.Usage)
	logTurn(t, 2, resp2.Usage)
	logTurn(t, 3, resp3.Usage)
}

func runStreamingMultiTurnScenario(t *testing.T, prov Provider, label string) {
	t.Helper()
	sp, ok := prov.(StreamingProvider)
	if !ok {
		t.Skipf("%s: provider does not support streaming", label)
	}
	ctx := testContext()
	sys := TextMessage("system", "You translate to French. "+strings.Repeat("Always respond with just the translation. ", 30))

	// Turn 1
	user1 := TextMessage("user", "Hello")
	req1 := &ChatCompletionRequest{
		Messages:  []*aop.Message{sys, user1},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-stream", Stream: true,
	}
	msg1, usage1 := collectStream(t, sp, ctx, req1)

	// Turn 2
	a1 := TextMessage("assistant", msg1)
	user2 := TextMessage("user", "Goodbye")
	req2 := &ChatCompletionRequest{
		Messages:  []*aop.Message{sys, user1, a1, user2},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-stream", Stream: true,
	}
	_, usage2 := collectStream(t, sp, ctx, req2)

	if usage1 == nil || usage2 == nil {
		t.Fatalf("%s: streaming did not return usage", label)
	}

	if usage2.Detail["cache_read"] == 0 {
		t.Errorf("%s: expected cache_read > 0 in stream turn 2", label)
	}

	t.Logf("\n=== %s Streaming Summary ===", label)
	logTurn(t, 1, usage1)
	logTurn(t, 2, usage2)
}

func runForkScenario(t *testing.T, prov Provider, label string) {
	t.Helper()
	ctx := testContext()
	sys := TextMessage("system", "You are a scanner. "+strings.Repeat("Analyze targets. ", 30))

	// Build parent conversation (3 exchanges)
	parentMsgs := []*aop.Message{sys}
	for i := 1; i <= 3; i++ {
		parentMsgs = append(parentMsgs,
			TextMessage("user", fmt.Sprintf("question %d", i)),
			TextMessage("assistant", fmt.Sprintf("answer %d", i)),
		)
	}

	// Parent's next request
	parentReq := &ChatCompletionRequest{
		Messages:  append(append([]*aop.Message(nil), parentMsgs...), TextMessage("user", "parent question 4")),
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-fork",
	}
	parentResp, err := prov.ChatCompletion(ctx, parentReq)
	if err != nil {
		t.Fatalf("%s parent: %v", label, err)
	}

	// Fork child: inherits parent messages, new prompt
	childReq := &ChatCompletionRequest{
		Messages:  append(append([]*aop.Message(nil), parentMsgs...), TextMessage("user", "forked child task")),
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-fork",
	}
	childResp, err := prov.ChatCompletion(ctx, childReq)
	if err != nil {
		t.Fatalf("%s child: %v", label, err)
	}

	// Both should have cache reads (shared prefix)
	if childResp.Usage.Detail["cache_read"] == 0 {
		t.Errorf("%s: fork child expected cache_read > 0", label)
	}

	t.Logf("\n=== %s Fork Summary ===", label)
	t.Logf("  Parent: prompt=%d cache_read=%d cache_write=%d (%.0f%%)",
		parentResp.Usage.InputTokens, parentResp.Usage.Detail["cache_read"], parentResp.Usage.Detail["cache_write"],
		CacheHitRatio(parentResp.Usage)*100)
	t.Logf("  Child:  prompt=%d cache_read=%d cache_write=%d (%.0f%%)",
		childResp.Usage.InputTokens, childResp.Usage.Detail["cache_read"], childResp.Usage.Detail["cache_write"],
		CacheHitRatio(childResp.Usage)*100)
}

// =============================================================================
// Shared helpers
// =============================================================================

func mustJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func mustJSONStr(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func mustAnthropicProvider(t *testing.T) *AnthropicProvider {
	t.Helper()
	p, err := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: "https://api.anthropic.com/v1",
		APIKey: "test", Timeout: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustMarshal(t *testing.T, p *AnthropicProvider, req *ChatCompletionRequest) []byte {
	t.Helper()
	data, err := p.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustParse(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

func prettyJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func assertSystemCached(t *testing.T, parsed map[string]interface{}) {
	t.Helper()
	sys, ok := parsed["system"].([]interface{})
	if !ok {
		t.Error("system should be array when cache enabled")
		return
	}
	block := sys[len(sys)-1].(map[string]interface{})
	if _, ok := block["cache_control"]; !ok {
		t.Error("system prompt should have cache_control")
	}
}

func assertLastToolCached(t *testing.T, parsed map[string]interface{}) {
	t.Helper()
	tools, ok := parsed["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		return // no tools to check
	}
	lastTool := tools[len(tools)-1].(map[string]interface{})
	if _, ok := lastTool["cache_control"]; !ok {
		t.Error("last tool should have cache_control")
	}
	// Ensure non-last tools do NOT have cache_control
	for i := 0; i < len(tools)-1; i++ {
		tool := tools[i].(map[string]interface{})
		if _, ok := tool["cache_control"]; ok {
			t.Errorf("tool[%d] should NOT have cache_control (only last tool)", i)
		}
	}
}

func assertLastUserCached(t *testing.T, parsed map[string]interface{}) {
	t.Helper()
	msgs := parsed["messages"].([]interface{})
	lastUser := findLastMsgWithRole(msgs, "user")
	if lastUser == nil {
		t.Error("no user message found")
		return
	}
	assertBlockHasCacheControl(t, "last user", lastUser)
}

func assertNoCacheOnRole(t *testing.T, parsed map[string]interface{}, role string) {
	t.Helper()
	msgs := parsed["messages"].([]interface{})
	for i, m := range msgs {
		msg := m.(map[string]interface{})
		if msg["role"] != role {
			continue
		}
		blocks, ok := msg["content"].([]interface{})
		if !ok {
			continue
		}
		for _, b := range blocks {
			block := b.(map[string]interface{})
			if _, ok := block["cache_control"]; ok {
				t.Errorf("message[%d] (role=%s) should NOT have cache_control", i, role)
			}
		}
	}
}

func assertBlockHasCacheControl(t *testing.T, label string, msg map[string]interface{}) {
	t.Helper()
	blocks, ok := msg["content"].([]interface{})
	if !ok || len(blocks) == 0 {
		t.Errorf("%s: no content blocks", label)
		return
	}
	lastBlock := blocks[len(blocks)-1].(map[string]interface{})
	if _, ok := lastBlock["cache_control"]; !ok {
		t.Errorf("%s: last content block should have cache_control", label)
	}
}

func assertPrefixStable(t *testing.T, key string, a, b map[string]interface{}) {
	t.Helper()
	aJSON, _ := json.Marshal(a[key])
	bJSON, _ := json.Marshal(b[key])
	if string(aJSON) != string(bJSON) {
		t.Errorf("%s prefix differs between requests:\n  a: %s\n  b: %s", key, aJSON, bJSON)
	}
}

func findLastMsgWithRole(msgs []interface{}, role string) map[string]interface{} {
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i].(map[string]interface{})
		if msg["role"] == role {
			return msg
		}
	}
	return nil
}

func stripCacheControl(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	out := make(map[string]interface{})
	for k, val := range m {
		if k == "cache_control" {
			continue
		}
		switch typed := val.(type) {
		case []interface{}:
			stripped := make([]interface{}, len(typed))
			for i, item := range typed {
				stripped[i] = stripCacheControl(item)
			}
			out[k] = stripped
		case map[string]interface{}:
			out[k] = stripCacheControl(typed)
		default:
			out[k] = val
		}
	}
	return out
}

func testContext() context.Context { return context.Background() }

func skipLive(t *testing.T) (*ProviderConfig, Provider) {
	t.Helper()
	apiKey := os.Getenv("TEST_API_KEY")
	baseURL := os.Getenv("TEST_BASE_URL")
	model := os.Getenv("TEST_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("set TEST_API_KEY, TEST_BASE_URL, TEST_MODEL")
	}
	cfg := &ProviderConfig{BaseURL: baseURL, APIKey: apiKey, Model: model, Timeout: 60}
	cfg, _ = Resolve(cfg)
	p, _ := NewProviderFromResolved(cfg)
	return cfg, p
}

func collectStream(t *testing.T, sp StreamingProvider, ctx context.Context, req *ChatCompletionRequest) (string, *aop.TokenUsage) {
	t.Helper()
	ch, err := sp.ChatCompletionStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	var lastUsage *aop.TokenUsage
	for event := range ch {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Usage != nil {
			lastUsage = event.Usage
		}
		if delta := event.MessageDelta; delta != nil {
			content.WriteString(delta.GetText())
		}
		if event.Done {
			break
		}
	}
	return content.String(), lastUsage
}

func assertCacheFields(t *testing.T, label string, u *aop.TokenUsage) {
	t.Helper()
	if u == nil {
		t.Errorf("%s: usage is nil", label)
		return
	}
	if u.InputTokens == 0 {
		t.Errorf("%s: prompt_tokens = 0", label)
	}
}

func logTurn(t *testing.T, turn int, u *aop.TokenUsage) {
	t.Helper()
	if u == nil {
		t.Logf("  Turn %d: usage=nil", turn)
		return
	}
	t.Logf("  Turn %d: prompt=%d completion=%d cache_read=%d cache_write=%d hit_ratio=%.0f%%",
		turn, u.InputTokens, u.OutputTokens,
		u.Detail["cache_read"], u.Detail["cache_write"], CacheHitRatio(u)*100)
}
