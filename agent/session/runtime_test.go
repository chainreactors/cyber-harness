package session

import (
	"context"
	"errors"
	"github.com/chainreactors/cyber/cmd/harness"
	"github.com/chainreactors/cyber/core/extension"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/tmux"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	coreevents "github.com/chainreactors/cyber/core/events"
	coreoutput "github.com/chainreactors/cyber/core/output"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/toolset"
	types "github.com/chainreactors/cyber/pkg/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type lifecycleLoop func(context.Context, agent.Config) (*agent.Result, error)

func TestLoopPanicCompletesRunAndLeavesSessionDrainable(t *testing.T) {
	runtime := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	owner := newLoopExtension(lifecycleLoop(func(context.Context, agent.Config) (*agent.Result, error) {
		panic("test loop failure")
	}))
	set := harness.Set(t, extension.Entry{ID: "agent", Extension: owner})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime.config.Loop = owner.Runtime()
	session, err := runtime.EnsureSession(SessionOptions{ID: "panic"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(t.Context(), RunInput{Message: agent.TextInput("test")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.done:
	case <-time.After(time.Second):
		t.Fatal("panicking loop stranded the Run")
	}
	result, err := run.Wait()
	if err == nil || !strings.Contains(err.Error(), "test loop failure") || result == nil || result.Stop != agent.StopReasonError {
		t.Fatalf("panic result: %v, %v", result, err)
	}
	if _, err := session.Command(t.Context(), "/status"); err != nil {
		t.Fatalf("queue stopped: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := runtime.CloseSession(ctx, "panic", SessionCloseError); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOneExtensionDrainsSessionsAndDirectLoopCalls(t *testing.T) {
	application := newTestApp(t, apppkg.Config{SkipEngines: true}, apppkg.AppServices{})
	application.App.SetProvider(&runtimeSemanticProvider{}, agent.ProviderConfig{Model: "test-model"})
	started, canceled := make(chan struct{}, 2), make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	owner, err := New(Config{Application: testEnvironment(application.App), Option: &cfg.Option{}, Loop: lifecycleLoop(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		<-release
		return nil, ctx.Err()
	})})
	if err != nil {
		t.Fatal(err)
	}
	entries := harness.AppEntries(t, application)
	var dependencyClosed atomic.Bool
	entries = append(entries, extension.Entry{ID: "dependency", Extension: extension.Func{CloseFunc: func(context.Context) error {
		dependencyClosed.Store(true)
		return nil
	}}}, extension.Entry{ID: "agent", DependsOn: []string{"application.tool-registry", "dependency"}, Extension: owner})
	set := harness.Set(t, entries...)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	init, cancelInit := context.WithCancel(t.Context())
	if err := set.Load(init); err != nil {
		t.Fatal(err)
	}
	cancelInit()
	runtime := owner.Runtime()
	runtime.config.Provider = &runtimeSemanticProvider{}
	session, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "owned"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(t.Context(), RunInput{Message: agent.TextInput("generic harness test")})
	if err != nil {
		t.Fatal(err)
	}
	direct := make(chan error, 1)
	go func() { _, err := runtime.Run(t.Context(), agent.Config{SessionID: "direct"}); direct <- err }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("execution did not start")
		}
	}
	deadline, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := set.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close: %v", err)
	}
	for range 2 {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("execution was not canceled")
		}
	}
	if dependencyClosed.Load() {
		t.Fatal("dependency released before drain")
	}
	if _, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "late"}); err == nil {
		t.Fatal("session admitted during close")
	}
	if _, err := runtime.Run(t.Context(), agent.Config{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("loop admitted during close: %v", err)
	}
	once.Do(func() { close(release) })
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("session result: %v", err)
	}
	if err := <-direct; !errors.Is(err, context.Canceled) {
		t.Fatalf("direct result: %v", err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !dependencyClosed.Load() {
		t.Fatal("dependency not released after drain")
	}
}

func (f lifecycleLoop) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	return f(ctx, config)
}

