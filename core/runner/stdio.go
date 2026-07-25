package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// RunStdio hosts the same explicit Session/Run/Command protocol used by the
// WebSocket adapter. Both stdin and stdout are webproto.Message JSONL streams.
func RunStdio(ctx context.Context, option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer) error {
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
		host.emitError("", fmt.Errorf("read stdin: %w", err))
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

	rt *AgentRuntime
}

func newStdioHost(ctx context.Context, option *cfg.Option, logger telemetry.Logger, output io.Writer) *stdioHost {
	return &stdioHost{
		ctx: ctx, option: option, logger: logger, enc: json.NewEncoder(output),
	}
}

func (h *stdioHost) init() error {
	rt, err := NewAgentRuntime(h.ctx, h.option, h.logger, &RuntimeConfig{NoOutput: true})
	if err != nil {
		return err
	}
	h.rt = rt
	rt.Subscribe(func(event aop.Event) {
		payload, err := json.Marshal(event)
		if err == nil {
			_ = h.emit(webproto.Message{Type: webproto.TypeAOP, TurnID: event.TurnID, Payload: payload})
		}
	})
	return nil
}

func (h *stdioHost) close() {
	if h.rt != nil {
		h.rt.Close()
	}
}

func (h *stdioHost) emit(message webproto.Message) error {
	h.encMu.Lock()
	defer h.encMu.Unlock()
	if h.encErr != nil {
		return h.encErr
	}
	if err := h.enc.Encode(message); err != nil {
		h.encErr = err
		return err
	}
	return nil
}

func (h *stdioHost) emitError(turnID string, err error) {
	payload, _ := json.Marshal(webproto.ErrorPayload{Message: err.Error()})
	_ = h.emit(webproto.Message{Type: webproto.TypeError, TurnID: turnID, Payload: payload})
}

func (h *stdioHost) err() error {
	h.encMu.Lock()
	defer h.encMu.Unlock()
	if h.encErr != nil {
		return fmt.Errorf("write stdio protocol: %w", h.encErr)
	}
	return nil
}

func (h *stdioHost) accept(line string) {
	var message webproto.Message
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		h.emitError("", fmt.Errorf("decode frame: %w", err))
		return
	}
	if h.rt == nil || !h.rt.HandleProtocol(h.ctx, message, func(response webproto.Message) { _ = h.emit(response) }) {
		h.emitError(message.TurnID, fmt.Errorf("unsupported frame type %q", message.Type))
	}
}

func (h *stdioHost) drain() {
	if h.rt != nil {
		h.rt.WaitOperations()
	}
}
