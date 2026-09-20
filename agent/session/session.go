package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/evaluator"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	inboxpkg "github.com/chainreactors/cyber/agent/inbox"
	providerpkg "github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/skills"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"

	"google.golang.org/protobuf/proto"
)

const DefaultSessionPendingLimit = 64

type SessionOptions struct {
	ID               string
	LogicalID        string
	ParentSessionID  string
	ParentToolCallID string
	AgentName        string
	Messages         []*aop.Message
	// HistorySnapshot marks Messages as a new persisted transcript snapshot.
	// Ordinary continuations keep the in-memory context and refer to their
	// parent session instead; replaying those messages as events would append
	// every large tool result again to JSONL and the durable event stream.
	HistorySnapshot bool
}

type SessionCloseReason string

const (
	SessionCloseCompleted SessionCloseReason = "completed"
	SessionCloseCanceled  SessionCloseReason = "canceled"
	SessionCloseError     SessionCloseReason = "error"
	SessionCloseCleared   SessionCloseReason = "cleared"
	SessionCloseCompacted SessionCloseReason = "compacted"
	SessionCloseResumed   SessionCloseReason = "resumed"
	SessionCloseRuntime   SessionCloseReason = "runtime_closed"
)

type RunInput struct {
	TurnID       string
	Message      *aop.Message
	Content      []*aop.Content
	MaxTurns     int
	EvalCriteria string
	EvalRounds   string
	Continue     bool

	automatic bool
}

const (
	CommandPresentationPlain        = "plain"
	CommandPresentationPreformatted = "preformatted"
)

type Session struct {
	mu    sync.RWMutex
	state *sessionState
}

type Run struct {
	sessionID string
	turnID    string
	done      chan struct{}
	cancel    context.CancelFunc
	mu        sync.Mutex
	result    *agent.Result
	err       error
}

func (r *Run) TurnID() string {
	if r == nil {
		return ""
	}
	return r.turnID
}

// Wait returns the completed Agent result. The result and its messages are
// read-only; all waiters observe the same completed value.
func (r *Run) Wait() (*agent.Result, error) {
	if r == nil {
		return nil, fmt.Errorf("run is nil")
	}
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.err
}

func (r *Run) finish(result *agent.Result, err error) {
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
	result *types.CommandResult
	err    error
}

// Session lifecycle payloads belong to this extension; State only stamps and publishes events.
func emitSessionStarted(application aop.EventPublisher, sessionID, agentName string, started *aop.SessionStarted, historyMode types.SessionHistory_Mode) {
	event := &aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionStarted{SessionStarted: started}}
	if historyMode != types.SessionHistory_MODE_UNSPECIFIED {
		_ = types.SetSessionHistory(event, &types.SessionHistory{Mode: historyMode})
	}
	application.Publish(event)
}

func emitSessionEnded(application aop.EventPublisher, sessionID, agentName, reason string) {
	application.Publish(&aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: reason}}})
}

func (s *sessionState) emitTurnStarted(turnID string) {
	s.runtime.events.Publish(&aop.Event{SessionId: s.id, TurnId: turnID, Emitter: s.agentName, Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}})
}

func (s *sessionState) emitTurnEnded(turnID string, result *agent.Result, runErr error) {
	ended := &aop.TurnEnded{StopReason: string(result.Stop), Usage: result.TotalUsage, ContextTokens: uint64(max(result.ContextTokens, 0))}
	if runErr != nil {
		ended.Error = &aop.ProtocolError{Message: runErr.Error()}
	}
	s.runtime.events.Publish(&aop.Event{SessionId: s.id, TurnId: turnID, Emitter: s.agentName, Payload: &aop.Event_TurnEnded{TurnEnded: ended}})
}

type commandSession struct {
	state        *sessionState
	evalCriteria string
	evalRounds   string
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
	invocation := operation.InvocationFromContext(ctx)
	invocation.SessionID, invocation.Emitter = s.state.id, s.state.agentName
	ctx = operation.ContextWithInvocation(ctx, invocation)
	ctx = inboxpkg.ContextWithInbox(ctx, s.state.inbox)
	ctx = agent.ContextWithLoopScheduler(ctx, s.state.scheduler)

	if strings.HasPrefix(line, "!") {
		return s.executeBash(ctx, line, strings.TrimSpace(strings.TrimPrefix(line, "!")))
	}
	args, err := coretool.SplitCommandLine(line)
	if err != nil {
		return commandOutcome{err: err}
	}
	if len(args) == 0 {
		return commandOutcome{err: fmt.Errorf("command line is required")}
	}
	name := args[0]
	declaration, ok := s.state.runtime.commandIndex[name]
	if !ok || declaration.rotation {
		return commandOutcome{err: fmt.Errorf("command %q is not a Runtime command", name)}
	}
	result, err := declaration.invoke(ctx, &Session{state: s.state}, args[1:])
	if result != nil {
		result.Command = line
	}
	return commandOutcome{result: result, err: err}
}

