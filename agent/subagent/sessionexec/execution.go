package sessionexec

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
)

// prepare admits work before calling user preparation. Its release belongs to
// the caller until dispatch succeeds, then to the waiter through session close.
func (t *Tool) prepare(ctx context.Context, cfg agent.Config, request subagent.Request) (*subagent.Run, func(), error) {
	t.mu.Lock()
	if t.lifetime.Err() != nil || !t.runtime.Active() {
		t.mu.Unlock()
		return nil, nil, fmt.Errorf("subagent executor is not active")
	}
	t.wg.Add(1)
	t.mu.Unlock()

	// The tool and parent both own preparation; invocation cancellation is
	// resolved by the registry only after the effective mode is known.
	parent := cfg.Lifetime
	if parent == nil {
		parent = t.lifetime
	}
	lifetime, cancel := context.WithCancel(parent)
	stopTool := context.AfterFunc(t.lifetime, cancel)
	if t.lifetime.Err() != nil {
		cancel()
	}
	cfg.Lifetime = lifetime
	task, err := t.executor.Start(ctx, cfg, request)
	release := func() {
		if task != nil {
			task.Finish()
		}
		stopTool()
		cancel()
		t.wg.Done()
	}
	if err != nil {
		release()
		return nil, nil, err
	}
	return task, release, nil
}

// track publishes the instance before OpenSession can invoke start hooks.
// The returned callback also rolls back a failed open, which may already have
// closed the session. Notification and producer release must happen only once.
func (t *Tool) track(id string, task *subagent.Run, cfg agent.Config) func(session.Outcome) {
	detail := task.Detail
	var producer *inbox.ProducerHandle
	completion := cfg.Inbox
	if task.Mode != subagent.Sync && completion != nil {
		producer = completion.RegisterProducer("subagent:" + id)
	}
	t.mu.Lock()
	t.runs[id] = task
	t.mu.Unlock()
	var closed sync.Once
	return func(outcome session.Outcome) {
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
}

// wait keeps the definition lease alive until final records and notification
// have drained, including when another owner initiates session closure.
func (t *Tool) wait(id string, run *session.Run) (*agent.Result, error) {
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
