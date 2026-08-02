package runner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
)

type runtimeSemanticProvider struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	usage   *aop.TokenUsage
}

func (p *runtimeSemanticProvider) Name() string { return "runtime-semantic" }

func (p *runtimeSemanticProvider) ChatCompletion(ctx context.Context, _ *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 && p.started != nil {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}},
		Usage:   p.usage,
	}, nil
}

func (p *runtimeSemanticProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestSessionRunHasOneReliableTurnLifecycle(t *testing.T) {
	provider := &runtimeSemanticProvider{}
	rt := newBareRuntime(t, nil, provider)
	var all []*aop.Event
	unsubscribe := rt.Subscribe(func(event *aop.Event) { all = append(all, event) })
	defer unsubscribe()

	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{TurnID: "turn-1", Content: []*aop.Content{aop.Text("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	if run.TurnID() != "turn-1" {
		t.Fatalf("turn id = %q", run.TurnID())
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}

	var turnEvents []*aop.Event
	for _, event := range all {
		if event.TurnId != "turn-1" {
			continue
		}
		turnEvents = append(turnEvents, event)
		if event.SessionId != "session-1" || event.TurnId != "turn-1" {
			t.Fatalf("run event identity = %+v", event)
		}
	}
	if len(turnEvents) < 2 || turnEvents[0].GetTurnStarted() == nil || turnEvents[len(turnEvents)-1].GetTurnEnded() == nil {
		t.Fatalf("turn events = %+v", turnEvents)
	}
	starts, ends := 0, 0
	for _, event := range turnEvents {
		if event.GetTurnStarted() != nil {
			starts++
		}
		if event.GetTurnEnded() != nil {
			ends++
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("turn lifecycle starts=%d ends=%d", starts, ends)
	}
	if err := rt.CloseSession(context.Background(), "session-1", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	if all[0].GetSessionStarted() == nil || all[len(all)-1].GetSessionEnded() == nil {
		t.Fatalf("session lifecycle = %+v", all)
	}
}

func TestRunAOPTurnPreservesClientMessageIdentity(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	events := make(chan *aop.Event, 16)
	unsubscribe := rt.Subscribe(func(event *aop.Event) { events <- proto.Clone(event).(*aop.Event) })
	defer unsubscribe()

	opened := rt.OpenAOPSession(&aop.OpenSessionRequest{SessionId: "session-1"})
	if opened.GetAccepted() == nil {
		t.Fatalf("OpenAOPSession = %v", opened)
	}
	input := &aop.Message{
		Id: "client-message-1", Role: "user", Name: "operator",
		Content: []*aop.Content{aop.Text("preserve my identity")},
	}
	run := rt.RunAOPTurn(context.Background(), &aop.RunTurnRequest{
		SessionId: "session-1", TurnId: "turn-1", Input: input,
	})
	if run.GetAccepted() == nil {
		t.Fatalf("RunAOPTurn = %v", run)
	}

	var emitted *aop.Message
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if message := event.GetMessage(); message != nil && message.Id == input.Id {
				emitted = message
			}
			if event.TurnId == "turn-1" && event.GetTurnEnded() != nil {
				if !proto.Equal(emitted, input) {
					t.Fatalf("emitted input = %v, want %v", emitted, input)
				}
				return
			}
		case <-deadline:
			t.Fatal("turn did not finish")
		}
	}
}

func TestConsoleRuntimeAdapterPreservesTotalContextTokens(t *testing.T) {
	provider := &runtimeSemanticProvider{usage: provider.TokenUsage(8192, 0, 8200, 0, 0)}
	rt := newBareRuntime(t, nil, provider)
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := rt.consoleAppInfoForSession(session).Run(context.Background(), "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextTokens != 8200 {
		t.Fatalf("context tokens = %d, want 8200", result.ContextTokens)
	}
}

func TestSessionContextCancellationStopsActiveRun(t *testing.T) {
	provider := &runtimeSemanticProvider{started: make(chan struct{}), release: make(chan struct{})}
	rt := newBareRuntime(t, nil, provider)
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	session, err := rt.OpenSession(sessionCtx, SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{
		TurnID:  "turn-1",
		Content: []*aop.Content{aop.Text("hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}

	cancelSession()
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context canceled", err)
	}
}

func TestCommandAddsAOPHistoryWithoutChangingTranscript(t *testing.T) {
	registry := commands.NewRegistry()
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"core"}}), &commands.Deps{WorkDir: t.TempDir(), BashTimeout: 5, Logger: telemetry.NopLogger()}, registry)
	rt := newBareRuntime(t, registry, nil)
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	before := session.MessagesSnapshot()
	var commandEvent *aop.Event
	rt.Subscribe(func(event *aop.Event) {
		if event.GetMessage() != nil && event.TurnId == "" {
			commandEvent = event
		}
	})
	result, err := session.Command(context.Background(), "!printf COMMAND_OK")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].GetText().GetText(), "COMMAND_OK") {
		t.Fatalf("command result = %+v", result)
	}
	if commandEvent == nil || commandEvent.GetMessage() == nil || commandEvent.TurnId != "" {
		t.Fatalf("command AOP event = %+v", commandEvent)
	}
	detail, ok, err := types.GetCommandDetail(commandEvent)
	if err != nil || !ok || detail.Line != "!printf COMMAND_OK" || detail.Presentation != CommandPresentationPreformatted {
		t.Fatalf("command extension = %+v ok=%v err=%v", detail, ok, err)
	}
	after := session.MessagesSnapshot()
	if len(after) != len(before) {
		t.Fatalf("command changed transcript: before=%d after=%d", len(before), len(after))
	}
}

func TestActiveRunSteersAsyncInputWithoutSecondLifecycle(t *testing.T) {
	provider := &runtimeSemanticProvider{started: make(chan struct{}), release: make(chan struct{})}
	rt := newBareRuntime(t, nil, provider)
	var events []*aop.Event
	rt.Subscribe(func(event *aop.Event) { events = append(events, event) })
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{TurnID: "turn-1", Content: []*aop.Content{aop.Text("start")}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	if err := session.state.inbox.Push(inbox.NewSystemMessage("steer now")); err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if provider.callCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 inside one Run", provider.callCount())
	}
	starts, ends := 0, 0
	for _, event := range events {
		if event.TurnId != "turn-1" {
			continue
		}
		if event.GetTurnStarted() != nil {
			starts++
		}
		if event.GetTurnEnded() != nil {
			ends++
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("steered lifecycle starts=%d ends=%d", starts, ends)
	}
}

func TestIdleAsyncInputCreatesAutomaticRun(t *testing.T) {
	provider := &runtimeSemanticProvider{}
	rt := newBareRuntime(t, nil, provider)
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan *aop.Event, 1)
	rt.Subscribe(func(event *aop.Event) {
		if event.SessionId == "session-1" && event.GetTurnEnded() != nil {
			ended <- event
		}
	})
	if err := session.state.inbox.Push(inbox.NewSystemMessage("automatic work")); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ended:
		if event.TurnId == "" {
			t.Fatal("automatic Run has no turn_id")
		}
	case <-time.After(time.Second):
		t.Fatal("idle async input did not create a Run")
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.callCount())
	}
}
