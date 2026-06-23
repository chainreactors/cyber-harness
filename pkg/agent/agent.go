package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
)

type Agent struct {
	Cfg Config

	mu      sync.Mutex
	state   State
	running bool
}

// Run executes the agent with a prompt and returns the result.
// For one-shot usage, create an agent and call Run once.
// For multi-turn, call Run repeatedly — message history accumulates.
func (a *Agent) Run(ctx context.Context, prompt string) (*Result, error) {
	runCtx, cancel, err := a.startRun(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer a.finishRun()

	cfg := a.Cfg
	cfg = cfg.init()
	cfg.Messages = a.messagesSnapshot()
	if cfg.Inbox == nil {
		cfg.Inbox = inbox.NewBuffered(SubInboxCapacity)
	}
	if err := cfg.Inbox.Push(inbox.NewUserMessage(prompt)); err != nil {
		return nil, fmt.Errorf("push prompt: %w", err)
	}

	result, runErr := runLoop(runCtx, cfg)
	a.saveState(result, runErr)
	return result, runErr
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

	cfg := a.Cfg
	cfg = cfg.init()
	cfg.Messages = a.messagesSnapshot()
	result, runErr := runLoop(runCtx, cfg)
	a.saveState(result, runErr)
	return result, runErr
}

// Derive creates a new Agent with the same infrastructure (provider, tools,
// model, logger) but clean state. Use for spawning independent agent tasks.
func (a *Agent) Derive() *Agent {
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
		ParentSessionID:  a.Cfg.SessionID,
	})
}

// SteerUserMessage pushes user input into the running agent's inbox.
// The loop drains it at the next turn boundary.
func (a *Agent) SteerUserMessage(content string) {
	a.mu.Lock()
	ib := a.Cfg.Inbox
	a.mu.Unlock()
	if ib == nil {
		return
	}
	msg := inbox.NewUserMessage(content)
	msg.Priority = inbox.PriorityHigh
	_ = ib.Push(msg)
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

func (a *Agent) messagesSnapshot() []ChatMessage {
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
