package agent

import "strings"

var contextOverflowPatterns = []string{
	"prompt is too long",
	"request_too_large",
	"input is too long for requested model",
	"exceeds the context window",
	"maximum context length",
	"maximum prompt length",
	"reduce the length of the messages",
	"maximum allowed input length",
	"exceeds the available context size",
	"greater than the context length",
	"context window exceeds limit",
	"exceeded model token limit",
	"model_context_window_exceeded",
	"context_length_exceeded",
	"context length exceeded",
	"range of input length should be",
	"prompt too long",
	"too many tokens",
	"token limit exceeded",
}

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "service unavailable:") {
		return false
	}
	for _, exclusion := range []string{"rate limit", "too many requests", "throttling"} {
		if strings.Contains(message, exclusion) {
			return false
		}
	}
	for _, pattern := range contextOverflowPatterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

func isLengthContextOverflow(finishReason string, usage *Usage, contextWindow int) bool {
	if !isOutputLimitFinishReason(finishReason) || usage == nil || contextWindow <= 0 {
		return false
	}
	if usage.CompletionTokens != 0 {
		return false
	}
	return usage.PromptTokens >= contextWindow*99/100
}
