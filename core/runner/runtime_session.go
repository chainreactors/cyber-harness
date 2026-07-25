package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	outputpkg "github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/skills"
)

const DefaultSessionPendingLimit = 64

type SessionOptions struct {
	ID               string
	ParentSessionID  string
	ParentToolCallID string
	AgentName        string
	Messages         []agent.ChatMessage
}

type SessionCloseReason string

const (
	SessionCloseCompleted SessionCloseReason = "completed"
	SessionCloseCanceled  SessionCloseReason = "canceled"
	SessionCloseRuntime   SessionCloseReason = "runtime_closed"
)

type RunInput struct {
	TurnID        string
	Parts         []aop.MessagePart
	NoEcho        bool
	MaxTurns      int
	EvalCriteria  string
	EvalMaxRounds int
	Continue      bool

	automatic bool
}

type RunResult struct {
	Output        string
	Stop          agent.StopReason
	Usage         agent.Usage
	ContextTokens int
}

type CommandResult struct {
	Parts    []aop.MessagePart
	Metadata map[string]any
}

type Session struct {
	state *sessionState
}

type Run struct {
	turnID string
	log    *runEventLog
	done   chan struct{}
	mu     sync.Mutex
	result RunResult
	err    error
}

func (r *Run) TurnID() string {
	if r == nil {
		return ""
	}
	return r.turnID
}

func (r *Run) Events(ctx context.Context) <-chan aop.Event {
	if r == nil || r.log == nil {
		ch := make(chan aop.Event)
		close(ch)
		return ch
	}
	return r.log.events(ctx)
}

func (r *Run) Wait() (RunResult, error) {
	if r == nil {
		return RunResult{}, fmt.Errorf("run is nil")
	}
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.err
}

func (r *Run) finish(result RunResult, err error) {
	r.mu.Lock()
	r.result, r.err = result, err
	r.mu.Unlock()
	close(r.done)
}

type sessionOperation struct {
	ctx     context.Context
	cancel  context.CancelFunc
	execute func(context.Context)
	reject  func(error)
}

type commandOutcome struct {
	result CommandResult
	err    error
}

type runEventLog struct {
	mu        sync.Mutex
	eventsLog []aop.Event
	notify    chan struct{}
	closed    bool
}

func newRunEventLog() *runEventLog {
	return &runEventLog{notify: make(chan struct{})}
}

func (l *runEventLog) append(event aop.Event) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.eventsLog = append(l.eventsLog, event)
	close(l.notify)
	l.notify = make(chan struct{})
	l.mu.Unlock()
}

func (l *runEventLog) close() {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.notify)
	}
	l.mu.Unlock()
}

