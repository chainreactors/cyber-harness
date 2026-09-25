package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	terminaltools "github.com/chainreactors/cyber/pkg/exts/terminal"

	looptool "github.com/chainreactors/cyber/tools/loop"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
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

func TestSessionHandleCannotRebindToReplacement(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	previous, err := rt.OpenSession(t.Context(), SessionOptions{ID: "previous", LogicalID: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.CloseSession(t.Context(), "stable", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	replacement, err := rt.OpenSession(t.Context(), SessionOptions{ID: "replacement", LogicalID: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if previous.ID() != "previous" || previous.currentState() != nil {
		t.Fatal("closed handle rebound to the replacement session")
	}
	if _, err := previous.Command(t.Context(), "/status"); err == nil {
		t.Fatal("closed handle operated on a replacement session")
	}
	if _, err := replacement.Command(t.Context(), "/status"); err != nil {
		t.Fatalf("replacement is unavailable: %v", err)
	}
}

func TestSessionHistorySnapshotsAreIsolated(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	input := &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("original")}}
	session, err := rt.OpenSession(t.Context(), SessionOptions{ID: "snapshot", Messages: []*aop.Message{input}})
	if err != nil {
		t.Fatal(err)
	}
	input.Content[0].GetText().Text = "changed input"
	first := session.MessagesSnapshot()
	if len(first) != 1 || first[0].Content[0].GetText().Text != "original" {
		t.Fatal("session retained mutable caller history")
	}
	first[0].Content[0].GetText().Text = "changed snapshot"
	second := session.MessagesSnapshot()
	if len(second) != 1 || second[0].Content[0].GetText().Text != "original" {
		t.Fatal("snapshot exposed mutable session history")
	}
}

func TestOpenSessionRejectsCanceledCaller(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := rt.OpenSession(ctx, SessionOptions{ID: "canceled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenSession with canceled caller = %v", err)
	}
	rt.mu.RLock()
	count := len(rt.sessions)
	rt.mu.RUnlock()
	if count != 0 {
		t.Fatal("canceled open retained a session")
	}
}

func TestExternalSessionContextIsBoundedByExtensionLifetime(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "external"})
	if err != nil {
		t.Fatal(err)
	}
	state := session.currentState()
	// Exercise lifetime propagation before Close performs any explicit cleanup.
	rt.cancel()
	select {
	case <-state.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("external caller detached the session from its extension lifetime")
	}
}

func TestSessionAdmissionRejectsCanceledContexts(t *testing.T) {
	for _, canceled := range []string{"caller", "session"} {
		t.Run(canceled, func(t *testing.T) {
			sessionCtx, stopSession := context.WithCancel(t.Context())
			defer stopSession()
			callerCtx, stopCaller := context.WithCancel(t.Context())
			defer stopCaller()
			if canceled == "caller" {
				stopCaller()
			} else {
				stopSession()
			}
			state := &sessionState{
				runtime: &Runtime{}, ctx: sessionCtx,
				ops: make(chan *sessionOperation, DefaultSessionPendingLimit),
			}
			operation := &sessionOperation{}
			if err := state.admit(callerCtx, operation); !errors.Is(err, context.Canceled) {
				t.Fatalf("admission with canceled %s = %v", canceled, err)
			}
			if len(state.ops) != 0 || state.pending != 0 {
				t.Fatal("rejected operation changed the queue")
			}
		})
	}
}

func TestSessionRotationOnlyRebindsExplicitHandle(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	session, err := rt.EnsureSession(SessionOptions{ID: "rotation"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := rt.EnsureSession(SessionOptions{ID: "rotation"})
	if err != nil {
		t.Fatal(err)
	}
	original := session.ID()
	if _, err := session.Command(t.Context(), "/clear"); err != nil {
		t.Fatal(err)
	}
	if session.currentState() == nil || session.ID() == original {
		t.Fatal("explicit rotation failed to update its handle")
	}
	if other.currentState() != nil || other.ID() != original {
		t.Fatal("rotation implicitly rebound another handle")
	}
}

func TestSessionAdmissionAndCancellationDrainEveryAcceptedOperation(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		ctx, cancel := context.WithCancel(t.Context())
		rt := &Runtime{}
		state := &sessionState{
			runtime: rt, ctx: ctx, cancel: cancel,
			ops: make(chan *sessionOperation, DefaultSessionPendingLimit), done: make(chan struct{}),
		}
		rt.wg.Add(1)
		go rt.runSession(state)
		var accepted, finished atomic.Int32
		var callers sync.WaitGroup
		start := make(chan struct{})
		for range 32 {
			callers.Add(1)
			go func() {
				defer callers.Done()
				<-start
				op := &sessionOperation{
					execute: func(context.Context) { finished.Add(1) },
					reject:  func(error) { finished.Add(1) },
				}
				if err := state.admit(t.Context(), op); err == nil {
					accepted.Add(1)
				}
			}()
		}
		close(start)
		cancel()
		callers.Wait()
		select {
		case <-state.done:
		case <-time.After(time.Second):
			t.Fatal("session did not finish draining")
		}
		rt.wg.Wait()
		if finished.Load() != accepted.Load() || state.pending != 0 || len(state.ops) != 0 {
			t.Fatalf("iteration %d: accepted=%d finished=%d pending=%d queued=%d",
				iteration, accepted.Load(), finished.Load(), state.pending, len(state.ops))
		}
	}
}

func TestSessionWithoutLoopDoesNotQueueInput(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	rt.agentConfig.Loop = nil
	session, err := rt.EnsureSession(SessionOptions{ID: "history-only"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(t.Context(), RunInput{Message: agent.TextInput("must not be queued")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err == nil || !strings.Contains(err.Error(), "agent loop is not configured") {
		t.Fatalf("run without loop = %v", err)
	}
	if len(session.MessagesSnapshot()) != 0 || session.currentState().inbox.Len() != 0 {
		t.Fatal("unavailable execution changed history or queued input")
	}
	if _, err := session.Command(t.Context(), "/status"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRunHasOneReliableTurnLifecycle(t *testing.T) {
	provider := &runtimeSemanticProvider{}
	rt := newBareRuntime(t, nil, provider)
	var all []*aop.Event
	unsubscribe := rt.Observe(func(event *aop.Event) { all = append(all, event) })
	defer unsubscribe.Cancel()

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
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Output != "done" || result.Stop != agent.StopReasonCompleted {
		t.Fatalf("completed result = %+v", result)
	}
	if again, err := run.Wait(); again != result || err != nil {
		t.Fatalf("second Wait() = %p, %v; want same result %p", again, err, result)
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
	unsubscribe := rt.Observe(func(event *aop.Event) { events <- proto.Clone(event).(*aop.Event) })
	defer unsubscribe.Cancel()

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
	result, err := run.Wait()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context canceled", err)
	}
	if result == nil || result.Stop != agent.StopReasonCanceled || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("canceled result = %+v", result)
	}
}

func TestCommandAddsAOPHistoryWithoutChangingTranscript(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	before := session.MessagesSnapshot()
	var commandEvent *aop.Event
	rt.Observe(func(event *aop.Event) {
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

// The hub keeps a session's transcript while the node that served it restarts,
// so a command result emitted afterwards must not reuse the id of one emitted
// before: the web transcript identifies messages by id, and a repeated id makes
// the newer result overwrite the older one instead of appearing beside it.
func TestCommandResultIDsSurviveNodeRestart(t *testing.T) {
	ids := make([]string, 0, 2)
	for _, marker := range []string{"BEFORE", "AFTER"} {
		rt := newBareRuntime(t, nil, nil)
		session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-restart"})
		if err != nil {
			t.Fatal(err)
		}
		var commandEvent *aop.Event
		rt.Observe(func(event *aop.Event) {
			if event.GetMessage() != nil && event.TurnId == "" {
				commandEvent = event
			}
		})
		if _, err := session.Command(context.Background(), "!printf "+marker); err != nil {
			t.Fatal(err)
		}
		if commandEvent == nil || commandEvent.GetMessage().GetId() == "" {
			t.Fatalf("command %s emitted no identified AOP message event: %+v", marker, commandEvent)
		}
		ids = append(ids, commandEvent.GetMessage().GetId())
	}
	if ids[0] == ids[1] {
		t.Fatalf("command result id %q was reused across runtimes", ids[0])
	}
}

func TestStatusReportsLLMAndToolHealth(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	if _, _, err := rt.providers.Reload(context.Background(), agent.ProviderConfig{
		Provider: "openai", Model: "gpt-test", BaseURL: srv.URL + "/v1", APIKey: "test",
		ContextWindow: 128000, MaxTokens: 8192, Timeout: 45,
	}, nil); err != nil {
		t.Fatal(err)
	}
	rt.agentConfig.Model = "gpt-test"
	rt.agentConfig.MaxTokens, rt.agentConfig.ContextWindow = 8192, 128000

	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-status", AgentName: "node-test"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Command(context.Background(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GetContent()) != 1 || result.GetContent()[0].GetText() == nil {
		t.Fatalf("status result = %+v", result)
	}
	text := result.GetContent()[0].GetText().GetText()
	for _, want := range []string{
		"Session: session-status",
		"Agent: node-test",
		"LLM probe: ready",
		"Provider: openai",
		"Model: gpt-test",
		"Limits: context=128000 · max_output=8192 · timeout=45s",
		"Tools: ready",
		"bash",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q:\n%s", want, text)
		}
	}
}

func TestActiveRunSteersAsyncInputWithoutSecondLifecycle(t *testing.T) {
	provider := &runtimeSemanticProvider{started: make(chan struct{}), release: make(chan struct{})}
	rt := newBareRuntime(t, nil, provider)
	var events []*aop.Event
	rt.Observe(func(event *aop.Event) { events = append(events, event) })
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
	if err := rt.Deliver(t.Context(), inbox.NewSystemMessage("steer now")); err != nil {
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
	_, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan *aop.Event, 1)
	rt.Observe(func(event *aop.Event) {
		if event.SessionId == "session-1" && event.GetTurnEnded() != nil {
			ended <- event
		}
	})
	if err := rt.Deliver(t.Context(), inbox.NewSystemMessage("automatic work")); err != nil {
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

func TestNilProviderRunDoesNotAutoRetry(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	var mu sync.Mutex
	var events []*aop.Event
	rt.Observe(func(event *aop.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{TurnID: "turn-1", Content: []*aop.Content{aop.Text("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err == nil || !strings.Contains(err.Error(), "provider is nil") {
		t.Fatalf("Wait() error = %v, want provider is nil", err)
	}
	if result == nil || result.Stop != agent.StopReasonError || result.Err != err {
		t.Fatalf("failed result = %+v, want error %v", result, err)
	}
	time.Sleep(50 * time.Millisecond)
	starts, ends := countSessionTurnLifecycle(&mu, &events, "session-1")
	if starts != 1 || ends != 1 {
		t.Fatalf("nil provider Run looped: starts=%d ends=%d", starts, ends)
	}
	if session.state.inbox.Len() != 0 {
		t.Fatalf("inbox len = %d, want 0", session.state.inbox.Len())
	}
}

func TestNilProviderIdlePushDoesNotLoop(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	var mu sync.Mutex
	var events []*aop.Event
	ended := make(chan struct{}, 1)
	rt.Observe(func(event *aop.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		if event.SessionId == "session-1" && event.GetTurnEnded() != nil {
			select {
			case ended <- struct{}{}:
			default:
			}
		}
	})
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.state.inbox.Push(inbox.NewSystemMessage("queued")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(200 * time.Millisecond):
	}
	time.Sleep(50 * time.Millisecond)
	starts, ends := countSessionTurnLifecycle(&mu, &events, "session-1")
	if starts > 1 || ends > 1 {
		t.Fatalf("nil provider idle push looped: starts=%d ends=%d", starts, ends)
	}
}

func countSessionTurnLifecycle(mu *sync.Mutex, events *[]*aop.Event, sessionID string) (starts, ends int) {
	mu.Lock()
	defer mu.Unlock()
	for _, event := range *events {
		if event.SessionId != sessionID {
			continue
		}
		if event.GetTurnStarted() != nil {
			starts++
		}
		if event.GetTurnEnded() != nil {
			ends++
		}
	}
	return starts, ends
}

func newBareRuntime(t *testing.T, values []coretool.Command, provider agent.Provider) *Runtime {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	reg := coretool.NewCommandRegistry()
	tools := coretool.NewToolRegistry()
	terminal := terminaltools.New(terminaltools.Config{Directory: t.TempDir(), Timeout: 5})
	var bash *terminaltool.BashTool
	borrow := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		bash, err = extension.Use[*terminaltool.BashTool](scope)
		return err
	}}
	entries := []extension.Extension{hosttest.Capabilities(), reg, tools, terminal, borrow}
	if len(values) > 0 {
		contributor := extension.Func{LoadFunc: func(scope *extension.Scope) error {
			return extension.Add(scope, values...)
		}}
		entries = append(entries, contributor)
	}
	terminalSet := hosttest.Set(t, entries...)
	if err := terminalSet.Load(ctx); err != nil {
		t.Fatal(err)
	}
	application := apptest.NewFixture(t, nil, nil)
	rt := &Runtime{
		history: JSONLHistory{}, primarySessionID: "main-repl", providers: application.Providers, events: application.Stream, Logger: application.Logger, ctx: ctx, cancel: cancel,
		commandRegistry: reg, tools: tools, shell: bash,
		sessions: make(map[string]*sessionState), runs: make(map[string]*Run),
		agentConfig: agent.Config{Loop: agent.StandardLoop{}, Provider: provider, Tools: tools, Bus: application.Stream, Logger: telemetry.NopLogger(), PromptResolver: defaultPromptResolver(t)},
		closeDone:   make(chan struct{}), loaded: true,
	}
	commandValues, commandIndex, err := commandDeclarations(nil)
	if err != nil {
		t.Fatal(err)
	}
	rt.commands, rt.commandIndex = commandValues, commandIndex
	t.Cleanup(func() {
		_ = terminalSet.Close(context.Background())
	})
	t.Cleanup(func() { _ = rt.close(context.Background()) })
	return rt
}

func TestRuntimeSessionDirectLoopUsesSessionScheduler(t *testing.T) {
	rt := newBareRuntime(t, []coretool.Command{looptool.NewCommand()}, nil)

	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Command(context.Background(), "!loop 10s check progress"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for session.state.scheduler.Active() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := session.state.scheduler.Active(); got != 1 {
		t.Fatalf("session scheduler active = %d, want 1", got)
	}
}

func TestRuntimeSessionRejectsRequestsPastPendingLimit(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	block := func(ctx context.Context) { <-ctx.Done() }
	for i := 0; i < DefaultSessionPendingLimit; i++ {
		op := &sessionOperation{
			execute: block,
			reject:  func(error) {},
		}
		if err := session.state.admit(context.Background(), op); err != nil {
			t.Fatalf("admit request %d: %v", i, err)
		}
	}
	op := &sessionOperation{execute: block, reject: func(error) {}}
	if err := session.state.admit(context.Background(), op); err == nil {
		t.Fatal("request past pending limit was admitted")
	} else if got := err.Error(); got == "" {
		t.Fatal(fmt.Errorf("empty overflow error"))
	}
}

func TestRotationCommandsRejectActiveRunWithoutSwitchingSession(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.jsonl")
	writePersistenceSession(t, target)
	provider := &runtimeSemanticProvider{started: make(chan struct{}), release: make(chan struct{})}
	runtime := newBareRuntime(t, nil, provider)
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "main-repl"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{Content: []*aop.Content{aop.Text("running")}})
	if err != nil {
		t.Fatal(err)
	}
	<-provider.started
	originalID := session.ID()
	for _, command := range []string{"/clear", "/compact"} {
		if _, err := session.rotateCommand(context.Background(), command); err == nil || !strings.Contains(err.Error(), "task is running") {
			t.Fatalf("%s error = %v", command, err)
		}
		if session.ID() != originalID {
			t.Fatalf("session switched during %s: %q -> %q", command, originalID, session.ID())
		}
	}
	if _, err := session.Resume(context.Background(), target); err == nil || !strings.Contains(err.Error(), "task is running") {
		t.Fatalf("Resume error = %v", err)
	}
	if session.ID() != originalID {
		t.Fatalf("session switched while active: %q -> %q", originalID, session.ID())
	}
	close(provider.release)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
}

// The web hub drops the CommandResult that a command returns to its caller —
// only messages published onto the AOP stream reach the chat. /compact used to
// return its no-op text without publishing it, so an operator with too little
// context to compact saw the command silently do nothing.
func TestCompactWithoutEnoughContextPublishesItsResult(t *testing.T) {
	runtime := newBareRuntime(t, nil, nil)
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	originalID := session.ID()

	var events []*aop.Event
	unsub := runtime.Observe(func(event *aop.Event) { events = append(events, event) })
	result, err := session.Command(context.Background(), "/compact")
	unsub.Cancel()
	if err != nil {
		t.Fatalf("/compact: %v", err)
	}
	if text := provider.MessageText(&aop.Message{Content: result.Content}); !strings.Contains(text, "Nothing to compact") {
		t.Fatalf("compact result = %#v", result)
	}
	if session.ID() != originalID {
		t.Fatalf("compact rotated with nothing to compact: %q -> %q", originalID, session.ID())
	}
	published := false
	for _, event := range events {
		if message := event.GetMessage(); message != nil && strings.Contains(provider.MessageText(message), "Nothing to compact") {
			published = true
		}
	}
	if !published {
		t.Fatal("nothing-to-compact result never reached the session stream")
	}
}
