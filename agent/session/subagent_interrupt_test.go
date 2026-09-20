package session

import (
	"context"
	"github.com/chainreactors/cyber/agent"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
)

func TestInboxInterruptSubagentReturnsOnlyAfterContinuation(t *testing.T) {
	reg := hooks.New()
	var deliver func(context.Context, inbox.Message) error
	var starts, ends atomic.Int32
	agenthooks.SessionStart.On(reg, "test", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
		if ev.ParentToolCallID == "" {
			return struct{}{}, nil
		}
		deliver = ev.Deliver
		starts.Add(1)
		return struct{}{}, nil
	})
	agenthooks.SessionEnd.On(reg, "test", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
		if ev.ParentToolCallID == "" {
			return struct{}{}, nil
		}
		ends.Add(1)
		if ev.Output != "continued child" || ev.Stop != agent.StopReasonCompleted {
			t.Errorf("end=%+v", ev)
		}
		return struct{}{}, nil
	})
	calls := 0
	llm := &callbackProvider{fn: func(ctx context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			m := inbox.NewUserMessage("redirect child")
			m.Interrupt = true
			if err := deliver(ctx, m); err != nil {
				return nil, err
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if starts.Load() != 1 || ends.Load() != 0 {
			t.Errorf("task ended/restarted on interruption")
		}
		return chatResponse(newTextMessage("assistant", "continued child")), nil
	}}
	parent := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{}, Provider: llm, Model: "test", Hooks: reg, SessionID: "parent"})

	tool := newSubagentTestTool(t, parent.Cfg)
	defer tool.runtime.close(context.Background())
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ctx = operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(ctx, parent.Cfg), operation.Invocation{CallID: "spawn"})
	if _, err := tool.Execute(ctx, `{"mode":"async","prompt":"work"}`); err != nil {
		t.Fatal(err)
	}
	if !parent.Cfg.Inbox.Wait(ctx) {
		t.Fatal("missing completion")
	}
	if starts.Load() != 1 || ends.Load() != 1 {
		t.Fatalf("starts=%d ends=%d", starts.Load(), ends.Load())
	}
	msgs := parent.Cfg.Inbox.Drain()
	if len(msgs) != 1 || !strings.Contains(provider.MessageText(msgs[0].Message), "continued child") {
		t.Fatalf("completion=%+v", msgs)
	}
	if err := deliver(t.Context(), inbox.NewUserMessage("late")); err == nil {
		t.Fatal("completed child revived")
	}
}
