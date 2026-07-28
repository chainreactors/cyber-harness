package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/aop"
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
	})).Run(context.Background(), TextInput("hello"))
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

func TestClampMaxTokens(t *testing.T) {
	tests := []struct {
		name                     string
		configured, window, used int
		want                     int
		wantErr                  bool
	}{
		{name: "configured limit fits", configured: 16384, window: 128000, used: 10000, want: 16384},
		{name: "remaining context clamps", configured: 32768, window: 100000, used: 80000, want: 15904},
		{name: "safety margin exhausted", configured: 4096, window: 4096, used: 1, wantErr: true},
		{name: "default max tokens", configured: 0, window: 128000, used: 10000, want: DefaultMaxTokens},
		{name: "unknown window leaves configured", configured: 12345, window: 0, used: 10000, want: 12345},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := clampMaxTokens(tt.configured, tt.window, tt.used)
			if tt.wantErr {
				if !errors.Is(err, errContextWindowExhausted) {
					t.Fatalf("clampMaxTokens(%d, %d, %d) error = %v, want context window exhausted", tt.configured, tt.window, tt.used, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("clampMaxTokens(%d, %d, %d) error = %v", tt.configured, tt.window, tt.used, err)
			}
			if got != tt.want {
				t.Fatalf("clampMaxTokens(%d, %d, %d) = %d, want %d", tt.configured, tt.window, tt.used, got, tt.want)
			}
		})
	}
}

func TestAgentRejectsExhaustedContextBeforeProviderCall(t *testing.T) {
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			callCount++
			return chatResponse(NewTextMessage("assistant", "unexpected")), nil
		},
	}

	_, err := NewAgent(Config{
		Provider:      llm,
		Model:         "test",
		ContextWindow: ContextSafetyTokens,
		MaxRetries:    -1,
	}).Run(context.Background(), TextInput("hello"))
	if !errors.Is(err, errContextWindowExhausted) {
		t.Fatalf("Run() error = %v, want context window exhausted", err)
	}
	if callCount != 0 {
		t.Fatalf("provider call count = %d, want 0", callCount)
	}
}

func TestConfigInitUsesPiModelLimitDefaults(t *testing.T) {
	cfg := (Config{Model: "unknown-custom-model"}).init()
	if cfg.MaxTokens != DefaultMaxTokens || cfg.ContextWindow != DefaultContextWindow {
		t.Fatalf("default limits = max:%d context:%d", cfg.MaxTokens, cfg.ContextWindow)
	}
}

func TestAgentRequestUsesConfiguredAndRemainingContextLimits(t *testing.T) {
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(NewTextMessage("assistant", "done")),
	}}
	ag := NewAgent(Config{
		Provider:      llm,
		Model:         "custom",
		MaxTokens:     20000,
		ContextWindow: 10000,
		MaxRetries:    -1,
	})
	if _, err := ag.Run(context.Background(), TextInput(strings.Repeat("x", 8000))); err != nil {
		t.Fatal(err)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	// 8000 ASCII bytes estimate to 2000 tokens. No tools are registered.
	if got, want := requests[0].MaxTokens, 10000-2000-ContextSafetyTokens; got != want {
		t.Fatalf("request max_tokens = %d, want %d", got, want)
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
	})).Run(context.Background(), TextInput("hello"))
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
	})).Run(context.Background(), TextInput("hello"))
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
	if !isRetryableError(fmt.Errorf("wrapped: %w", ErrStreamIncomplete)) {
		t.Fatal("ErrStreamIncomplete should be retryable")
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

func TestContextOverflowBypassesTransportRetry(t *testing.T) {
	if isRetryableError(&APIError{StatusCode: 500, Message: "maximum context length exceeded"}) {
		t.Fatal("context overflow should go directly to compaction")
	}
	if !isRetryableError(&APIError{StatusCode: 503, Message: "Service unavailable: too many tokens"}) {
		t.Fatal("service unavailable should remain a transport retry")
	}
}

func TestStreamAssistantMessageReturnsContextErrorOnClosedCanceledStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := streamAssistantMessageWithUsage(ctx,
		&scriptedProvider{},
		&ChatCompletionRequest{Model: "test"},
		newAOPEmitter(eventbus.New[aop.Event](), "aiscan", "test-session", "", "", nil, 0),
		telemetry.NopLogger(),
		1,
		"m-1",
	)
	if err != context.Canceled {
		t.Fatalf("streamAssistantMessageWithUsage() error = %v, want context.Canceled", err)
	}
}

