package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

var errEmptyResponse = errors.New("empty response from LLM")

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCallTimeout) || errors.Is(err, ErrStreamStalled) || errors.Is(err, errEmptyResponse) {
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
	var apiErr *APIError
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

func RetryDelay(attempt int) time.Duration {
	delay := time.Second << uint(attempt)
	if delay > 10*time.Second {
		delay = 10 * time.Second
	}
	return delay
}

func requestWithRetry(ctx context.Context, cfg Config, bus emitter, messages []ChatMessage, tools []ToolDefinition, turn int) (ChatMessage, *Usage, error) {
	var lastErr error
	maxAttempts := cfg.MaxRetries + 1
	if cfg.MaxRetries < 0 {
		maxAttempts = 1
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := RetryDelay(attempt - 1)
			cfg.Logger.Warnf("retrying LLM call (attempt %d/%d) after %s: %v", attempt+1, maxAttempts, delay, lastErr)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ChatMessage{}, nil, ctx.Err()
			}
		}

		msg, usage, err := requestAssistantMessageWithUsage(ctx, cfg, bus, messages, tools, turn)
		if err == nil {
			return msg, usage, nil
		}
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return ChatMessage{}, nil, ctxErr
		}
		if !isRetryableError(err) {
			return ChatMessage{}, nil, err
		}
	}
	return ChatMessage{}, nil, lastErr
}

func requestAssistantMessageWithUsage(ctx context.Context, cfg Config, bus emitter, messages []ChatMessage, tools []ToolDefinition, turn int) (ChatMessage, *Usage, error) {
	req := &ChatCompletionRequest{
		Model:          cfg.Model,
		Messages:       messages,
		Tools:          tools,
		MaxTokens:      cfg.MaxTokens,
		Temperature:    cfg.Temperature,
		ResponseFormat: cfg.ResponseFormat,
		CacheRetention: cfg.CacheRetention,
		SessionID:      cfg.SessionID,
	}
	bus.Emit(Event{Type: EventLLMRequest, Turn: turn, Request: req})
	if cfg.Stream {
		if streaming, ok := cfg.Provider.(StreamingProvider); ok {
			return streamAssistantMessageWithUsage(ctx, streaming, req, bus, cfg.Logger, turn)
		}
	}

	resp, err := cfg.Provider.ChatCompletion(ctx, req)
	if err != nil {
		return ChatMessage{}, nil, fmt.Errorf("LLM call failed at turn %d: %w", turn, err)
	}
	if len(resp.Choices) == 0 {
		return ChatMessage{}, nil, fmt.Errorf("%w at turn %d", errEmptyResponse, turn)
	}
	msg := resp.Choices[0].Message
	bus.Emit(Event{Type: EventMessageStart, Turn: turn, Message: msg})
	bus.Emit(Event{Type: EventMessageEnd, Turn: turn, Message: msg})
	logAssistantAndUsage(cfg.Logger, msg, resp.Usage)
	return msg, resp.Usage, nil
}

func streamAssistantMessageWithUsage(ctx context.Context, p StreamingProvider, req *ChatCompletionRequest, bus emitter, logger telemetry.Logger, turn int) (ChatMessage, *Usage, error) {
	events, err := p.ChatCompletionStream(ctx, req)
	if err != nil {
		return ChatMessage{}, nil, fmt.Errorf("LLM stream failed at turn %d: %w", turn, err)
	}

	builder := newMessageBuilder()
	started := false
	var usage *Usage
	for event := range events {
		if event.Err != nil {
			return ChatMessage{}, nil, fmt.Errorf("LLM stream failed at turn %d: %w", turn, event.Err)
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
			bus.Emit(Event{Type: EventMessageStart, Turn: turn, Message: updated})
		}
		bus.Emit(Event{Type: EventMessageUpdate, Turn: turn, Message: updated})
	}
	if err := ctx.Err(); err != nil {
		return ChatMessage{}, nil, err
	}

	msg := builder.Message()
	if !started {
		bus.Emit(Event{Type: EventMessageStart, Turn: turn, Message: msg})
	}
	bus.Emit(Event{Type: EventMessageEnd, Turn: turn, Message: msg})
	logAssistantAndUsage(logger, msg, usage)
	return msg, usage, nil
}