func TestCloseSessionTimeoutRetainsInstanceUntilCleanup(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	runtime := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	managed := newLoopExtension(lifecycleLoop(func(ctx context.Context, _ agent.Config) (*agent.Result, error) {
		close(started)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}))
	set := harness.Set(t, extension.Entry{ID: "agent", Extension: managed})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	runtime.config.Loop = managed.Runtime()
	var ended atomic.Int32
	sub := runtime.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if event.SessionId == "closing" && event.GetSessionEnded() != nil {
			ended.Add(1)
		}
	}))
	defer sub.Cancel()
	session, err := runtime.EnsureSession(SessionOptions{ID: "closing"})
	if err != nil {
		t.Fatal(err)
	}
	state := session.currentState()
	run, err := session.Run(t.Context(), RunInput{Message: agent.TextInput("local lifecycle test")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := runtime.CloseSession(deadline, "closing", SessionCloseCanceled); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close deadline: %v", err)
	}
	if session.currentState() != state || state.inbox.Closed() || ended.Load() != 0 {
		t.Fatal("incomplete close discarded state or reported completion")
	}
	if _, err := runtime.EnsureSession(SessionOptions{ID: "closing"}); err == nil {
		t.Fatal("reconnected to a closing session")
	}
	if _, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "closing"}); err == nil {
		t.Fatal("reused a session ID before its old owner finished")
	}
	unblock.Do(func() { close(release) })
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("run result: %v", err)
	}
	if err := runtime.CloseSession(t.Context(), "closing", SessionCloseCompleted); err != nil {
		t.Fatalf("close retry: %v", err)
	}
	if session.currentState() != nil || !state.inbox.Closed() || ended.Load() != 1 {
		t.Fatal("close retry did not release state and emit one completion")
	}
	if state.closeReason != SessionCloseCanceled {
		t.Fatal("close retry changed the original reason")
	}
}

func TestCloseSessionDeadlineBoundsFinalEventDelivery(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	session, err := rt.EnsureSession(SessionOptions{ID: "final-event"})
	if err != nil {
		t.Fatal(err)
	}
	state := session.currentState()
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	var deliveries atomic.Int32
	subscription := rt.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if event.SessionId == "final-event" && event.GetSessionEnded() != nil {
			deliveries.Add(1)
			close(entered)
			<-release
		}
	}))
	t.Cleanup(func() {
		unblock.Do(func() { close(release) })
		subscription.Cancel()
	})
	deadline, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- rt.CloseSession(deadline, "final-event", SessionCloseCompleted) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("final event was not published")
	}
	select {
	case err := <-closed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close during final event delivery = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked observer ignored the close deadline")
	}
	if session.currentState() != state {
		t.Fatal("released session identity before final event delivery finished")
	}
	if _, err := rt.OpenSession(t.Context(), SessionOptions{ID: "final-event"}); err == nil {
		t.Fatal("reused identity before final event delivery finished")
	}
	unblock.Do(func() { close(release) })
	if err := rt.CloseSession(t.Context(), "final-event", SessionCloseCanceled); err != nil {
		t.Fatal(err)
	}
	if err := rt.CloseSession(t.Context(), "final-event", SessionCloseCanceled); err != nil {
		t.Fatalf("completed close is not idempotent: %v", err)
	}
	if session.currentState() != nil || deliveries.Load() != 1 || state.closeReason != SessionCloseCompleted {
		t.Fatal("close retry did not preserve the original completion")
	}
}

func TestRuntimeCloseCompletesDespiteObserverFailure(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	session, err := rt.EnsureSession(SessionOptions{ID: "observer-failure"})
	if err != nil {
		t.Fatal(err)
	}
	state := session.currentState()
	subscription := rt.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if event.GetSessionEnded() != nil {
			panic("test observer failure")
		}
	}))
	defer subscription.Cancel()
	var completions atomic.Int32
	healthy := rt.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if event.GetSessionEnded() != nil {
			completions.Add(1)
		}
	}))
	defer healthy.Cancel()
	if err := rt.Close(t.Context()); err != nil {
		t.Fatalf("runtime close = %v", err)
	}
	if session.currentState() != nil || !state.inbox.Closed() {
		t.Fatal("observer failure prevented resource cleanup")
	}
	if err := rt.Close(t.Context()); err != nil {
		t.Fatalf("repeated runtime close = %v", err)
	}
	if completions.Load() != 1 {
		t.Fatalf("healthy observer received %d completion events, want 1", completions.Load())
	}
}

