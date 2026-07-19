// Package aop implements Agent Output Protocol v1 — a language-neutral
// JSONL event protocol for AI coding agents.
package aop

import "encoding/json"

const Version = 1

// Event is the AOP envelope. Every JSONL line is one Event.
type Event struct {
	V         int             `json:"v"`
	Type      string          `json:"type"`
	TS        string          `json:"ts"`
	SessionID string          `json:"session_id"`
	Agent     string          `json:"agent"`
	Seq       int             `json:"seq,omitempty"`
	Data      json.RawMessage `json:"data"`
	Ext       map[string]any  `json:"ext,omitempty"`
}

// ── Core event types ────────────────────────────────────────────

const (
	TypeSessionStart = "session.start"
	TypeSessionEnd   = "session.end"
	TypeText         = "text"
	TypeToolCall     = "tool.call"
	TypeToolResult   = "tool.result"
	TypeUsage        = "usage"
	TypeTurnStart    = "turn.start"
	TypeTurnEnd      = "turn.end"
	TypeError        = "error"
	TypeStatus       = "status"
)

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

type TextData struct {
	Content string `json:"content"`
	Role    string `json:"role,omitempty"`
	Delta   bool   `json:"delta,omitempty"` // append-only streaming fragment
}

type ToolCallData struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Args       any    `json:"args"`
}

type ToolResultData struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name,omitempty"`
	Content    any    `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
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
