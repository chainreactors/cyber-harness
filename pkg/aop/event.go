// Package aop implements Agent Output Protocol — a language-neutral
// JSONL event protocol for AI coding agents.
package aop

import "encoding/json"

// Event is the AOP envelope. Every JSONL line is one Event.
type Event struct {
	Type      string          `json:"type"`
	TS        string          `json:"ts"`
	SessionID string          `json:"session_id"`
	Agent     string          `json:"agent"`
	Seq       int             `json:"seq,omitempty"`
	Data      json.RawMessage `json:"data"`
	Ext       map[string]any  `json:"ext,omitempty"`
}

// Valid reports whether the required AOP envelope fields are present.
func (e Event) Valid() bool {
	return e.Type != "" && e.TS != "" && e.SessionID != "" && e.Agent != "" && len(e.Data) > 0
}

// ── Core event types ────────────────────────────────────────────

const (
	TypeSessionStart = "session.start"
	TypeSessionEnd   = "session.end"
	TypeMessage      = "message"
	TypeMessageDelta = "message.delta"
	TypeToolCall     = "tool.call"
	TypeToolResult   = "tool.result"
	TypeUsage        = "usage"
	TypeTurnStart    = "turn.start"
	TypeTurnEnd      = "turn.end"
	TypeError        = "error"
	TypeStatus       = "status"
)

// ── Message parts ───────────────────────────────────────────────

const (
	PartText      = "text"
	PartReasoning = "reasoning"
	PartImage     = "image"
)

// ImageSource carries an image by local path or inline base64. URLs are
// not supported; exactly one of Path or Base64 is set.
type ImageSource struct {
	Path      string `json:"path,omitempty"`
	Base64    string `json:"base64,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type MessagePart struct {
	Type  string       `json:"type"`
	Text  string       `json:"text,omitempty"`
	Image *ImageSource `json:"image,omitempty"`
}

// ── Data payloads ───────────────────────────────────────────────

type SessionStartData struct {
	Model           string `json:"model,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
}

type SessionEndData struct {
	Stop  string `json:"stop"`
	Turns int    `json:"turns,omitempty"`
	Error string `json:"error,omitempty"`
}

// MessageData is a complete message. Assistant streaming produces a run of
// message.delta events followed by one authoritative message event; only the
// complete message is persisted.
type MessageData struct {
	MessageID string        `json:"message_id"`
	Role      string        `json:"role"`
	Parts     []MessagePart `json:"parts"`
}

// MessageDeltaData is an incremental fragment of one message part.
type MessageDeltaData struct {
	MessageID string `json:"message_id"`
	PartIndex int    `json:"part_index"`
	PartType  string `json:"part_type"`
	Delta     string `json:"delta"`
}

type ToolCallData struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Args       any    `json:"args"`
}

type ToolResultData struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name,omitempty"`
	// Content is a plain string, or a ToolResultContent when the tool
	// returned images alongside text.
	Content    any  `json:"content"`
	IsError    bool `json:"is_error,omitempty"`
	DurationMs int  `json:"duration_ms,omitempty"`
}

// ToolResultContent is the structured Content variant for tool results that
// include images.
type ToolResultContent struct {
	Content string        `json:"content"`
	Images  []ImageSource `json:"images,omitempty"`
}

type UsageData struct {
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int    `json:"cache_write_tokens,omitempty"`
	Model            string `json:"model,omitempty"`
}

type TurnData struct {
	Turn int `json:"turn"`
}

type ErrorData struct {
	Message   string `json:"message"`
	Code      string `json:"code,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

type StatusData struct {
	State string `json:"state"`
}