func TestManagerCloseCancelsAllExternallyParentedSessionsBeforeWaiting(t *testing.T) {
	started, canceled := make(chan string, 2), make(chan string, 2)
	release := make(chan struct{})
	var unblock sync.Once
	runtime := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	managed := newLoopExtension(lifecycleLoop(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		started <- config.SessionID
		<-ctx.Done()
		canceled <- config.SessionID
		<-release
		return nil, ctx.Err()
	}))
	set := harness.Set(t, extension.Entry{ID: "agent", Extension: managed})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	runtime.config.Loop = managed.Runtime()
	var runs []*Run
	for _, id := range []string{"first", "second"} {
		session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		run, err := session.Run(t.Context(), RunInput{Message: agent.TextInput("local lifecycle test")})
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("both sessions did not start")
		}
	}
	deadline, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := runtime.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close while loops are still draining: %v", err)
	}
	for range 2 {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("waiting for one session prevented cancellation of another")
		}
	}
	unblock.Do(func() { close(release) })
	for _, run := range runs {
		if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatalf("run result: %v", err)
		}
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type persistenceProvider struct {
	requests []*provider.ChatCompletionRequest
}

type lifecycleOutput struct {
	mu    sync.Mutex
	kinds []string
}

func loadTestApplication(t *testing.T, application *apppkg.Resource) *extension.Set {
	return harness.AppLoad(t, t.Context(), application)
}

func TestNewRuntimeIsInertUntilLoad(t *testing.T) {
	a := newTestApp(t, apppkg.Config{SkipEngines: true}, apppkg.AppServices{})
	rt, err := New(Config{Application: testEnvironment(a.App), Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Loop: agent.StandardLoop{}})
	if err != nil {
		t.Fatal(err)
	}
	if rt.Runtime().Context() != nil {
		t.Fatal("New created a runtime lifetime before Load")
	}
	if _, err := rt.Runtime().OpenSession(t.Context(), SessionOptions{ID: "too-early"}); err == nil {
		t.Fatal("runtime admitted a session before Load")
	}
	if err := rt.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCloseCanResumeWaitingAfterContextCancellation(t *testing.T) {
	rt := &Runtime{
		loaded: true, closeDone: make(chan struct{}),
		sessions: make(map[string]*sessionState), runs: make(map[string]*Run),
	}
	rt.wg.Add(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := rt.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close error = %v, want context cancellation", err)
	}
	select {
	case <-rt.closeDone:
		t.Fatal("Close released the runtime while owned work remained")
	default:
	}
	rt.wg.Done()
	if err := rt.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (o *lifecycleOutput) HandleEvent(event *aop.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.kinds = append(o.kinds, aop.Kind(event))
}

func (o *lifecycleOutput) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.kinds...)
}

