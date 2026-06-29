package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func TestRetryOnTransientError(t *testing.T) {
	tools := commands.NewRegistry()
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			callCount++
			if callCount == 1 {
				return nil, fmt.Errorf("API error (502): bad gateway")
			}
			return chatResponse(NewTextMessage("assistant", "recovered")), nil
		},
	}

	result, err := (NewAgent(Config{
		Provider:   llm,
		Tools:      tools,
		Model:      "test",
		MaxRetries: 2,
	})).Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v, want success after retry", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("result = %q, want recovered", result.Output)
	}
	if callCount != 2 {
		t.Fatalf("call count = %d, want 2", callCount)
	}
}

func TestNoRetryOnAuthError(t *testing.T) {
	tools := commands.NewRegistry()
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			callCount++
			return nil, fmt.Errorf("API error (401): invalid_api_key")
		},
	}

	_, err := (NewAgent(Config{
		Provider:   llm,
		Tools:      tools,
		Model:      "test",
		MaxRetries: 3,
	})).Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("Run() error = nil, want auth error")
	}
	if callCount != 1 {
		t.Fatalf("call count = %d, want 1 (no retry for auth errors)", callCount)
	}
}

func TestRetryExhaustedReturnsLastError(t *testing.T) {
	tools := commands.NewRegistry()
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			callCount++
			return nil, fmt.Errorf("API error (503): service unavailable")
		},
	}

	_, err := (NewAgent(Config{
		Provider:   llm,
		Tools:      tools,
		Model:      "test",
		MaxRetries: 2,
	})).Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("Run() error = nil, want error after retries exhausted")
	}
	if callCount != 3 {
		t.Fatalf("call count = %d, want 3 (1 initial + 2 retries)", callCount)
	}
}

func TestRetryableProviderTimeoutAndStallErrors(t *testing.T) {
	if !isRetryableError(fmt.Errorf("wrapped: %w", ErrCallTimeout)) {
		t.Fatal("ErrCallTimeout should be retryable")
	}
	if !isRetryableError(fmt.Errorf("wrapped: %w", ErrStreamStalled)) {
		t.Fatal("ErrStreamStalled should be retryable")
	}
	if !isRetryableError(retryableTimeoutError{}) {
		t.Fatal("network timeout should be retryable")
	}
	if isRetryableError(fmt.Errorf("wrapped: %w", context.Canceled)) {
		t.Fatal("context.Canceled should not be retryable")
	}
	if isRetryableError(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)) {
		t.Fatal("context.DeadlineExceeded should not be retryable")
	}
}

func TestStreamAssistantMessageReturnsContextErrorOnClosedCanceledStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := streamAssistantMessageWithUsage(ctx,
		&scriptedProvider{},
		&ChatCompletionRequest{Model: "test"},
		newEmitter(eventbus.New[Event](), "test", ""),
		telemetry.NopLogger(),
		1,
	)
	if err != context.Canceled {
		t.Fatalf("streamAssistantMessageWithUsage() error = %v, want context.Canceled", err)
	}
}

func TestProviderFallbackOnRetryExhaustion(t *testing.T) {
	primary := &scriptedProvider{err: &APIError{StatusCode: 401, Message: "invalid api key"}}
	fallback := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "from fallback")),
		},
	}

	a := NewAgent(Config{
		Provider:   primary,
		Model:      "primary-model",
		Fallbacks:  []ProviderEntry{{Provider: fallback, Model: "fallback-model"}},
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	result, err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (fallback should succeed)", err)
	}
	if result.Output != "from fallback" {
		t.Fatalf("Output = %q, want 'from fallback'", result.Output)
	}
	if len(fallback.requestsSnapshot()) == 0 {
		t.Fatal("fallback provider was never called")
	}
}

