package runner

// This file owns the runtime session layer. Three session concepts coexist in
// this package, each with a single responsibility:
//   - runtime session (here): execution state + per-session FIFO work queue
//   - tmux session (pkg/agent/tmux): the PTY process a terminal attaches to
//   - agent session id (agent.Config.SessionID): the AOP protocol identifier
//
// The resident main REPL aligns all three under MainREPLName.

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	outputpkg "github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/aop/x/eval"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
)

type sessionState struct {
	id        string
	agent     *agent.Agent
	inbox     inboxpkg.Inbox
	scheduler *agent.LoopScheduler
	work      chan func()

	mu     sync.Mutex
	cancel context.CancelFunc
	tail   chan struct{}
}

type runtimeRequestState struct {
	sessionID string
	cancel    context.CancelFunc
}

func (rt *AgentRuntime) Execute(ctx context.Context, requestID string, inbound agent.Inbound, onStart func(string)) (*agent.Result, error) {
	wait, err := rt.Submit(ctx, requestID, inbound, onStart)
	if err != nil {
		return nil, err
	}
	return wait()
}

// Submit admits an AOP request in call order and returns a waiter for its
// result. Admission is non-blocking for the transport; execution still crosses
// the session's unbuffered worker channel.
func (rt *AgentRuntime) Submit(ctx context.Context, requestID string, inbound agent.Inbound, onStart func(string)) (func() (*agent.Result, error), error) {
	if inbound.Kind != agent.InboundUserMessage {
		return nil, fmt.Errorf("runtime Execute requires an inbound user message")
	}
	sessionID := strings.TrimSpace(inbound.Event.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("runtime session_id is required")
	}
	return submitSession(rt, ctx, sessionID, requestID, onStart, func(runCtx context.Context, ag *agent.Agent) (*agent.Result, error) {
		return agent.ExecuteInbound(runCtx, ag, inbound, agent.InboundDependencies{
			DefaultMaxTurns: rt.Config.MaxTurns,
			Eval: func(evalCtx context.Context, evalAgent *agent.Agent, goal string, control eval.Control) (*agent.Result, error) {
				p, model, logger := rt.providerSnapshot()
				result, _, evalErr := evaluator.RunWithEval(evalCtx, evalAgent,
					evaluator.NewLoopConfig(p, model, logger, goal, control.Criteria, control.MaxRounds))
				return result, evalErr
			},
		})
	})
}

// ExecuteLine serializes a slash/direct command through the same per-session
// FIFO used by AOP input. It is used by the Web chat adapter only; the resident
// terminal runs its console directly because it is the sole producer for its
// dedicated conversation session.
func (rt *AgentRuntime) ExecuteLine(ctx context.Context, requestID, sessionID, line string) (string, error) {
	wait, err := rt.SubmitLine(ctx, requestID, sessionID, line)
	if err != nil {
		return "", err
	}
	return wait()
}

func (rt *AgentRuntime) SubmitLine(ctx context.Context, requestID, sessionID, line string) (func() (string, error), error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("runtime session_id is required")
	}
	return submitSession(rt, ctx, sessionID, requestID, nil, func(runCtx context.Context, ag *agent.Agent) (string, error) {
		var stdout, stderr bytes.Buffer
		option := rt.Option
		if option != nil {
			copy := *option
			copy.NoColor = true
			option = &copy
		}
		appInfo := rt.consoleAppInfo()
		console := tui.NewAgentConsoleWithWriters(runCtx, option, appInfo, ag, &stdout, &stderr)
		_, execErr := console.ExecuteLineAndWait(line)
		out := strings.TrimRight(outputpkg.StripANSI(stdout.String()), " \t\r\n")
		errOut := strings.TrimRight(outputpkg.StripANSI(stderr.String()), " \t\r\n")
		if execErr != nil {
			if errOut != "" {
				return "", fmt.Errorf("%s: %w", errOut, execErr)
			}
			return "", execErr
		}
		if out == "" {
			out = errOut
		} else if errOut != "" {
			out = strings.TrimRight(out+"\n"+errOut, " \t\r\n")
		}
		return out, nil
	})
}

