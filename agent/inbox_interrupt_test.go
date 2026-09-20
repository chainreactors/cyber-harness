package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestInboxInterruptContinuesSameRun(t *testing.T) {
	ib := inbox.NewBuffered(8)
	defer ib.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	calls := 0
	llm := &callbackProvider{fn: func(requestCtx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		calls++
		if calls == 1 {
			m := inbox.NewUserMessage("new direction")
			m.Interrupt = true
			if err := ib.Push(m); err != nil {
				t.Fatal(err)
			}
			<-requestCtx.Done()
			if !errors.Is(context.Cause(requestCtx), inbox.ErrInterrupted) {
				t.Errorf("cause = %v", context.Cause(requestCtx))
			}
			return nil, requestCtx.Err()
		}
		found := false
		for _, m := range req.Messages {
			found = found || strings.Contains(provider.MessageText(m), "new direction")
		}
		if !found {
			t.Fatal("new request lacks interrupting input")
		}
		return chatResponse(NewTextMessage("assistant", "continued")), nil
	}}
	ag := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, Model: "test", Inbox: ib})
	result, err := ag.Run(ctx, TextInput("original"))
	if err != nil || result.Stop != StopReasonCompleted || result.Output != "continued" || calls != 2 || ib.Closed() {
		t.Fatalf("result=%+v calls=%d closed=%v err=%v", result, calls, ib.Closed(), err)
	}
}

func TestInboxWaitDoesNotConsumeOrPoll(t *testing.T) {
	ib := inbox.NewBuffered(8)
	defer ib.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ctx = inbox.ContextWithInbox(ctx, ib)
	tool := &InboxWaitTool{}
	done := make(chan error, 1)
	go func() { _, err := tool.Execute(ctx, `{}`); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("wait returned without input: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := ib.Push(inbox.NewUserMessage("ordinary wake")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if ib.Len() != 1 {
		t.Fatal("wait consumed message")
	}
	if _, err := tool.Execute(ctx, `{}`); err != nil || ib.Len() != 1 {
		t.Fatalf("already queued: %v", err)
	}
	ib.Drain()
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := tool.Execute(canceled, `{}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := tool.Execute(t.Context(), `{}`); err == nil {
		t.Fatal("missing Inbox accepted")
	}
	if _, err := tool.Execute(ctx, `{"timeout":-1}`); err == nil {
		t.Fatal("negative timeout accepted")
	}
	res, err := tool.Execute(ctx, `{"timeout":1}`)
	if err != nil || !strings.Contains(coretool.ResultText(res), "timed out") {
		t.Fatalf("timeout: %v %v", res, err)
	}
}

func TestInboxInterruptSkipsUnstartedTool(t *testing.T) {
	ib := inbox.NewBuffered(8)
	defer ib.Close()
	first := &inboxInterruptTool{ib: ib}
	second := &recordingTool{name: "later", output: "must not execute"}
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(ChatMessage{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "one", Type: "function", Function: FunctionCall{Name: "interrupt_test", Arguments: `{}`}},
			{ID: "two", Type: "function", Function: FunctionCall{Name: "later", Arguments: `{}`}},
		}}),
		chatResponse(NewTextMessage("assistant", "handled")),
	}}
	ag := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, Model: "test", Inbox: ib, MaxParallelTools: 1, Tools: newTestTools(t, first, second)})
	result, err := ag.Run(t.Context(), TextInput("work"))
	if err != nil || result.Output != "handled" {
		t.Fatalf("%+v %v", result, err)
	}
	requests := llm.requestsSnapshot()
	paired := 0
	for _, m := range requests[1].Messages {
		if m.Role == "tool" {
			paired++
			if strings.Contains(provider.MessageText(m), "must not execute") {
				t.Fatal("unstarted tool ran")
			}
		}
	}
	if paired != 2 {
		t.Fatalf("tool result count=%d", paired)
	}
}

type inboxInterruptTool struct{ ib inbox.Inbox }

func (*inboxInterruptTool) Name() string        { return "interrupt_test" }
func (*inboxInterruptTool) Description() string { return "test" }
func (t *inboxInterruptTool) Definition() *coretool.Definition {
	return coretool.Def(t.Name(), t.Description(), struct{}{})
}
func (t *inboxInterruptTool) Execute(context.Context, string) (*coretool.Result, error) {
	m := inbox.NewUserMessage("stop old tools")
	m.Interrupt = true
	return coretool.TextResult("first completed"), t.ib.Push(m)
}

type inboxStreamProvider struct {
	scriptedProvider
	stream func(context.Context, *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error)
}

func (p *inboxStreamProvider) ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
	return p.stream(ctx, req)
}

func TestInboxInterruptStreamingPreservesUsageWithoutPartialCalls(t *testing.T) {
	ib := inbox.NewBuffered(8)
	defer ib.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	calls := 0
	llm := &inboxStreamProvider{stream: func(requestCtx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
		calls++
		ch := make(chan ChatCompletionStreamEvent)
		if calls == 1 {
			go func() {
				defer close(ch)
				for _, event := range []ChatCompletionStreamEvent{
					{Usage: provider.TokenUsage(10, 2, 12, 0, 0)},
					textDelta("unfinished response"),
					toolCallDelta(0, "partial-call", "later", `{"incomplete":`),
				} {
					select {
					case ch <- event:
					case <-requestCtx.Done():
						return
					}
				}
				m := inbox.NewUserMessage("updated task")
				m.Interrupt = true
				_ = ib.Push(m)
				<-requestCtx.Done()
			}()
		} else {
			for _, m := range req.Messages {
				if strings.Contains(provider.MessageText(m), "unfinished response") || len(provider.MessageToolCalls(m)) != 0 {
					t.Fatal("interrupted response entered model history")
				}
			}
			go func() {
				defer close(ch)
				for _, event := range []ChatCompletionStreamEvent{textDelta("done"), {Usage: provider.TokenUsage(15, 3, 18, 0, 0)}, {Done: true}} {
					select {
					case ch <- event:
					case <-requestCtx.Done():
						return
					}
				}
			}()
		}
		return ch, nil
	}}
	ag := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, Model: "test", Stream: true, Inbox: ib})
	result, err := ag.Run(ctx, TextInput("start"))
	if err != nil || result.Output != "done" || result.TotalUsage.GetTotalTokens() != 30 || calls != 2 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestInboxInterruptSubagentReturnsOnlyAfterContinuation(t *testing.T) {
	reg := hooks.New()
	var deliver func(context.Context, inbox.Message) error
	var starts, ends atomic.Int32
	agenthooks.SessionStart.On(reg, "test", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
		deliver = ev.Deliver
		starts.Add(1)
		return struct{}{}, nil
	})
	agenthooks.SessionEnd.On(reg, "test", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
		ends.Add(1)
		if ev.Output != "continued child" || ev.Stop != StopReasonCompleted {
			t.Errorf("end=%+v", ev)
		}
		return struct{}{}, nil
	})
	calls := 0
	llm := &callbackProvider{fn: func(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
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
		return chatResponse(NewTextMessage("assistant", "continued child")), nil
	}}
	parent := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, Model: "test", Hooks: reg, SessionID: "parent"})
	tool := NewSubAgentTool(nil)
	defer tool.Close(context.Background())
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ctx = operation.ContextWithInvocation(withToolAgentConfig(ctx, parent.Cfg), operation.Invocation{CallID: "spawn"})
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
