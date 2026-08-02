package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"google.golang.org/protobuf/encoding/protojson"
)

// RunStdio carries AOP Envelope messages as protobuf JSONL.
func RunStdio(ctx context.Context, option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer) error {
	host := newStdioHost(ctx, option, logger, output)
	if err := host.init(); err != nil {
		return err
	}
	defer host.close()

	stream := newStdioEnvelopeStream(input, host.emit)
	err := host.rt.ServeEnvelopeStream(ctx, stream)
	host.drain()
	if writeErr := host.err(); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("stdio protocol: %w", err)
	}
	return nil
}

// stdioEnvelopeStream is the stdio framing adapter: one protobuf-JSON Envelope
// per line. It owns no runtime or namespace semantics.
type stdioEnvelopeStream struct {
	scanner *bufio.Scanner
	send    func(*aop.Envelope) error
}

func newStdioEnvelopeStream(input io.Reader, send func(*aop.Envelope) error) *stdioEnvelopeStream {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	return &stdioEnvelopeStream{scanner: scanner, send: send}
}

func (s *stdioEnvelopeStream) Recv() (*aop.Envelope, error) {
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		envelope := new(aop.Envelope)
		if err := protojson.Unmarshal([]byte(line), envelope); err != nil {
			return nil, fmt.Errorf("decode stdio envelope: %w", err)
		}
		return envelope, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return nil, io.EOF
}

func (s *stdioEnvelopeStream) Send(envelope *aop.Envelope) error {
	if s.send == nil {
		return fmt.Errorf("stdio envelope sender is unavailable")
	}
	return s.send(envelope)
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
	rt, err := NewAgentRuntime(h.ctx, h.option, h.logger, &RuntimeConfig{})
	if err != nil {
		return err
	}
	h.rt = rt
	rt.Subscribe(func(event *aop.Event) {
		envelope, err := aop.Wrap(runtimeEnvelopeID(), "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}})
		if err == nil {
			_ = h.emit(envelope)
		}
	})
	return nil
}

func (h *stdioHost) close() {
	if h.rt != nil {
		h.rt.Close()
	}
}

func (h *stdioHost) emit(message *aop.Envelope) error {
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
	_ = h.emit(runtimeReply(correlationID, runtimeProtocolError("STDIO_PROTOCOL_ERROR", err.Error())))
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
	message := new(aop.Envelope)
	if err := protojson.Unmarshal([]byte(line), message); err != nil {
		h.emitError("", fmt.Errorf("decode frame: %w", err))
		return
	}
	if h.rt == nil || !h.rt.HandleEnvelope(h.ctx, message, func(response *aop.Envelope) { _ = h.emit(response) }) {
		h.emitError(message.Id, fmt.Errorf("unsupported protocol message"))
	}
}

func (h *stdioHost) drain() {
	if h.rt != nil {
		h.rt.WaitOperations()
	}
}