func (rt *AgentRuntime) PushInbox(sessionID string, message inboxpkg.Message) error {
	sess, err := rt.session(sessionID)
	if err != nil {
		return err
	}
	return sess.inbox.Push(message)
}

// inboxPush returns the push function for runtime-owned producers (tmux
// completions, IOA subscriptions): with a resident main REPL, messages route
// to its session inbox; otherwise to the shared runtime inbox.
func (rt *AgentRuntime) inboxPush() func(inboxpkg.Message) error {
	if rt.replMode != REPLDisabled {
		return func(message inboxpkg.Message) error { return rt.PushInbox(MainREPLName, message) }
	}
	return rt.Config.Inbox.Push
}

func (rt *AgentRuntime) Cancel(requestID string) bool {
	if rt == nil || requestID == "" {
		return false
	}
	rt.mu.RLock()
	state, ok := rt.requests[requestID]
	rt.mu.RUnlock()
	if ok && state.cancel != nil {
		state.cancel()
	}
	return ok
}

func (rt *AgentRuntime) CancelSession(sessionID string) bool {
	if rt == nil || sessionID == "" {
		return false
	}
	rt.mu.RLock()
	sess := rt.sessions[sessionID]
	cancels := make([]context.CancelFunc, 0)
	for _, state := range rt.requests {
		if state.sessionID == sessionID && state.cancel != nil {
			cancels = append(cancels, state.cancel)
		}
	}
	rt.mu.RUnlock()
	if sess == nil && len(cancels) == 0 {
		return false
	}
	for _, cancel := range cancels {
		cancel()
	}
	if sess != nil {
		sess.mu.Lock()
		cancel := sess.cancel
		sess.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return true
}

func submitSession[T any](rt *AgentRuntime, ctx context.Context, sessionID, requestID string, onStart func(string), run func(context.Context, *agent.Agent) (T, error)) (func() (T, error), error) {
	var zero T
	if rt == nil || run == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sess, err := rt.session(sessionID)
	if err != nil {
		return nil, err
	}

	reqCtx, reqCancel := context.WithCancel(ctx)
	requestID, err = rt.registerRequest(requestID, sessionID, reqCancel)
	if err != nil {
		reqCancel()
		return nil, err
	}
	finish := func() {
		reqCancel()
		rt.unregisterRequest(requestID)
	}

	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	task := func() {
		defer finish()
		if reqCtx.Err() != nil {
			done <- result{err: reqCtx.Err()}
			return
		}
		runCtx, cancel := context.WithCancel(reqCtx)
		sess.mu.Lock()
		sess.cancel = cancel
		sess.mu.Unlock()
		if onStart != nil {
			onStart(sess.id)
		}
		value, runErr := run(runCtx, sess.agent)
		cancel()
		sess.mu.Lock()
		sess.cancel = nil
		sess.mu.Unlock()
		done <- result{value: value, err: runErr}
	}
	sess.mu.Lock()
	previous := sess.tail
	next := make(chan struct{})
	sess.tail = next
	sess.mu.Unlock()
	go func() {
		select {
		case <-previous:
		case <-reqCtx.Done():
			finish()
			done <- result{err: reqCtx.Err()}
			close(next)
			return
		case <-rt.ctx.Done():
			finish()
			done <- result{err: rt.ctx.Err()}
			close(next)
			return
		}
		select {
		case sess.work <- task:
			close(next)
		case <-reqCtx.Done():
			finish()
			done <- result{err: reqCtx.Err()}
			close(next)
		case <-rt.ctx.Done():
			finish()
			done <- result{err: rt.ctx.Err()}
			close(next)
		}
	}()
	return func() (T, error) {
		result := <-done
		if result.err != nil {
			return zero, result.err
		}
		return result.value, nil
	}, nil
}

func (rt *AgentRuntime) session(sessionID string) (*sessionState, error) {
	if rt == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("runtime session_id is required")
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.ctx.Err() != nil {
		return nil, rt.ctx.Err()
	}
	if sess := rt.sessions[sessionID]; sess != nil {
		return sess, nil
	}

	ib := rt.Config.Inbox
	scheduler := rt.Config.LoopScheduler
	if sessionID != MainREPLName || ib == nil || scheduler == nil {
		ib = inboxpkg.NewBuffered(agent.DefaultInboxCapacity)
		scheduler = agent.NewLoopScheduler(ib, rt.Config.Logger)
	}
	if sessionID == MainREPLName && rt.Option != nil && rt.Option.Heartbeat > 0 {
		_, _ = scheduler.Add(rt.ctx, agent.LoopEntry{
			Name:     "heartbeat",
			Interval: time.Duration(rt.Option.Heartbeat) * time.Minute,
			Mode:     agent.ModeInbox,
			Prompt:   "Heartbeat: review current context, check on any running sessions, and decide if action is needed.",
		})
	}
	agentCfg := rt.Config.
		WithSystemPrompt(rt.SystemPrompt).
		WithStream(true).
		WithInbox(ib).
		WithSessionID(sessionID)
	agentCfg.LoopScheduler = scheduler
	ag := agent.NewAgent(agentCfg)
	if sessionID == MainREPLName && len(rt.ResumeMessages) > 0 {
		ag.LoadMessages(rt.ResumeMessages)
	}
	sess := &sessionState{
		id:        sessionID,
		agent:     ag,
		inbox:     ib,
		scheduler: scheduler,
		work:      make(chan func()),
		tail:      make(chan struct{}),
	}
	close(sess.tail)
	rt.sessions[sessionID] = sess
	rt.wg.Add(1)
	go rt.runSession(sess)
	return sess, nil
}

func (rt *AgentRuntime) runSession(sess *sessionState) {
	defer rt.wg.Done()
	for {
		select {
		case <-rt.ctx.Done():
			return
		case task := <-sess.work:
			task()
		}
	}
}

func (rt *AgentRuntime) registerRequest(requestID, sessionID string, cancel context.CancelFunc) (string, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if requestID == "" {
		rt.requestSeq++
		requestID = fmt.Sprintf("runtime-%d", rt.requestSeq)
	}
	if _, exists := rt.requests[requestID]; exists {
		return "", fmt.Errorf("runtime request %q already exists", requestID)
	}
	rt.requests[requestID] = runtimeRequestState{sessionID: sessionID, cancel: cancel}
	return requestID, nil
}

func (rt *AgentRuntime) unregisterRequest(requestID string) {
	rt.mu.Lock()
	delete(rt.requests, requestID)
	rt.mu.Unlock()
}

func (rt *AgentRuntime) cancelAllRequests() {
	rt.mu.RLock()
	cancels := make([]context.CancelFunc, 0, len(rt.requests))
	for _, state := range rt.requests {
		if state.cancel != nil {
			cancels = append(cancels, state.cancel)
		}
	}
	rt.mu.RUnlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (rt *AgentRuntime) closeSessions() {
	rt.mu.Lock()
	sessions := make([]*sessionState, 0, len(rt.sessions))
	for _, sess := range rt.sessions {
		sessions = append(sessions, sess)
	}
	rt.sessions = make(map[string]*sessionState)
	rt.mu.Unlock()
	for _, sess := range sessions {
		if sess.scheduler != nil {
			sess.scheduler.Stop()
		}
		if sess.inbox != nil {
			sess.inbox.Close()
		}
	}
}

func (rt *AgentRuntime) providerSnapshot() (agent.Provider, string, telemetry.Logger) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.Config.Provider, rt.Config.Model, rt.Config.Logger
}

func (rt *AgentRuntime) consoleAppInfo() tui.AppInfo {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return tui.AppInfo{
		Provider:          rt.App.Provider,
		ProviderConfig:    rt.App.ProviderConfig,
		ProviderFallbacks: rt.App.ProviderFallbacks,
		Commands:          rt.App.Commands,
		Skills:            rt.App.Skills,
		OnProviderChange:  rt.SetProvider,
		OnLoggerChange:    rt.SetLogger,
	}
}