func TestProviderFailureDoesNotAutomaticallyFallback(t *testing.T) {
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

	_, err := a.Run(context.Background(), TextInput("hello"))
	if err == nil {
		t.Fatal("Run() error = nil, want the primary provider error")
	}
	if len(fallback.requestsSnapshot()) != 0 {
		t.Fatal("fallback provider was called automatically")
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

	result, err := a.Run(context.Background(), TextInput("hello"))
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

	result, err := a.Run(context.Background(), TextInput("analyze this"))
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

	result, err := a.Run(context.Background(), TextInput("analyze the screenshot"))
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

	result, err := a.Run(context.Background(), TextInput("analyze"))
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if result.Output != "success without images" {
		t.Fatalf("first output = %q", result.Output)
	}

	imgProvider.callCount.Store(0)
	_, err = a.Run(context.Background(), TextInput("follow up"))
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

// --- Backoff & Retry-After parsing tests ---

func TestRetryDelayBackoffSequence(t *testing.T) {
	// RetryDelay keeps the original conservative policy (1s·2^attempt, cap 10s)
	// for backward compatibility with external callers (runner, webagent).
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		10 * time.Second, // cap reached
		10 * time.Second, // stays capped
	}
	for i, w := range want {
		if got := RetryDelay(i); got != w {
			t.Errorf("attempt %d: RetryDelay = %s, want %s", i, got, w)
		}
	}
}

func TestComputeRetryDelaySequence(t *testing.T) {
	// New LLM retry policy: 0.5s base, doubling, capped at 32s (no jitter here).
	want := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second, // cap reached
		32 * time.Second, // stays capped
	}
	for i, w := range want {
		if got := computeRetryDelay(i, 0); got != w {
			t.Errorf("attempt %d: computeRetryDelay = %s, want %s", i, got, w)
		}
	}
}

func TestRetryDelayJitterBounds(t *testing.T) {
	// With jitter, delay must fall in [base, base + 0.25·base] (inclusive upper
	// bound because jitterFrac can equal 1.0 in the test).
	for attempt := 0; attempt < 8; attempt++ {
		base := baseRetryDelay << uint(attempt)
		if base > maxRetryDelay {
			base = maxRetryDelay
		}
		upper := base + time.Duration(retryJitterFactor*float64(base))
		for i := 0; i < 50; i++ {
			got := computeRetryDelay(attempt, 1.0) // max jitter
			if got < base || got > upper {
				t.Errorf("attempt %d sample %d: got %s, want in [%s, %s]", attempt, i, got, base, upper)
			}
		}
	}
}

func TestRetryAfterFromError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want time.Duration
	}{
		{
			name: "seconds form",
			err:  &APIError{StatusCode: 429, Header: http.Header{"Retry-After": []string{"30"}}},
			want: 30 * time.Second,
		},
		{
			name: "header absent",
			err:  &APIError{StatusCode: 429},
			want: 0,
		},
		{
			name: "non-integer",
			err:  &APIError{StatusCode: 429, Header: http.Header{"Retry-After": []string{"Wed, 21 Oct 2015 07:28:00 GMT"}}},
			want: 0,
		},
		{
			name: "wrapped APIError",
			err:  fmt.Errorf("call failed: %w", &APIError{StatusCode: 429, Header: http.Header{"Retry-After": []string{"5"}}}),
			want: 5 * time.Second,
		},
		{
			name: "non-APIError",
			err:  fmt.Errorf("plain error"),
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryAfterFromError(tt.err); got != tt.want {
				t.Errorf("retryAfterFromError() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRetryDelayForHonorsRetryAfter(t *testing.T) {
	err := &APIError{StatusCode: 429, Header: http.Header{"Retry-After": []string{"60"}}}
	// Retry-After bypasses both the formula and the 32s cap.
	if got := retryDelayFor(0, err); got != 60*time.Second {
		t.Errorf("retryDelayFor with Retry-After=60 = %s, want 60s", got)
	}
}

func TestRetryDelayForFallsBackToBackoffWhenNoHeader(t *testing.T) {
	err := &APIError{StatusCode: 500} // no Header
	got := retryDelayFor(2, err)
	// attempt 2 base = 2s; with jitter it must stay in [2s, 2.5s)
	if got < 2*time.Second || got >= 2500*time.Millisecond {
		t.Errorf("retryDelayFor(attempt=2, no header) = %s, want in [2s, 2.5s)", got)
	}
}

func TestIsRetryableNowIncludes408409(t *testing.T) {
	for _, code := range []int{408, 409, 429, 500, 502, 503, 529} {
		if !isRetryableError(&APIError{StatusCode: code}) {
			t.Errorf("status %d should be retryable", code)
		}
	}
	for _, code := range []int{400, 401, 403, 404} {
		if isRetryableError(&APIError{StatusCode: code}) {
			t.Errorf("status %d should NOT be retryable", code)
		}
	}
}
