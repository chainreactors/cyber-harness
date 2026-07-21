package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

// StdioRequest describes the single agent run accepted on stdin.
type StdioRequest struct {
	Prompt       string             `json:"prompt"`
	SessionID    string             `json:"session_id,omitempty"`
	SystemPrompt string             `json:"system_prompt,omitempty"`
	OutputSchema *StdioOutputSchema `json:"output_schema,omitempty"`
}

type StdioOutputSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// RunStdio executes exactly one request. stdin contains one StdioRequest and
// stdout is raw AOP JSONL; stderr remains owned by the caller's logger.
func RunStdio(
	ctx context.Context,
	option *cfg.Option,
	logger telemetry.Logger,
	input io.Reader,
	output io.Writer,
) error {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sender := newStdioSender(output, cancel, newStdioSessionID())

	var request StdioRequest
	if err := json.NewDecoder(input).Decode(&request); err != nil {
		runErr := fmt.Errorf("decode stdio request: %w", err)
		_ = sender.Fail(runErr)
		return runErr
	}
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.SystemPrompt = strings.TrimSpace(request.SystemPrompt)
	sender.SetFallbackSession(request.SessionID)
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		runErr := fmt.Errorf("stdio prompt is empty")
		_ = sender.Fail(runErr)
		return runErr
	}

	rt, err := NewAgentRuntime(runCtx, option, logger, &RuntimeConfig{NoOutput: true})
	if err != nil {
		_ = sender.Fail(err)
		return err
	}
	defer rt.Close()

	unsub := rt.Bus.Subscribe(func(event agent.Event) {
		for _, protocolEvent := range aop.FromAgentEvent(event, "aiscan") {
			if sendErr := sender.Send(protocolEvent); sendErr != nil {
				return
			}
		}
	})
	defer unsub()

	systemPrompt := joinSystemPrompts(rt.SystemPrompt, request.SystemPrompt)
	agentConfig := rt.Config.WithStream(true)
	if request.SessionID != "" {
		agentConfig = agentConfig.WithSessionID(request.SessionID)
	}
	if request.OutputSchema != nil {
		responseFormat, schemaInstruction, formatErr := responseFormatFromSchema(
			request.OutputSchema,
			provider.StructuredOutputModeFor(rt.App.ProviderConfig),
		)
		if formatErr != nil {
			_ = sender.Fail(formatErr)
			return formatErr
		}
		agentConfig = agentConfig.WithResponseFormat(responseFormat)
		systemPrompt = joinSystemPrompts(systemPrompt, schemaInstruction)
	}
	agentConfig = agentConfig.WithSystemPrompt(systemPrompt)

	_, runErr := agent.NewAgent(agentConfig).Run(runCtx, prompt)
	if transportErr := sender.Err(); transportErr != nil {
		return fmt.Errorf("write AOP stdout: %w", transportErr)
	}
	if runErr != nil {
		if err := sender.Fail(runErr); err != nil {
			return fmt.Errorf("write AOP stdout: %w", err)
		}
		return runErr
	}

	if err := sender.Complete(); err != nil {
		return fmt.Errorf("write AOP stdout: %w", err)
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

func responseFormatFromSchema(
	schema *StdioOutputSchema,
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
	mu              sync.Mutex
	enc             *json.Encoder
	cancel          context.CancelCauseFunc
	err             error
	fallbackSession string
	rootSession     string
	agentName       string
	sentError       bool
	terminal        bool
}

func newStdioSender(output io.Writer, cancel context.CancelCauseFunc, fallbackSession string) *stdioSender {
	return &stdioSender{
		enc:             json.NewEncoder(output),
		cancel:          cancel,
		fallbackSession: fallbackSession,
		agentName:       "aiscan",
	}
}

func newStdioSessionID() string {
	return fmt.Sprintf("stdio-%d", time.Now().UnixNano())
}

func (s *stdioSender) SetFallbackSession(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	s.fallbackSession = sessionID
	s.mu.Unlock()
}

func (s *stdioSender) Send(event aop.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.Agent != "" {
		s.agentName = event.Agent
	}
	if event.Type == aop.TypeSessionStart {
		var data aop.SessionStartData
		if json.Unmarshal(event.Data, &data) == nil && data.ParentSessionID == "" && s.rootSession == "" {
			s.rootSession = event.SessionID
		}
	}
	if event.Type == aop.TypeError && event.SessionID == s.sessionIDLocked() {
		s.sentError = true
	}
	if event.Type == aop.TypeSessionEnd && event.SessionID == s.sessionIDLocked() {
		s.terminal = true
	}
	return s.encodeLocked(event)
}

func (s *stdioSender) Fail(cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID := s.sessionIDLocked()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if !s.sentError {
		data, _ := json.Marshal(aop.ErrorData{Message: cause.Error()})
		if err := s.encodeLocked(aop.Event{
			Type: aop.TypeError, TS: now, SessionID: sessionID, Agent: s.agentName, Data: data,
		}); err != nil {
			return err
		}
		s.sentError = true
	}
	if s.terminal {
		return nil
	}
	data, _ := json.Marshal(aop.SessionEndData{Stop: string(agent.StopReasonError), Error: cause.Error()})
	if err := s.encodeLocked(aop.Event{
		Type: aop.TypeSessionEnd, TS: now, SessionID: sessionID, Agent: s.agentName, Data: data,
	}); err != nil {
		return err
	}
	s.terminal = true
	return nil
}

func (s *stdioSender) Complete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return nil
	}
	data, _ := json.Marshal(aop.SessionEndData{Stop: string(agent.StopReasonCompleted)})
	if err := s.encodeLocked(aop.Event{
		Type:      aop.TypeSessionEnd,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: s.sessionIDLocked(),
		Agent:     s.agentName,
		Data:      data,
	}); err != nil {
		return err
	}
	s.terminal = true
	return nil
}

func (s *stdioSender) sessionIDLocked() string {
	if s.rootSession != "" {
		return s.rootSession
	}
	return s.fallbackSession
}

func (s *stdioSender) encodeLocked(value any) error {
	if s.err != nil {
		return s.err
	}
	if err := s.enc.Encode(value); err != nil {
		s.err = err
		s.cancel(err)
		return err
	}
	return nil
}

func (s *stdioSender) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
