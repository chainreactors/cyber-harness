package runner

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

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/evaluator"
	inboxpkg "github.com/chainreactors/aiscan/agent/inbox"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	toolpkg "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tui"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const DefaultSessionPendingLimit = 64

type SessionOptions struct {
	ID               string
	LogicalID        string
	ParentSessionID  string
	ParentToolCallID string
	AgentName        string
	Messages         []*aop.Message
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
	TurnID        string
	Message       *aop.Message
	Content       []*aop.Content
	MaxTurns      int
	EvalCriteria  string
	EvalMaxRounds int
	Continue      bool

	automatic bool
}

type RunResult struct {
	Output        string
	Stop          agent.StopReason
	Usage         *aop.TokenUsage
	ContextTokens int
}

const (
	CommandPresentationPlain        = "plain"
	CommandPresentationPreformatted = "preformatted"
)

type Session struct {
	mu        sync.RWMutex
	state     *sessionState
	logicalID string
}

type Run struct {
	sessionID string
	turnID    string
	done      chan struct{}
	cancel    context.CancelFunc
	mu        sync.Mutex
	result    RunResult
	err       error
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
	result *types.CommandResult
	err    error
}

type sessionEmitter struct {
	bus *eventbus.Bus[*aop.Event]
	mu  sync.Mutex
	seq map[string]uint64
}

func newSessionEmitter(bus *eventbus.Bus[*aop.Event]) *sessionEmitter {
	return &sessionEmitter{bus: bus, seq: make(map[string]uint64)}
}

// Emit stamps the event with runtime metadata (timestamp, per-session
// sequence, fallback id) and forwards it to the runtime's single public bus.
// It satisfies aop.EventEmitter so session agents and tools emit through it directly.
func (e *sessionEmitter) Emit(event *aop.Event) {
	if event.EmittedAt == nil {
		event.EmittedAt = timestamppb.Now()
	}
	e.mu.Lock()
	e.seq[event.SessionId]++
	event.Seq = e.seq[event.SessionId]
	if event.Id == "" {
		event.Id = fmt.Sprintf("runtime-%d", event.Seq)
	}
	e.mu.Unlock()
	e.bus.Emit(event)
}

func (e *sessionEmitter) sessionStarted(sessionID, agentName string, started *aop.SessionStarted) {
	e.Emit(&aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionStarted{SessionStarted: started}})
}

func (e *sessionEmitter) sessionEnded(sessionID, agentName, reason string) {
	e.Emit(&aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: reason}}})
}

type turnEmitter struct {
	sessionID string
	turnID    string
	agentName string
	emitter   *sessionEmitter
}

func (e *turnEmitter) start() {
	e.emitter.Emit(&aop.Event{SessionId: e.sessionID, TurnId: e.turnID, Emitter: e.agentName, Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}})
}

