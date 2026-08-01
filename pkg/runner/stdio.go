package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"google.golang.org/protobuf/encoding/protojson"
)

// RunStdio carries generated ServerFrame/AgentFrame messages as protobuf JSONL.
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
	output io.Writer
	encErr error

	rt *AgentRuntime
}

func newStdioHost(ctx context.Context, option *cfg.Option, logger telemetry.Logger, output io.Writer) *stdioHost {
	return &stdioHost{
		ctx: ctx, option: option, logger: logger, output: output,
	}
}

func (h *stdioHost) init() error {
	rt, err := NewAgentRuntime(h.ctx, h.option, h.logger, &RuntimeConfig{NoOutput: true})
	if err != nil {
		return err
	}
	h.rt = rt
	rt.Subscribe(func(event *aop.Event) {
		_ = h.emit(&transport.AgentFrame{CorrelationId: event.TurnId, Payload: &transport.AgentFrame_Event{Event: event}})
	})
	return nil
}

func (h *stdioHost) close() {
	if h.rt != nil {
		h.rt.Close()
	}
}

func (h *stdioHost) emit(message *transport.AgentFrame) error {
	h.encMu.Lock()
	defer h.encMu.Unlock()
	if h.encErr != nil {
		return h.encErr
	}
	data, err := protojson.Marshal(message)
	if err == nil {
		data = append(data, '\n')
		_, err = h.output.Write(data)
	}
	if err != nil {
		h.encErr = err
		return err
	}
	return nil
}

func (h *stdioHost) emitError(correlationID string, err error) {
	_ = h.emit(operationError(correlationID, correlationID, err.Error()))
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
	message := new(transport.ServerFrame)
	if err := protojson.Unmarshal([]byte(line), message); err != nil {
		h.emitError("", fmt.Errorf("decode frame: %w", err))
		return
	}
	if h.rt == nil || !h.rt.HandleServerFrame(h.ctx, message, func(response *transport.AgentFrame) { _ = h.emit(response) }) {
		h.emitError(message.CorrelationId, fmt.Errorf("unsupported server frame"))
	}
}

func (h *stdioHost) drain() {
	if h.rt != nil {
		h.rt.WaitOperations()
	}
}