func TestRuntimeCloseKeepsSharedTerminalManager(t *testing.T) {
	appResource := newTestApp(t, apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()}, apppkg.AppServices{})
	app := appResource.App
	terminal, err := terminalext.New(app.Hooks, app.Tools.(*toolset.Registry), app.Commands, terminalext.Config{
		Directory: t.TempDir(), Timeout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	app.Bash = terminal.Bash()
	entries := harness.AppEntries(t, appResource)
	entries[1].Extension = terminal
	appSet := harness.Set(t, entries...)
	if err := appSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	bash := app.Bash
	t.Cleanup(func() { _ = appSet.Close(context.Background()) })
	output := new(lifecycleOutput)
	rt, err := New(Config{Application: testEnvironment(app), Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Loop: agent.StandardLoop{}})
	if err != nil {
		t.Fatal(err)
	}

	rtSet := harness.Set(t, extension.Entry{ID: "rt", Extension: rt})
	if err := rtSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rtSet.Close(context.Background()) })
	unsubscribe := rt.Runtime().Observe(coreevents.ObserverFunc(output.HandleEvent))
	defer unsubscribe.Cancel()
	if _, err := rt.Runtime().OpenSession(context.Background(), SessionOptions{ID: "owned-session"}); err != nil {
		t.Fatal(err)
	}

	// This work belongs to the terminal extension, not the Runtime or App.
	// No shell or subprocess is used.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	info, err := bash.Manager().CreateFunc(ctx, "app-work", 0, func(ctx context.Context, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("App work did not start")
	}

	_ = rtSet.Close(context.Background())
	unsubscribe.Cancel()
	seen := output.snapshot()
	if len(seen) == 0 || seen[len(seen)-1] != "session.ended" {
		t.Fatalf("output detached before session end: %v", seen)
	}
	if current, ok := bash.Manager().Get(info.ID); !ok || current.State != tmux.StateRunning {
		t.Fatalf("Runtime closed App-owned work: %+v, found=%v", current, ok)
	}

	_ = rtSet.Close(context.Background())
	app.Publish(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Role: "user"}}})
	if got := output.snapshot(); len(got) != len(seen) {
		t.Fatalf("closed Runtime still receives App events: before=%v after=%v", seen, got)
	}

	_ = appSet.Close(context.Background())
	if current, ok := bash.Manager().Get(info.ID); ok && current.State == tmux.StateRunning {
		t.Fatalf("terminal extension failed to stop owned work: %+v", current)
	}
}

func (p *persistenceProvider) Name() string { return "persistence" }

func (p *persistenceProvider) ChatCompletion(_ context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.requests = append(p.requests, request)
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "persisted response")}},
	}, nil
}

func TestFileFlagPersistsOneCanonicalAOPStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit.jsonl")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}
	provider := new(persistenceProvider)
	_, runtime, output := newPersistenceRuntime(t, option, provider)

	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "task"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{Content: []*aop.Content{aop.Text("persist this")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CloseSession(context.Background(), "task", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	flushPersistenceOutput(t, output)

	events, err := coreoutput.ReadJSONL(path)
	if err != nil {
		t.Fatalf("ReadJSONL: %v", err)
	}
	counts := map[string]int{}
	for _, event := range events {
		if event.SessionId == "" || event.Payload == nil {
			t.Fatalf("invalid AOP event: %#v", event)
		}
		counts[aop.Kind(event)]++
	}
	for _, kind := range []string{"session.started", "turn.started", "message", "turn.ended", "session.ended"} {
		if counts[kind] == 0 {
			t.Fatalf("missing %s in %#v", kind, counts)
		}
	}
	if counts["message"] != 2 {
		t.Fatalf("message count = %d, want user + assistant", counts["message"])
	}
}

func TestResumeRestoresWithoutMutatingSource(t *testing.T) {
	dir := t.TempDir()
	resumePath := filepath.Join(dir, "resume.jsonl")
	writePersistenceSession(t, resumePath)
	baseEvents, err := coreoutput.ReadJSONL(resumePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("resume source stays immutable", func(t *testing.T) {
		option := &cfg.Option{}
		option.Resume = resumePath
		provider := new(persistenceProvider)
		_, runtime, _ := newPersistenceRuntime(t, option, provider)
		runResumedTurn(t, runtime, "continued prompt")

		if len(provider.requests) != 1 {
			t.Fatalf("provider requests = %d", len(provider.requests))
		}
		requestText := persistenceRequestText(provider.requests[0])
		for _, expected := range []string{"old user", "old assistant", "continued prompt"} {
			if !strings.Contains(requestText, expected) {
				t.Fatalf("resumed request missing %q:\n%s", expected, requestText)
			}
		}
		events, err := coreoutput.ReadJSONL(resumePath)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != len(baseEvents) {
			t.Fatalf("resume implicitly changed its source: before=%d after=%d", len(baseEvents), len(events))
		}
	})
}

func TestContinuationReferencesHistoryWithoutReemittingLargeMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "continuation.jsonl")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}
	provider := new(persistenceProvider)
	_, runtime, output := newPersistenceRuntimeWithMode(t, option, provider, true)

	root, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "main-repl"})
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", 4<<20)
	oldID := root.ID()
	runtime.app.Publish(&aop.Event{
		SessionId: root.ID(), TurnId: "turn-1", Emitter: "cyber",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text(large)}}},
	})
	flushPersistenceOutput(t, output)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := root.rotate(context.Background(), SessionCloseResumed, root.ID(), root.MessagesSnapshot()); err != nil {
		t.Fatal(err)
	}
	flushPersistenceOutput(t, output)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if growth := after.Size() - before.Size(); growth > 64<<10 {
		t.Fatalf("continuation appended %d bytes for inherited history", growth)
	}

	events, err := coreoutput.ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	childID := root.ID()
	if childID == oldID {
		t.Fatalf("rotation did not create a child session: %q", childID)
	}
	for _, event := range events {
		if event.SessionId == childID && (event.GetMessage() != nil || event.GetToolResult() != nil) {
			t.Fatalf("inherited history was re-emitted in child stream: %s", aop.Kind(event))
		}
	}

	data, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Messages) != 1 || data.Messages[0].Content[0].GetText().GetText() != large {
		t.Fatalf("resumed inherited history = %d messages, want the original large message", len(data.Messages))
	}
}

