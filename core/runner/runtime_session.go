package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/aop"
	xcommand "github.com/chainreactors/aiscan/pkg/aop/x/command"
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
	Command      string            `json:"command"`
	Presentation string            `json:"presentation,omitempty"`
	Parts        []aop.MessagePart `json:"parts,omitempty"`
}

const (
	CommandPresentationPlain        = "plain"
	CommandPresentationPreformatted = "preformatted"
)

type Session struct {
	state *sessionState
}

type Run struct {
	turnID string
	done   chan struct{}
	cancel context.CancelFunc
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
	ctx = commands.ContextWithInbox(ctx, s.state.inbox)
	ctx = agent.ContextWithLoopScheduler(ctx, s.state.scheduler)

	if strings.HasPrefix(line, "!") {
		return s.executeBash(ctx, line, strings.TrimSpace(strings.TrimPrefix(line, "!")))
	}
	args, err := commands.SplitCommandLine(line)
	if err != nil {
		return commandOutcome{err: err}
	}
	if len(args) == 0 {
		return commandOutcome{err: fmt.Errorf("command line is required")}
	}
	name := args[0]
	values := args[1:]
	switch name {
	case "/help":
		return commandText(line, CommandPresentationPreformatted,
			"Runtime commands:\n  /status\n  /clear\n  /compact [focus]\n  /eval [criteria|off]\n  /loop [interval prompt|list|stop name]\n  !<command>")
	case "/status":
		provider, model, _ := s.state.runtime.providerSnapshot()
		providerName := "not configured"
		if provider != nil {
			providerName = provider.Name()
		}
		return commandText(line, CommandPresentationPreformatted, fmt.Sprintf(
			"Session: %s\nAgent: %s\nProvider: %s\nModel: %s\nMessages: %d",
			s.state.id, s.state.agentName, providerName, model, len(s.state.agent.MessagesSnapshot())))
	case "/clear":
		s.state.agent.Reset()
		return commandText(line, CommandPresentationPlain, "Context cleared.")
	case "/compact":
		if len(s.state.agent.MessagesSnapshot()) < 4 {
			return commandText(line, CommandPresentationPlain, "Nothing to compact (too few messages).")
		}
		result, err := s.state.agent.Compact(ctx, agent.CompactConfig{CustomInstructions: strings.TrimSpace(strings.Join(values, " "))})
		if err != nil {
			return commandOutcome{err: err}
		}
		return commandText(line, CommandPresentationPlain, fmt.Sprintf(
			"Compacted: ~%d -> ~%d tokens (%d messages kept)", result.TokensBefore, result.TokensAfter, result.KeptMessages))
	case "/eval", "/goal":
		criteria := strings.TrimSpace(strings.Join(values, " "))
		switch criteria {
		case "":
			if s.evalCriteria == "" {
				return commandText(line, CommandPresentationPlain, "Goal evaluation: off")
			}
			return commandText(line, CommandPresentationPlain, "Goal evaluation: on\n  criteria: "+s.evalCriteria)
		case "off":
			s.evalCriteria = ""
			return commandText(line, CommandPresentationPlain, "Goal evaluation disabled.")
		default:
			s.evalCriteria = criteria
			return commandText(line, CommandPresentationPlain, "Goal evaluation enabled: "+criteria)
		}
	case "/loop":
		command := "loop"
		if len(values) == 0 {
			command += " list"
		} else {
			command += " " + strings.Join(values, " ")
		}
		return s.executeBash(ctx, line, command)
	default:
		return commandOutcome{err: fmt.Errorf("command %q is not a Runtime command", name)}
	}
}

func (s *commandSession) executeBash(ctx context.Context, line, command string) commandOutcome {
	if command == "" {
		return commandOutcome{err: fmt.Errorf("command is required after !")}
	}
	registry := s.state.runtime.app.Commands
	if registry == nil {
		return commandOutcome{err: fmt.Errorf("command registry is not available")}
	}
	bash, ok := registry.GetTool("bash")
	if !ok {
		return commandOutcome{err: fmt.Errorf("bash tool is not registered")}
	}
	payload, _ := json.Marshal(commands.BashArgs{Command: command})
	result, err := bash.Execute(ctx, string(payload))
	if err != nil {
		return commandOutcome{err: err}
	}
	return commandText(line, CommandPresentationPreformatted, strings.TrimRight(result.Text(), " \t\r\n"))
}