func (s *commandSession) statusText() string {
	if s == nil || s.state == nil || s.state.runtime == nil {
		return "Agent runtime: unavailable"
	}
	rt := s.state.runtime
	rt.mu.RLock()
	providers := rt.providers
	tools, commandRegistry, store := rt.tools, rt.commandRegistry, rt.skills
	provider := rt.agentConfig.Provider
	model := rt.agentConfig.Model
	providerConfig := agent.ProviderConfig{}
	if providers != nil {
		_, providerConfig = providers.Current()
	}
	rt.mu.RUnlock()

	providerName := strings.TrimSpace(providerConfig.Provider)
	if providerName == "" && provider != nil {
		providerName = provider.Name()
	}
	if providerName == "" {
		providerName = "not configured"
	}
	if strings.TrimSpace(model) == "" {
		model = strings.TrimSpace(providerConfig.Model)
	}
	if model == "" {
		model = "-"
	}

	contextWindow := providerConfig.ContextWindow
	if contextWindow <= 0 {
		contextWindow = agent.ModelContextWindow(model)
	}
	maxTokens := providerConfig.MaxTokens
	if maxTokens <= 0 {
		maxTokens = agent.DefaultMaxTokens
	}
	timeout := providerConfig.Timeout
	if timeout <= 0 {
		timeout = 120
	}

	llmState := "not configured"
	if providers != nil {
		health := providers.Health()
		switch health.State {
		case providerpkg.HealthReady:
			llmState = "ready"
			if health.LatencyMs > 0 {
				llmState += fmt.Sprintf(" (%dms)", health.LatencyMs)
			}
		case providerpkg.HealthFailed:
			llmState = "failed"
			if detail := statusOneLine(health.Error, 160); detail != "" {
				llmState += " · " + detail
			}
		case providerpkg.HealthConfigured:
			llmState = "configured (probe pending)"
		case providerpkg.HealthNotConfigured:
			if provider != nil && strings.TrimSpace(health.Error) == "" {
				llmState = "configured (probe unavailable)"
			} else if detail := statusOneLine(health.Error, 160); detail != "" {
				llmState += " · " + detail
			}
		}
	}

	toolState := "unavailable"
	toolNames := []string(nil)
	commandNames := []string(nil)
	skillState := "not loaded"
	if providers != nil {
		if tools != nil {
			for _, definition := range tools.ToolDefinitions() {
				if definition != nil && strings.TrimSpace(definition.Name) != "" {
					toolNames = append(toolNames, definition.Name)
				}
			}
			if len(toolNames) > 0 {
				toolState = "ready"
			}
		}
		if commandRegistry != nil {
			commandNames = commandRegistry.Names()
		}
		if store != nil {
			visible := 0
			for _, skill := range store.All() {
				if strings.TrimSpace(skill.Name) != "" && !skill.Internal {
					visible++
				}
			}
			skillState = fmt.Sprintf("ready (%d loaded)", visible)
			if diagnostics := store.Diagnostics(); len(diagnostics) > 0 {
				skillState = fmt.Sprintf("degraded (%d loaded, %d diagnostics)", visible, len(diagnostics))
			}
		}
	}

	toolDetail := fmt.Sprintf("%s (%d tools, %d commands)", toolState, len(toolNames), len(commandNames))
	if names := summarizeStatusNames(toolNames, 12); names != "" {
		toolDetail += " · " + names
	}
	commandDetail := summarizeStatusNames(commandNames, 16)
	if commandDetail == "" {
		commandDetail = "-"
	}

	messages := 0
	if s.state.agent != nil {
		messages = len(s.state.agent.MessagesSnapshot())
	}
	return strings.Join([]string{
		fmt.Sprintf("Session: %s", s.state.id),
		fmt.Sprintf("Agent: %s", s.state.agentName),
		fmt.Sprintf("LLM probe: %s", llmState),
		fmt.Sprintf("Provider: %s", providerName),
		fmt.Sprintf("Model: %s", model),
		fmt.Sprintf("Limits: context=%d · max_output=%d · timeout=%ds", contextWindow, maxTokens, timeout),
		fmt.Sprintf("Tools: %s", toolDetail),
		fmt.Sprintf("Commands: %s", commandDetail),
		fmt.Sprintf("Skills: %s", skillState),
		fmt.Sprintf("Messages: %d", messages),
	}, "\n")
}

func summarizeStatusNames(names []string, limit int) string {
	if len(names) == 0 || limit <= 0 {
		return ""
	}
	clean := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		clean = append(clean, name)
	}
	sort.Strings(clean)
	if len(clean) <= limit {
		return strings.Join(clean, ",")
	}
	return strings.Join(clean[:limit], ",") + fmt.Sprintf(",+%d", len(clean)-limit)
}

func statusOneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func (s *commandSession) executeBash(ctx context.Context, line, command string) commandOutcome {
	if command == "" {
		return commandOutcome{err: fmt.Errorf("command is required after the ! prefix")}
	}
	bash := s.state.runtime.shell
	if bash == nil {
		return commandOutcome{err: fmt.Errorf("bash tool is not registered")}
	}
	payload, _ := json.Marshal(map[string]string{"command": command})
	result, err := bash.Execute(ctx, string(payload))
	if err != nil {
		return commandOutcome{err: err}
	}
	return commandText(line, CommandPresentationPreformatted, strings.TrimRight(coretool.ResultText(result), " \t\r\n"))
}

