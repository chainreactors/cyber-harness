package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestInboxWaitRejectsParallelWork(t *testing.T) {
	llm := &scriptedProvider{responses: []*provider.ChatCompletionResponse{
		chatResponse(ChatMessage{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "wait", Type: "function", Function: FunctionCall{Name: "inbox_wait", Arguments: `{}`}},
			{ID: "work", Type: "function", Function: FunctionCall{Name: "work", Arguments: `{}`}},
		}}),
		chatResponse(NewTextMessage("assistant", "finished")),
	}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ag := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, MaxParallelTools: 1, Tools: newTestTools(t, &InboxWaitTool{}, &recordingTool{name: "work", output: "work done"})})
	result, err := ag.Run(ctx, TextInput("parallel wait and work"))
	if err != nil || result.Output != "finished" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	waitRejected, workDone := false, false
	for _, m := range llm.requestsSnapshot()[1].Messages {
		for _, part := range m.Content {
			if result := part.GetToolResult(); result != nil {
				waitRejected = waitRejected || result.Name == "inbox_wait" && result.IsError && strings.Contains(coretool.ResultText(result), "Call inbox_wait alone")
				workDone = workDone || result.Name == "work" && coretool.ResultText(result) == "work done"
			}
		}
	}
	if !waitRejected || !workDone {
		t.Fatalf("wait rejected=%v work=%v", waitRejected, workDone)
	}
}

func TestInboxWaitTimeoutReacquiresWithRunContext(t *testing.T) {
	ib := inbox.NewBuffered(1)
	defer ib.Close()
	reg := hooks.New()
	resuming, release := make(chan struct{}), make(chan struct{})
	agenthooks.InboxWait.On(reg, "test.resume", func(ctx context.Context, event agenthooks.InboxWaitEvent) (struct{}, error) {
		if !event.Waiting {
			if err := ctx.Err(); err != nil {
				return struct{}{}, err
			}
			close(resuming)
			select {
			case <-release:
			case <-ctx.Done():
				return struct{}{}, ctx.Err()
			}
		}
		return struct{}{}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ctx = ContextWithToolAgentConfig(inbox.ContextWithInbox(ctx, ib), Config{SessionID: "worker", Hooks: reg})
	done := make(chan error, 1)
	go func() { _, err := (&InboxWaitTool{}).Execute(ctx, `{"timeout":1}`); done <- err }()
	select {
	case <-resuming:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-done:
		t.Fatalf("resumed before capacity admission: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestInboxWaitPairsResumeAfterHandlerFailure(t *testing.T) {
	ib := inbox.NewBuffered(1)
	defer ib.Close()
	reg := hooks.New()
	suspended := false
	agenthooks.InboxWait.On(reg, "test.capacity", func(_ context.Context, event agenthooks.InboxWaitEvent) (struct{}, error) {
		suspended = event.Waiting
		return struct{}{}, nil
	})
	failure := errors.New("later wait handler failed")
	agenthooks.InboxWait.On(reg, "test.failure", func(_ context.Context, event agenthooks.InboxWaitEvent) (struct{}, error) {
		if event.Waiting {
			return struct{}{}, failure
		}
		return struct{}{}, nil
	})
	ctx := ContextWithToolAgentConfig(inbox.ContextWithInbox(t.Context(), ib), Config{SessionID: "worker", Hooks: reg})
	_, err := (&InboxWaitTool{}).Execute(ctx, "{}")
	if !errors.Is(err, failure) || suspended {
		t.Fatalf("failed wait did not restore capacity: suspended=%v err=%v", suspended, err)
	}
}

func TestInboxInterruptContinuesSameRun(t *testing.T) {
	ib := inbox.NewBuffered(8)
	defer ib.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	calls := 0
	llm := &callbackProvider{fn: func(requestCtx context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
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
	if err != nil || result.Stop != agenthooks.StopReasonCompleted || result.Output != "continued" || calls != 2 || ib.Closed() {
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
	llm := &scriptedProvider{responses: []*provider.ChatCompletionResponse{
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
func (t *inboxInterruptTool) Definition() *aop.ToolDefinition {
	return coretool.Def(t.Name(), t.Description(), struct{}{})
}
func (t *inboxInterruptTool) Execute(context.Context, string) (*aop.ToolResult, error) {
	m := inbox.NewUserMessage("stop old tools")
	m.Interrupt = true
	return coretool.TextResult("first completed"), t.ib.Push(m)
}

type inboxStreamProvider struct {
	scriptedProvider
	stream func(context.Context, *provider.ChatCompletionRequest) (<-chan provider.ChatCompletionStreamEvent, error)
}

func (p *inboxStreamProvider) ChatCompletionStream(ctx context.Context, req *provider.ChatCompletionRequest) (<-chan provider.ChatCompletionStreamEvent, error) {
	return p.stream(ctx, req)
}

func TestInboxInterruptStreamingPreservesUsageWithoutPartialCalls(t *testing.T) {
	ib := inbox.NewBuffered(8)
	defer ib.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	calls := 0
	llm := &inboxStreamProvider{stream: func(requestCtx context.Context, req *provider.ChatCompletionRequest) (<-chan provider.ChatCompletionStreamEvent, error) {
		calls++
		ch := make(chan provider.ChatCompletionStreamEvent)
		if calls == 1 {
			go func() {
				defer close(ch)
				for _, event := range []provider.ChatCompletionStreamEvent{
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
				for _, event := range []provider.ChatCompletionStreamEvent{textDelta("done"), {Usage: provider.TokenUsage(15, 3, 18, 0, 0)}, {Done: true}} {
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