func commandText(line, presentation, text string) commandOutcome {
	result := CommandResult{Command: line, Presentation: presentation}
	if text != "" {
		result.Parts = []aop.MessagePart{{Type: aop.PartText, Text: text}}
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
	runtime          *AgentRuntime
	id               string
	agentName        string
	parentSessionID  string
	parentToolCallID string
	agent            *agent.Agent
	inbox            *sessionMailbox
	scheduler        *agent.LoopScheduler
	commands         *commandSession
	ctx              context.Context
	cancel           context.CancelFunc
	ops              chan *sessionOperation
	done             chan struct{}

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
		runtime: rt, id: id, agentName: agentName,
		parentSessionID: options.ParentSessionID, parentToolCallID: options.ParentToolCallID,
		agent: ag, inbox: mailbox,
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

// EnsureSession returns an existing Runtime-owned Session or opens it with the
// Runtime lifetime. It is idempotent so a transport reconnect can safely
// announce the same logical Session again.
func (rt *AgentRuntime) EnsureSession(options SessionOptions) (*Session, error) {
	if rt == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	id := strings.TrimSpace(options.ID)
	if id != "" {
		rt.mu.RLock()
		state := rt.sessions[id]
		rt.mu.RUnlock()
		if state != nil {
			return ensuredSession(state, options)
		}
	}
	session, err := rt.OpenSession(rt.ctx, options)
	if err == nil || id == "" {
		return session, err
	}
	// Concurrent reconnects may both observe the Session as absent. The strict
	// OpenSession call admits one; the loser re-reads and validates that Session.
	rt.mu.RLock()
	state := rt.sessions[id]
	rt.mu.RUnlock()
	if state == nil {
		return nil, err
	}
	return ensuredSession(state, options)
}

func ensuredSession(state *sessionState, options SessionOptions) (*Session, error) {
	if options.ParentSessionID != "" && options.ParentSessionID != state.parentSessionID {
		return nil, fmt.Errorf("session %q parent_session_id conflicts with open session", state.id)
	}
	if options.ParentToolCallID != "" && options.ParentToolCallID != state.parentToolCallID {
		return nil, fmt.Errorf("session %q parent_tool_call_id conflicts with open session", state.id)
	}
	if options.AgentName != "" && options.AgentName != state.agentName {
		return nil, fmt.Errorf("session %q agent name conflicts with open session", state.id)
	}
	return &Session{state: state}, nil
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

func (rt *AgentRuntime) session(sessionID string) (*Session, error) {
	if rt == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	rt.mu.RLock()
	state := rt.sessions[strings.TrimSpace(sessionID)]
	rt.mu.RUnlock()
	if state == nil {
		return nil, fmt.Errorf("session %q is not open", sessionID)
	}
	return &Session{state: state}, nil
}

func (rt *AgentRuntime) RunSession(ctx context.Context, sessionID string, input RunInput) (*Run, error) {
	session, err := rt.session(sessionID)
	if err != nil {
		return nil, err
	}
	return session.Run(ctx, input)
}

func (rt *AgentRuntime) CommandSession(ctx context.Context, sessionID, line string) (CommandResult, error) {
	session, err := rt.session(sessionID)
	if err != nil {
		return CommandResult{}, err
	}
	return session.Command(ctx, line)
}

func (rt *AgentRuntime) CancelRun(turnID string) error {
	if rt == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	turnID = strings.TrimSpace(turnID)
	rt.mu.RLock()
	run := rt.runs[turnID]
	rt.mu.RUnlock()
	if run == nil {
		return fmt.Errorf("turn %q is not active", turnID)
	}
	run.cancel()
	return nil
}

// WaitOperations waits for all Runs and asynchronous control operations that
// were admitted before the call. Transports use it to drain before shutdown.
func (rt *AgentRuntime) WaitOperations() {
	if rt != nil {
		rt.operations.Wait()
	}
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
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, runCancel := context.WithCancel(ctx)
	run := &Run{turnID: turnID, done: make(chan struct{}), cancel: runCancel}
	s.runtime.mu.Lock()
	if _, exists := s.runtime.runs[turnID]; exists {
		s.runtime.mu.Unlock()
		runCancel()
		return nil, fmt.Errorf("turn %q already exists", turnID)
	}
	s.runtime.runs[turnID] = run
	s.runtime.operations.Add(1)
	s.runtime.mu.Unlock()
	emitter := &turnEmitter{sessionID: s.id, turnID: turnID, agentName: s.agentName, emitter: s.runtime.sessionEvents}
	op := &sessionOperation{
		execute: func(runCtx context.Context) {
			defer s.runtime.releaseRun(run)
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
			defer s.runtime.releaseRun(run)
			result := RunResult{Stop: agent.StopReasonCanceled}
			if !errors.Is(err, context.Canceled) {
				result.Stop = agent.StopReasonError
			}
			emitter.start()
			emitter.end(result, err)
			run.finish(result, err)
		},
	}
	if err := s.admit(runCtx, op); err != nil {
		s.runtime.releaseRun(run)
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
	event := aop.Event{Type: aop.TypeMessage, SessionID: s.id, Agent: s.agentName, Data: raw}
	_ = xcommand.SetDetail(&event, xcommand.Detail{Line: result.Command, Presentation: result.Presentation})
	s.runtime.sessionEvents.emit(event)
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

func (rt *AgentRuntime) releaseRun(run *Run) {
	if run == nil {
		return
	}
	run.cancel()
	rt.mu.Lock()
	if rt.runs[run.turnID] == run {
		delete(rt.runs, run.turnID)
	}
	rt.mu.Unlock()
	rt.operations.Done()
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
