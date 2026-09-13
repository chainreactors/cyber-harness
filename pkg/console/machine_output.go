package console

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	"google.golang.org/protobuf/encoding/protojson"
)

// machineOutput is the non-interactive stdout renderer. stream-json is the
// canonical typed AOP stream; json is a compact result document intended for
// shell pipelines. Durable event persistence remains the eventoutput
// extension selected by -o/--output.
type machineOutput struct {
	mu     sync.Mutex
	writer io.Writer
	format string

	sessionID string
	turnID    string
	result    string
	stop      string
	failure   *aop.ProtocolError
	usage     *aop.TokenUsage
	err       error
	closed    bool
}

type machineResult struct {
	Type       string          `json:"type"`
	SessionID  string          `json:"session_id,omitempty"`
	TurnID     string          `json:"turn_id,omitempty"`
	IsError    bool            `json:"is_error"`
	StopReason string          `json:"stop_reason,omitempty"`
	Result     string          `json:"result,omitempty"`
	Error      *machineFailure `json:"error,omitempty"`
	Usage      *machineUsage   `json:"usage,omitempty"`
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

func newMachineOutput(writer io.Writer, format string) *machineOutput {
	if writer == nil {
		writer = io.Discard
	}
	return &machineOutput{writer: writer, format: strings.ToLower(strings.TrimSpace(format))}
}

func (o *machineOutput) HandleEvent(event *aop.Event) {
	if o == nil || event == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.err != nil {
		return
	}
	if o.format == "stream-json" {
		line, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(event)
		if err == nil {
			line = append(line, '\n')
			err = writeMachineOutput(o.writer, line)
		}
		if err != nil {
			o.err = fmt.Errorf("write stream-json output: %w", err)
		}
		return
	}

	o.sessionID = event.SessionId
	if event.TurnId != "" {
		o.turnID = event.TurnId
	}
	if message := event.GetMessage(); message != nil && message.Role == "assistant" {
		o.result = strings.TrimSpace(messagePartText(message, false))
	}
	if usage := event.GetUsage(); usage != nil {
		o.usage = usage
	}
	if ended := event.GetTurnEnded(); ended != nil {
		o.stop = ended.StopReason
		o.failure = ended.Error
		if ended.Usage != nil {
			o.usage = ended.Usage
		}
	}
}

func (o *machineOutput) SetError(err error) {
	if o == nil || err == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failure == nil {
		o.failure = &aop.ProtocolError{Code: "execution_error", Message: err.Error()}
	}
	if o.stop == "" {
		o.stop = string(agent.StopReasonError)
	}
}

func (o *machineOutput) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.err
	}
	o.closed = true
	if o.format == "stream-json" {
		return o.err
	}

	result := machineResult{
		Type:       "result",
		SessionID:  o.sessionID,
		TurnID:     o.turnID,
		StopReason: o.stop,
		Result:     o.result,
	}
	if o.failure != nil {
		result.Error = &machineFailure{Code: o.failure.Code, Message: o.failure.Message, Retryable: o.failure.Retryable}
	}
	result.IsError = result.Error != nil || o.stop == string(agent.StopReasonError) || o.stop == string(agent.StopReasonCanceled) || o.stop == string(agent.StopReasonBudget)
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