func TestREPLResumeLoadsMainSessionContext(t *testing.T) {
	resumePath := filepath.Join(t.TempDir(), "repl-resume.jsonl")
	writePersistenceSessionForID(t, resumePath, "main-repl")
	option := &cfg.Option{}
	option.Resume = resumePath
	provider := new(persistenceProvider)
	_, runtime, _ := newPersistenceRuntimeWithMode(t, option, provider, true)

	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "main-repl"})
	if err != nil {
		t.Fatal(err)
	}
	messages := session.MessagesSnapshot()
	if len(messages) != 2 {
		t.Fatalf("REPL resumed messages = %d, want 2", len(messages))
	}
	text := persistenceMessagesText(messages)
	if !strings.Contains(text, "old user") || !strings.Contains(text, "old assistant") {
		t.Fatalf("REPL context = %q", text)
	}
	if err := runtime.CloseSession(context.Background(), "main-repl", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
}

func TestClearRotatesToAnEmptyContinuationSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clear.jsonl")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}
	provider := new(persistenceProvider)
	_, runtime, output := newPersistenceRuntimeWithMode(t, option, provider, true)
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "main-repl"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{Content: []*aop.Content{aop.Text("before clear")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if runtime.sessionRunActive(session.ID()) {
		t.Fatal("Run.Wait returned before the active run registration was released")
	}
	oldID := session.ID()

	var events []*aop.Event
	unsub := runtime.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events = append(events, event) }))
	result, err := session.Command(context.Background(), "/clear")
	unsub.Cancel()
	if err != nil {
		t.Fatalf("/clear: %v", err)
	}
	if text := persistenceMessagesText([]*aop.Message{{Content: result.Content}}); !strings.Contains(text, "Context cleared") {
		t.Fatalf("clear result = %#v", result)
	}
	newID := session.ID()
	if newID == "" || newID == oldID {
		t.Fatalf("clear session id = %q, old = %q", newID, oldID)
	}
	if messages := session.MessagesSnapshot(); len(messages) != 0 {
		t.Fatalf("new clear context has %d messages", len(messages))
	}
	assertRotationEvents(t, events, oldID, newID, string(SessionCloseCleared))

	if err := runtime.CloseSession(context.Background(), "main-repl", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	flushPersistenceOutput(t, output)
	data, err := ReadHistory(path)
	if err != nil {
		t.Fatalf("LoadSession after clear: %v", err)
	}
	if data.SessionID != newID || len(data.Messages) != 0 {
		t.Fatalf("clear resume data = %#v", data)
	}
}

