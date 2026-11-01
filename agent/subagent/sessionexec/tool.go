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
	return "Delegate an independent task. Omit name for an anonymous subagent, or use a registered name from catalog. Modes: sync (block), async (background), fork (background with parent conversation)."
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

func (t *Tool) Execute(ctx context.Context, arguments string) (*aop.ToolResult, error) {
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
	task, finish, err := t.prepare(ctx, cfg, subagent.Request{
		Name: args.Name, Label: args.Label, Input: subagent.Input{Prompt: args.Prompt}, Mode: args.Mode, Timeout: timeout,
	})
	if err != nil {
		return "", err
	}
	detail := task.Detail
	var messages []*aop.Message
	if task.Mode == subagent.Fork {
		messages = truncateToLastCompleteBoundary(cfg.Messages)
	}
	id := aop.EnvelopeID()
	_, run, err := t.open(task, cfg, callID, session.SessionOptions{
		ID: id, ParentSessionID: cfg.SessionID, ParentToolCallID: callID,
		Messages: messages, Attached: true,
	}, nil)
	if err != nil {
		finish()
		return "", err
	}
	wait := func() (*agent.Result, error) {
		defer finish()
		return t.wait(id, run)
	}
	if task.Mode == subagent.Sync {
		result, err := wait()
		if err != nil {
			return fmt.Sprintf("subagent %q (session=%s) failed: %s\n%s", detail.AgentName, id, err, resultOutput(result)), err
		}
		return fmt.Sprintf("<subagent_result name=%q label=%q session_id=%q status=\"completed\">\n%s\n</subagent_result>", detail.AgentType, detail.AgentName, id, resultOutput(result)), nil
	}
	go func() { _, _ = wait() }()
	return fmt.Sprintf("Started subagent %q (session=%s, mode=%s, name=%s). Will notify on completion.", detail.AgentName, id, task.Mode, detail.AgentType), nil
}

// Dispatch runs a host-owned task through the same preparation and Session
// backend as the model tool. Checkpoint runs before releasing its Session and
// lease. Inputs retain their original envelopes; history is supplied separately.
func (t *Tool) Dispatch(ctx context.Context, request subagent.Request, options session.SessionOptions, inputs []inbox.Message, checkpoint func([]*aop.Message, *agent.Result, error) error) (*agent.Result, error) {
	options.Messages = truncateToLastCompleteBoundary(options.Messages)
	restored := make(map[string]bool)
	for _, m := range options.Messages {
		restored[m.Id] = true
	}
	fresh := make([]inbox.Message, 0, len(inputs))
	for _, m := range inputs {
		if !restored[m.Message.Id] {
			fresh = append(fresh, m)
		}
	}
	inputs = fresh
	cfg, err := t.runtime.TaskConfig()
	if err != nil {
		return nil, err
	}
	cfg.Lifetime = ctx
	task, finish, err := t.prepare(ctx, cfg, request)
	if err != nil {
		return nil, err
	}
	defer finish()
	s, run, err := t.open(task, cfg, "", options, inputs)
	if err != nil {
		return nil, err
	}
	result, runErr := run.Wait()
	var saved error
	if checkpoint != nil {
		saved = checkpoint(s.MessagesSnapshot(), result, runErr)
	}
	reason := session.SessionCloseCompleted
	if runErr != nil {
		reason = session.SessionCloseError
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			reason = session.SessionCloseCanceled
		}
	}
	closeErr := t.runtime.CloseSession(context.Background(), s.ID(), reason)
	return result, errors.Join(runErr, saved, closeErr)
}

func (t *Tool) open(task *subagent.Run, cfg agent.Config, callID string, options session.SessionOptions, inputs []inbox.Message) (*session.Session, *session.Run, error) {
	if options.ID == "" {
		options.ID = aop.EnvelopeID()
	}
	onClosed := t.track(options.ID, task, cfg)
	childCfg := task.Config.ForTask(task.Detail.AgentName, callID, task.Detail)
	options.AgentName, options.Config, options.SingleTask, options.OnClosed = task.Detail.AgentName, &childCfg, true, onClosed
	if inputs == nil {
		options.Input = task.Detail.Task
	}
	s, err := t.runtime.OpenSession(task.Context, options)
	if err != nil {
		onClosed(session.Outcome{})
		return nil, nil, err
	}
	for _, message := range inputs {
		if err = s.Deliver(task.Context, message); err != nil {
			break
		}
	}
	var run *session.Run
	if err == nil {
		run, err = t.runtime.RunSession(task.Context, s.ID(), session.RunInput{Continue: true})
	}
	if err != nil {
		_ = t.runtime.CloseSession(context.Background(), s.ID(), session.SessionCloseError)
		return nil, nil, err
	}
	return s, run, nil
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
