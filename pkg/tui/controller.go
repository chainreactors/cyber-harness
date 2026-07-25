package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type agentRunFunc func(context.Context) (*agent.Result, error)

type pendingRun struct {
	label       string
	displayText string
	run         agentRunFunc
}

type EvalSettings struct {
	Criteria string
	Model    string
	Provider agent.Provider
	Logger   telemetry.Logger
}

type interactiveRunController struct {
	ctx     context.Context
	session *agent.Agent
	output  *AgentOutput

	mu       sync.Mutex
	running  bool
	stopping bool
	cancel   context.CancelFunc
	done     chan struct{}
	onFinish func()
	pending  []pendingRun

	Eval *EvalSettings

	compactContextTokens int
	compactContextWindow int
}

func newInteractiveRunController(ctx context.Context, session *agent.Agent, output *AgentOutput) *interactiveRunController {
	if ctx == nil {
		ctx = context.Background()
	}
	return &interactiveRunController{ctx: ctx, session: session, output: output}
}

func (c *interactiveRunController) SubmitPrompt(label, displayText, prompt string) error {
	if c == nil || c.session == nil {
		return fmt.Errorf("agent session is not configured")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	return c.submit(label, displayText, c.buildRunFunc(prompt))
}

func (c *interactiveRunController) submit(label, displayText string, run agentRunFunc) error {
	c.mu.Lock()
	if c.running {
		// Busy input joins the run-boundary FIFO: each queued prompt becomes a
		// full Input→Run cycle, matching the stdio/web entry semantics.
		c.pending = append(c.pending, pendingRun{label: label, displayText: displayText, run: run})
		inbox := pendingDisplayTexts(c.pending)
		c.mu.Unlock()
		c.output.SetInbox(inbox)
		return nil
	}
	c.mu.Unlock()
	return c.start(label, displayText, run)
}

func (c *interactiveRunController) Continue() error {
	if c == nil || c.session == nil {
		return fmt.Errorf("agent session is not configured")
	}
	c.mu.Lock()
	if c.running {
		c.pending = append(c.pending, pendingRun{label: "continue", run: func(ctx context.Context) (*agent.Result, error) {
			return c.session.Continue(ctx)
		}})
		inbox := pendingDisplayTexts(c.pending)
		c.mu.Unlock()
		c.output.SetInbox(inbox)
		return nil
	}
	c.mu.Unlock()
	return c.start("continue", "", func(ctx context.Context) (*agent.Result, error) {
		return c.session.Continue(ctx)
	})
}

func (c *interactiveRunController) buildRunFunc(prompt string) agentRunFunc {
	if c.Eval == nil || c.Eval.Criteria == "" {
		return func(ctx context.Context) (*agent.Result, error) {
			return c.session.Run(ctx, agent.TextInput(prompt))
		}
	}
	eval := c.Eval
	return func(ctx context.Context) (*agent.Result, error) {
		result, _, err := evaluator.RunWithEval(ctx, c.session,
			evaluator.NewLoopConfig(eval.Provider, eval.Model, eval.Logger, prompt, eval.Criteria, 0))
		return result, err
	}
}

func (c *interactiveRunController) start(label, displayText string, run agentRunFunc) error {
	runCtx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})

	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		cancel()
		return fmt.Errorf("agent is already running")
	}
	c.running = true
	c.stopping = false
	c.cancel = cancel
	c.done = done
	c.mu.Unlock()

	c.output.Start(label, displayText)
	go c.run(runCtx, cancel, done, run)
	return nil
}

func (c *interactiveRunController) run(ctx context.Context, cancel context.CancelFunc, done chan struct{}, run agentRunFunc) {
	defer close(done)
	defer cancel()
	defer func() { c.finish(); c.notifyFinish(); c.drainPending() }()

	result, err := run(ctx)
	if ctx.Err() != nil {
		c.output.EnsureStreamNewline()
		c.output.Stopped()
		return
	}
	if err != nil {
		c.output.EnsureStreamNewline()
		if errors.Is(err, context.Canceled) {
			c.output.Stopped()
			return
		}
		c.output.Error(err)
		return
	}
	if result == nil || strings.TrimSpace(result.Output) == "" {
		c.output.Empty()
		return
	}
	c.output.Final(result.Output)

	c.checkContextUsage(result)
}

func (c *interactiveRunController) checkContextUsage(result *agent.Result) {
	if result == nil || result.ContextTokens <= 0 {
		return
	}
	contextWindow := c.session.ContextWindow()
	if contextWindow <= 0 {
		contextWindow = agent.ModelContextWindow(c.session.Model())
	}
	if result.ContextTokens*100/contextWindow >= 80 {
		c.mu.Lock()
		c.compactContextTokens = result.ContextTokens
		c.compactContextWindow = contextWindow
		c.mu.Unlock()
	}
}

func (c *interactiveRunController) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	c.stopping = false
	c.cancel = nil
}

// drainPending starts the oldest queued run, if any. Queued runs chain: each
// run's defer drains the next, preserving FIFO order.
func (c *interactiveRunController) drainPending() {
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		return
	}
	next := c.pending[0]
	c.pending = c.pending[1:]
	inbox := pendingDisplayTexts(c.pending)
	c.mu.Unlock()
	c.output.SetInbox(inbox)
	if err := c.start(next.label, next.displayText, next.run); err != nil {
		c.output.Error(err)
		c.drainPending()
	}
}

func (c *interactiveRunController) SetOnFinish(fn func()) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onFinish = fn
}

func (c *interactiveRunController) notifyFinish() {
	c.mu.Lock()
	fn := c.onFinish
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (c *interactiveRunController) Stop() bool {
	c.mu.Lock()
	if !c.running || c.cancel == nil {
		c.mu.Unlock()
		return false
	}
	cancel := c.cancel
	c.stopping = true
	// Canceling the current run also drops queued input.
	c.pending = nil
	c.mu.Unlock()

	if c.output != nil {
		c.output.SetInbox(nil)
		c.output.AbortCurrentRun()
	}
	cancel()
	return true
}

// pendingDisplayTexts extracts stable, user-facing inbox previews without
// exposing expanded prompts or internal run closures in the status row.
func pendingDisplayTexts(pending []pendingRun) []string {
	items := make([]string, 0, len(pending))
	for _, run := range pending {
		text := strings.TrimSpace(run.displayText)
		if text == "" {
			text = strings.TrimSpace(run.label)
		}
		items = append(items, text)
	}
	return items
}

func (c *interactiveRunController) Running() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *interactiveRunController) Wait() {
	if c == nil {
		return
	}
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (c *interactiveRunController) StopAndWait() {
	if c == nil {
		return
	}
	c.Stop()
	c.Wait()
}
