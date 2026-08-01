package agent

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent/provider"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/telemetry"
)

type imageDisabler interface {
	DisableImages()
}

var (
	errEmptyResponse          = errors.New("empty response from LLM")
	errContextWindowExhausted = errors.New("context window exhausted")
)

const (
	baseRetryDelay    = 500 * time.Millisecond
	maxRetryDelay     = 32 * time.Second
	retryJitterFactor = 0.25
)

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if isContextOverflowError(err) {
		return false
	}
	if errors.Is(err, ErrCallTimeout) || errors.Is(err, ErrStreamStalled) ||
		errors.Is(err, ErrStreamIncomplete) || errors.Is(err, errEmptyResponse) {
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

// RetryDelay returns the backoff duration for the given attempt index (0-based).
// It keeps the original conservative policy (1s·2^attempt, capped at 10s) for
// backward compatibility with external callers such as runner and webagent
// reconnect logic.
func RetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// Clamp before shifting. A large attempt previously overflowed the duration
	// shift to zero, turning a persistent authentication failure into a tight
	// reconnect loop that could saturate the control plane.
	if attempt >= 4 {
		return 10 * time.Second
	}
	return time.Second << uint(attempt)
}

// retryDelayFor computes the backoff for an LLM call retry. It honors a
// Retry-After header when the error carries one (server directive wins and
// bypasses both the backoff formula and the cap); otherwise it falls back to
// exponential backoff with additive jitter: min(base·2^attempt, maxDelay) + jitter.
func retryDelayFor(attempt int, err error) time.Duration {
	if after := retryAfterFromError(err); after > 0 {
		return after
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	jitter := float64(binary.LittleEndian.Uint64(b[:])>>11) / (1 << 53)
	return computeRetryDelay(attempt, jitter)
}

// retryAfterFromError parses a Retry-After header (integer seconds form) from
// an APIError, if present. Returns 0 when absent or unparseable.
func retryAfterFromError(err error) time.Duration {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return 0
	}
	if apiErr.Header == nil {
		return 0
	}
	val := strings.TrimSpace(apiErr.Header.Get("Retry-After"))
	if val == "" {
		return 0
	}
	secs, perr := strconv.Atoi(val)
	if perr != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// computeRetryDelay is the exponential backoff + additive jitter core used by
// the LLM retry loop. Formula: min(baseRetryDelay·2^attempt, maxRetryDelay),
// then add random jitter in [0, retryJitterFactor·delay).
func computeRetryDelay(attempt int, jitterFrac float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// baseDelay·2^attempt, capped at maxDelay
	delay := baseRetryDelay << uint(attempt)
	if delay > maxRetryDelay || delay <= 0 {
		delay = maxRetryDelay
	}
	if jitterFrac > 0 {
		// additive jitter: delay += random·[0, jitterFactor·delay)
		delay += time.Duration(jitterFrac * retryJitterFactor * float64(delay))
	}
	return delay
}

func requestWithRetry(ctx context.Context, cfg Config, em *aopEmitter, messages []ChatMessage, tools []ToolDefinition, turn int) (ChatMessage, *Usage, error) {
	var lastErr error
	maxAttempts := cfg.MaxRetries + 1
	if cfg.MaxRetries < 0 {
		maxAttempts = 1
	}
	// The message id is allocated once per logical assistant message so that
	// retries (including the image-downgrade retry) reuse it — consumers merge
	// deltas and the final message by id.
	messageID := em.allocMessageID()
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryDelayFor(attempt-1, lastErr)
			cfg.Logger.Warnf("retrying LLM call (attempt %d/%d) after %s: %v", attempt+1, maxAttempts, delay, lastErr)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ChatMessage{}, nil, ctx.Err()
			}
		}

		msg, usage, err := requestAssistantMessageWithUsage(ctx, cfg, em, messages, tools, turn, messageID)
		if err == nil {
			return msg, usage, nil
		}
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return ChatMessage{}, nil, ctxErr
		}

		if provider.IsImageUnsupportedError(err) {
			cfg.Logger.Warnf("provider does not support images, disabling and retrying")
			if d, ok := cfg.Provider.(imageDisabler); ok {
				d.DisableImages()
			}
			msg, usage, retryErr := requestAssistantMessageWithUsage(ctx, cfg, em, messages, tools, turn, messageID)
			if retryErr == nil {
				return msg, usage, nil
			}
			return ChatMessage{}, nil, retryErr
		}

		if !isRetryableError(err) {
			return ChatMessage{}, nil, err
		}
	}
	return ChatMessage{}, nil, lastErr
}

