package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/aop/x/delegation"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type Agent struct {
	Cfg Config

	mu      sync.Mutex
	state   State
	running bool
}

// Run executes the agent with an input and returns the result.
// For one-shot usage, create an agent and call Run once.
// For multi-turn, call Run repeatedly — message history accumulates.
type RunOption func(*Config)

func InSession(sessionID, parentSessionID string) RunOption {
	return func(cfg *Config) {
		cfg.SessionID = sessionID
		cfg.ParentSessionID = parentSessionID
		cfg.emitter = cfg.emitter.scoped(sessionID, parentSessionID, "", nil)
	}
}

func WithRunMaxTurns(maxTurns int) RunOption {
	return func(cfg *Config) { cfg.MaxTurns = maxTurns }
}

func (a *Agent) Run(ctx context.Context, input Input, opts ...RunOption) (*Result, error) {
	userMsg, err := input.chatMessage()
	if err != nil {
		return nil, err
	}
	runCtx, cancel, err := a.startRun(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer a.finishRun()

	cfg := a.configSnapshot()
	cfg = cfg.init()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	cfg.Messages = a.MessagesSnapshot()
	if cfg.Inbox == nil {
		cfg.Inbox = inbox.NewBuffered(SubInboxCapacity)
	}
	msg := inbox.FromChatMessage(userMsg, inbox.OriginUser)
	if input.NoEcho {
		msg.Meta = map[string]any{"no_echo": true}
	}
	if err := cfg.Inbox.Push(msg); err != nil {
		return nil, fmt.Errorf("push prompt: %w", err)
	}

	result, runErr := runLoop(runCtx, cfg)
	a.saveState(result, runErr)
	return result, runErr
}

func (a *Agent) SessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Cfg.SessionID
}

// BeginEvalSession opens the root bracket used by the evaluator coordinator.
func (a *Agent) BeginEvalSession() {
	a.mu.Lock()
	em, model := a.Cfg.emitter, a.Cfg.Model
	a.mu.Unlock()
	em.sessionStart(model)
}

// EndEvalSession closes the evaluator coordinator's root bracket.
func (a *Agent) EndEvalSession(stop StopReason, turns int, usage Usage, err error) {
	a.mu.Lock()
	em := a.Cfg.emitter
	a.mu.Unlock()
	em.sessionEnd(stop, turns, usage, err)
}

// Continue resumes the agent without a new prompt (e.g. after tool results).
func (a *Agent) Continue(ctx context.Context) (*Result, error) {
	if err := a.validateContinue(); err != nil {
		return nil, err
	}

	runCtx, cancel, err := a.startRun(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer a.finishRun()

	cfg := a.configSnapshot()
	cfg = cfg.init()
	cfg.Messages = a.MessagesSnapshot()
	result, runErr := runLoop(runCtx, cfg)
	a.saveState(result, runErr)
	return result, runErr
}

// SetProvider hot-swaps the LLM provider (and model, when non-empty) on the
// agent. A run already in flight keeps the provider it snapshotted at start; the
// next run picks up the new one. Safe to call concurrently with Run/Continue.
func (a *Agent) SetProvider(p Provider, model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Cfg.Provider = p
	if model != "" {
		a.Cfg.Model = model
	}
}

// SetMaxTurns overrides the per-run turn cap (0 = unlimited). Applied to the
// next Run; a run already in flight keeps the cap it snapshotted at its start.
func (a *Agent) SetMaxTurns(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Cfg.MaxTurns = n
}

func (a *Agent) SetLogger(logger telemetry.Logger) {
	if a == nil {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	a.mu.Lock()
	a.Cfg.Logger = logger
	if a.Cfg.LoopScheduler != nil {
		a.Cfg.LoopScheduler.SetLogger(logger)
	}
	tools := a.Cfg.Tools
	a.mu.Unlock()
	if sl, ok := tools.(interface{ SetLogger(telemetry.Logger) }); ok {
		sl.SetLogger(logger)
	}
}

// configSnapshot copies Cfg under the lock so a concurrent SetProvider can't
// tear the read a run takes at its start.
func (a *Agent) configSnapshot() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Cfg
}

// Derive creates a new Agent with the same infrastructure (provider, tools,
// model, logger) but clean state. Use for spawning independent agent tasks.
func (a *Agent) Derive() *Agent {
	return a.DeriveNamed(a.Cfg.AgentName)
}

// DeriveNamed creates an isolated child agent and gives its AOP stream a
// distinct actor name while preserving the current session as its parent.
func (a *Agent) DeriveNamed(name string) *Agent {
	return a.deriveNamed(name, "", nil)
}

func (a *Agent) deriveNamed(name, parentToolCallID string, detail *delegation.DelegationDetail) *Agent {
	return NewAgent(Config{
		Provider:         a.Cfg.Provider,
		Fallbacks:        a.Cfg.Fallbacks,
		Tools:            a.Cfg.Tools,
		Model:            a.Cfg.Model,
		Logger:           a.Cfg.Logger,
		MaxRetries:       a.Cfg.MaxRetries,
		MaxParallelTools: a.Cfg.MaxParallelTools,
		Stream:           a.Cfg.Stream,
		Temperature:      a.Cfg.Temperature,
		CacheRetention:   a.Cfg.CacheRetention,
		Bus:              a.Cfg.Bus,
		AgentName:        name,
		ParentSessionID:  a.Cfg.SessionID,
		ParentToolCallID: parentToolCallID,
		Delegation:       detail,
	})
}

// EmitStatus emits an AOP status event on the agent's session. Used by
// out-of-kernel helpers (evaluator) so their events carry session/seq.
func (a *Agent) EmitStatus(state, namespace string, detail any) {
	a.mu.Lock()
	em := a.Cfg.emitter
	a.mu.Unlock()
	if em != nil {
		em.status(state, namespace, detail)
	}
}

// IsRunning returns whether the agent loop is currently executing.
func (a *Agent) IsRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = nil
	a.state.LastError = nil
	a.state.ErrorMessage = ""
}

func (a *Agent) LoadMessages(messages []ChatMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append([]ChatMessage(nil), messages...)
}

func (a *Agent) validateContinue() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.state.Messages) == 0 {
		return fmt.Errorf("cannot continue: no messages in context")
	}
	if a.state.Messages[len(a.state.Messages)-1].Role == "assistant" {
		return fmt.Errorf("cannot continue from message role: assistant")
	}
	return nil
}

func (a *Agent) startRun(ctx context.Context) (context.Context, context.CancelFunc, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil, nil, fmt.Errorf("agent is already running")
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.running = true
	a.state.LastError = nil
	a.state.ErrorMessage = ""
	return runCtx, cancel, nil
}

func (a *Agent) finishRun() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = false
}

func (a *Agent) MessagesSnapshot() []ChatMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ChatMessage(nil), a.state.Messages...)
}

func (a *Agent) saveState(result *Result, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.state.LastError = err
		a.state.ErrorMessage = err.Error()
	}
	if result != nil {
		a.state.Messages = append([]ChatMessage(nil), result.Messages...)
	}
}
