package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, provider.ErrCallTimeout) || errors.Is(err, provider.ErrStreamStalled) {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsRetryable()
	}
	return isRetryableByMessage(err)
}

func isRetryableByMessage(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, pattern := range []string{
		"stream stalled",
		"connection reset",
		"connection refused",
		"connection closed",
		"eof",
		"temporary failure",
		"network is unreachable",
		"no such host",
		"api error (429)",
		"api error (500)",
		"api error (502)",
		"api error (503)",
		"api error (529)",
		"rate limit",
		"rate_limit",
		"overloaded",
		"server_error",
		"service unavailable",
		"internal server error",
		"bad gateway",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

func retryDelay(attempt int) time.Duration {
	delay := time.Second << uint(attempt)
	if delay > 10*time.Second {
		delay = 10 * time.Second
	}
	return delay
}

func requestWithRetry(ctx context.Context, cfg Config, messages []provider.ChatMessage, tools []provider.ToolDefinition, turn int) (provider.ChatMessage, *provider.Usage, error) {
	var lastErr error
	maxAttempts := cfg.MaxRetries + 1
	if cfg.MaxRetries < 0 {
		maxAttempts = 1
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt - 1)
			cfg.Logger.Warnf("retrying LLM call (attempt %d/%d) after %s: %v", attempt+1, maxAttempts, delay, lastErr)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return provider.ChatMessage{}, nil, ctx.Err()
			}
		}

		msg, usage, err := requestAssistantMessageWithUsage(ctx, cfg, messages, tools, turn)
		if err == nil {
			return msg, usage, nil
		}
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return provider.ChatMessage{}, nil, ctxErr
		}
		if !isRetryableError(err) {
			return provider.ChatMessage{}, nil, err
		}
	}
	return provider.ChatMessage{}, nil, lastErr
}

func requestAssistantMessageWithUsage(ctx context.Context, cfg Config, messages []provider.ChatMessage, tools []provider.ToolDefinition, turn int) (provider.ChatMessage, *provider.Usage, error) {
	req := &provider.ChatCompletionRequest{
		Model:          cfg.Model,
		Messages:       messages,
		Tools:          tools,
		MaxTokens:      cfg.MaxTokens,
		Temperature:    cfg.Temperature,
		ResponseFormat: cfg.ResponseFormat,
		CacheRetention: cfg.CacheRetention,
		SessionID:      cfg.SessionID,
	}
	if cfg.Stream {
		if streaming, ok := cfg.Provider.(provider.StreamingProvider); ok {
			return streamAssistantMessageWithUsage(ctx, streaming, req, cfg.Emit, cfg.Logger, turn)
		}
	}

	resp, err := cfg.Provider.ChatCompletion(ctx, req)
	if err != nil {
		return provider.ChatMessage{}, nil, fmt.Errorf("LLM call failed at turn %d: %w", turn, err)
	}
	if len(resp.Choices) == 0 {
		return provider.ChatMessage{}, nil, fmt.Errorf("empty response from LLM at turn %d", turn)
	}
	msg := resp.Choices[0].Message
	if err := emit(ctx, cfg.Emit, Event{Type: EventMessageStart, Turn: turn, Message: msg}); err != nil {
		return provider.ChatMessage{}, nil, err
	}
	if err := emit(ctx, cfg.Emit, Event{Type: EventMessageEnd, Turn: turn, Message: msg}); err != nil {
		return provider.ChatMessage{}, nil, err
	}
	logAssistantAndUsage(cfg.Logger, msg, resp.Usage)
	return msg, resp.Usage, nil
}

func streamAssistantMessageWithUsage(ctx context.Context, p provider.StreamingProvider, req *provider.ChatCompletionRequest, emitFn EventHandler, logger telemetry.Logger, turn int) (provider.ChatMessage, *provider.Usage, error) {
	events, err := p.ChatCompletionStream(ctx, req)
	if err != nil {
		return provider.ChatMessage{}, nil, fmt.Errorf("LLM stream failed at turn %d: %w", turn, err)
	}

	builder := newMessageBuilder()
	started := false
	var usage *provider.Usage
	for event := range events {
		if event.Err != nil {
			return provider.ChatMessage{}, nil, fmt.Errorf("LLM stream failed at turn %d: %w", turn, event.Err)
		}
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.Done {
			break
		}
		updated := builder.Apply(event.Delta)
		if !started {
			started = true
			if err := emit(ctx, emitFn, Event{Type: EventMessageStart, Turn: turn, Message: updated}); err != nil {
				return provider.ChatMessage{}, nil, err
			}
		}
		if err := emit(ctx, emitFn, Event{Type: EventMessageUpdate, Turn: turn, Message: updated}); err != nil {
			return provider.ChatMessage{}, nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return provider.ChatMessage{}, nil, err
	}

	msg := builder.Message()
	if !started {
		if err := emit(ctx, emitFn, Event{Type: EventMessageStart, Turn: turn, Message: msg}); err != nil {
			return provider.ChatMessage{}, nil, err
		}
	}
	if err := emit(ctx, emitFn, Event{Type: EventMessageEnd, Turn: turn, Message: msg}); err != nil {
		return provider.ChatMessage{}, nil, err
	}
	logAssistantAndUsage(logger, msg, usage)
	return msg, usage, nil
}
