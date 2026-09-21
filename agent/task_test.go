package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/inbox"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	types "github.com/chainreactors/cyber/core/types"
)

type taskTestLoop func(context.Context, Config) (*Result, error)

func (f taskTestLoop) Run(ctx context.Context, cfg Config) (*Result, error) { return f(ctx, cfg) }

func TestRunTaskIsolatesParentAndCorrelatesEvents(t *testing.T) {
	bus := coreevents.New()
	var events []*aop.Event
	bus.Observe(coreevents.ObserverFunc(func(ev *aop.Event) { events = append(events, ev) }))
	parent := NewAgent(Config{Loop: StandardLoop{}, Model: "selected-model", SessionID: "parent", TurnID: "parent-turn", MessageCounter: 42, Bus: bus,
		Provider: &scriptedProvider{responses: []*ChatCompletionResponse{chatResponse(NewTextMessage("assistant", "done"))}},
	})
	parent.LoadMessages([]*aop.Message{TextInput("private history")})
	_ = parent.Cfg.Inbox.Push(inbox.NewUserMessage("pending parent input"))
	cfg := parent.ConfigSnapshot()
	cfg.Messages = parent.MessagesSnapshot()
	ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{CallID: "scan-call"})
	result, err := RunTask(ctx, cfg, &types.DelegationDetail{Task: "verify finding", AgentName: "verify", AgentType: "verify"})
	if err != nil || result.Output != "done" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if parent.Cfg.Inbox.Len() != 1 || parent.Cfg.Inbox.Closed() || len(parent.MessagesSnapshot()) != 1 || parent.Cfg.emitter.messageCounter() != 42 {
		t.Fatal("child changed parent state")
	}
	if len(events) < 5 {
		t.Fatalf("missing events: %v", events)
	}
	started := events[0]
	if started.GetSessionStarted().GetParentSessionId() != "parent" || started.GetSessionStarted().GetParentToolCallId() != "scan-call" {
		t.Fatalf("missing delegation ancestry: %v", started)
	}
	detail, ok, err := types.GetDelegation(started)
	if err != nil || !ok || detail.RunMode != types.DelegationRunForeground || detail.ContextMode != types.DelegationContextFresh {
		t.Fatalf("delegation=%v err=%v", detail, err)
	}
	for _, event := range events {
		if event.SessionId == "parent" || event.SessionId != started.SessionId || event.Emitter != "verify" || event.TurnId == "parent-turn" {
			t.Fatalf("event escaped child identity: %v", event)
		}
	}
	if events[len(events)-2].GetTurnEnded() == nil || events[len(events)-1].GetSessionEnded() == nil {
		t.Fatal("missing terminal events")
	}
	requests := cfg.Provider.(*scriptedProvider).requestsSnapshot()
	for _, msg := range requests[0].Messages {
		for _, part := range msg.Content {
			if text := part.GetText().GetText(); text == "private history" || text == "pending parent input" {
				t.Fatal("inherited parent input")
			}
		}
	}
}

func TestRunTaskCancellationDrainsAndClosesInbox(t *testing.T) {
	for _, cancelOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "invocation", true: "owner"}[cancelOwner], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			owner, closeOwner := context.WithCancel(t.Context())
			defer closeOwner()
			started := make(chan struct{})
			release := make(chan struct{})
			var childInbox inbox.Inbox
			cfg := Config{Lifetime: owner, Provider: &scriptedProvider{}, Loop: taskTestLoop(func(ctx context.Context, cfg Config) (*Result, error) {
				childInbox = cfg.Inbox
				close(started)
				<-ctx.Done()
				<-release
				return nil, ctx.Err()
			})}
			done := make(chan error, 1)
			go func() {
				_, err := RunTask(ctx, cfg, &types.DelegationDetail{Task: "work", AgentName: "worker"})
				done <- err
			}()
			<-started
			if cancelOwner {
				closeOwner()
			} else {
				cancel()
			}
			select {
			case err := <-done:
				t.Fatalf("returned before child drained: %v", err)
			default:
			}
			close(release)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || !childInbox.Closed() {
					t.Fatalf("err=%v inbox closed=%v", err, childInbox.Closed())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("child did not cancel")
			}
		})
	}
}

func TestRunTaskPanicFinishesTrace(t *testing.T) {
	bus := coreevents.New()
	var ended *aop.TurnEnded
	var closed bool
	var childInbox inbox.Inbox
	bus.Observe(coreevents.ObserverFunc(func(ev *aop.Event) {
		if ev.GetTurnEnded() != nil {
			ended = ev.GetTurnEnded()
		}
		if ev.GetSessionEnded() != nil {
			closed = true
		}
	}))
	_, err := RunTask(t.Context(), Config{Bus: bus, Provider: &scriptedProvider{}, Loop: taskTestLoop(func(_ context.Context, cfg Config) (*Result, error) {
		childInbox = cfg.Inbox
		panic("worker failure")
	})}, &types.DelegationDetail{Task: "work"})
	if err == nil || ended == nil || ended.Error == nil || ended.StopReason != string(StopReasonError) || !closed || !childInbox.Closed() {
		t.Fatalf("err=%v terminal=%v closed=%v", err, ended, closed)
	}
}

func TestToolCallDoesNotInferDelegation(t *testing.T) {
	bus := coreevents.New()
	events := make(chan *aop.Event, 1)
	bus.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events <- event }))
	em := newAOPEmitter(bus, "cyber", "parent-session", "", "", nil, 0)

	em.toolCall(&aop.ToolCall{
		Id:   "spawn-1",
		Name: "subagent",
		Kind: "function",
		Arguments: &aop.EncodedValue{
			Data:      []byte(`{"action":"create","prompt":"inspect the repository","name":"reviewer","label":"explorer","mode":"fork"}`),
			MediaType: aop.JSONMediaType,
		},
	})

	event := <-events
	_, ok, err := types.GetDelegation(event)
	if err != nil || ok {
		t.Fatalf("tool call inferred delegation: %v %v", ok, err)
	}
}