func TestCompactRotatesAndPersistsOnlyCompactedContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compact.jsonl")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}
	provider := new(persistenceProvider)
	_, runtime, output := newPersistenceRuntimeWithMode(t, option, provider, true)
	runtime.config.Compaction = agent.CompactionSettings{KeepRecentTokens: 20, ReserveTokens: 64}
	long := strings.Repeat("history ", 120)
	messages := []*aop.Message{
		agent.TextMessage("user", long+"one"),
		agent.TextMessage("assistant", long+"two"),
		agent.TextMessage("user", long+"three"),
		agent.TextMessage("assistant", "recent answer"),
	}
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "main-repl", Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	oldID := session.ID()
	var events []*aop.Event
	unsub := runtime.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events = append(events, event) }))
	if _, err := session.Command(context.Background(), "/compact focus on findings"); err != nil {
		unsub.Cancel()
		t.Fatalf("/compact: %v", err)
	}
	unsub.Cancel()
	newID := session.ID()
	if newID == oldID {
		t.Fatal("compact did not rotate the session")
	}
	compacted := session.MessagesSnapshot()
	if len(compacted) == 0 || len(compacted) >= len(messages) {
		t.Fatalf("compacted messages = %d, original = %d", len(compacted), len(messages))
	}
	assertRotationEvents(t, events, oldID, newID, string(SessionCloseCompacted))

	if err := runtime.CloseSession(context.Background(), "main-repl", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	flushPersistenceOutput(t, output)
	data, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if data.SessionID != newID || len(data.Messages) != len(compacted) {
		t.Fatalf("compacted resume data = %#v, want %d messages", data, len(compacted))
	}
	if strings.Contains(persistenceMessagesText(data.Messages), long+"one") {
		t.Fatal("compacted resume context retained discarded history")
	}
}

