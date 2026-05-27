package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/ioa"
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

type ToolDefinition = ioa.ToolDefinition

type FunctionDefinition = ioa.FunctionDefinition

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
	case 429, 500, 502, 503, 529:
		return true
	}
	return false
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
