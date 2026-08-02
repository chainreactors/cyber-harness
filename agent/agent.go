package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/agent/inbox"
	providerpkg "github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
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

func WithRunMaxTurns(maxTurns int) RunOption {
	return func(cfg *Config) { cfg.MaxTurns = maxTurns }
}

func WithTurnID(turnID string) RunOption {
	return func(cfg *Config) {
		cfg.TurnID = turnID
		cfg.emitter = cfg.emitter.turn(turnID)
	}
}

func (a *Agent) Run(ctx context.Context, input *aop.Message, opts ...RunOption) (*Result, error) {
	userMsg, err := resolveInputMessage(input)
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
	if cfg.TurnID == "" {
		cfg.TurnID = randomID()
		cfg.emitter = cfg.emitter.turn(cfg.TurnID)
	}
	if cfg.CaptureProviderFrames {
		runCtx = providerpkg.WithFrameObserver(runCtx, cfg.emitter.providerFrame)
	}
	cfg.Messages = a.MessagesSnapshot()
	if cfg.Inbox == nil {
		cfg.Inbox = inbox.NewBuffered(SubInboxCapacity)
	}
	msg := inbox.FromAOPMessage(userMsg, inbox.OriginUser)
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

func (a *Agent) beginSession() {
	cfg := a.configSnapshot()
	cfg.emitter.sessionStart(cfg.Model)
	emitSessionStart(context.Background(), cfg)
}

func (a *Agent) endSession(reason string) {
	cfg := a.configSnapshot()
	cfg.emitter.sessionEnd(reason)
	emitSessionEnd(context.Background(), cfg, reason)
}

// Continue resumes the agent without a new prompt (e.g. after tool results).
func (a *Agent) Continue(ctx context.Context, opts ...RunOption) (*Result, error) {
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
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.TurnID == "" {
		cfg.TurnID = randomID()
		cfg.emitter = cfg.emitter.turn(cfg.TurnID)
	}
	if cfg.CaptureProviderFrames {
		runCtx = providerpkg.WithFrameObserver(runCtx, cfg.emitter.providerFrame)
	}
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

// SetProviderConfig hot-swaps the provider together with its model limits.
func (a *Agent) SetProviderConfig(p Provider, providerConfig ProviderConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Cfg.Provider = p
	if providerConfig.Model != "" {
		a.Cfg.Model = providerConfig.Model
	}
	a.Cfg.MaxTokens = providerConfig.MaxTokens
	a.Cfg.ContextWindow = providerConfig.ContextWindow
}

// SetMaxTurns overrides the per-run turn cap (0 = unlimited). Applied to the
// next Run; a run already in flight keeps the cap it snapshotted at its start.
func (a *Agent) SetMaxTurns(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Cfg.MaxTurns = n
}

func (a *Agent) Model() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Cfg.Model
}

func (a *Agent) ContextWindow() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Cfg.ContextWindow > 0 {
		return a.Cfg.ContextWindow
	}
	return ModelContextWindow(a.Cfg.Model)
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
	cfg := a.configSnapshot()
	return deriveNamedFromConfig(cfg, cfg.AgentName, "", nil)
}

// DeriveNamed creates an isolated child agent and gives its AOP stream a
// distinct actor name while preserving the current session as its parent.
func (a *Agent) DeriveNamed(name string) *Agent {
	return a.deriveNamed(name, "", nil)
}

func (a *Agent) deriveNamed(name, parentToolCallID string, detail *types.DelegationDetail) *Agent {
	return deriveNamedFromConfig(a.configSnapshot(), name, parentToolCallID, detail)
}

func deriveNamedFromConfig(cfg Config, name, parentToolCallID string, detail *types.DelegationDetail) *Agent {
	return NewAgent(Config{
		Provider:              cfg.Provider,
		Tools:                 cfg.Tools,
		Model:                 cfg.Model,
		MaxTokens:             cfg.MaxTokens,
		ContextWindow:         cfg.ContextWindow,
		Logger:                cfg.Logger,
		MaxRetries:            cfg.MaxRetries,
		MaxParallelTools:      cfg.MaxParallelTools,
		Stream:                cfg.Stream,
		Temperature:           cfg.Temperature,
		CacheRetention:        cfg.CacheRetention,
		CaptureProviderFrames: cfg.CaptureProviderFrames,
		Bus:                   cfg.Bus,
		Hooks:                 cfg.Hooks,
		AgentName:             name,
		ParentSessionID:       cfg.SessionID,
		ParentToolCallID:      parentToolCallID,
		Delegation:            detail,
	})
}

// EmitStatus emits an AOP status event on the agent's session. Used by
// out-of-kernel helpers (evaluator) so their events carry session/seq.
func (a *Agent) EmitStatus(state string, detail proto.Message, turnID ...string) {
	a.mu.Lock()
	em := a.Cfg.emitter
	a.mu.Unlock()
	if em != nil {
		if len(turnID) > 0 && turnID[0] != "" {
			em = em.turn(turnID[0])
		}
		em.status(state, detail)
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

func (a *Agent) LoadMessages(messages []*aop.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append([]*aop.Message(nil), messages...)
}

func (a *Agent) validateContinue() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Cfg.Inbox != nil && a.Cfg.Inbox.Len() > 0 {
		return nil
	}
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

func (a *Agent) MessagesSnapshot() []*aop.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*aop.Message(nil), a.state.Messages...)
}

func (a *Agent) saveState(result *Result, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.state.LastError = err
		a.state.ErrorMessage = err.Error()
	}
	if result != nil {
		a.state.Messages = append([]*aop.Message(nil), result.Messages...)
	}
}
