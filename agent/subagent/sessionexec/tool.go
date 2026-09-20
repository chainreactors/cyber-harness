// Package sessionexec adapts subagent execution to the public Session API.
package sessionexec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/resource"
	coretool "github.com/chainreactors/cyber/core/tool"
)

type Tool struct {
	runtime   *session.Runtime
	executor  subagent.Executor
	lifetime  context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	stopping  bool
	runs      map[string]*subagent.Run
	wg        sync.WaitGroup
	closeOnce sync.Once
	done      chan struct{}
}

func New(runtime *session.Runtime, executor subagent.Executor, lifetime context.Context) *Tool {
	ctx, cancel := context.WithCancel(lifetime)
	return &Tool{runtime: runtime, executor: executor, lifetime: ctx, cancel: cancel, runs: make(map[string]*subagent.Run), done: make(chan struct{})}
}

func (t *Tool) Name() string { return "subagent" }
func (t *Tool) Description() string {
	return "Delegate an independent task. Omit name for an anonymous subagent, or use a registered name from catalog. Modes: sync (block), async (background), fork (background with parent conversation). Use ioa send for communication."
}

type Args struct {
	Action    string        `json:"action,omitempty" jsonschema:"description=Default create. catalog lists registered subagents; list shows running instances; kill cancels a session_id.,enum=create,enum=catalog,enum=list,enum=kill"`
	Prompt    string        `json:"prompt,omitempty" jsonschema:"description=Task text required for create"`
	Name      string        `json:"name,omitempty" jsonschema:"description=Optional registered subagent name. Omit for an anonymous task."`
	Label     string        `json:"label,omitempty" jsonschema:"description=Optional human-readable instance label; not a unique identifier"`
	SessionID string        `json:"session_id,omitempty" jsonschema:"description=Execution ID required for kill"`
	Mode      subagent.Mode `json:"mode,omitempty" jsonschema:"description=Explicit mode overrides the registered default. Anonymous default is async.,enum=sync,enum=async,enum=fork"`
	Timeout   string        `json:"timeout,omitempty" jsonschema:"description=Positive duration for sync mode only, e.g. 30s or 2m"`
}

func (t *Tool) Definition() *aop.ToolDefinition {
	return coretool.Def(t.Name(), t.Description(), Args{})
}

func (t *Tool) Execute(ctx context.Context, arguments string) (*coretool.Result, error) {
	args, err := coretool.ParseArgs[Args](arguments)
	if err != nil {
		return nil, err
	}
	switch args.Action {
	case "catalog":
		var out strings.Builder
		for _, value := range t.executor.Catalog() {
			fmt.Fprintf(&out, "%s (default=%s): %s\n", value.Name, value.DefaultMode, value.Description)
		}
		if out.Len() == 0 {
			out.WriteString("No named subagents registered. Anonymous tasks are available.")
		}
		return coretool.TextResult(out.String()), nil
	case "list":
		return coretool.TextResult(t.list()), nil
	case "kill":
		output, err := t.kill(args.SessionID)
		return coretool.TextResult(output), err
	case "", "create":
		output, err := t.create(ctx, args)
		return coretool.TextResult(output), err
	default:
		return nil, fmt.Errorf("unknown action: %s", args.Action)
	}
}

