package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

const (
	eventPreviewLimit = 4 * 1024
	eventResultLimit  = 16 * 1024
)

// eventsWriter persists every agent.Event to a JSONL file. Each line is one
// event, fsync'd before the call returns, so the file remains a faithful
// audit log even if the agent process is SIGKILL'd later.
type eventsWriter struct {
	mu   sync.Mutex
	file *os.File
	path string
}

func newEventsWriter(path string) (*eventsWriter, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open events file %s: %w", path, err)
	}
	return &eventsWriter{file: f, path: path}, nil
}

func (w *eventsWriter) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.file.Close()
	w.file = nil
	return err
}

// HandleEvent serializes the event and appends one JSONL line. It never
// blocks the agent loop on a write error — that would defeat the purpose
// of "agent should keep running even if observability is broken" — but it
// returns the error so the caller can log it.
func (w *eventsWriter) HandleEvent(_ context.Context, event agent.Event) error {
	if w == nil || w.file == nil {
		return nil
	}
	line, err := json.Marshal(serializableEvent(event))
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Write(line); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sync events file: %w", err)
	}
	return nil
}

type eventJSON struct {
	Timestamp     string                 `json:"ts"`
	Type          agent.EventType        `json:"type"`
	Turn          int                    `json:"turn,omitempty"`
	Message       *messageJSON           `json:"message,omitempty"`
	ToolResults   []messageJSON          `json:"tool_results,omitempty"`
	ToolCallID    string                 `json:"tool_call_id,omitempty"`
	ToolName      string                 `json:"tool_name,omitempty"`
	Arguments     string                 `json:"arguments,omitempty"`
	Result        string                 `json:"result,omitempty"`
	IsError       bool                   `json:"is_error,omitempty"`
	Error         string                 `json:"error,omitempty"`
	NewMessages   int                    `json:"new_messages,omitempty"`
	Usage         *provider.Usage        `json:"usage,omitempty"`
	ContextTokens int                    `json:"context_tokens,omitempty"`
}

type messageJSON struct {
	Role       string                     `json:"role"`
	Content    string                     `json:"content,omitempty"`
	ToolCalls  []messageToolCallJSON      `json:"tool_calls,omitempty"`
	ToolCallID string                     `json:"tool_call_id,omitempty"`
}

type messageToolCallJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func serializableEvent(e agent.Event) eventJSON {
	out := eventJSON{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Type:       e.Type,
		Turn:       e.Turn,
		ToolCallID: e.ToolCallID,
		ToolName:   e.ToolName,
		Arguments:  truncate(e.Arguments, eventPreviewLimit),
		Result:     truncate(e.Result, eventResultLimit),
		IsError:    e.IsError,
	}
	if e.Err != nil {
		out.Error = e.Err.Error()
	}
	if m := toMessageJSON(e.Message); m != nil {
		out.Message = m
	}
	// Per-turn ToolResults can be many; serialize them but truncate content.
	for _, msg := range e.ToolResults {
		if m := toMessageJSON(msg); m != nil {
			out.ToolResults = append(out.ToolResults, *m)
		}
	}
	// Messages/NewMessages on agent_end are the full transcript — record
	// only counts to keep the file readable.
	if len(e.NewMessages) > 0 {
		out.NewMessages = len(e.NewMessages)
	}
	if e.Usage != nil {
		out.Usage = e.Usage
		out.ContextTokens = e.ContextTokens
	}
	return out
}

func toMessageJSON(msg provider.ChatMessage) *messageJSON {
	if msg.Role == "" && msg.Content == nil && len(msg.ToolCalls) == 0 && msg.ToolCallID == "" {
		return nil
	}
	out := &messageJSON{
		Role:       msg.Role,
		ToolCallID: msg.ToolCallID,
	}
	if msg.Content != nil {
		out.Content = truncate(*msg.Content, eventPreviewLimit)
	}
	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, messageToolCallJSON{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: truncate(tc.Function.Arguments, eventPreviewLimit),
		})
	}
	return out
}

func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("...[truncated %d bytes]", len(s)-limit)
}

// combineEventHandlers fans an event out to multiple handlers and returns
// the first error encountered. Handlers run in order; a later handler still
// runs even if an earlier one errored, so a broken writer doesn't suppress
// the rest of the observability stack.
func combineEventHandlers(handlers ...agent.EventHandler) agent.EventHandler {
	cleaned := make([]agent.EventHandler, 0, len(handlers))
	for _, h := range handlers {
		if h != nil {
			cleaned = append(cleaned, h)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	if len(cleaned) == 1 {
		return cleaned[0]
	}
	return func(ctx context.Context, event agent.Event) error {
		var firstErr error
		for _, h := range cleaned {
			if err := h(ctx, event); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
}
