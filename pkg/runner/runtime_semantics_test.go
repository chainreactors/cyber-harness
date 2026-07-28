package runner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/core/aop"
	xcommand "github.com/chainreactors/aiscan/core/aop/x/command"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
)

type runtimeSemanticProvider struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	usage   *agent.Usage
}

func (p *runtimeSemanticProvider) Name() string { return "runtime-semantic" }

func (p *runtimeSemanticProvider) ChatCompletion(ctx context.Context, _ *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
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
	return &agent.ChatCompletionResponse{
		Choices: []agent.Choice{{Message: agent.NewTextMessage("assistant", "done")}},
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
	var all []aop.Event
	unsubscribe := rt.Subscribe(func(event aop.Event) { all = append(all, event) })
	defer unsubscribe()

	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{TurnID: "turn-1", Parts: []aop.MessagePart{{Type: aop.PartText, Text: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if run.TurnID() != "turn-1" {
		t.Fatalf("turn id = %q", run.TurnID())
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}

	var turnEvents []aop.Event
	for _, event := range all {
		if event.TurnID != "turn-1" {
			continue
		}
		turnEvents = append(turnEvents, event)
		if event.SessionID != "session-1" || event.TurnID != "turn-1" {
			t.Fatalf("run event identity = %+v", event)
		}
	}
	if len(turnEvents) < 2 || turnEvents[0].Type != aop.TypeTurnStart || turnEvents[len(turnEvents)-1].Type != aop.TypeTurnEnd {
		t.Fatalf("turn events = %+v", turnEvents)
	}
	starts, ends := 0, 0
	for _, event := range turnEvents {
		if event.Type == aop.TypeTurnStart {
			starts++
		}
		if event.Type == aop.TypeTurnEnd {
			ends++
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("turn lifecycle starts=%d ends=%d", starts, ends)
	}
	if err := rt.CloseSession(context.Background(), "session-1", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	if all[0].Type != aop.TypeSessionStart || all[len(all)-1].Type != aop.TypeSessionEnd {
		t.Fatalf("session lifecycle = %+v", all)
	}
}

func TestConsoleRuntimeAdapterPreservesTotalContextTokens(t *testing.T) {
	provider := &runtimeSemanticProvider{usage: &agent.Usage{
		PromptTokens: 8192,
		TotalTokens:  8200,
	}}
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
		TurnID: "turn-1",
		Parts:  []aop.MessagePart{{Type: aop.PartText, Text: "hello"}},
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
	var commandEvent aop.Event
	rt.Subscribe(func(event aop.Event) {
		if event.Type == aop.TypeMessage && event.TurnID == "" {
			commandEvent = event
		}
	})
	result, err := session.Command(context.Background(), "!printf COMMAND_OK")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Parts) != 1 || !strings.Contains(result.Parts[0].Text, "COMMAND_OK") {
		t.Fatalf("command result = %+v", result)
	}
	if commandEvent.Type != aop.TypeMessage || commandEvent.TurnID != "" {
		t.Fatalf("command AOP event = %+v", commandEvent)
	}
	detail, ok, err := xcommand.GetDetail(commandEvent)
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
	var events []aop.Event
	rt.Subscribe(func(event aop.Event) { events = append(events, event) })
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{TurnID: "turn-1", Parts: []aop.MessagePart{{Type: aop.PartText, Text: "start"}}})
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
		if event.TurnID != "turn-1" {
			continue
		}
		if event.Type == aop.TypeTurnStart {
			starts++
		}
		if event.Type == aop.TypeTurnEnd {
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
	ended := make(chan aop.Event, 1)
	rt.Subscribe(func(event aop.Event) {
		if event.SessionID == "session-1" && event.Type == aop.TypeTurnEnd {
			ended <- event
		}
	})
	if err := session.state.inbox.Push(inbox.NewSystemMessage("automatic work")); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ended:
		if event.TurnID == "" {
			t.Fatal("automatic Run has no turn_id")
		}
	case <-time.After(time.Second):
		t.Fatal("idle async input did not create a Run")
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.callCount())
	}
}
