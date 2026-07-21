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
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// stdioQueueCapacity bounds each session's pending inbound messages. A full
// queue rejects the message with an error event instead of growing unbounded.
const stdioQueueCapacity = 64

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

// stdioQueuedMessage is one inbound user message waiting on a session's FIFO.
type stdioQueuedMessage struct {
	event aop.Event
	data  aop.MessageData
	goal  webproto.GoalExt
}

type stdioSession struct {
	id    string
	agent *agent.Agent
	queue chan stdioQueuedMessage
	done  chan struct{} // closed when the FIFO goroutine exits
}

type stdioHost struct {
	ctx    context.Context
	option *cfg.Option
	logger telemetry.Logger

	encMu sync.Mutex
	enc   *json.Encoder
	encErr error

	rt  *AgentRuntime
	rtErr error

	mu       sync.Mutex
	sessions map[string]*stdioSession
}

func newStdioHost(ctx context.Context, option *cfg.Option, logger telemetry.Logger, output io.Writer) *stdioHost {
	return &stdioHost{
		ctx:      ctx,
		option:   option,
		logger:   logger,
		enc:      json.NewEncoder(output),
		sessions: make(map[string]*stdioSession),
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
	if event.Type != aop.TypeMessage {
		return // only user messages are executable inbound units
	}
	data, err := aop.DecodeData[aop.MessageData](event)
	if err != nil || data.Role != "user" {
		h.emitLocal(aop.TypeError, event.SessionID, aop.ErrorData{Message: "inbound message must be a user message"})
		return
	}
	goal := webproto.DecodeGoalExt(event)

	h.mu.Lock()
	sess := h.sessions[event.SessionID]
	if sess == nil {
		sess = h.startSessionLocked(event.SessionID)
	}
	select {
	case sess.queue <- stdioQueuedMessage{event: event, data: data, goal: goal}:
	default:
		h.emitLocal(aop.TypeError, sess.id, aop.ErrorData{Message: "session queue full"})
	}
	h.mu.Unlock()
}

func (h *stdioHost) startSessionLocked(id string) *stdioSession {
	if id == "" {
		id = fmt.Sprintf("stdio-%d", time.Now().UnixNano())
	}
	agentCfg := h.rt.Config.
		WithSystemPrompt(h.rt.SystemPrompt).
		WithStream(true).
		WithInbox(nil).
		WithSessionID(id)
	sess := &stdioSession{
		id:    id,
		agent: agent.NewAgent(agentCfg),
		queue: make(chan stdioQueuedMessage, stdioQueueCapacity),
		done:  make(chan struct{}),
	}
	h.sessions[id] = sess
	go h.runSession(sess)
	return sess
}

// runSession is the per-session FIFO: one run at a time, in arrival order.
func (h *stdioHost) runSession(sess *stdioSession) {
	defer close(sess.done)
	for {
		select {
		case <-h.ctx.Done():
			return
		case queued, ok := <-sess.queue:
			if !ok {
				return
			}
			h.runOne(sess, queued)
		}
	}
}

func (h *stdioHost) runOne(sess *stdioSession, queued stdioQueuedMessage) {
	input := agent.InputFromAOPMessage(queued.data)
	input.NoEcho = queued.goal.NoEcho
	text := strings.TrimSpace(inputText(input))
	if text == "" {
		h.emitLocal(aop.TypeError, sess.id, aop.ErrorData{Message: "empty prompt"})
		return
	}

	if queued.goal.EvalCriteria != "" {
		maxRounds := queued.goal.EvalMaxRounds
		if maxRounds <= 0 {
			maxRounds = 3
		}
		sess.agent.SetMaxTurns(h.rt.Config.MaxTurns)
		evalCfg := evaluator.EvalLoopConfig{
			Evaluator: evaluator.New(evaluator.Config{
				Provider: h.rt.App.Provider,
				Model:    h.rt.Config.Model,
				Logger:   h.rt.Config.Logger,
			}),
			MaxEvalRounds: maxRounds,
			Goal:          text,
			Criteria:      queued.goal.EvalCriteria,
		}
		_, _, _ = evaluator.RunWithEval(h.ctx, sess.agent, evalCfg)
		return
	}

	if queued.goal.PersistMaxTurns > 0 {
		sess.agent.SetMaxTurns(queued.goal.PersistMaxTurns)
	} else {
		sess.agent.SetMaxTurns(h.rt.Config.MaxTurns)
	}
	_, _ = sess.agent.Run(h.ctx, input)
}

// drain closes every session queue and waits for the FIFO goroutines to exit.
func (h *stdioHost) drain() {
	h.mu.Lock()
	sessions := make([]*stdioSession, 0, len(h.sessions))
	for _, sess := range h.sessions {
		close(sess.queue)
		sessions = append(sessions, sess)
	}
	h.mu.Unlock()
	for _, sess := range sessions {
		<-sess.done
	}
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
