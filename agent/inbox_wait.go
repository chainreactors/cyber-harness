package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"
)

// InboxWaitTool exposes the existing mailbox wait without consuming its messages.
type InboxWaitTool struct{}

type inboxWaitArgs struct {
	Timeout int `json:"timeout,omitempty" jsonschema:"minimum=0,description=Maximum seconds to wait. Omit or use 0 to wait until a message arrives or the task is canceled."`
}

func (*InboxWaitTool) Name() string { return "inbox_wait" }
func (*InboxWaitTool) Description() string {
	return "Wait for incoming messages without polling or ending the task. Use only when the current task requires waiting; after requested work is complete return a final response instead. Call this tool alone after other tools complete. Returns immediately if messages are already queued. Messages enter your context on the next turn; do not read history to retrieve them."
}
func (t *InboxWaitTool) Definition() *aop.ToolDefinition {
	return coretool.Def(t.Name(), t.Description(), inboxWaitArgs{})
}
func (*InboxWaitTool) Execute(ctx context.Context, arguments string) (*aop.ToolResult, error) {
	args, err := coretool.ParseArgs[inboxWaitArgs](arguments)
	if err != nil {
		return nil, err
	}
	if args.Timeout < 0 || int64(args.Timeout) > int64((1<<63-1)/time.Second) {
		return nil, fmt.Errorf("timeout must be a nonnegative duration in seconds")
	}
	ib := inbox.FromContext(ctx)
	if ib == nil {
		return nil, fmt.Errorf("inbox_wait requires an active Inbox")
	}
	waitCtx := ctx
	if args.Timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
		defer cancel()
	}
	cfg, _ := ToolAgentConfig(ctx)
	ready, err := waitInbox(ctx, waitCtx, cfg, ib, false)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ready {
		return coretool.TextResult("Inbox has messages; they will be consumed on the next turn."), nil
	}
	if ib.Closed() {
		return nil, inbox.ErrInboxClosed
	}
	return coretool.TextResult("Inbox wait timed out; no message was consumed."), nil
}

func waitInbox(ctx, waitCtx context.Context, cfg Config, ib inbox.Inbox, whileActive bool) (ready bool, err error) {
	if ib.Len() > 0 {
		return true, nil
	}
	if ib.Closed() || whileActive && ib.ActiveProducers() == 0 {
		return false, nil
	}
	event := agenthooks.InboxWaitEvent{SessionID: cfg.SessionID, Waiting: true}
	// A later handler can fail after the owner has released capacity. Pair the
	// resume even on admission errors so the next model turn cannot bypass it.
	defer func() {
		event.Waiting = false
		_, resumeErr := agenthooks.InboxWait.Emit(ctx, cfg.Hooks, event)
		err = errors.Join(err, resumeErr)
	}()
	if _, err = agenthooks.InboxWait.Emit(ctx, cfg.Hooks, event); err != nil {
		return false, err
	}
	if whileActive {
		return ib.WaitWhileActive(waitCtx), nil
	}
	return ib.Wait(waitCtx), nil
}
