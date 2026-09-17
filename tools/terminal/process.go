package terminal

import (
	"context"
	"errors"
	"strings"
	"time"

	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/utils/proc"
)

// Start is the real managed-process boundary. Its completion notification is
// emitted on actual process exit, even when the Bash tool has already returned
// a background status to its caller.
func (t *BashTool) Start(ctx context.Context, command string, options BashExecOptions) (*commands.Execution, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dir := options.WorkDir
	if dir == "" {
		dir = operation.WorkDirFromContext(ctx, t.workDir)
	}
	options.WorkDir = dir

	// Keep operation identity while allowing an explicitly backgrounded process
	// to detach from its tool call's cancellation scope.
	processCtx, cancel := operation.Begin(context.WithoutCancel(ctx), "process", command)
	stopParent := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	ref := operation.Correlation(processCtx)
	event := toolhooks.ProcessEvent{Operation: ref, Directory: dir, Command: command}
	var startedAt time.Time
	releaseEgress := func() {}

	completeStartFailure := func(startErr error) error {
		wrapped := errors.Join(operation.ErrStartFailed, startErr)
		if toolhooks.ProcessCompleted.Has(t.hooks) {
			corehooks.Notify(context.WithoutCancel(processCtx), t.hooks, toolhooks.ProcessCompleted, toolhooks.ProcessCompletion{
				Lifecycle: toolhooks.Lifecycle{Operation: operation.Correlation(processCtx), StartedAt: startedAt, EndedAt: time.Now(), Err: wrapped},
				Process:   event,
			})
		}
		stopParent()
		cancel(wrapped)
		releaseEgress()
		return wrapped
	}

	if toolhooks.BeforeProcess.Has(t.hooks) {
		admission, hookErr := toolhooks.BeforeProcess.Emit(processCtx, t.hooks, event)
		if err := toolhooks.Check(admission, hookErr); err != nil {
			if toolhooks.ProcessCompleted.Has(t.hooks) {
				corehooks.Notify(context.WithoutCancel(processCtx), t.hooks, toolhooks.ProcessCompleted, toolhooks.ProcessCompletion{
					Lifecycle: toolhooks.Lifecycle{Operation: ref, EndedAt: time.Now(), Err: err},
					Process:   event,
				})
			}
			stopParent()
			cancel(err)
			return nil, err
		}
	}
	if cause := context.Cause(processCtx); cause != nil {
		return nil, completeStartFailure(cause)
	}
	if toolhooks.ProcessStarting.Has(t.hooks) {
		corehooks.Notify(processCtx, t.hooks, toolhooks.ProcessStarting, event)
	}
	if cause := context.Cause(processCtx); cause != nil {
		return nil, completeStartFailure(cause)
	}
	if t.egressResolver != nil {
		proxyURL, caPath, release := t.egressResolver(processCtx)
		if release != nil {
			releaseEgress = release
		}
		options.Env = withEgressEnvironment(options.Env, proxyURL, caPath)
	}

	startedAt = time.Now()
	t.processMu.Lock()
	if t.processClosed {
		t.processMu.Unlock()
		return nil, completeStartFailure(commands.ErrUnavailable)
	}
	t.processWG.Add(1)
	execution, err := t.start(processCtx, command, options)
	if err != nil {
		t.processWG.Done()
		t.processMu.Unlock()
		return nil, completeStartFailure(err)
	}
	processCtx = operation.ContextWithResource(processCtx, execution.ID)
	event.Operation = operation.Correlation(processCtx)
	execution.BindProcessControl(processCtx, cancel, stopParent, releaseEgress)
	t.processMu.Unlock()

	var cancelCause error
	if toolhooks.ProcessStartedControl.Has(t.hooks) {
		response, hookErr := toolhooks.ProcessStartedControl.Emit(processCtx, t.hooks, event)
		cancelCause = toolhooks.CancellationCause(response, hookErr)
		if cancelCause != nil {
			operation.RequestCancel(processCtx, cancelCause)
			_ = execution.Kill()
		}
	}
	if toolhooks.ProcessStartedObserved.Has(t.hooks) {
		corehooks.Notify(processCtx, t.hooks, toolhooks.ProcessStartedObserved, event)
	}

	go t.observeProcessCompletion(processCtx, execution, event, startedAt)
	if cancelCause != nil {
		return nil, cancelCause
	}
	return execution, nil
}

func withEgressEnvironment(overrides map[string]string, proxyURL, caPath string) map[string]string {
	result := make(map[string]string, len(overrides)+8)
	for key, value := range overrides {
		result[key] = value
	}
	for _, item := range commands.EgressEnvironment(proxyURL, caPath) {
		if key, value, ok := strings.Cut(item, "="); ok {
			result[key] = value
		}
	}
	return result
}

func (t *BashTool) observeProcessCompletion(ctx context.Context, execution *commands.Execution, event toolhooks.ProcessEvent, startedAt time.Time) {
	defer t.processWG.Done()
	waitErr := execution.Wait(context.WithoutCancel(ctx))
	cause := context.Cause(ctx)
	completionErr := waitErr
	if cause != nil && !errors.Is(completionErr, cause) {
		completionErr = errors.Join(completionErr, cause)
	}
	session := executionSession(execution)
	if toolhooks.ProcessCompleted.Has(t.hooks) {
		corehooks.Notify(context.WithoutCancel(ctx), t.hooks, toolhooks.ProcessCompleted, toolhooks.ProcessCompletion{
			Lifecycle: toolhooks.Lifecycle{Operation: event.Operation, StartedAt: startedAt, EndedAt: time.Now(), Err: completionErr},
			Process:   event,
			Session:   session,
		})
	}
	execution.FinishProcess(completionErr)
}

// executionSession takes a stable copy; hook observers must not retain a
// manager-owned mutable value.
func executionSession(execution *commands.Execution) *proc.Info {
	if info, ok := execution.Session(); ok {
		return &info
	}
	return nil
}