func TestProviderFallbackAllExhausted(t *testing.T) {
	primary := &scriptedProvider{err: &APIError{StatusCode: 401, Message: "bad key"}}
	fallback := &scriptedProvider{err: &APIError{StatusCode: 403, Message: "forbidden"}}

	a := NewAgent(Config{
		Provider:   primary,
		Model:      "primary-model",
		Fallbacks:  []ProviderEntry{{Provider: fallback, Model: "fallback-model"}},
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	_, err := a.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("Run() error = nil, want error when all providers exhausted")
	}
}

func TestNoFallbackWhenPrimarySucceeds(t *testing.T) {
	primary := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "from primary")),
		},
	}
	fallback := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "from fallback")),
		},
	}

	a := NewAgent(Config{
		Provider:   primary,
		Fallbacks:  []ProviderEntry{{Provider: fallback, Model: "fallback-model"}},
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	result, err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "from primary" {
		t.Fatalf("Output = %q, want 'from primary'", result.Output)
	}
	if len(fallback.requestsSnapshot()) != 0 {
		t.Fatal("fallback provider should not be called when primary succeeds")
	}
}

// --- Image error recovery tests ---

func TestImageErrorAutoRecovery(t *testing.T) {
	imgProvider := &imageErrorProvider{}

	a := NewAgent(Config{
		Provider:   imgProvider,
		Model:      "test",
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	a.LoadMessages([]ChatMessage{
		NewTextMessage("user", "take screenshot"),
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID: "tc1", Type: "function",
				Function: FunctionCall{Name: "screenshot", Arguments: "{}"},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "tc1",
			ContentParts: []ContentPart{
				provider.TextPart("Screenshot captured"),
				provider.ImagePart("image/png", "iVBORw0KGgo=", "high"),
			},
		},
	})

	result, err := a.Run(context.Background(), "analyze this")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Output, "success") {
		t.Fatalf("output = %q, want 'success without images'", result.Output)
	}
	if !imgProvider.imagesDisabled.Load() {
		t.Fatal("DisableImages() was not called")
	}
}

func TestImageErrorRecoveryWithRealRetryPath(t *testing.T) {
	imgProvider := &imageErrorProvider{}

	a := NewAgent(Config{
		Provider:   imgProvider,
		Model:      "test",
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	a.LoadMessages([]ChatMessage{
		NewTextMessage("user", "take screenshot"),
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID: "tc1", Type: "function",
				Function: FunctionCall{Name: "screenshot", Arguments: "{}"},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "tc1",
			ContentParts: []ContentPart{
				provider.TextPart("Screenshot taken"),
				provider.ImagePart("image/png", "iVBORw0KGgo=", "high"),
			},
		},
	})

	result, err := a.Run(context.Background(), "analyze the screenshot")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (image error should auto-recover)", err)
	}
	if result.Output != "success without images" {
		t.Fatalf("output = %q, want 'success without images'", result.Output)
	}
	if !imgProvider.imagesDisabled.Load() {
		t.Fatal("DisableImages() was not called on provider")
	}
	if got := imgProvider.callCount.Load(); got != 2 {
		t.Fatalf("provider call count = %d, want 2 (initial + retry)", got)
	}
}

func TestMultiTurnAfterImageError(t *testing.T) {
	imgProvider := &imageErrorProvider{}

	a := NewAgent(Config{
		Provider:   imgProvider,
		Model:      "test",
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	a.LoadMessages([]ChatMessage{
		NewTextMessage("user", "screenshot"),
		{
			Role:       "tool",
			ToolCallID: "tc1",
			ContentParts: []ContentPart{
				provider.TextPart("img"),
				provider.ImagePart("image/png", "iVBORw0KGgo=", "high"),
			},
		},
	})

	result, err := a.Run(context.Background(), "analyze")
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if result.Output != "success without images" {
		t.Fatalf("first output = %q", result.Output)
	}

	imgProvider.callCount.Store(0)
	_, err = a.Run(context.Background(), "follow up")
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if got := imgProvider.callCount.Load(); got != 1 {
		t.Fatalf("second run call count = %d, want 1 (no retry needed)", got)
	}
}

func TestInferImageSupportModelRegistry(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		want     bool
	}{
		{"openai", "claude-sonnet-4-20250514", true},
		{"openai", "gemini-2.5-pro", true},
		{"openai", "gpt-4o-2024-05-13", true},
		{"openai", "gpt-4-turbo-2024-04-09", true},
		{"openai", "pixtral-large-2411", true},
		{"openai", "qwen-vl-plus", true},

		{"openai", "deepseek-v4-pro", false},
		{"openai", "deepseek-v4-flash", false},
		{"openai", "Qwen3-235B-A22B", false},
		{"openai", "glm-4.7", false},
		{"openai", "mistral-large-2411", false},
		{"openai", "llama-3.3-70b-instruct", false},
		{"openai", "grok-3", false},
		{"openai", "kimi-k2-thinking", false},
		{"openai", "minimax-m2.7", false},
		{"openai", "nemotron-3-super-120b", false},
		{"openai", "o3-mini", false},
		{"openai", "gpt-oss-120b", false},
		{"openai", "codestral-latest", false},
		{"openai", "devstral-2512", false},
		{"openai", "mimo-v2-flash", false},
		{"openai", "command-r-plus-08-2024", false},

		{"anthropic", "some-unknown-model", true},
		{"openai", "some-random-model", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.model, func(t *testing.T) {
			cfg := &ProviderConfig{
				Provider: tt.provider,
				Model:    tt.model,
				APIKey:   "test-key",
			}
			resolved, err := ResolveProvider(cfg)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got := *resolved.Images; got != tt.want {
				t.Errorf("inferImageSupport(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}