func requestAssistantMessageWithUsage(ctx context.Context, cfg Config, em *aopEmitter, messages []ChatMessage, tools []ToolDefinition, turn int, messageID string) (ChatMessage, *Usage, error) {
	req := &ChatCompletionRequest{
		Model:          cfg.Model,
		Messages:       messages,
		Tools:          tools,
		MaxTokens:      cfg.MaxTokens,
		Temperature:    cfg.Temperature,
		CacheRetention: cfg.CacheRetention,
		SessionID:      cfg.SessionID,
	}
	estimatedInputTokens := estimateRequestTokens(messages, tools)
	maxTokens, err := clampMaxTokens(cfg.MaxTokens, cfg.ContextWindow, estimatedInputTokens)
	if err != nil {
		return ChatMessage{}, nil, fmt.Errorf("cannot create LLM request at turn %d: %w", turn, err)
	}
	req.MaxTokens = maxTokens
	em.status(statusLLMRequest, aopStatusNamespace, &transport.LLMRequestDetail{
		Model: req.Model, Messages: uint32(len(req.Messages)), MaxTokens: uint32(max(req.MaxTokens, 0)), Stream: cfg.Stream,
	})
	if cfg.Stream {
		if streaming, ok := cfg.Provider.(StreamingProvider); ok {
			return streamAssistantMessageWithUsage(ctx, streaming, req, em, cfg.Logger, turn, messageID)
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
	msg.FinishReason = resp.Choices[0].FinishReason
	if parts := messagePartsFromChat(msg); len(parts) > 0 {
		em.messageWithID(messageID, "assistant", parts)
	}
	logUsage(cfg.Logger, resp.Usage)
	return msg, resp.Usage, nil
}

func clampMaxTokens(configured, contextWindow, contextTokens int) (int, error) {
	if configured <= 0 {
		configured = DefaultMaxTokens
	}
	if contextWindow <= 0 {
		return configured, nil
	}
	available := contextWindow - contextTokens - ContextSafetyTokens
	if available < 1 {
		return 0, fmt.Errorf(
			"%w: context_window=%d, estimated_input_tokens=%d, safety_reserve=%d; increase context_window or reduce the conversation history",
			errContextWindowExhausted, contextWindow, contextTokens, ContextSafetyTokens,
		)
	}
	if configured > available {
		return available, nil
	}
	return configured, nil
}

func estimateRequestTokens(messages []ChatMessage, tools []ToolDefinition) int {
	total := estimateAllTokens(messages)
	if len(tools) == 0 {
		return total
	}
	if encoded, err := json.Marshal(tools); err == nil {
		total += (len(encoded) + 3) / 4
	}
	return total
}

func streamAssistantMessageWithUsage(ctx context.Context, p StreamingProvider, req *ChatCompletionRequest, em *aopEmitter, logger telemetry.Logger, turn int, messageID string) (ChatMessage, *Usage, error) {
	events, err := p.ChatCompletionStream(ctx, req)
	if err != nil {
		return ChatMessage{}, nil, fmt.Errorf("LLM stream failed at turn %d: %w", turn, err)
	}

	builder := newMessageBuilder()
	seenReasoning := false
	finishReason := ""
	var usage *Usage
	for {
		select {
		case <-ctx.Done():
			return ChatMessage{}, nil, ctx.Err()
		case event, ok := <-events:
			if !ok {
				goto streamDone
			}
			if event.Err != nil {
				return ChatMessage{}, nil, fmt.Errorf("LLM stream failed at turn %d: %w", turn, event.Err)
			}
			if event.Usage != nil {
				usage = event.Usage
			}
			if event.FinishReason != "" {
				finishReason = event.FinishReason
			}
			if event.Done {
				goto streamDone
			}
			builder.Apply(event.Delta)
			if event.Delta.ReasoningContent != nil && *event.Delta.ReasoningContent != "" {
				seenReasoning = true
				em.messageDelta(messageID, 0, partReasoning, *event.Delta.ReasoningContent)
			}
			if event.Delta.Content != nil && *event.Delta.Content != "" {
				textIndex := 0
				if seenReasoning {
					textIndex = 1
				}
				em.messageDelta(messageID, textIndex, partText, *event.Delta.Content)
			}
		}
	}
streamDone:

	msg := builder.Message()
	msg.FinishReason = finishReason
	if parts := messagePartsFromChat(msg); len(parts) > 0 {
		em.messageWithID(messageID, "assistant", parts)
	}
	logUsage(logger, usage)
	return msg, usage, nil
}
