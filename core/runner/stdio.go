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

	rt       *AgentRuntime
	mu       sync.Mutex
	sessions map[string]*Session
	runs     map[string]context.CancelFunc
	wg       sync.WaitGroup
}

func newStdioHost(ctx context.Context, option *cfg.Option, logger telemetry.Logger, output io.Writer) *stdioHost {
	return &stdioHost{
		ctx: ctx, option: option, logger: logger, enc: json.NewEncoder(output),
		sessions: make(map[string]*Session), runs: make(map[string]context.CancelFunc),
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
			_ = h.emit(webproto.Message{Type: webproto.TypeAOP, RunID: event.TurnID, Payload: payload})
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

func (h *stdioHost) emitError(runID string, err error) {
	payload, _ := json.Marshal(webproto.ErrorPayload{Message: err.Error()})
	_ = h.emit(webproto.Message{Type: webproto.TypeError, RunID: runID, Payload: payload})
}

func (h *stdioHost) emitTaskError(taskID string, err error) {
	payload, _ := json.Marshal(webproto.ErrorPayload{Message: err.Error()})
	_ = h.emit(webproto.Message{Type: webproto.TypeError, TaskID: taskID, Payload: payload})
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
	switch message.Type {
	case webproto.TypeSessionOpen:
		var payload webproto.SessionOpenPayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			h.emitError("", err)
			return
		}
		session, err := h.rt.OpenSession(h.ctx, SessionOptions{
			ID: payload.SessionID, ParentSessionID: payload.ParentSessionID, ParentToolCallID: payload.ParentToolCallID,
		})
		if err != nil {
			h.emitError("", err)
			return
		}
		h.mu.Lock()
		h.sessions[session.Snapshot().ID] = session
		h.mu.Unlock()
		opened, _ := json.Marshal(webproto.SessionLifecyclePayload{SessionID: session.Snapshot().ID})
		_ = h.emit(webproto.Message{Type: webproto.TypeSessionOpened, Payload: opened})

	case webproto.TypeSessionClose:
		var payload webproto.SessionLifecyclePayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			h.emitError("", err)
			return
		}
		reason := SessionCloseReason(payload.Reason)
		if err := h.rt.CloseSession(h.ctx, payload.SessionID, reason); err != nil {
			h.emitError("", err)
			return
		}
		h.mu.Lock()
		delete(h.sessions, payload.SessionID)
		h.mu.Unlock()
		closed, _ := json.Marshal(webproto.SessionLifecyclePayload{SessionID: payload.SessionID, Reason: string(reason)})
		_ = h.emit(webproto.Message{Type: webproto.TypeSessionClosed, Payload: closed})

	case webproto.TypeRun:
		var payload webproto.RunPayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			h.emitError(message.RunID, err)
			return
		}
		h.mu.Lock()
		session := h.sessions[payload.SessionID]
		h.mu.Unlock()
		if session == nil {
			h.emitError(message.RunID, fmt.Errorf("session %q is not open", payload.SessionID))
			return
		}
		runCtx, cancel := context.WithCancel(h.ctx)
		run, err := session.Run(runCtx, RunInput{
			ID: message.RunID, Parts: payload.Parts, NoEcho: payload.NoEcho, MaxTurns: payload.MaxTurns,
			EvalCriteria: payload.EvalCriteria, EvalMaxRounds: payload.EvalMaxRounds,
		})
		if err != nil {
			cancel()
			h.emitError(message.RunID, err)
			return
		}
		runID := run.ID()
		h.mu.Lock()
		h.runs[runID] = cancel
		h.mu.Unlock()
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer cancel()
			_, _ = run.Wait()
			h.mu.Lock()
			delete(h.runs, runID)
			h.mu.Unlock()
		}()

	case webproto.TypeRunCancel:
		h.mu.Lock()
		cancel := h.runs[message.RunID]
		h.mu.Unlock()
		if cancel == nil {
			h.emitError(message.RunID, fmt.Errorf("run %q is not active", message.RunID))
			return
		}
		cancel()

	case webproto.TypeCommand:
		var payload webproto.CommandPayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			h.emitTaskError(message.TaskID, err)
			return
		}
		h.mu.Lock()
		session := h.sessions[payload.SessionID]
		h.mu.Unlock()
		if session == nil {
			h.emitTaskError(message.TaskID, fmt.Errorf("session %q is not open", payload.SessionID))
			return
		}
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			result, err := session.Command(h.ctx, CommandInput{Line: payload.Line})
			if err != nil {
				h.emitTaskError(message.TaskID, err)
				return
			}
			encoded, _ := json.Marshal(webproto.CommandResultPayload{
				SessionID: payload.SessionID, Parts: result.Parts, Metadata: result.Metadata,
			})
			_ = h.emit(webproto.Message{Type: webproto.TypeCommandResult, TaskID: message.TaskID, Payload: encoded})
		}()

	default:
		h.emitError(message.RunID, fmt.Errorf("unsupported frame type %q", message.Type))
	}
}

func (h *stdioHost) drain() { h.wg.Wait() }
