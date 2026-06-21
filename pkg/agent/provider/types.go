package provider

import (
	"encoding/json"
	"fmt"
	"strings"

)

// CacheRetention controls prompt caching behavior across providers.
type CacheRetention string

const (
	CacheNone  CacheRetention = ""      // no caching (zero value, backward compatible)
	CacheShort CacheRetention = "short" // Anthropic ephemeral / OpenAI automatic
	CacheLong  CacheRetention = "long"  // Anthropic ephemeral+TTL / OpenAI 24h retention
)

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

func ImagePart(mimeType, base64Data, detail string) ContentPart {
	return ContentPart{
		Type:     "image_url",
		ImageURL: &ImageURL{URL: "data:" + mimeType + ";base64," + base64Data, Detail: detail},
	}
}

type ChatMessage struct {
	Role             string        `json:"role"`
	Content          *string       `json:"content,omitempty"`
	ContentParts     []ContentPart `json:"-"`
	ReasoningContent *string       `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
}

func (m ChatMessage) MarshalJSON() ([]byte, error) {
	if len(m.ContentParts) == 0 {
		type plain ChatMessage
		return json.Marshal(plain(m))
	}
	obj := map[string]interface{}{"role": m.Role, "content": m.ContentParts}
	if m.ReasoningContent != nil {
		obj["reasoning_content"] = *m.ReasoningContent
	}
	if len(m.ToolCalls) > 0 {
		obj["tool_calls"] = m.ToolCalls
	}
	if m.ToolCallID != "" {
		obj["tool_call_id"] = m.ToolCallID
	}
	return json.Marshal(obj)
}

func NewMultimodalMessage(role string, parts []ContentPart) ChatMessage {
	return ChatMessage{Role: role, ContentParts: parts}
}

func ParseDataURI(dataURI string) (mediaType, base64Data string) {
	rest, ok := strings.CutPrefix(dataURI, "data:")
	if !ok {
		return "", dataURI
	}
	parts := strings.SplitN(rest, ";base64,", 2)
	if len(parts) != 2 {
		return "", dataURI
	}
	return parts[0], parts[1]
}

// stripImageParts returns a copy of msgs with all image_url content parts
// removed and replaced by a text notice.  Used when the target provider does
// not support multimodal input.
func stripImageParts(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(msgs))
	for i, m := range msgs {
		if len(m.ContentParts) == 0 {
			out[i] = m
			continue
		}
		hasImage := false
		for _, p := range m.ContentParts {
			if p.Type == "image_url" {
				hasImage = true
				break
			}
		}
		if !hasImage {
			out[i] = m
			continue
		}
		filtered := make([]ContentPart, 0, len(m.ContentParts))
		for _, p := range m.ContentParts {
			if p.Type != "image_url" {
				filtered = append(filtered, p)
			}
		}
		filtered = append(filtered, TextPart("[image omitted: model does not support images]"))
		cp := m
		cp.ContentParts = filtered
		out[i] = cp
	}
	return out
}

type ChatMessageDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          *string         `json:"content,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCallDelta `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type ToolCallDelta struct {
	Index    int               `json:"index,omitempty"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function FunctionCallDelta `json:"function,omitempty"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type FunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *JSONSchemaSpec  `json:"json_schema,omitempty"`
}

type JSONSchemaSpec struct {
	Name   string      `json:"name"`
	Schema interface{} `json:"schema"`
	Strict bool        `json:"strict,omitempty"`
}

type ChatCompletionRequest struct {
	Model          string           `json:"model"`
	Messages       []ChatMessage    `json:"messages"`
	Tools          []ToolDefinition `json:"tools,omitempty"`
	MaxTokens      int              `json:"max_tokens,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	Stream         bool             `json:"stream,omitempty"`
	ResponseFormat *ResponseFormat  `json:"response_format,omitempty"`
	CacheRetention CacheRetention   `json:"-"`
	SessionID      string           `json:"-"`
}

type ChatCompletionResponse struct {
	ID      string    `json:"id"`
	Choices []Choice  `json:"choices"`
	Usage   *Usage    `json:"usage,omitempty"`
	Error   *APIError `json:"error,omitempty"`
}

type Choice struct {
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// CacheHitRatio returns the proportion of prompt tokens served from cache,
// based on the API response. Returns 0 when no cache data is available.
func (u *Usage) CacheHitRatio() float64 {
	if u == nil || u.PromptTokens == 0 {
		return 0
	}
	return float64(u.CacheReadTokens) / float64(u.PromptTokens)
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	type plain Usage
	var raw struct {
		plain
		// OpenAI format
		PromptTokensDetails *struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
		// DeepSeek format
		PromptCacheHitTokens  *int `json:"prompt_cache_hit_tokens,omitempty"`
		PromptCacheMissTokens *int `json:"prompt_cache_miss_tokens,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = Usage(raw.plain)
	if raw.PromptTokensDetails != nil {
		u.CacheReadTokens = raw.PromptTokensDetails.CachedTokens
		u.CacheWriteTokens = raw.PromptTokensDetails.CacheWriteTokens
	} else if raw.PromptCacheHitTokens != nil {
		u.CacheReadTokens = *raw.PromptCacheHitTokens
		if raw.PromptCacheMissTokens != nil {
			u.CacheWriteTokens = *raw.PromptCacheMissTokens
		}
	}
	return nil
}

type APIError struct {
	Message    string `json:"message"`
	Type       string `json:"type"`
	Code       string `json:"code"`
	StatusCode int    `json:"-"`
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
	case 401, 403, 404:
		return false
	default:
		return e.StatusCode >= 400
	}
}

type ChatCompletionStreamEvent struct {
	Delta        ChatMessageDelta
	FinishReason string
	Usage        *Usage
	Done         bool
	Err          error
}

func NewTextMessage(role, content string) ChatMessage {
	return ChatMessage{Role: role, Content: &content}
}

func NewToolResultMessage(toolCallID, content string) ChatMessage {
	return ChatMessage{Role: "tool", Content: &content, ToolCallID: toolCallID}
}