func (t *Tool) create(ctx context.Context, args Args) (string, error) {
	if strings.TrimSpace(args.Prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	cfg, ok := agent.ToolAgentConfig(ctx)
	if !ok {
		return "", fmt.Errorf("subagent create requires the executing agent context")
	}
	callID := operation.InvocationFromContext(ctx).CallID
	if callID == "" {
		return "", fmt.Errorf("subagent create requires the spawning tool call id")
	}
	var timeout time.Duration
	if args.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(args.Timeout)
		if err != nil || timeout <= 0 {
			return "", fmt.Errorf("timeout requires sync mode and a positive duration")
		}
	}
	t.mu.Lock()
	if t.stopping || t.lifetime.Err() != nil || !t.runtime.Active() {
		t.mu.Unlock()
		return "", fmt.Errorf("subagent executor is not active")
	}
	t.wg.Add(1)
	t.mu.Unlock()
	// Protect preparation too. Start separately binds the caller lifetime and
	// decides whether invocation cancellation applies based on the resolved mode.
	parentLifetime := cfg.Lifetime
	lifetime, cancel := context.WithCancel(t.lifetime)
	stopParent := func() bool { return false }
	if parentLifetime != nil {
		stopParent = context.AfterFunc(parentLifetime, cancel)
		if parentLifetime.Err() != nil {
			cancel()
		}
	}
	cfg.Lifetime = lifetime
	cleanup := func() { stopParent(); cancel(); t.wg.Done() }
	task, err := t.executor.Start(ctx, cfg, subagent.Request{Name: args.Name, Label: args.Label, Input: subagent.Input{Prompt: args.Prompt}, Mode: args.Mode, Timeout: timeout})
	if err != nil {
		cleanup()
		return "", err
	}
	finishLease := func() { task.Finish(); cleanup() }
	detail := task.Detail
	var messages []*aop.Message
	if task.Mode == subagent.Fork {
		messages = truncateToLastCompleteBoundary(cfg.Messages)
	}
	var producer *inbox.ProducerHandle
	completion := cfg.Inbox
	id := aop.EnvelopeID()
	if task.Mode != subagent.Sync && completion != nil {
		producer = completion.RegisterProducer("subagent:" + id)
	}
	// Publish before OpenSession: its start hooks can fail and close immediately.
	t.mu.Lock()
	t.runs[id] = task
	t.mu.Unlock()
	var closed sync.Once
	onClosed := func(outcome session.Outcome) {
		closed.Do(func() {
			t.mu.Lock()
			delete(t.runs, id)
			t.mu.Unlock()
			if producer == nil {
				return
			}
			defer producer.Done()
			if !outcome.Started || outcome.Result == nil {
				return
			}
			status, content := subagentCompletion(outcome.Result, outcome.Err)
			msg := inbox.NewMessage(inbox.OriginSystem, "user", fmt.Sprintf("<subagent_completion name=%q label=%q session_id=%q status=%q>\n%s\n</subagent_completion>", detail.AgentType, detail.AgentName, id, status, content))
			msg.Meta = map[string]any{"subagent": detail.AgentName, "name": detail.AgentType, "session_id": id, "status": status}
			if err := completion.Push(msg); err != nil && cfg.Logger != nil {
				cfg.Logger.Warnf("inbox push subagent completion %s: %s", detail.AgentName, err)
			}
		})
	}
	childCfg := task.Config.ForTask(detail.AgentName, callID, detail)
	_, err = t.runtime.OpenSession(task.Context, session.SessionOptions{
		ID: id, ParentSessionID: cfg.SessionID, ParentToolCallID: callID,
		AgentName: detail.AgentName, Messages: messages, Config: &childCfg,
		Input: detail.Task, Attached: true, SingleTask: true, OnClosed: onClosed,
	})
	if err != nil {
		onClosed(session.Outcome{})
		finishLease()
		return "", err
	}
	run, err := t.runtime.RunSession(task.Context, id, session.RunInput{Continue: true})
	if err != nil {
		_ = t.runtime.CloseSession(context.Background(), id, session.SessionCloseError)
		finishLease()
		return "", err
	}
	finish := func() (*agent.Result, error) {
		defer finishLease()
		result, err := run.Wait()
		reason := session.SessionCloseCompleted
		if err != nil {
			reason = session.SessionCloseError
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				reason = session.SessionCloseCanceled
			}
		}
		return result, errors.Join(err, t.runtime.CloseSession(context.Background(), id, reason))
	}
	if task.Mode == subagent.Sync {
		result, err := finish()
		if err != nil {
			return fmt.Sprintf("subagent %q (session=%s) failed: %s\n%s", detail.AgentName, id, err, resultOutput(result)), err
		}
		return fmt.Sprintf("<subagent_result name=%q label=%q session_id=%q status=\"completed\">\n%s\n</subagent_result>", detail.AgentType, detail.AgentName, id, resultOutput(result)), nil
	}
	go func() { _, _ = finish() }()
	return fmt.Sprintf("Started subagent %q (session=%s, mode=%s, name=%s). Will notify on completion.", detail.AgentName, id, task.Mode, detail.AgentType), nil
}

func (t *Tool) list() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for id, run := range t.runs {
		lines = append(lines, fmt.Sprintf("  - %s (session=%s, name=%s)\n", run.Detail.AgentName, id, run.Detail.AgentType))
	}
	if len(lines) == 0 {
		return "No subagents running."
	}
	sort.Strings(lines)
	return "Running subagents:\n" + strings.Join(lines, "")
}

func (t *Tool) kill(id string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	run := t.runs[id]
	if run == nil {
		return "", fmt.Errorf("no running subagent session %q", id)
	}
	run.Cancel()
	return fmt.Sprintf("Subagent session %q canceled.", id), nil
}

func (t *Tool) Close(ctx context.Context) error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.stopping = true
		t.cancel()
		t.mu.Unlock()
		go func() { t.wg.Wait(); close(t.done) }()
	})
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return errors.Join(resource.ErrCloseIncomplete, ctx.Err())
	}
}
