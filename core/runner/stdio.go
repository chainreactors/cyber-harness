package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// RunStdio executes exactly one webproto chat message. stdin and stdout use
// the same message envelope as the websocket transport; stderr remains owned
// by the logger configured by the caller.
func RunStdio(
	ctx context.Context,
	option *cfg.Option,
	logger telemetry.Logger,
	input io.Reader,
	output io.Writer,
) error {
	var msg webproto.Message
	if err := json.NewDecoder(input).Decode(&msg); err != nil {
		return fmt.Errorf("decode webproto message: %w", err)
	}
	if msg.Type != "chat" {
		return fmt.Errorf("unsupported webproto message type %q", msg.Type)
	}
	if strings.TrimSpace(msg.TaskID) == "" {
		return fmt.Errorf("webproto chat task_id is required")
	}
	prompt := strings.TrimSpace(msg.Data)
	if prompt == "" {
		return fmt.Errorf("webproto chat data is empty")
	}
	opts, err := webproto.DecodeChatPayload(msg.Payload)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sender := newStdioSender(output, cancel)

	rt, err := NewAgentRuntime(runCtx, option, logger, nil)
	if err != nil {
		_ = sender.Send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return err
	}
	defer rt.Close()

	unsub := rt.Bus.Subscribe(func(event agent.Event) {
		for _, protocolEvent := range aop.FromAgentEvent(event, "aiscan") {
			payload, marshalErr := json.Marshal(protocolEvent)
			if marshalErr != nil {
				sender.Fail(marshalErr)
				return
			}
			if sendErr := sender.Send(webproto.Message{
				Type:    "aop." + protocolEvent.Type,
				TaskID:  msg.TaskID,
				Payload: payload,
			}); sendErr != nil {
				return
			}
		}
	})
	defer unsub()

	systemPrompt := joinSystemPrompts(rt.SystemPrompt, opts.SystemPrompt)
	agentConfig := rt.Config.WithStream(true)
	if opts.SessionID != "" {
		agentConfig = agentConfig.WithSessionID(opts.SessionID)
	}
	if opts.OutputSchema != nil {
		responseFormat, schemaInstruction, formatErr := responseFormatFromWebProto(
			opts.OutputSchema,
			provider.StructuredOutputModeFor(rt.App.ProviderConfig),
		)
		if formatErr != nil {
			_ = sender.Send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: formatErr.Error()})
			return formatErr
		}
		agentConfig = agentConfig.WithResponseFormat(responseFormat)
		systemPrompt = joinSystemPrompts(systemPrompt, schemaInstruction)
	}
	agentConfig = agentConfig.WithSystemPrompt(systemPrompt)

	result, runErr := agent.NewAgent(agentConfig).Run(runCtx, prompt)
	if transportErr := sender.Err(); transportErr != nil {
		return fmt.Errorf("write webproto stdout: %w", transportErr)
	}
	if runErr != nil {
		if err := sender.Send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: runErr.Error()}); err != nil {
			return fmt.Errorf("write webproto stdout: %w", err)
		}
		return runErr
	}

	final := ""
	if result != nil {
		final = result.Output
	}
	if err := sender.Send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Data: final}); err != nil {
		return fmt.Errorf("write webproto stdout: %w", err)
	}
	return nil
}

func joinSystemPrompts(base, caller string) string {
	base = strings.TrimSpace(base)
	caller = strings.TrimSpace(caller)
	switch {
	case base == "":
		return caller
	case caller == "":
		return base
	default:
		return base + "\n\n" + caller
	}
}

func responseFormatFromWebProto(
	schema *webproto.OutputSchema,
	mode provider.StructuredOutputMode,
) (*agent.ResponseFormat, string, error) {
	if schema == nil {
		return nil, "", nil
	}
	name := strings.TrimSpace(schema.Name)
	if name == "" {
		return nil, "", fmt.Errorf("output_schema.name is required")
	}
	var value map[string]any
	if err := json.Unmarshal(schema.Schema, &value); err != nil {
		return nil, "", fmt.Errorf("decode output_schema.schema: %w", err)
	}
	if value == nil {
		return nil, "", fmt.Errorf("output_schema.schema must be a JSON object")
	}
	if mode == provider.StructuredOutputJSONObject {
		compact, err := json.Marshal(value)
		if err != nil {
			return nil, "", fmt.Errorf("encode output_schema.schema: %w", err)
		}
		instruction := "Return only one valid JSON object matching this JSON Schema exactly:\n" + string(compact)
		return &agent.ResponseFormat{Type: string(provider.StructuredOutputJSONObject)}, instruction, nil
	}
	return &agent.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &agent.JSONSchemaSpec{
			Name:   name,
			Schema: value,
			Strict: true,
		},
	}, "", nil
}

type stdioSender struct {
	mu     sync.Mutex
	enc    *json.Encoder
	cancel context.CancelCauseFunc
	err    error
}

func newStdioSender(output io.Writer, cancel context.CancelCauseFunc) *stdioSender {
	return &stdioSender{enc: json.NewEncoder(output), cancel: cancel}
}

func (s *stdioSender) Send(msg webproto.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if err := s.enc.Encode(msg); err != nil {
		s.err = err
		s.cancel(err)
		return err
	}
	return nil
}

func (s *stdioSender) Fail(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.err == nil {
		s.err = err
		s.cancel(err)
	}
	s.mu.Unlock()
}

func (s *stdioSender) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
