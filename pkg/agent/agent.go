package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/command"
)

type Agent struct {
	provider provider.Provider
	tools    *command.CommandRegistry
	config   Config
	emit     EventHandler

	mu        sync.Mutex
	state     State
	listeners []EventHandler
	cancel    context.CancelFunc
	done      chan struct{}
	running   bool
}

func New(p provider.Provider, tools *command.CommandRegistry, opts ...Option) *Agent {
	cfg := newConfig(opts...)
	cfg.Provider = p
	if tools == nil {
		tools = command.NewRegistry()
	}
	return &Agent{
		provider: p,
		tools:    tools,
		config:   cfg,
		emit:     cfg.Emit,
		state: State{
			SystemPrompt:     cfg.SystemPrompt,
			Tools:            tools,
			PendingToolCalls: make(map[string]struct{}),
		},
		done: closedChan(),
	}
}

func (a *Agent) Subscribe(fn EventHandler) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listeners = append(a.listeners, fn)
	index := len(a.listeners) - 1
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if index >= 0 && index < len(a.listeners) {
			a.listeners[index] = nil
		}
	}
}

func (a *Agent) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cloneState(a.state)
}

func (a *Agent) Abort() {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *Agent) WaitForIdle() {
	a.mu.Lock()
	done := a.done
	a.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (a *Agent) Run(ctx context.Context, task string) (string, error) {
	result, err := a.Prompt(ctx, task)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func (a *Agent) Prompt(ctx context.Context, prompt string) (*Result, error) {
	msg := provider.NewTextMessage("user", prompt)
	runCtx, cancel, err := a.startRun(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer a.finishRun()

	result, runErr := runLoop(runCtx, []provider.ChatMessage{msg}, a.contextSnapshotLocked(), a.runtimeConfig())
	a.finish(result, runErr)
	return result, runErr
}

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

	result, runErr := runLoop(runCtx, nil, a.contextSnapshotLocked(), a.runtimeConfig())
	a.finish(result, runErr)
	return result, runErr
}

func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = nil
	a.state.LastError = nil
	a.state.ErrorMessage = ""
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = make(map[string]struct{})
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
	a.state.IsRunning = true
	a.state.LastError = nil
	a.state.ErrorMessage = ""
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = make(map[string]struct{})
	a.cancel = cancel
	a.done = make(chan struct{})
	return runCtx, cancel, nil
}

func (a *Agent) finishRun() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = false
	a.state.IsRunning = false
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = make(map[string]struct{})
	a.cancel = nil
	if a.done != nil {
		close(a.done)
	}
}

func (a *Agent) contextSnapshotLocked() Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Context{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     append([]provider.ChatMessage(nil), a.state.Messages...),
		Tools:        a.tools,
	}
}

func (a *Agent) runtimeConfig() Config {
	cfg := a.config
	cfg.Provider = a.provider
	cfg.Emit = a.dispatchEvent
	return normalizeConfig(cfg)
}

func (a *Agent) dispatchEvent(ctx context.Context, event Event) error {
	a.applyEvent(event)
	if a.emit != nil {
		if err := a.emit(ctx, event); err != nil {
			return err
		}
	}
	listeners := a.listenersSnapshot()
	for _, listener := range listeners {
		if listener == nil {
			continue
		}
		if err := listener(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) listenersSnapshot() []EventHandler {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]EventHandler(nil), a.listeners...)
}

func (a *Agent) applyEvent(event Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch event.Type {
	case EventMessageStart, EventMessageUpdate:
		if event.Message.Role == "assistant" {
			msg := event.Message
			a.state.StreamingMessage = &msg
		}
	case EventMessageEnd:
		if event.Message.Role == "assistant" {
			a.state.StreamingMessage = nil
		}
		a.state.Messages = append(a.state.Messages, event.Message)
	case EventToolExecutionStart:
		a.state.PendingToolCalls[event.ToolCallID] = struct{}{}
	case EventToolExecutionEnd:
		delete(a.state.PendingToolCalls, event.ToolCallID)
	case EventAgentEnd:
		a.state.StreamingMessage = nil
		if event.Err != nil {
			a.state.LastError = event.Err
			a.state.ErrorMessage = event.Err.Error()
		}
	}
}

func (a *Agent) finish(result *Result, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.state.LastError = err
		a.state.ErrorMessage = err.Error()
	}
	if result != nil {
		a.state.Messages = append([]provider.ChatMessage(nil), result.Messages...)
	}
}

func cloneState(state State) State {
	cloned := state
	cloned.Messages = append([]provider.ChatMessage(nil), state.Messages...)
	cloned.PendingToolCalls = make(map[string]struct{}, len(state.PendingToolCalls))
	for id := range state.PendingToolCalls {
		cloned.PendingToolCalls[id] = struct{}{}
	}
	if state.StreamingMessage != nil {
		msg := *state.StreamingMessage
		cloned.StreamingMessage = &msg
	}
	return cloned
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