func (e *turnEmitter) end(result RunResult, runErr error) {
	ended := &aop.TurnEnded{StopReason: string(result.Stop), Usage: result.Usage, ContextTokens: uint64(max(result.ContextTokens, 0))}
	if runErr != nil {
		ended.Error = &aop.ProtocolError{Message: runErr.Error()}
	}
	e.emitter.Emit(&aop.Event{SessionId: e.sessionID, TurnId: e.turnID, Emitter: e.agentName, Payload: &aop.Event_TurnEnded{TurnEnded: ended}})
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
	ctx = inboxpkg.ContextWithInbox(ctx, s.state.inbox)
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
		return commandText(line, CommandPresentationPreformatted, s.statusText())
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

func (s *commandSession) statusText() string {
	if s == nil || s.state == nil || s.state.runtime == nil {
		return "Agent runtime: unavailable"
	}
	rt := s.state.runtime
	rt.mu.RLock()
	app := rt.app
	provider := rt.config.Provider
	model := rt.config.Model
	providerConfig := agent.ProviderConfig{}
	if app != nil {
		providerConfig = app.ProviderConfig
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
	if app != nil {
		health := app.LLMHealth()
		switch health.State {
		case LLMHealthReady:
			llmState = "ready"
			if health.LatencyMs > 0 {
				llmState += fmt.Sprintf(" (%dms)", health.LatencyMs)
			}
		case LLMHealthFailed:
			llmState = "failed"
			if detail := statusOneLine(health.Error, 160); detail != "" {
				llmState += " · " + detail
			}
		case LLMHealthConfigured:
			llmState = "configured (probe pending)"
		case LLMHealthNotConfigured:
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
	scannerState := "unavailable"
	scannerNames := []string(nil)
	skillState := "not loaded"
	if app != nil {
		if app.Commands != nil {
			for _, registered := range app.Commands.Tools() {
				if registered != nil && strings.TrimSpace(registered.Name()) != "" {
					toolNames = append(toolNames, registered.Name())
				}
			}
			commandNames = app.Commands.Names()
			scannerNames = app.Commands.GroupNames("scanner")
			if len(toolNames) > 0 {
				toolState = "ready"
			}
		}
		if !app.enginesEnabled {
			scannerState = "disabled"
		} else if app.enginesReady != nil {
			select {
			case <-app.enginesReady:
				if app.Engines == nil {
					scannerState = "failed"
				} else if len(scannerNames) > 0 {
					scannerState = "ready"
				} else {
					scannerState = "degraded"
				}
			default:
				scannerState = "loading"
			}
		} else if len(scannerNames) > 0 {
			scannerState = "ready"
		}
		if app.Skills != nil {
			visible := 0
			for _, skill := range app.Skills.Skills {
				if strings.TrimSpace(skill.Name) != "" && !skill.Internal {
					visible++
				}
			}
			skillState = fmt.Sprintf("ready (%d loaded)", visible)
			if len(app.SkillDiagnostics) > 0 {
				skillState = fmt.Sprintf("degraded (%d loaded, %d diagnostics)", visible, len(app.SkillDiagnostics))
			}
		}
	}

	toolDetail := fmt.Sprintf("%s (%d tools, %d commands)", toolState, len(toolNames), len(commandNames))
	if names := summarizeStatusNames(toolNames, 12); names != "" {
		toolDetail += " · " + names
	}
	scannerDetail := scannerState
	if names := summarizeStatusNames(scannerNames, 12); names != "" {
		scannerDetail += fmt.Sprintf(" (%d) · %s", len(scannerNames), names)
	}
	commandDetail := summarizeStatusNames(commandNames, 16)
	if commandDetail == "" {
		commandDetail = "-"
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
		fmt.Sprintf("Scanners: %s", scannerDetail),
		fmt.Sprintf("Skills: %s", skillState),
		fmt.Sprintf("Messages: %d", len(s.state.agent.MessagesSnapshot())),
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
	return commandText(line, CommandPresentationPreformatted, strings.TrimRight(toolpkg.ResultText(result), " \t\r\n"))
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
	runtime          *AgentRuntime
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
	logicalID := strings.TrimSpace(options.LogicalID)
	if logicalID == "" {
		logicalID = id
	}
	if rt.resumeSessionID != "" && options.LogicalID == "" && (logicalID == MainREPLName || logicalID == "task") {
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
		agentName = "aiscan"
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
	sessionCtx, cancel := context.WithCancel(ctx)
	baseInbox := inboxpkg.NewBuffered(agent.DefaultInboxCapacity)
	mailbox := &sessionMailbox{base: baseInbox}
	scheduler := agent.NewLoopScheduler(sessionCtx, mailbox, rt.config.Logger)
	agentCfg := rt.config.
		WithSystemPrompt(rt.systemPrompt).
		WithStream(true).
		WithInbox(mailbox).
		WithSessionID(id).
		WithAgentName(agentName).
		WithBus(rt.sessionEvents)
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
		runtime: rt, id: id, logicalID: logicalID, agentName: agentName,
		parentSessionID: options.ParentSessionID, parentToolCallID: options.ParentToolCallID,
		agent: ag, inbox: mailbox,
		scheduler: scheduler, ctx: sessionCtx, cancel: cancel,
		ops: make(chan *sessionOperation, rt.pendingLimit()), done: make(chan struct{}),
	}
	public := &Session{state: state, logicalID: logicalID}
	state.commands = &commandSession{state: state}
	mailbox.automatic = func() { state.startAutomaticRun() }
	rt.sessions[logicalID] = state
	rt.wg.Add(1)
	rt.mu.Unlock()

	if logicalID == MainREPLName && rt.option != nil && rt.option.Heartbeat > 0 {
		_, _ = scheduler.Add(agent.LoopEntry{
			Name: "heartbeat", Interval: time.Duration(rt.option.Heartbeat) * time.Minute,
			Mode:   agent.ModeInbox,
			Prompt: "Heartbeat: review current context, check on any running sessions, and decide if action is needed.",
		})
	}
	go rt.runSession(state)
	rt.sessionEvents.sessionStarted(id, agentName, &aop.SessionStarted{
		Model: rt.config.Model, ParentSessionId: options.ParentSessionID, ParentToolCallId: options.ParentToolCallID,
	})
	if options.ParentSessionID != "" && options.ParentToolCallID == "" && len(options.Messages) > 0 {
		emitContinuationMessages(state, prepareContinuationMessages(options.Messages))
	}
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
	if options.ParentSessionID != "" && options.ParentSessionID != state.parentSessionID {
		return nil, fmt.Errorf("session %q parent_session_id conflicts with open session", state.id)
	}
	if options.ParentToolCallID != "" && options.ParentToolCallID != state.parentToolCallID {
		return nil, fmt.Errorf("session %q parent_tool_call_id conflicts with open session", state.id)
	}
	if options.AgentName != "" && options.AgentName != state.agentName {
		return nil, fmt.Errorf("session %q agent name conflicts with open session", state.id)
	}
	return &Session{state: state, logicalID: state.logicalID}, nil
}

func (rt *AgentRuntime) CloseSession(ctx context.Context, sessionID string, reason SessionCloseReason) error {
	if rt == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	if reason == "" {
		reason = SessionCloseCompleted
	}
	rt.mu.Lock()
	logicalID, state := rt.findSessionLocked(sessionID)
	if state != nil {
		delete(rt.sessions, logicalID)
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
	rt.sessionEvents.sessionEnded(state.id, state.agentName, string(reason))
	return nil
}

func (rt *AgentRuntime) findSessionLocked(sessionID string) (string, *sessionState) {
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

func (rt *AgentRuntime) Subscribe(fn func(*aop.Event)) func() {
	if rt == nil || rt.bus == nil || fn == nil {
		return func() {}
	}
	return rt.bus.Subscribe(fn)
}

// EmitEvent publishes an already-formed runtime event through the App-owned
// AOP bus, applying the same timestamp and sequence stamping as agent events.
func (rt *AgentRuntime) EmitEvent(event *aop.Event) {
	if rt == nil || rt.sessionEvents == nil || event == nil {
		return
	}
	rt.sessionEvents.Emit(event)
}

func (rt *AgentRuntime) session(sessionID string) (*Session, error) {
	if rt == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	rt.mu.RLock()
	logicalID, state := rt.findSessionLocked(sessionID)
	rt.mu.RUnlock()
	if state == nil {
		return nil, fmt.Errorf("session %q is not open", sessionID)
	}
	return &Session{state: state, logicalID: logicalID}, nil
}

func (rt *AgentRuntime) RunSession(ctx context.Context, sessionID string, input RunInput) (*Run, error) {
	session, err := rt.session(sessionID)
	if err != nil {
		return nil, err
	}
	return session.Run(ctx, input)
}

func (rt *AgentRuntime) CommandSession(ctx context.Context, sessionID, line string) (*types.CommandResult, error) {
	session, err := rt.session(sessionID)
	if err != nil {
		return nil, err
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

func (rt *AgentRuntime) CancelSessionRun(sessionID, turnID string) error {
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
func (rt *AgentRuntime) WaitOperations() {
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
	if name := commandName(line); name == "/clear" || name == "/compact" {
		return s.rotateCommand(ctx, line)
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
	return state.agent.MessagesSnapshot()
}

func (s *Session) Agent() *agent.Agent {
	state := s.currentState()
	if state == nil {
		state = s.baseState()
	}
	if state == nil {
		return nil
	}
	return state.agent
}

func (s *Session) currentState() *sessionState {
	base := s.baseState()
	if base == nil || base.runtime == nil {
		return nil
	}
	s.mu.RLock()
	logicalID := s.logicalID
	s.mu.RUnlock()
	if logicalID == "" {
		logicalID = base.logicalID
	}
	base.runtime.mu.RLock()
	state := base.runtime.sessions[logicalID]
	base.runtime.mu.RUnlock()
	return state
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
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
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
		newState, err := s.rotate(ctx, SessionCloseCleared, state.id, nil, "")
		if err != nil {
			return nil, err
		}
		outcome := commandText(line, CommandPresentationPlain, "Context cleared.")
		newState.emitCommandResult(outcome.result)
		return outcome.result, nil
	case "/compact":
		messages := state.agent.MessagesSnapshot()
		if len(messages) < 4 {
			return commandText(line, CommandPresentationPlain, "Nothing to compact (too few messages).").result, nil
		}
		values, err := commands.SplitCommandLine(line)
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
		newState, err := s.rotate(ctx, SessionCloseCompacted, state.id, state.agent.MessagesSnapshot(), "")
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
	data, err := loadResumeState(path)
	if err != nil {
		return 0, err
	}
	if err := output.ValidateJSONLTarget(path); err != nil {
		return 0, err
	}
	if _, err := s.rotate(ctx, SessionCloseResumed, data.SessionID, data.Messages, path); err != nil {
		return 0, err
	}
	return len(data.Messages), nil
}

func (s *Session) rotate(ctx context.Context, reason SessionCloseReason, parentSessionID string, messages []*aop.Message, recordPath string) (*sessionState, error) {
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
	if recordPath != "" && !samePath(rt.recordPath, recordPath) {
		if rt.app == nil {
			return nil, fmt.Errorf("runtime application is unavailable")
		}
		if err := rt.app.SwitchRecording(recordPath); err != nil {
			return nil, err
		}
		rt.recordPath = recordPath
		if rt.option != nil {
			rt.option.OutputFile = recordPath
			rt.option.Resume = recordPath
		}
	}
	newID := rt.nextContinuationID(logicalID)
	continuation, err := rt.OpenSession(ctx, SessionOptions{
		ID: newID, LogicalID: logicalID, ParentSessionID: parentSessionID,
		AgentName: agentName, Messages: prepared,
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

func (rt *AgentRuntime) sessionRunActive(sessionID string) bool {
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
					state.runtime.sessionEvents.Emit(&aop.Event{
						SessionId: state.id, Emitter: state.agentName,
						Payload: &aop.Event_ToolResult{ToolResult: proto.CloneOf(result)},
					})
				}
			}
			continue
		}
		state.runtime.sessionEvents.Emit(&aop.Event{
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
			if runErr == nil {
				s.inbox.kickAutomatic()
			}
			s.runtime.finishRun(run, runResult, runErr)
		},
		reject: func(err error) {
			result := RunResult{Stop: agent.StopReasonCanceled}
			if !errors.Is(err, context.Canceled) {
				result.Stop = agent.StopReasonError
			}
			emitter.start()
			emitter.end(result, err)
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

func (s *sessionState) executeRun(ctx context.Context, turnID string, input RunInput) (*agent.Result, error) {
	if input.automatic || input.Continue {
		return s.agent.Continue(ctx, agent.WithTurnID(turnID), agent.WithRunMaxTurns(input.MaxTurns))
	}
	if input.EvalCriteria == "" {
		input.EvalCriteria = s.commands.evalCriteria
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
		message.Content[0].GetText().Text = skills.ExpandCommand(message.Content[0].GetText().Text, s.runtime.app.Skills)
	}
	if input.EvalCriteria != "" {
		provider, model, logger := s.runtime.providerSnapshot()
		evalConfig := evaluator.NewLoopConfigWithInput(provider, model, logger, message, input.EvalCriteria, input.EvalMaxRounds)
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

func (s *sessionState) emitCommandResult(result *types.CommandResult) {
	event := &aop.Event{SessionId: s.id, Emitter: s.agentName, Payload: &aop.Event_Message{Message: &aop.Message{
		Id: s.runtime.nextRuntimeID("command"), Role: "assistant", Content: result.GetContent(),
	}}}
	_ = types.SetCommandDetail(event, &types.CommandDetail{Line: result.GetCommand(), Presentation: result.GetPresentation()})
	s.runtime.sessionEvents.Emit(event)
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

func (rt *AgentRuntime) nextContinuationID(logicalID string) string {
	return logicalID + "-" + rt.nextRuntimeID(fmt.Sprintf("session-%d", time.Now().UnixNano()))
}

func (rt *AgentRuntime) releaseRun(run *Run) {
	if run == nil {
		return
	}
	rt.unregisterRun(run)
	rt.operations.Done()
}

func (rt *AgentRuntime) finishRun(run *Run, result RunResult, err error) {
	if run == nil {
		return
	}
	rt.unregisterRun(run)
	run.finish(result, err)
	rt.operations.Done()
}

func (rt *AgentRuntime) unregisterRun(run *Run) {
	run.cancel()
	rt.mu.Lock()
	if rt.runs[run.turnID] == run {
		delete(rt.runs, run.turnID)
	}
	rt.mu.Unlock()
}

func (rt *AgentRuntime) providerSnapshot() (agent.Provider, string, telemetry.Logger) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.config.Provider, rt.config.Model, rt.config.Logger
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
			input.Content = []*aop.Content{aop.Text(prompt)}
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
	info.Resume = session.Resume
	info.ListSessions = func() ([]tui.SavedSession, error) {
		return listSavedSessions(cfg.DataSubDir("sessions"))
	}
	info.ActiveAgent = session.Agent
	info.ActiveSessionID = session.ID
	return info
}
