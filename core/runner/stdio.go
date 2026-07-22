package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

// RunStdio hosts a persistent multi-session AOP endpoint. stdin carries AOP
// JSONL; each inbound user message selects (or creates) the agent session named
// by its envelope session_id. Messages to one session run FIFO; sessions run
// concurrently. stdout is the raw AOP event stream of all sessions. stdin EOF
// drains every session before exit.
func RunStdio(
	ctx context.Context,
	option *cfg.Option,
	logger telemetry.Logger,
	input io.Reader,
	output io.Writer,
) error {
	host := newStdioHost(ctx, option, logger, output)
	if err := host.init(); err != nil {
		return err
	}
	defer host.close()

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		host.accept(line)
	}
	if err := scanner.Err(); err != nil {
		host.failAll(fmt.Errorf("read stdin: %w", err))
	}
	host.drain()
	return host.err()
}

type stdioHost struct {
	ctx    context.Context
	option *cfg.Option
	logger telemetry.Logger

	encMu  sync.Mutex
	enc    *json.Encoder
	encErr error

	rt    *AgentRuntime
	rtErr error

	wg sync.WaitGroup
}

func newStdioHost(ctx context.Context, option *cfg.Option, logger telemetry.Logger, output io.Writer) *stdioHost {
	return &stdioHost{
		ctx:    ctx,
		option: option,
		logger: logger,
		enc:    json.NewEncoder(output),
	}
}

func (h *stdioHost) init() error {
	rt, err := NewAgentRuntime(h.ctx, h.option, h.logger, &RuntimeConfig{NoOutput: true})
	if err != nil {
		return err
	}
	h.rt = rt
	rt.Bus.Subscribe(func(event aop.Event) {
		_ = h.emit(event)
	})
	return nil
}

func (h *stdioHost) close() {
	if h.rt != nil {
		h.rt.Close()
	}
}

func (h *stdioHost) err() error {
	h.encMu.Lock()
	defer h.encMu.Unlock()
	if h.encErr != nil {
		return fmt.Errorf("write AOP stdout: %w", h.encErr)
	}
	return nil
}

func (h *stdioHost) emit(event aop.Event) error {
	h.encMu.Lock()
	defer h.encMu.Unlock()
	if h.encErr != nil {
		return h.encErr
	}
	if err := h.enc.Encode(event); err != nil {
		h.encErr = err
		return err
	}
	return nil
}

// emitLocal writes a synthetic event not produced by any agent (transport-level
// errors: bad frames, queue overflow).
func (h *stdioHost) emitLocal(typ, sessionID string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = h.emit(aop.Event{
		Type:      typ,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		Agent:     "aiscan",
		Data:      raw,
	})
}

func (h *stdioHost) failAll(err error) {
	h.emitLocal(aop.TypeError, "stdio", aop.ErrorData{Message: err.Error()})
}

// accept parses one inbound JSONL line and enqueues it to its session.
func (h *stdioHost) accept(line string) {
	var event aop.Event
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		h.emitLocal(aop.TypeError, "stdio", aop.ErrorData{Message: "decode inbound event: " + err.Error()})
		return
	}
	inbound, err := agent.Classify(event)
	if err != nil || inbound.Kind != agent.InboundUserMessage {
		h.emitLocal(aop.TypeError, event.SessionID, aop.ErrorData{Message: "invalid inbound event"})
		return
	}

	wait, err := h.rt.Submit(h.ctx, "", inbound, nil)
	if err != nil {
		h.emitLocal(aop.TypeError, event.SessionID, aop.ErrorData{Message: err.Error()})
		return
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		if _, err := wait(); err != nil {
			h.emitLocal(aop.TypeError, event.SessionID, aop.ErrorData{Message: err.Error()})
		}
	}()
}

// drain waits for every accepted Runtime request. Session workers themselves
// remain Runtime-owned and are stopped by Runtime.Close.
func (h *stdioHost) drain() {
	h.wg.Wait()
}

// inputText flattens the text parts of an agent Input.
func inputText(in agent.Input) string {
	var sb strings.Builder
	for _, p := range in.Parts {
		if p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}
