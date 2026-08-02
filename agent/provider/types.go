package provider

import (
	"fmt"
	"net/http"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
)

// CacheRetention controls prompt caching behavior across providers.
type CacheRetention string

const (
	CacheNone  CacheRetention = ""      // no caching (zero value, backward compatible)
	CacheShort CacheRetention = "short" // Anthropic ephemeral / OpenAI automatic
	CacheLong  CacheRetention = "long"  // Anthropic ephemeral+TTL / OpenAI 24h retention
)

// The provider boundary speaks AOP protos. Adapters (openai.go, anthropic.go)
// serialize []*aop.Message into the vendor wire format and parse responses
// back into aop types; nothing upstream of this package sees vendor JSON.

type ChatCompletionRequest struct {
	Model          string
	Messages       []*aop.Message
	Tools          []*aop.ToolDefinition
	MaxTokens      int
	Temperature    *float64
	Stream         bool
	CacheRetention CacheRetention
	SessionID      string
}

type ChatCompletionResponse struct {
	ID      string
	Choices []Choice
	Usage   *aop.TokenUsage
	Error   *APIError
}

type Choice struct {
	Message      *aop.Message
	FinishReason string
}

// ChatCompletionStreamEvent is one parsed SSE chunk. A chunk may carry a text
// or reasoning delta and/or tool-call deltas; Role is set on the first chunk
// of a message.
type ChatCompletionStreamEvent struct {
	Role         string
	MessageDelta *aop.MessageDelta
	ToolDeltas   []*aop.ToolCallDelta
	FinishReason string
	Usage        *aop.TokenUsage
	Done         bool
	Err          error
}

type APIError struct {
	Message    string      `json:"message"`
	Type       string      `json:"type"`
	Code       string      `json:"code"`
	StatusCode int         `json:"-"`
	Header     http.Header `json:"-"`
}

func (e *APIError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Message)
	}
	if e.Type != "" {
		return fmt.Sprintf("API error [%s]: %s", e.Type, e.Message)
	}
	return fmt.Sprintf("API error: %s", e.Message)
}

func (e *APIError) IsRetryable() bool {
	switch e.StatusCode {
	case 408, 409, 429, 500, 502, 503, 529:
		return true
	default:
		return false
	}
}

func IsImageUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "image_url") ||
		strings.Contains(msg, "image url") ||
		(strings.Contains(msg, "image") && strings.Contains(msg, "not support"))
}

// --- aop.Message constructors used across the agent ---

func TextMessage(role, content string) *aop.Message {
	return &aop.Message{Role: role, Content: []*aop.Content{aop.Text(content)}}
}

func ToolResultMessage(callID string, result *aop.ToolResult) *aop.Message {
	result.CallId = callID
	return &aop.Message{Role: "tool", Content: []*aop.Content{{Value: &aop.Content_ToolResult{ToolResult: result}}}}
}

// MessageText joins the text parts of an aop message.
func MessageText(msg *aop.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Content {
		if text := part.GetText(); text != nil {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

// MessageReasoning joins the reasoning parts of an aop message.
func MessageReasoning(msg *aop.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Content {
		if reasoning := part.GetReasoning(); reasoning != nil {
			sb.WriteString(reasoning.Text)
		}
	}
	return sb.String()
}

// MessageToolCalls extracts the tool calls carried by an assistant message.
func MessageToolCalls(msg *aop.Message) []*aop.ToolCall {
	if msg == nil {
		return nil
	}
	var calls []*aop.ToolCall
	for _, part := range msg.Content {
		if call := part.GetToolCall(); call != nil {
			calls = append(calls, call)
		}
	}
	return calls
}

// MessageToolResult returns the tool result carried by a tool-role message.
func MessageToolResult(msg *aop.Message) *aop.ToolResult {
	if msg == nil {
		return nil
	}
	for _, part := range msg.Content {
		if result := part.GetToolResult(); result != nil {
			return result
		}
	}
	return nil
}

// StripImageParts rewrites media parts into a placeholder note for models
// without image support.
func StripImageParts(msgs []*aop.Message) []*aop.Message {
	out := make([]*aop.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		hasImage := false
		for _, part := range m.Content {
			if part.GetMedia() != nil {
				hasImage = true
				break
			}
		}
		if !hasImage {
			continue
		}
		filtered := make([]*aop.Content, 0, len(m.Content)+1)
		for _, part := range m.Content {
			if part.GetMedia() == nil {
				filtered = append(filtered, part)
			}
		}
		filtered = append(filtered, aop.Text("[image omitted: model does not support images]"))
		out[i] = &aop.Message{Id: m.Id, Role: m.Role, Name: m.Name, Content: filtered}
	}
	return out
}

// TokenUsage builds the canonical usage proto from vendor-reported counters.
func TokenUsage(promptTokens, completionTokens, totalTokens, cacheRead, cacheWrite int) *aop.TokenUsage {
	if totalTokens <= 0 {
		totalTokens = promptTokens + completionTokens
	}
	return &aop.TokenUsage{
		InputTokens:  uint64(max(promptTokens, 0)),
		OutputTokens: uint64(max(completionTokens, 0)),
		TotalTokens:  uint64(max(totalTokens, 0)),
		Detail: map[string]uint64{
			"cache_read":  uint64(max(cacheRead, 0)),
			"cache_write": uint64(max(cacheWrite, 0)),
		},
	}
}

// CacheHitRatio returns the proportion of prompt tokens served from cache.
func CacheHitRatio(usage *aop.TokenUsage) float64 {
	if usage == nil || usage.InputTokens == 0 {
		return 0
	}
	return float64(usage.Detail["cache_read"]) / float64(usage.InputTokens)
}

// UsageTotalTokens prefers the vendor-reported total and falls back to the
// sum of input and output tokens.
func UsageTotalTokens(usage *aop.TokenUsage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return int(usage.TotalTokens)
	}
	return int(usage.InputTokens + usage.OutputTokens)
}