func commandText(line, presentation, text string) commandOutcome {
	result := &types.CommandResult{Command: line, Presentation: presentation}
	if text != "" {
		result.Content = []*aop.Content{aop.Text(text)}
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
	kick, err := m.enqueue(message)
	if kick != nil {
		kick()
	}
	return err
}

// enqueue commits input without invoking the scheduler under admission locks.
func (m *sessionMailbox) enqueue(message inboxpkg.Message) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.base.Closed() {
		return nil, inboxpkg.ErrInboxClosed
	}
	err := m.base.Push(message)
	if err != nil || m.active || m.automaticPending {
		return nil, err
	}
	m.automaticPending = true
	return m.automatic, nil
}

func (m *sessionMailbox) setActive(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = active
	if active {
		m.automaticPending = false
	}
}

// kickAutomatic starts a run only when the session is idle and the inbox still
// has work. Failed turns must not call this: automatic continuation drains
// leftover input after success, it is not a retry loop.
func (m *sessionMailbox) kickAutomatic() {
	m.mu.Lock()
	pending := !m.active && m.base.Len() > 0 && !m.automaticPending
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
func (m *sessionMailbox) WaitWhileActive(ctx context.Context) bool {
	return m.base.WaitWhileActive(ctx)
}
func (m *sessionMailbox) RegisterProducer(name string) *inboxpkg.ProducerHandle {
	return m.base.RegisterProducer(name)
}
func (m *sessionMailbox) ActiveProducers() int { return m.base.ActiveProducers() }

type sessionState struct {
	runtime          *Runtime
	id               string
	logicalID        string
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

	mu          sync.Mutex
	pending     int
	closed      bool
	closeReason SessionCloseReason
	starting    bool
	closeErr    error
	finishClose sync.Once
	closeDone   chan struct{}
}

func (rt *Runtime) OpenSession(ctx context.Context, options SessionOptions) (*Session, error) {
	if err := rt.ready(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = rt.ctx
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(options.ID)
	if id == "" {
		id = rt.nextRuntimeID("session")
	}
	logicalID := strings.TrimSpace(options.LogicalID)
	if logicalID == "" {
		logicalID = id
	}
	if rt.resumeSessionID != "" && options.LogicalID == "" && logicalID == rt.primarySessionID {
		id = rt.nextContinuationID(logicalID)
		if options.ParentSessionID == "" {
			options.ParentSessionID = rt.resumeSessionID
		}
		if len(options.Messages) == 0 {
			options.Messages = rt.resumeMessages
		}
	}
	agentName := strings.TrimSpace(options.AgentName)
	if agentName == "" {
		agentName = rt.nodeName
	}
	if agentName == "" {
		agentName = "cyber"
	}

	rt.mu.Lock()
	if rt.ctx.Err() != nil {
		rt.mu.Unlock()
		return nil, rt.ctx.Err()
	}
	if _, exists := rt.sessions[logicalID]; exists {
		rt.mu.Unlock()
		return nil, fmt.Errorf("session %q already exists", logicalID)
	}
	// A caller can shorten a session's lifetime, never detach it from its owner.
	sessionCtx, cancelSession := context.WithCancel(ctx)
	stopLifetime := context.AfterFunc(rt.ctx, cancelSession)
	cancel := func() { stopLifetime(); cancelSession() }
	baseInbox := inboxpkg.NewBuffered(agent.DefaultInboxCapacity)
	mailbox := &sessionMailbox{base: baseInbox}
	scheduler := agent.NewLoopScheduler(sessionCtx, mailbox, rt.agentConfig.Logger)
	agentCfg := rt.agentConfig.
		WithStream(true).
		WithInbox(mailbox).
		WithSessionID(id).
		WithAgentName(agentName).
		WithBus(rt.events)
	agentCfg.ParentSessionID = options.ParentSessionID
	agentCfg.ParentToolCallID = options.ParentToolCallID
	agentCfg.LoopScheduler = scheduler
	agentCfg.Lifetime = sessionCtx
	ag := agent.NewAgent(agentCfg)
	if len(options.Messages) > 0 {
		ag.LoadMessages(cloneSessionMessages(options.Messages))
	} else if id == rt.primarySessionID && len(rt.resumeMessages) > 0 {
		ag.LoadMessages(cloneSessionMessages(rt.resumeMessages))
	}
	state := &sessionState{
		runtime: rt, id: id, logicalID: logicalID, agentName: agentName, starting: true,
		parentSessionID: options.ParentSessionID, parentToolCallID: options.ParentToolCallID,
		agent: ag, inbox: mailbox,
		scheduler: scheduler, ctx: sessionCtx, cancel: cancel,
		ops: make(chan *sessionOperation, rt.pendingLimit()), done: make(chan struct{}),
	}
	public := &Session{state: state}
	state.commands = &commandSession{state: state}
	mailbox.automatic = func() { state.startAutomaticRun() }
	rt.sessions[logicalID] = state
	rt.wg.Add(1)
	rt.mu.Unlock()

	if logicalID == rt.primarySessionID && rt.heartbeat > 0 {
		_, _ = scheduler.Add(agent.LoopEntry{
			Name: "heartbeat", Interval: rt.heartbeat,
			Mode:   agent.ModeInbox,
			Prompt: "Heartbeat: review current context, check on any running sessions, and decide if action is needed.",
		})
	}
	startDone := make(chan struct{})
	go func() { <-startDone; rt.runSession(state) }()
	ev := agenthooks.SessionEvent{SessionID: id, ParentID: options.ParentSessionID, ParentToolCallID: options.ParentToolCallID, AgentName: agentName, Model: agentCfg.Model, Primary: logicalID == rt.primarySessionID, Deliver: state.deliver}
	_, startErr := agenthooks.SessionStart.Emit(sessionCtx, rt.hooks, ev)
	if startErr == nil {
		startErr = sessionCtx.Err()
	}
	if startErr != nil {
		state.mu.Lock()
		state.closed = true
		state.mu.Unlock()
		state.cancel()
		close(startDone)
		closeErr := rt.CloseSession(context.WithoutCancel(sessionCtx), logicalID, SessionCloseError)
		return nil, errors.Join(startErr, closeErr)
	}
	historyMode := types.SessionHistory_MODE_INHERIT
	if options.HistorySnapshot {
		historyMode = types.SessionHistory_MODE_SNAPSHOT
	}
	emitSessionStarted(rt.events, id, agentName, &aop.SessionStarted{
		Model: rt.agentConfig.Model, ParentSessionId: options.ParentSessionID, ParentToolCallId: options.ParentToolCallID,
	}, historyMode)
	if options.HistorySnapshot && len(options.Messages) > 0 {
		emitContinuationMessages(state, prepareContinuationMessages(options.Messages))
	}
	state.mu.Lock()
	state.starting = false
	state.mu.Unlock()
	close(startDone)
	return public, nil
}

// EnsureSession returns an existing Runtime-owned Session or opens it with the
// Runtime lifetime. It is idempotent so a transport reconnect can safely
// announce the same logical Session again.
func (rt *Runtime) EnsureSession(options SessionOptions) (*Session, error) {
	if err := rt.ready(); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(options.ID)
	logicalID := strings.TrimSpace(options.LogicalID)
	if logicalID == "" {
		logicalID = id
	}
	if logicalID != "" {
		rt.mu.RLock()
		state := rt.sessions[logicalID]
		rt.mu.RUnlock()
		if state != nil {
			return ensuredSession(state, options)
		}
	}
	session, err := rt.OpenSession(rt.ctx, options)
	if err == nil || logicalID == "" {
		return session, err
	}
	// Concurrent reconnects may both observe the Session as absent. The strict
	// OpenSession call admits one; the loser re-reads and validates that Session.
	rt.mu.RLock()
	state := rt.sessions[logicalID]
	rt.mu.RUnlock()
	if state == nil {
		return nil, err
	}
	return ensuredSession(state, options)
}

func ensuredSession(state *sessionState, options SessionOptions) (*Session, error) {
	state.mu.Lock()
	closed := state.closed || state.starting
	state.mu.Unlock()
	if closed || state.ctx.Err() != nil {
		return nil, fmt.Errorf("session %q is closing", state.id)
	}
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

func (rt *Runtime) CloseSession(ctx context.Context, sessionID string, reason SessionCloseReason) error {
	if rt == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	if reason == "" {
		reason = SessionCloseCompleted
	}
	rt.mu.Lock()
	logicalID, state := rt.findSessionLocked(sessionID)
	if state != nil {
		rt.operations.Add(1)
	}
	rt.mu.Unlock()
	if state == nil {
		return nil
	}
	defer rt.operations.Done()
	// Cleanup belongs to the Runtime, not to any individual close waiter.
	state.finishClose.Do(func() {
		state.mu.Lock()
		state.closeReason = reason
		state.closed = true
		state.mu.Unlock()
		state.cancel()
		state.closeDone = make(chan struct{})
		// The current caller is already counted, so Add cannot race a zero
		// counter Wait after the identity is removed from rt.sessions.
		rt.operations.Add(1)
		go func() {
			defer rt.operations.Done()
			defer close(state.closeDone)
			<-state.done
			state.scheduler.Stop()
			state.inbox.Close()
			endCtx, endCancel := context.WithTimeout(context.WithoutCancel(state.ctx), 5*time.Second)
			_, state.closeErr = agenthooks.SessionEnd.Emit(endCtx, rt.hooks, agenthooks.SessionEvent{SessionID: state.id, ParentID: state.parentSessionID, ParentToolCallID: state.parentToolCallID, AgentName: state.agentName, Model: state.agent.Cfg.Model, Reason: string(state.closeReason)})
			endCancel()
			if state.closeErr != nil {
				state.closeErr = fmt.Errorf("session end hooks failed: %v", state.closeErr)
			}
			// The canonical stream isolates and reports observer failures.
			// Publication completes before this session identity is released.
			emitSessionEnded(rt.events, state.id, state.agentName, string(state.closeReason))
			rt.mu.Lock()
			if rt.sessions[logicalID] == state {
				delete(rt.sessions, logicalID)
			}
			rt.mu.Unlock()
		}()
	})
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-state.closeDone:
		return state.closeErr
	default:
	}
	select {
	case <-state.closeDone:
		return state.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (rt *Runtime) findSessionLocked(sessionID string) (string, *sessionState) {
	sessionID = strings.TrimSpace(sessionID)
	if state := rt.sessions[sessionID]; state != nil {
		return sessionID, state
	}
	for logicalID, state := range rt.sessions {
		if state != nil && state.id == sessionID {
			return logicalID, state
		}
	}
	return "", nil
}

func (rt *Runtime) Observe(observer coreevents.Observer) *eventbus.Subscription[*aop.Event] {
	if rt == nil || rt.events == nil || observer == nil {
		return nil
	}
	return rt.events.Observe(observer)
}

// Publish publishes an already-formed runtime event through the State-owned
// AOP bus, applying the same timestamp and sequence stamping as agent events.
func (rt *Runtime) Publish(event *aop.Event) {
	if rt == nil || rt.events == nil || event == nil {
		return
	}
	rt.events.Publish(event)
}

func (rt *Runtime) session(sessionID string) (*Session, error) {
	if rt == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	rt.mu.RLock()
	_, state := rt.findSessionLocked(sessionID)
	rt.mu.RUnlock()
	if state == nil {
		return nil, fmt.Errorf("session %q is not open", sessionID)
	}
	return &Session{state: state}, nil
}

func (rt *Runtime) RunSession(ctx context.Context, sessionID string, input RunInput) (*Run, error) {
	session, err := rt.session(sessionID)
	if err != nil {
		return nil, err
	}
	return session.Run(ctx, input)
}

func (rt *Runtime) CommandSession(ctx context.Context, sessionID, line string) (*types.CommandResult, error) {
	session, err := rt.session(sessionID)
	if err != nil {
		return nil, err
	}
	return session.Command(ctx, line)
}

func (rt *Runtime) CancelRun(turnID string) error {
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

func (rt *Runtime) CancelSessionRun(sessionID, turnID string) error {
	if rt == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	sessionID = strings.TrimSpace(sessionID)
	turnID = strings.TrimSpace(turnID)
	rt.mu.RLock()
	run := rt.runs[turnID]
	_, state := rt.findSessionLocked(sessionID)
	rt.mu.RUnlock()
	actualID := sessionID
	if state != nil {
		actualID = state.id
	}
	if run == nil || run.sessionID != actualID {
		return fmt.Errorf("turn %q is not active in session %q", turnID, sessionID)
	}
	run.cancel()
	return nil
}

// WaitOperations waits for all Runs and asynchronous control operations that
// were admitted before the call. Transports use it to drain before shutdown.
func (rt *Runtime) WaitOperations() {
	if rt != nil {
		rt.operations.Wait()
	}
}

func (s *Session) Run(ctx context.Context, input RunInput) (*Run, error) {
	state := s.currentState()
	if state == nil {
		return nil, fmt.Errorf("session is not configured")
	}
	return state.startRun(ctx, input)
}

func (s *Session) Command(ctx context.Context, line string) (*types.CommandResult, error) {
	state := s.currentState()
	if state == nil {
		return nil, fmt.Errorf("session is not configured")
	}
	if declaration, ok := state.runtime.commandIndex[commandName(line)]; ok && declaration.rotation {
		args, err := coretool.SplitCommandLine(line)
		if err != nil {
			return nil, err
		}
		return declaration.invoke(ctx, s, args[1:])
	}
	done := make(chan commandOutcome, 1)
	op := &sessionOperation{
		execute: func(runCtx context.Context) {
			outcome := state.commands.execute(runCtx, line)
			if outcome.err == nil && len(outcome.result.GetContent()) > 0 {
				state.emitCommandResult(outcome.result)
			}
			done <- outcome
		},
		reject: func(err error) { done <- commandOutcome{err: err} },
	}
	if err := state.admit(ctx, op); err != nil {
		return nil, err
	}
	outcome := <-done
	return outcome.result, outcome.err
}

func (s *Session) ID() string {
	state := s.currentState()
	if state == nil {
		state = s.baseState()
	}
	if state == nil {
		return ""
	}
	return state.id

}

func (s *Session) MessagesSnapshot() []*aop.Message {
	state := s.currentState()
	if state == nil {
		return nil
	}
	return cloneSessionMessages(state.agent.MessagesSnapshot())
}

func cloneSessionMessages(messages []*aop.Message) []*aop.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]*aop.Message, len(messages))
	for i, message := range messages {
		cloned[i] = proto.CloneOf(message)
	}
	return cloned
}

func (s *Session) currentState() *sessionState {
	base := s.baseState()
	if base == nil || base.runtime == nil {
		return nil
	}
	base.runtime.mu.RLock()
	current := base.runtime.sessions[base.logicalID]
	base.runtime.mu.RUnlock()
	// Logical IDs may be reused, but handles belong to one concrete instance.
	// Explicit rotation updates s.state; lookup must never silently rebind it.
	if current != base {
		return nil
	}
	return base
}

func (s *Session) baseState() *sessionState {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	return state
}

func commandName(line string) string {
	fields, err := coretool.SplitCommandLine(strings.TrimSpace(line))
	if err != nil || len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (s *Session) rotateCommand(ctx context.Context, line string) (*types.CommandResult, error) {
	state := s.currentState()
	if state == nil {
		return nil, fmt.Errorf("session is not configured")
	}
	if state.runtime.sessionRunActive(state.id) {
		return nil, fmt.Errorf("task is running — use /stop first")
	}
	name := commandName(line)
	switch name {
	case "/clear":
		newState, err := s.rotate(ctx, SessionCloseCleared, state.id, nil)
		if err != nil {
			return nil, err
		}
		outcome := commandText(line, CommandPresentationPlain, "Context cleared.")
		newState.emitCommandResult(outcome.result)
		return outcome.result, nil
	case "/compact":
		messages := state.agent.MessagesSnapshot()
		if len(messages) < 4 {
			outcome := commandText(line, CommandPresentationPlain, "Nothing to compact (too few messages).")
			state.emitCommandResult(outcome.result)
			return outcome.result, nil
		}
		values, err := coretool.SplitCommandLine(line)
		if err != nil {
			return nil, err
		}
		instructions := ""
		if len(values) > 1 {
			instructions = strings.TrimSpace(strings.Join(values[1:], " "))
		}
		result, err := state.agent.Compact(ctx, agent.CompactConfig{CustomInstructions: instructions})
		if err != nil {
			return nil, err
		}
		newState, err := s.rotate(ctx, SessionCloseCompacted, state.id, state.agent.MessagesSnapshot())
		if err != nil {
			return nil, err
		}
		commandResult := commandText(line, CommandPresentationPlain, fmt.Sprintf(
			"Compacted: ~%d -> ~%d tokens (%d messages kept)", result.TokensBefore, result.TokensAfter, result.KeptMessages))
		newState.emitCommandResult(commandResult.result)
		return commandResult.result, nil
	default:
		return nil, fmt.Errorf("unsupported rotating command %q", name)
	}
}

func (s *Session) Resume(ctx context.Context, path string) (int, error) {
	state := s.currentState()
	if state == nil {
		return 0, fmt.Errorf("session is not configured")
	}
	if state.runtime.sessionRunActive(state.id) {
		return 0, fmt.Errorf("task is running — use /stop first")
	}
	if state.runtime.history == nil {
		return 0, fmt.Errorf("session history is not installed")
	}
	data, err := state.runtime.history.Load(ctx, path)
	if err != nil {
		return 0, err
	}
	if err := state.runtime.history.Validate(path); err != nil {
		return 0, err
	}
	if _, err := s.rotate(ctx, SessionCloseResumed, data.SessionID, data.Messages); err != nil {
		return 0, err
	}
	return len(data.Messages), nil
}

func (s *Session) rotate(ctx context.Context, reason SessionCloseReason, parentSessionID string, messages []*aop.Message) (*sessionState, error) {
	oldState := s.currentState()
	if oldState == nil {
		return nil, fmt.Errorf("session is not configured")
	}
	rt := oldState.runtime
	logicalID := oldState.logicalID
	agentName := oldState.agentName
	prepared := prepareContinuationMessages(messages)
	if err := rt.CloseSession(ctx, logicalID, reason); err != nil {
		return nil, err
	}
	newID := rt.nextContinuationID(logicalID)
	continuation, err := rt.OpenSession(ctx, SessionOptions{
		ID: newID, LogicalID: logicalID, ParentSessionID: parentSessionID,
		AgentName: agentName, Messages: prepared, HistorySnapshot: reason == SessionCloseCompacted,
	})
	if err != nil {
		return nil, err
	}
	newState := continuation.currentState()
	if newState == nil {
		return nil, fmt.Errorf("continuation session was not created")
	}
	s.mu.Lock()
	s.state = newState
	s.mu.Unlock()
	return newState, nil
}

func (rt *Runtime) sessionRunActive(sessionID string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for _, run := range rt.runs {
		if run != nil && run.sessionID == sessionID {
			return true
		}
	}
	return false
}

func prepareContinuationMessages(messages []*aop.Message) []*aop.Message {
	prepared := make([]*aop.Message, 0, len(messages))
	var counter int64
	for _, message := range messages {
		if message == nil {
			continue
		}
		cloned := proto.CloneOf(message)
		counter = max(counter, continuationMessageSequence(cloned.Id))
		prepared = append(prepared, cloned)
	}
	for _, message := range prepared {
		if strings.TrimSpace(message.Id) == "" {
			counter++
			message.Id = fmt.Sprintf("m-%d", counter)
		}
	}
	return prepared
}

func continuationMessageSequence(id string) int64 {
	if !strings.HasPrefix(id, "m-") {
		return 0
	}
	value, _ := strconv.ParseInt(strings.TrimPrefix(id, "m-"), 10, 64)
	return value
}

func emitContinuationMessages(state *sessionState, messages []*aop.Message) {
	if state == nil || state.runtime == nil {
		return
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		if message.Role == "tool" {
			for _, content := range message.Content {
				if result := content.GetToolResult(); result != nil {
					state.runtime.events.Publish(&aop.Event{
						SessionId: state.id, Emitter: state.agentName,
						Payload: &aop.Event_ToolResult{ToolResult: proto.CloneOf(result)},
					})
				}
			}
			continue
		}
		state.runtime.events.Publish(&aop.Event{
			SessionId: state.id, Emitter: state.agentName,
			Payload: &aop.Event_Message{Message: proto.CloneOf(message)},
		})
	}
}

func (s *sessionState) startRun(ctx context.Context, input RunInput) (*Run, error) {
	if !input.automatic && !input.Continue && !hasRunInput(runInputContent(input)) {
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
	run := &Run{sessionID: s.id, turnID: turnID, done: make(chan struct{}), cancel: runCancel}
	s.runtime.mu.Lock()
	if err := s.runtime.ctx.Err(); err != nil {
		s.runtime.mu.Unlock()
		runCancel()
		return nil, err
	}
	if err := s.ctx.Err(); err != nil {
		s.runtime.mu.Unlock()
		runCancel()
		return nil, err
	}
	if _, exists := s.runtime.runs[turnID]; exists {
		s.runtime.mu.Unlock()
		runCancel()
		return nil, fmt.Errorf("turn %q already exists", turnID)
	}
	s.runtime.runs[turnID] = run
	s.runtime.operations.Add(1)
	s.runtime.mu.Unlock()
	op := &sessionOperation{
		execute: func(runCtx context.Context) {
			s.inbox.setActive(true)
			s.emitTurnStarted(turnID)
			result, runErr := s.executeRun(runCtx, turnID, input)
			runResult := result
			if runResult == nil {
				runResult = &agent.Result{Stop: agent.StopReasonError, Err: runErr}
				if errors.Is(runErr, context.Canceled) {
					runResult.Stop = agent.StopReasonCanceled
				}
			}
			s.emitTurnEnded(turnID, runResult, runErr)
			s.inbox.setActive(false)
			if runErr == nil {
				s.inbox.kickAutomatic()
			}
			s.runtime.finishRun(run, runResult, runErr)
		},
		reject: func(err error) {
			result := &agent.Result{Stop: agent.StopReasonCanceled, Err: err}
			if !errors.Is(err, context.Canceled) {
				result.Stop = agent.StopReasonError
			}
			s.emitTurnStarted(turnID)
			s.emitTurnEnded(turnID, result, err)
			s.runtime.finishRun(run, result, err)
		},
	}
	if err := s.admit(runCtx, op); err != nil {
		s.runtime.releaseRun(run)
		return nil, err
	}
	return run, nil
}

func hasRunInput(content []*aop.Content) bool {
	for _, part := range content {
		if strings.TrimSpace(part.GetText().GetText()) != "" {
			return true
		}
		if part.GetMedia() != nil {
			return true
		}
	}
	return false
}

func (s *sessionState) executeRun(ctx context.Context, turnID string, input RunInput) (result *agent.Result, err error) {
	// A replaceable loop may panic. Convert that failure at the session task
	// boundary so the ordinary completion path releases the Run and queue.
	defer func() {
		if failure := recover(); failure != nil {
			result = nil
			err = fmt.Errorf("session %q run panicked: %v", s.id, failure)
		}
	}()
	if input.automatic || input.Continue {
		return s.agent.Continue(ctx, agent.WithTurnID(turnID), agent.WithRunMaxTurns(input.MaxTurns))
	}
	if input.EvalCriteria == "" {
		input.EvalCriteria = s.commands.evalCriteria
	}
	if input.EvalRounds == "" {
		input.EvalRounds = s.commands.evalRounds
	}
	message := input.Message
	if message == nil {
		message = &aop.Message{Role: "user", Content: input.Content}
	} else {
		message = proto.CloneOf(message)
	}
	if message.Role == "" {
		message.Role = "user"
	}
	if len(message.Content) == 1 && message.Content[0].GetText() != nil {
		message.Content[0].GetText().Text = skills.ExpandCommand(message.Content[0].GetText().Text, s.runtime.skills)
	}
	if input.EvalCriteria != "" {
		provider, model, logger := s.runtime.providerSnapshot()
		evalConfig := evaluator.NewLoopConfigWithInput(provider, model, logger, s.runtime.agentConfig.PromptResolver, message, input.EvalCriteria, input.EvalRounds)
		evalConfig.TurnID = turnID
		result, _, err := evaluator.RunWithEval(ctx, s.agent, evalConfig,
			agent.WithTurnID(turnID), agent.WithRunMaxTurns(input.MaxTurns))
		return result, err
	}
	return s.agent.Run(ctx, message, agent.WithTurnID(turnID), agent.WithRunMaxTurns(input.MaxTurns))
}

func runInputContent(input RunInput) []*aop.Content {
	if input.Message != nil {
		return input.Message.Content
	}
	return input.Content
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
	// Caller cancellation must reach queued work synchronously. Relaying it
	// through a goroutine can let the next operation run before cancellation.
	opCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	operation.ctx = opCtx
	operation.cancel = func() { stop(); cancel() }
	s.mu.Lock()
	if err := s.ctx.Err(); err != nil {
		s.mu.Unlock()
		operation.cancel()
		return err
	}
	if err := opCtx.Err(); err != nil {
		s.mu.Unlock()
		operation.cancel()
		return err
	}
	if s.closed {
		s.mu.Unlock()
		operation.cancel()
		return fmt.Errorf("session %q is closed", s.id)
	}
	if s.pending >= s.runtime.pendingLimit() {
		s.mu.Unlock()
		operation.cancel()
		return fmt.Errorf("session %q pending limit reached (%d)", s.id, s.runtime.pendingLimit())
	}
	// Publishing into the bounded queue and sealing admission share this lock.
	// Never leave a sender that can enqueue after the consumer's final drain.
	select {
	case s.ops <- operation:
		s.pending++
		s.mu.Unlock()
		return nil
	default:
		s.mu.Unlock()
		operation.cancel()
		return fmt.Errorf("session %q pending limit reached (%d)", s.id, s.runtime.pendingLimit())
	}
}

func (s *sessionState) releaseOperation() {
	s.mu.Lock()
	if s.pending > 0 {
		s.pending--
	}
	s.mu.Unlock()
}

func (rt *Runtime) runSession(session *sessionState) {
	defer rt.wg.Done()
	defer close(session.done)
	for {
		select {
		case operation := <-session.ops:
			if err := session.ctx.Err(); err != nil {
				operation.reject(err)
			} else if err := operation.ctx.Err(); err != nil {
				operation.reject(err)
			} else {
				operation.execute(operation.ctx)
			}
			operation.cancel()
			session.releaseOperation()
		case <-session.ctx.Done():
			session.mu.Lock()
			session.closed = true
			session.mu.Unlock()
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

func (s *sessionState) emitCommandResult(result *types.CommandResult) {
	event := &aop.Event{SessionId: s.id, Emitter: s.agentName, Payload: &aop.Event_Message{Message: &aop.Message{
		Id: s.runtime.nextCommandResultID(), Role: "assistant", Content: result.GetContent(),
	}}}
	_ = types.SetCommandDetail(event, &types.CommandDetail{Line: result.GetCommand(), Presentation: result.GetPresentation()})
	s.runtime.events.Publish(event)
}

func (rt *Runtime) pendingLimit() int {
	if rt != nil && rt.maxPending > 0 {
		return rt.maxPending
	}
	return DefaultSessionPendingLimit
}

// Deliver admits external input into the primary (or sole) open session.
// Calls before Load and after shutdown are safe and are rejected.
// A successful call transfers ownership of the message to the session.
func (rt *Runtime) Deliver(ctx context.Context, message inboxpkg.Message) error {
	if rt == nil {
		return ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if message.Message == nil {
		return fmt.Errorf("inbox message is required")
	}
	rt.lifecycle.Lock()
	if !rt.loaded || rt.closing || rt.ctx == nil || rt.ctx.Err() != nil {
		rt.lifecycle.Unlock()
		return ErrUnavailable
	}
	rt.mu.RLock()
	state := rt.sessions[rt.primarySessionID]
	if state == nil && len(rt.sessions) == 1 {
		for _, candidate := range rt.sessions {
			state = candidate
		}
	}
	rt.mu.RUnlock()
	if state == nil {
		rt.lifecycle.Unlock()
		return fmt.Errorf("no open session accepts asynchronous input")
	}
	rt.lifecycle.Unlock()
	return state.deliver(ctx, message)
}

// deliver admits into this exact execution, never a replacement logical session.
func (state *sessionState) deliver(ctx context.Context, message inboxpkg.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if message.Message == nil {
		return fmt.Errorf("inbox message is required")
	}
	rt := state.runtime
	rt.lifecycle.Lock()
	if rt.closing || rt.ctx.Err() != nil {
		rt.lifecycle.Unlock()
		return ErrUnavailable
	}
	state.mu.Lock()
	if state.closed || state.starting || state.ctx.Err() != nil {
		state.mu.Unlock()
		rt.lifecycle.Unlock()
		return inboxpkg.ErrInboxClosed
	}
	kick, err := state.inbox.enqueue(message)
	if err == nil {
		rt.operations.Add(1)
	}
	state.mu.Unlock()
	rt.lifecycle.Unlock()
	if err != nil {
		return err
	}
	defer rt.operations.Done()
	if kick != nil {
		kick()
	}
	return nil
}

func (rt *Runtime) nextRuntimeID(prefix string) string {
	rt.mu.Lock()
	rt.requestSeq++
	id := fmt.Sprintf("%s-%d", prefix, rt.requestSeq)
	rt.mu.Unlock()
	return id
}

// A command result is a durable transcript entry, but the runtime counter it
// used to be named after restarts at one with the node process. The hub keeps
// every earlier result, so after a reconnect two different results share a
// message id and any reader that identifies messages by id — the web transcript
// does — overwrites one with the other. Stamp the emission time into the id so
// it stays unique across restarts, the way rotated session ids already do.
func (rt *Runtime) nextCommandResultID() string {
	return rt.nextRuntimeID(fmt.Sprintf("command-%d", time.Now().UnixNano()))
}

func (rt *Runtime) nextContinuationID(logicalID string) string {
	return logicalID + "-" + rt.nextRuntimeID(fmt.Sprintf("session-%d", time.Now().UnixNano()))
}

func (rt *Runtime) releaseRun(run *Run) {
	if run == nil {
		return
	}
	rt.unregisterRun(run)
	rt.operations.Done()
}

func (rt *Runtime) finishRun(run *Run, result *agent.Result, err error) {
	if run == nil {
		return
	}
	rt.unregisterRun(run)
	run.finish(result, err)
	rt.operations.Done()
}

func (rt *Runtime) unregisterRun(run *Run) {
	run.cancel()
	rt.mu.Lock()
	if rt.runs[run.turnID] == run {
		delete(rt.runs, run.turnID)
	}
	rt.mu.Unlock()
}

func (rt *Runtime) providerSnapshot() (agent.Provider, string, telemetry.Logger) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.agentConfig.Provider, rt.agentConfig.Model, rt.agentConfig.Logger
}
