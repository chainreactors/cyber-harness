package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