func (l *runEventLog) events(ctx context.Context) <-chan aop.Event {
	if ctx == nil {
		ctx = context.Background()
	}
	out := make(chan aop.Event)
	go func() {
		defer close(out)
		index := 0
		for {
			l.mu.Lock()
			var event aop.Event
			hasEvent := index < len(l.eventsLog)
			if hasEvent {
				event = l.eventsLog[index]
				index++
			}
			closed := l.closed
			notify := l.notify
			l.mu.Unlock()
			if hasEvent {
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
				continue
			}
			if closed {
				return
			}
			select {
			case <-notify:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

type sessionEmitter struct {
	bus *eventbus.Bus[aop.Event]
	mu  sync.Mutex
	seq map[string]int
}

func newSessionEmitter(bus *eventbus.Bus[aop.Event]) *sessionEmitter {
	return &sessionEmitter{bus: bus, seq: make(map[string]int)}
}

func (e *sessionEmitter) emit(event aop.Event) {
	if event.TS == "" {
		event.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	e.mu.Lock()
	e.seq[event.SessionID]++
	event.Seq = e.seq[event.SessionID]
	e.mu.Unlock()
	e.bus.Emit(event)
}

func (e *sessionEmitter) lifecycle(typ, sessionID, agentName string, data any) {
	raw, _ := json.Marshal(data)
	e.emit(aop.Event{Type: typ, SessionID: sessionID, Agent: agentName, Data: raw})
}

type turnEmitter struct {
	sessionID string
	turnID    string
	agentName string
	emitter   *sessionEmitter
	log       *runEventLog
}

func (e *turnEmitter) observe(event aop.Event) {
	if event.SessionID != e.sessionID || event.TurnID != e.turnID {
		return
	}
	e.log.append(event)
	if event.Type == aop.TypeTurnEnd {
		e.log.close()
	}
}

func (e *turnEmitter) start() {
	raw, _ := json.Marshal(aop.TurnStartData{})
	e.emitter.emit(aop.Event{Type: aop.TypeTurnStart, SessionID: e.sessionID, TurnID: e.turnID, Agent: e.agentName, Data: raw})
}

func (e *turnEmitter) end(result RunResult, runErr error) {
	data := aop.TurnEndData{Stop: string(result.Stop), Usage: runtimeUsageData(result.Usage), ContextTokens: result.ContextTokens}
	if runErr != nil {
		data.Error = runErr.Error()
	}
	raw, _ := json.Marshal(data)
	e.emitter.emit(aop.Event{Type: aop.TypeTurnEnd, SessionID: e.sessionID, TurnID: e.turnID, Agent: e.agentName, Data: raw})
}

type commandSession struct {
	state        *sessionState
	evalCriteria string
}

func (s *commandSession) execute(ctx context.Context, input string) commandOutcome {
	line := strings.TrimSpace(input)
	if line == "" {
		return commandOutcome{err: fmt.Errorf("command line is required")}
	}
	if !strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "!") {
		return commandOutcome{err: fmt.Errorf("direct execution requires a command")}
	}
	if line == "/stop" || line == "/exit" || line == "/quit" {
		return commandOutcome{err: fmt.Errorf("%s is an adapter control", line)}
	}
	if line == "/continue" || strings.HasPrefix(line, "/followup ") || strings.HasPrefix(line, "/skill:") {
		return commandOutcome{err: fmt.Errorf("%s requires a Run", line)}
	}

	var stdout, stderr bytes.Buffer
	ctx = commands.ContextWithInbox(ctx, s.state.inbox)
	ctx = agent.ContextWithLoopScheduler(ctx, s.state.scheduler)
	option := s.state.runtime.option
	if option != nil {
		copy := *option
		copy.NoColor = true
		option = &copy
	}
	console := tui.NewAgentConsoleWithWriters(ctx, option, s.state.runtime.consoleAppInfo(), s.state.agent, &stdout, &stderr)
	console.SetEvalCriteria(s.evalCriteria)
	_, err := console.ExecuteLineAndWait(line)
	s.evalCriteria = console.EvalCriteria()
	out := strings.TrimRight(outputpkg.StripANSI(stdout.String()), " \t\r\n")
	errOut := strings.TrimRight(outputpkg.StripANSI(stderr.String()), " \t\r\n")
	if err != nil {
		if errOut != "" {
			err = fmt.Errorf("%s: %w", errOut, err)
		}
		return commandOutcome{err: err}
	}
	if out == "" {
		out = errOut
	} else if errOut != "" {
		out = strings.TrimRight(out+"\n"+errOut, " \t\r\n")
	}
	result := CommandResult{Metadata: map[string]any{"command": line}}
	if out != "" {
		result.Parts = []aop.MessagePart{{Type: aop.PartText, Text: out}}
	}
	return commandOutcome{result: result}
}

type sessionMailbox struct {
	base             inboxpkg.Inbox
	mu               sync.Mutex
	active           bool
	automaticPending bool
	automatic        func()
}

func (m *sessionMailbox) Push(message inboxpkg.Message) error {
	m.mu.Lock()
	if m.base.Closed() {
		m.mu.Unlock()
		return inboxpkg.ErrInboxClosed
	}
	if m.active {
		err := m.base.Push(message)
		m.mu.Unlock()
		return err
	}
	err := m.base.Push(message)
	automatic := m.automatic
	shouldStart := err == nil && !m.automaticPending
	if shouldStart {
		m.automaticPending = true
	}
	m.mu.Unlock()
	if err == nil && shouldStart && automatic != nil {
		automatic()
	}
	return err
}

func (m *sessionMailbox) setActive(active bool) {
	m.mu.Lock()
	m.active = active
	if active {
		m.automaticPending = false
	}
	pending := !active && m.base.Len() > 0 && !m.automaticPending
	if pending {
		m.automaticPending = true
	}
	automatic := m.automatic
	m.mu.Unlock()
	if pending && automatic != nil {
		automatic()
	}
}

func (m *sessionMailbox) Drain() []inboxpkg.Message     { return m.base.Drain() }
func (m *sessionMailbox) Close()                        { m.base.Close() }
func (m *sessionMailbox) Closed() bool                  { return m.base.Closed() }
func (m *sessionMailbox) Len() int                      { return m.base.Len() }
func (m *sessionMailbox) Wait(ctx context.Context) bool { return m.base.Wait(ctx) }
func (m *sessionMailbox) RegisterProducer(name string) *inboxpkg.ProducerHandle {
	return m.base.RegisterProducer(name)
}
func (m *sessionMailbox) ActiveProducers() int { return m.base.ActiveProducers() }

type sessionState struct {
	runtime   *AgentRuntime
	id        string
	agentName string
	agent     *agent.Agent
	inbox     *sessionMailbox
	scheduler *agent.LoopScheduler
	commands  *commandSession
	ctx       context.Context
	cancel    context.CancelFunc
	ops       chan *sessionOperation
	done      chan struct{}

	mu      sync.Mutex
	pending int
	closed  bool
}

func (rt *AgentRuntime) OpenSession(ctx context.Context, options SessionOptions) (*Session, error) {
	if rt == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	if ctx == nil {
		ctx = rt.ctx
	}
	id := strings.TrimSpace(options.ID)
	if id == "" {
		id = rt.nextRuntimeID("session")
	}
	agentName := strings.TrimSpace(options.AgentName)
	if agentName == "" {
		agentName = rt.nodeName
	}
	if agentName == "" {
		agentName = "aiscan"
	}

	rt.mu.Lock()
	if rt.ctx.Err() != nil {
		rt.mu.Unlock()
		return nil, rt.ctx.Err()
	}
	if _, exists := rt.sessions[id]; exists {
		rt.mu.Unlock()
		return nil, fmt.Errorf("session %q already exists", id)
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	baseInbox := inboxpkg.NewBuffered(agent.DefaultInboxCapacity)
	mailbox := &sessionMailbox{base: baseInbox}
	scheduler := agent.NewLoopScheduler(mailbox, rt.config.Logger)
	agentCfg := rt.config.
		WithSystemPrompt(rt.systemPrompt).
		WithStream(true).
		WithInbox(mailbox).
		WithSessionID(id).
		WithAgentName(agentName).
		WithBus(rt.kernelBus)
	agentCfg.ParentSessionID = options.ParentSessionID
	agentCfg.ParentToolCallID = options.ParentToolCallID
	agentCfg.LoopScheduler = scheduler
	ag := agent.NewAgent(agentCfg)
	if len(options.Messages) > 0 {
		ag.LoadMessages(options.Messages)
	} else if id == MainREPLName && len(rt.resumeMessages) > 0 {
		ag.LoadMessages(rt.resumeMessages)
	}
	state := &sessionState{
		runtime: rt, id: id, agentName: agentName, agent: ag, inbox: mailbox,
		scheduler: scheduler, ctx: sessionCtx, cancel: cancel,
		ops: make(chan *sessionOperation, rt.pendingLimit()), done: make(chan struct{}),
	}
	public := &Session{state: state}
	state.commands = &commandSession{state: state}
	mailbox.automatic = func() { state.startAutomaticRun() }
	rt.sessions[id] = state
	rt.wg.Add(1)
	rt.mu.Unlock()

	if id == MainREPLName && rt.option != nil && rt.option.Heartbeat > 0 {
		_, _ = scheduler.Add(sessionCtx, agent.LoopEntry{
			Name: "heartbeat", Interval: time.Duration(rt.option.Heartbeat) * time.Minute,
			Mode:   agent.ModeInbox,
			Prompt: "Heartbeat: review current context, check on any running sessions, and decide if action is needed.",
		})
	}
	go rt.runSession(state)
	rt.sessionEvents.lifecycle(aop.TypeSessionStart, id, agentName, aop.SessionStartData{
		Model: rt.config.Model, ParentSessionID: options.ParentSessionID, ParentToolCallID: options.ParentToolCallID,
	})
	return public, nil
}

func (rt *AgentRuntime) CloseSession(ctx context.Context, sessionID string, reason SessionCloseReason) error {
	if rt == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	if reason == "" {
		reason = SessionCloseCompleted
	}
	rt.mu.Lock()
	state := rt.sessions[sessionID]
	if state != nil {
		delete(rt.sessions, sessionID)
	}
	rt.mu.Unlock()
	if state == nil {
		return fmt.Errorf("session %q is not open", sessionID)
	}
	state.mu.Lock()
	state.closed = true
	state.mu.Unlock()
	state.cancel()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-state.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	state.scheduler.Stop()
	state.inbox.Close()
	rt.sessionEvents.lifecycle(aop.TypeSessionEnd, state.id, state.agentName, aop.SessionEndData{Reason: string(reason)})
	return nil
}

func (rt *AgentRuntime) Subscribe(fn func(aop.Event)) func() {
	if rt == nil || rt.bus == nil || fn == nil {
		return func() {}
	}
	return rt.bus.Subscribe(fn)
}

func (s *Session) Run(ctx context.Context, input RunInput) (*Run, error) {
	if s == nil || s.state == nil {
		return nil, fmt.Errorf("session is not configured")
	}
	return s.state.startRun(ctx, input)
}

func (s *Session) Command(ctx context.Context, line string) (CommandResult, error) {
	if s == nil || s.state == nil {
		return CommandResult{}, fmt.Errorf("session is not configured")
	}
	done := make(chan commandOutcome, 1)
	op := &sessionOperation{
		execute: func(runCtx context.Context) {
			outcome := s.state.commands.execute(runCtx, line)
			if outcome.err == nil && len(outcome.result.Parts) > 0 {
				s.state.emitCommandResult(outcome.result)
			}
			done <- outcome
		},
		reject: func(err error) { done <- commandOutcome{err: err} },
	}
	if err := s.state.admit(ctx, op); err != nil {
		return CommandResult{}, err
	}
	outcome := <-done
	return outcome.result, outcome.err
}

func (s *Session) ID() string {
	if s == nil || s.state == nil {
		return ""
	}
	return s.state.id

}

func (s *Session) MessagesSnapshot() []agent.ChatMessage {
	if s == nil || s.state == nil {
		return nil
	}
	return s.state.agent.MessagesSnapshot()
}

func (s *sessionState) startRun(ctx context.Context, input RunInput) (*Run, error) {
	if !input.automatic && !input.Continue && !hasRunInput(input.Parts) {
		return nil, fmt.Errorf("run input is empty")
	}
	turnID := strings.TrimSpace(input.TurnID)
	if turnID == "" {
		turnID = s.runtime.nextRuntimeID("turn")
	}
	s.runtime.mu.Lock()
	if _, exists := s.runtime.turnIDs[turnID]; exists {
		s.runtime.mu.Unlock()
		return nil, fmt.Errorf("turn %q already exists", turnID)
	}
	s.runtime.turnIDs[turnID] = struct{}{}
	s.runtime.mu.Unlock()
	log := newRunEventLog()
	run := &Run{turnID: turnID, log: log, done: make(chan struct{})}
	emitter := &turnEmitter{sessionID: s.id, turnID: turnID, agentName: s.agentName, emitter: s.runtime.sessionEvents, log: log}
	unsubscribe := s.runtime.Subscribe(emitter.observe)
	op := &sessionOperation{
		execute: func(runCtx context.Context) {
			defer s.runtime.releaseTurnID(turnID)
			defer unsubscribe()
			s.inbox.setActive(true)
			emitter.start()
			result, runErr := s.executeRun(runCtx, turnID, input)
			runResult := RunResult{}
			if result != nil {
				runResult = RunResult{
					Output:        result.Output,
					Stop:          result.Stop,
					Usage:         result.TotalUsage,
					ContextTokens: result.ContextTokens,
				}
			} else if errors.Is(runErr, context.Canceled) {
				runResult.Stop = agent.StopReasonCanceled
			} else {
				runResult.Stop = agent.StopReasonError
			}
			emitter.end(runResult, runErr)
			s.inbox.setActive(false)
			run.finish(runResult, runErr)
		},
		reject: func(err error) {
			defer s.runtime.releaseTurnID(turnID)
			defer unsubscribe()
			result := RunResult{Stop: agent.StopReasonCanceled}
			if !errors.Is(err, context.Canceled) {
				result.Stop = agent.StopReasonError
			}
			emitter.start()
			emitter.end(result, err)
			run.finish(result, err)
		},
	}
	if err := s.admit(ctx, op); err != nil {
		unsubscribe()
		s.runtime.releaseTurnID(turnID)
		return nil, err
	}
	return run, nil
}

func hasRunInput(parts []aop.MessagePart) bool {
	for _, part := range parts {
		if part.Type == aop.PartText && strings.TrimSpace(part.Text) != "" {
			return true
		}
		if part.Type == aop.PartImage && part.Image != nil {
			return true
		}
	}
	return false
}

func (s *sessionState) executeRun(ctx context.Context, turnID string, input RunInput) (*agent.Result, error) {
	if input.automatic || input.Continue {
		return s.agent.Continue(ctx, agent.WithTurnID(turnID), agent.WithRunMaxTurns(input.MaxTurns))
	}
	if input.EvalCriteria == "" {
		input.EvalCriteria = s.commands.evalCriteria
	}
	if len(input.Parts) == 1 && input.Parts[0].Type == aop.PartText {
		input.Parts[0].Text = skills.ExpandCommand(input.Parts[0].Text, s.runtime.app.Skills)
	}
	message := aop.MessageData{Role: "user", Parts: input.Parts}
	agentInput := agent.InputFromAOPMessage(message)
	agentInput.NoEcho = input.NoEcho
	if input.EvalCriteria != "" {
		provider, model, logger := s.runtime.providerSnapshot()
		evalConfig := evaluator.NewLoopConfigWithInput(provider, model, logger, agentInput, input.EvalCriteria, input.EvalMaxRounds)
		evalConfig.TurnID = turnID
		result, _, err := evaluator.RunWithEval(ctx, s.agent, evalConfig,
			agent.WithTurnID(turnID), agent.WithRunMaxTurns(input.MaxTurns))
		return result, err
	}
	return s.agent.Run(ctx, agentInput, agent.WithTurnID(turnID), agent.WithRunMaxTurns(input.MaxTurns))
}

func (s *sessionState) startAutomaticRun() {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	_, _ = s.startRun(s.ctx, RunInput{automatic: true})
}

func (s *sessionState) admit(ctx context.Context, operation *sessionOperation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	opCtx, cancel := context.WithCancel(s.ctx)
	operation.ctx, operation.cancel = opCtx, cancel
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("session %q is closed", s.id)
	}
	if s.pending >= s.runtime.pendingLimit() {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("session %q pending limit reached (%d)", s.id, s.runtime.pendingLimit())
	}
	s.pending++
	s.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-opCtx.Done():
		}
	}()
	select {
	case s.ops <- operation:
		return nil
	case <-s.ctx.Done():
		s.releaseOperation()
		cancel()
		return s.ctx.Err()
	}
}

func (s *sessionState) releaseOperation() {
	s.mu.Lock()
	if s.pending > 0 {
		s.pending--
	}
	s.mu.Unlock()
}

func (rt *AgentRuntime) runSession(session *sessionState) {
	defer rt.wg.Done()
	defer close(session.done)
	for {
		select {
		case operation := <-session.ops:
			if err := operation.ctx.Err(); err != nil {
				operation.reject(err)
			} else {
				operation.execute(operation.ctx)
			}
			operation.cancel()
			session.releaseOperation()
		case <-session.ctx.Done():
			for {
				select {
				case operation := <-session.ops:
					operation.cancel()
					operation.reject(session.ctx.Err())
					session.releaseOperation()
				default:
					return
				}
			}
		}
	}
}

func (s *sessionState) emitCommandResult(result CommandResult) {
	raw, _ := json.Marshal(aop.MessageData{
		MessageID: s.runtime.nextRuntimeID("command"), Role: "assistant", Parts: result.Parts,
	})
	s.runtime.sessionEvents.emit(aop.Event{Type: aop.TypeMessage, SessionID: s.id, Agent: s.agentName, Data: raw})
}

func (rt *AgentRuntime) pendingLimit() int {
	if rt != nil && rt.maxPending > 0 {
		return rt.maxPending
	}
	return DefaultSessionPendingLimit
}

func (rt *AgentRuntime) pushAsync(message inboxpkg.Message) error {
	if rt == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	rt.mu.RLock()
	state := rt.sessions[MainREPLName]
	if state == nil && len(rt.sessions) == 1 {
		for _, candidate := range rt.sessions {
			state = candidate
		}
	}
	rt.mu.RUnlock()
	if state == nil {
		return fmt.Errorf("no open session accepts asynchronous input")
	}
	return state.inbox.Push(message)
}

func (rt *AgentRuntime) nextRuntimeID(prefix string) string {
	rt.mu.Lock()
	rt.requestSeq++
	id := fmt.Sprintf("%s-%d", prefix, rt.requestSeq)
	rt.mu.Unlock()
	return id
}

func (rt *AgentRuntime) releaseTurnID(turnID string) {
	rt.mu.Lock()
	delete(rt.turnIDs, turnID)
	rt.mu.Unlock()
}

func (rt *AgentRuntime) providerSnapshot() (agent.Provider, string, telemetry.Logger) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.config.Provider, rt.config.Model, rt.config.Logger
}

func runtimeUsageData(usage agent.Usage) *aop.UsageData {
	if usage == (agent.Usage{}) {
		return nil
	}
	return &aop.UsageData{
		InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens,
		CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
	}
}

func (rt *AgentRuntime) consoleAppInfo() tui.AppInfo {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return tui.AppInfo{
		Provider:          rt.app.Provider,
		ProviderConfig:    rt.app.ProviderConfig,
		ProviderFallbacks: rt.app.ProviderFallbacks,
		Commands:          rt.app.Commands,
		Skills:            rt.app.Skills,
		OnProviderChange:  rt.SetProvider,
		OnLoggerChange:    rt.SetLogger,
	}
}

func (rt *AgentRuntime) consoleAppInfoForSession(session *Session) tui.AppInfo {
	info := rt.consoleAppInfo()
	info.Run = func(ctx context.Context, prompt string, continuation bool) (*agent.Result, error) {
		input := RunInput{Continue: continuation}
		if !continuation {
			input.Parts = []aop.MessagePart{{Type: aop.PartText, Text: prompt}}
		}
		run, err := session.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		result, err := run.Wait()
		return &agent.Result{
			Output:        result.Output,
			Stop:          result.Stop,
			TotalUsage:    result.Usage,
			ContextTokens: result.ContextTokens,
		}, err
	}
	info.Command = func(ctx context.Context, line string) error {
		_, err := session.Command(ctx, line)
		return err
	}
	return info
}