func TestInteractiveResumeReadsSelectedContextWithoutSwitchingOutput(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "current.jsonl")
	resumePath := filepath.Join(dir, "selected.jsonl")
	writePersistenceSessionForID(t, resumePath, "selected-main")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: currentPath}}
	provider := new(persistenceProvider)
	_, runtime, output := newPersistenceRuntimeWithMode(t, option, provider, true)
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "main-repl"})
	if err != nil {
		t.Fatal(err)
	}
	oldID := session.ID()
	count, err := session.Resume(context.Background(), resumePath)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if count != 2 {
		t.Fatalf("resumed messages = %d", count)
	}
	newID := session.ID()
	if newID == oldID {
		t.Fatal("interactive resume did not rotate the session")
	}
	if text := persistenceMessagesText(session.MessagesSnapshot()); !strings.Contains(text, "old user") || !strings.Contains(text, "old assistant") {
		t.Fatalf("resumed context = %q", text)
	}
	state := session.currentState()
	if state.parentSessionID != "selected-main" || state.parentToolCallID != "" {
		t.Fatalf("resumed continuation parent = %q/%q", state.parentSessionID, state.parentToolCallID)
	}

	if err := runtime.CloseSession(context.Background(), "main-repl", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	flushPersistenceOutput(t, output)
	data, err := ReadHistory(resumePath)
	if err != nil {
		t.Fatal(err)
	}
	if data.SessionID != "selected-main" || len(data.Messages) != 2 {
		t.Fatalf("resume source was modified: %#v", data)
	}
	currentEvents, err := coreoutput.ReadJSONL(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(currentEvents) == 0 || currentEvents[len(currentEvents)-1].GetSessionEnded().GetReason() != string(SessionCloseCompleted) {
		t.Fatalf("explicit output did not remain active after resume: %#v", currentEvents)
	}
}

func assertRotationEvents(t *testing.T, events []*aop.Event, oldID, newID, reason string) {
	t.Helper()
	var ended, started bool
	for _, event := range events {
		if event.SessionId == oldID && event.GetSessionEnded().GetReason() == reason {
			ended = true
		}
		if event.SessionId == newID && event.GetSessionStarted().GetParentSessionId() == oldID && event.GetSessionStarted().GetParentToolCallId() == "" {
			started = true
		}
	}
	if !ended || !started {
		t.Fatalf("rotation events ended=%v started=%v events=%#v", ended, started, events)
	}
}

func newPersistenceRuntime(t *testing.T, option *cfg.Option, llm *persistenceProvider) (*apppkg.App, *Runtime, *telemetryext.Extension) {
	return newPersistenceRuntimeWithMode(t, option, llm, false)
}

func newPersistenceRuntimeWithMode(t *testing.T, option *cfg.Option, llm *persistenceProvider, interactive bool) (*apppkg.App, *Runtime, *telemetryext.Extension) {
	t.Helper()
	stream := coreevents.New()
	appResource := newTestApp(t, apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()}, apppkg.AppServices{Events: stream})
	app := appResource.App
	var dependencies []string
	var entries []extension.Entry
	var output *telemetryext.Extension
	if option.OutputFile != "" {
		var outputErr error
		output, outputErr = telemetryext.New(stream, telemetryext.Options{Path: option.OutputFile})
		if outputErr != nil {
			t.Fatal(outputErr)
		}
		entries = append(entries, extension.Entry{ID: "output", Extension: output})
		dependencies = append(dependencies, "output")
	}
	applicationEntries := harness.AppEntries(t, appResource, dependencies...)
	entries = append(entries, applicationEntries...)
	appSet := harness.Set(t, entries...)
	if err := appSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.SetProvider(llm, agent.ProviderConfig{Provider: llm.Name(), Model: "test-model", MaxTokens: 128, ContextWindow: 128000})
	primary := "task"
	if interactive {
		primary = "main-repl"
	}
	runtimeResource, err := New(Config{Application: testEnvironment(app), Option: option, Logger: telemetry.NopLogger(), PrimarySessionID: primary, Loop: agent.StandardLoop{}})
	if err != nil {
		_ = appSet.Close(context.Background())
		t.Fatal(err)
	}

	runtimeSet := harness.Set(t, extension.Entry{ID: "runtime", Extension: runtimeResource})
	if err := runtimeSet.Load(t.Context()); err != nil {
		_ = appSet.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtimeSet.Close(context.Background())
		_ = appSet.Close(context.Background())
	})
	return app, runtimeResource.Runtime(), output
}

