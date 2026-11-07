package output

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/events"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// EventWriter renders the same typed events for live tasks, scanners and replay.
// JSONL streams immediately; document formats finish when Close is called.
type EventWriter struct {
	mu     sync.Mutex
	writer io.Writer
	format string

	sessionID string
	turnID    string
	result    string
	stop      string
	failure   *aop.ProtocolError
	usage     *aop.TokenUsage
	turnUsage map[string]*aop.TokenUsage
	err       error
	closed    bool
	events    []*aop.Event
	artifacts []*toolpb.Artifact
}

type machineResult struct {
	Type       string             `json:"type"`
	SessionID  string             `json:"session_id,omitempty"`
	TurnID     string             `json:"turn_id,omitempty"`
	IsError    bool               `json:"is_error"`
	StopReason string             `json:"stop_reason,omitempty"`
	Result     string             `json:"result,omitempty"`
	Error      *machineFailure    `json:"error,omitempty"`
	Usage      *machineUsage      `json:"usage,omitempty"`
	Artifacts  []*toolpb.Artifact `json:"artifacts,omitempty"`
}

type machineFailure struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type machineUsage struct {
	InputTokens  uint64            `json:"input_tokens,omitempty"`
	OutputTokens uint64            `json:"output_tokens,omitempty"`
	TotalTokens  uint64            `json:"total_tokens,omitempty"`
	Model        string            `json:"model,omitempty"`
	Detail       map[string]uint64 `json:"detail,omitempty"`
}

func NewEventWriter(writer io.Writer, format string) *EventWriter {
	if writer == nil {
		writer = io.Discard
	}
	normalized, err := cfg.NormalizeOutputFormat(format)
	return &EventWriter{writer: writer, format: normalized, err: err}
}

// WriteStartupError preserves the one-shot output contract before RunTask owns
// presentation. No session has started, so the failure has no session/turn ID.
// Text-mode errors remain the caller's responsibility.
func WriteStartupError(writer io.Writer, format string, err error) error {
	format, _ = cfg.NormalizeOutputFormat(format)
	if err == nil || format != "json" && format != "jsonl" {
		return nil
	}
	output := NewEventWriter(writer, format)
	if format == "jsonl" {
		event := &aop.Event{Payload: &aop.Event_Error{Error: &aop.ProtocolError{Code: "startup_error", Message: err.Error()}}}
		// A standalone stream stamps this pre-session failure's envelope.
		events.New().Publish(event)
		output.HandleEvent(event)
	} else {
		output.SetError(err)
		output.failure.Code = "startup_error"
	}
	return output.Close()
}

func (o *EventWriter) HandleEvent(event *aop.Event) {
	if o == nil || event == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.err != nil {
		return
	}
	if o.format == "jsonl" {
		line, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(event)
		if err == nil {
			line = append(line, '\n')
			err = writeMachineOutput(o.writer, line)
		}
		if err != nil {
			o.err = fmt.Errorf("write jsonl output: %w", err)
		}
		return
	}

	if o.format != "json" {
		o.events = append(o.events, proto.Clone(event).(*aop.Event))
		return
	}
	artifact, _, found, err := toolpb.FromEvent(event)
	if err != nil {
		o.err = err
		return
	}
	if found {
		o.artifacts = append(o.artifacts, artifact)
	}
	o.sessionID = event.SessionId
	if event.TurnId != "" {
		o.turnID = event.TurnId
	}
	if event.GetTurnStarted() != nil {
		o.result, o.stop, o.failure = "", "", nil
	}
	if message := event.GetMessage(); message != nil && message.Role == "assistant" {
		var text []string
		for _, content := range message.Content {
			if value := content.GetText().GetText(); value != "" {
				text = append(text, value)
			}
		}
		o.result = strings.TrimSpace(strings.Join(text, "\n"))
	}
	if usage := event.GetUsage(); usage != nil {
		o.updateUsage(o.turnID, usage, false)
	}
	if ended := event.GetTurnEnded(); ended != nil {
		o.stop = ended.StopReason
		o.failure = ended.Error
		if ended.Usage != nil {
			o.updateUsage(o.turnID, ended.Usage, true)
		}
	}
}

// Usage events report provider calls; TurnEnded supplies the authoritative turn
// total. Replace that turn at its end and add distinct repair turns exactly once.
func (o *EventWriter) updateUsage(turnID string, usage *aop.TokenUsage, final bool) {
	if o.turnUsage == nil {
		o.turnUsage = make(map[string]*aop.TokenUsage)
	}
	snapshot := &aop.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens, Model: usage.Model, Detail: maps.Clone(usage.Detail)}
	if previous := o.turnUsage[turnID]; previous != nil && !final {
		snapshot.InputTokens += previous.InputTokens
		snapshot.OutputTokens += previous.OutputTokens
		snapshot.TotalTokens += previous.TotalTokens
		if snapshot.Detail == nil {
			snapshot.Detail = make(map[string]uint64)
		}
		for key, value := range previous.Detail {
			snapshot.Detail[key] += value
		}
	}
	o.turnUsage[turnID] = snapshot
	total := &aop.TokenUsage{Model: usage.Model, Detail: make(map[string]uint64)}
	for _, snapshot := range o.turnUsage {
		total.InputTokens += snapshot.InputTokens
		total.OutputTokens += snapshot.OutputTokens
		total.TotalTokens += snapshot.TotalTokens
		for key, value := range snapshot.Detail {
			total.Detail[key] += value
		}
	}
	o.usage = total
}

func (o *EventWriter) SetError(err error) {
	if o == nil || err == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failure == nil {
		o.failure = &aop.ProtocolError{Code: "execution_error", Message: err.Error()}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		o.stop = string(agenthooks.StopReasonCanceled)
	} else {
		switch o.stop {
		case "", string(agenthooks.StopReasonCompleted), string(agenthooks.StopReasonStopped), string(agenthooks.StopReasonTerminated):
			o.stop = string(agenthooks.StopReasonError)
		}
	}
}

func (o *EventWriter) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.err
	}
	o.closed = true
	if o.err != nil || o.format == "jsonl" {
		return o.err
	}
	switch o.format {
	case "text":
		o.err = RenderEvents(o.writer, o.events)
	case "markdown":
		o.err = RenderEventsMarkdown(o.writer, o.events)
	case "csv":
		o.err = renderEventsCSV(o.writer, o.events)
	}
	if o.format != "json" {
		o.events = nil
		return o.err
	}

	result := machineResult{
		Type:       "result",
		SessionID:  o.sessionID,
		TurnID:     o.turnID,
		StopReason: o.stop,
		Result:     o.result,
		Artifacts:  o.artifacts,
	}
	if o.failure != nil {
		result.Error = &machineFailure{Code: o.failure.Code, Message: o.failure.Message, Retryable: o.failure.Retryable}
	}
	result.IsError = result.Error != nil || o.stop == string(agenthooks.StopReasonError) || o.stop == string(agenthooks.StopReasonCanceled) || o.stop == string(agenthooks.StopReasonBudget)
	if o.usage != nil {
		result.Usage = &machineUsage{
			InputTokens: o.usage.InputTokens, OutputTokens: o.usage.OutputTokens,
			TotalTokens: o.usage.TotalTokens, Model: o.usage.Model, Detail: o.usage.Detail,
		}
	}
	data, err := json.Marshal(result)
	if err == nil {
		data = append(data, '\n')
		err = writeMachineOutput(o.writer, data)
	}
	if err != nil {
		o.err = fmt.Errorf("write json output: %w", err)
	}
	return o.err
}

func writeMachineOutput(writer io.Writer, data []byte) error {
	n, err := writer.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