func flushPersistenceOutput(t *testing.T, output *telemetryext.Extension) {
	t.Helper()
	if output != nil {
		if err := output.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func runResumedTurn(t *testing.T, runtime *Runtime, prompt string) {
	t.Helper()
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "task", Messages: runtime.resumeMessages})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{Content: []*aop.Content{aop.Text(prompt)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CloseSession(context.Background(), "task", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
}

func writePersistenceSession(t *testing.T, path string) {
	writePersistenceSessionForID(t, path, "task")
}

func writePersistenceSessionForID(t *testing.T, path, sessionID string) {
	t.Helper()
	timestamp := timestamppb.New(time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC))
	events := []*aop.Event{
		{Id: "e-1", EmittedAt: timestamp, SessionId: sessionID, Emitter: "cyber", Seq: 1, Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: "test-model"}}},
		{Id: "e-2", EmittedAt: timestamp, SessionId: sessionID, TurnId: "old-turn", Emitter: "cyber", Seq: 2, Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("old user")}}}},
		{Id: "e-3", EmittedAt: timestamp, SessionId: sessionID, TurnId: "old-turn", Emitter: "cyber", Seq: 3, Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-2", Role: "assistant", Content: []*aop.Content{aop.Text("old assistant")}}}},
		{Id: "e-4", EmittedAt: timestamp, SessionId: sessionID, Emitter: "cyber", Seq: 4, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: "completed"}}},
	}
	_ = types.SetSessionHistory(events[0], &types.SessionHistory{Mode: types.SessionHistory_MODE_INHERIT})
	bus := coreevents.New()
	writer, err := telemetryext.New(bus, telemetryext.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := loadtelemetry(t, writer); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		bus.Publish(event)
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func persistenceRequestText(request *provider.ChatCompletionRequest) string {
	if request == nil {
		return ""
	}
	return persistenceMessagesText(request.Messages)
}

func persistenceMessagesText(messages []*aop.Message) string {
	var parts []string
	for _, message := range messages {
		parts = append(parts, provider.MessageText(message))
	}
	return strings.Join(parts, "\n")
}

func TestRuntimesShareOneAppEventSequenceAndOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.jsonl")
	bus := coreevents.New()
	output, err := telemetryext.New(bus, telemetryext.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, apppkg.Config{SkipEngines: true}, apppkg.AppServices{Events: bus})
	applicationEntries := harness.AppEntries(t, a, "output")
	aSet := harness.Set(t, append([]extension.Entry{{ID: "output", Extension: output}}, applicationEntries...)...)
	if err := aSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer aSet.Close(context.Background())
	var mu sync.Mutex
	var events []*aop.Event
	unsubscribe := a.App.ObserveEvents(coreevents.ObserverFunc(func(event *aop.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}))
	defer unsubscribe.Cancel()
	var runtimes []*Extension
	for range 2 {
		rt, err := New(Config{Application: testEnvironment(a.App), Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Loop: agent.StandardLoop{}})
		if err != nil {
			t.Fatal(err)
		}

		rtSet := harness.Set(t, extension.Entry{ID: "rt", Extension: rt})
		if err := rtSet.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer rtSet.Close(context.Background())
		runtimes = append(runtimes, rt)
		if _, err := rt.Runtime().OpenSession(context.Background(), SessionOptions{ID: "shared"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = runtimes[0].Close(context.Background())
	session, err := runtimes[1].Runtime().EnsureSession(SessionOptions{ID: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Command(context.Background(), "/status"); err != nil {
		t.Fatalf("closing sibling runtime broke shared App: %v", err)
	}
	_ = runtimes[1].Close(context.Background())
	if output.Path() == "" {
		t.Fatal("session runtime closed application output")
	}
	last := &aop.Event{SessionId: "shared", Id: "after-runtimes"}
	a.App.Publish(last)
	mu.Lock()
	defer mu.Unlock()
	var sequence uint64
	for _, event := range events {
		if event.SessionId == "shared" {
			sequence++
			if event.Seq != sequence {
				t.Fatalf("shared sequence restarted: got %d, want %d", event.Seq, sequence)
			}
		}
	}
	if sequence < 5 || events[len(events)-1] != last {
		t.Fatal("missing shared lifecycle events or replaced event object")
	}
}

func TestProviderSwapKeepsInFlightSnapshotAndUpdatesExistingSession(t *testing.T) {
	old := &runtimeSemanticProvider{started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(old.release) })
	rt := newBareRuntime(t, nil, old)
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "provider-swap"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(context.Background(), RunInput{Content: []*aop.Content{aop.Text("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.started:
	case <-time.After(time.Second):
		t.Fatal("inert provider did not start")
	}
	next := &runtimeSemanticProvider{}
	rt.SetProvider(next, agent.ProviderConfig{Model: "new", MaxTokens: 1024, ContextWindow: 8192})
	release.Do(func() { close(old.release) })
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if next.callCount() != 0 {
		t.Fatal("in-flight run switched provider")
	}
	run, err = session.Run(context.Background(), RunInput{Content: []*aop.Content{aop.Text("again")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	provider, config := rt.app.ProviderState()
	if next.callCount() != 1 || provider != next || config.Model != "new" {
		t.Fatal("provider change did not reach App and existing session")
	}
}

func newLoopExtension(loop agent.Loop) *Extension {
	value, err := New(Config{Loop: loop})
	if err != nil {
		panic(err)
	}
	return value
}
