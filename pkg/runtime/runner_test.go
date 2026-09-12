package runtime

import (
	"context"
	"errors"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/agent/tmux"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type persistenceProvider struct {
	requests []*provider.ChatCompletionRequest
}

type lifecycleOutput struct {
	mu    sync.Mutex
	kinds []string
}

func TestNewRuntimeIsInertUntilLoad(t *testing.T) {
	a := apppkg.New(apppkg.Config{SkipEngines: true}, nil, nil)
	rt, err := New(a, nil, &cfg.Option{}, telemetry.NopLogger(), RuntimeConfig{Loop: agent.StandardLoop{}})
	if err != nil {
		t.Fatal(err)
	}
	if rt.Context() != nil {
		t.Fatal("New created a runtime lifetime before Load")
	}
	if _, err := rt.OpenSession(t.Context(), SessionOptions{ID: "too-early"}); err == nil {
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
	rt := &AgentRuntime{
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

func TestRuntimeCloseKeepsBorrowedManager(t *testing.T) {
	app := apppkg.New(apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()}, nil, nil)

	appSet := extensiontest.Set(t, extension.Entry{ID: "app", Extension: app})
	if err := appSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	bash := app.Bash
	t.Cleanup(func() { _ = appSet.Close(context.Background()) })
	output := new(lifecycleOutput)
	rt, err := New(app, nil, &cfg.Option{}, telemetry.NopLogger(), RuntimeConfig{Loop: agent.StandardLoop{}})
	if err != nil {
		t.Fatal(err)
	}

	rtSet := extensiontest.Set(t, extension.Entry{ID: "rt", Extension: rt})
	if err := rtSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rtSet.Close(context.Background()) })
	unsubscribe := rt.Subscribe(output.HandleEvent)
	defer unsubscribe.Cancel()
	if _, err := rt.OpenSession(context.Background(), SessionOptions{ID: "owned-session"}); err != nil {
		t.Fatal(err)
	}

	// This work belongs to the App, not the Runtime. No shell or subprocess is used.
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
	app.EventBus.Emit(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Role: "user"}}})
	if got := output.snapshot(); len(got) != len(seen) {
		t.Fatalf("closed Runtime still receives App events: before=%v after=%v", seen, got)
	}

	_ = appSet.Close(context.Background())
	if current, ok := bash.Manager().Get(info.ID); ok && current.State == tmux.StateRunning {
		t.Fatalf("App failed to stop owned work: %+v", current)
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
	app, runtime := newPersistenceRuntime(t, option, provider)

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
	_ = runtime.Close(context.Background())
	_ = app.Close(context.Background())

	events, err := output.ReadJSONL(path)
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

func TestResumeRestoresAndAppendsAOPStream(t *testing.T) {
	dir := t.TempDir()
	resumePath := filepath.Join(dir, "resume.jsonl")
	writePersistenceSession(t, resumePath)
	baseEvents, err := output.ReadJSONL(resumePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("append same file", func(t *testing.T) {
		option := &cfg.Option{}
		option.Resume = resumePath
		provider := new(persistenceProvider)
		app, runtime := newPersistenceRuntime(t, option, provider)
		runResumedTurn(t, runtime, "continued prompt")
		_ = runtime.Close(context.Background())
		_ = app.Close(context.Background())

		if len(provider.requests) != 1 {
			t.Fatalf("provider requests = %d", len(provider.requests))
		}
		requestText := persistenceRequestText(provider.requests[0])
		for _, expected := range []string{"old user", "old assistant", "continued prompt"} {
			if !strings.Contains(requestText, expected) {
				t.Fatalf("resumed request missing %q:\n%s", expected, requestText)
			}
		}
		events, err := output.ReadJSONL(resumePath)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) <= len(baseEvents) {
			t.Fatalf("resume did not append: before=%d after=%d", len(baseEvents), len(events))
		}
		data, err := ReadHistory(resumePath)
		if err != nil {
			t.Fatal(err)
		}
		if data.MessageCounter < 4 || len(data.Messages) != 4 {
			t.Fatalf("resumed session = %#v", data)
		}
	})
}

func TestContinuationReferencesHistoryWithoutReemittingLargeMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "continuation.jsonl")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}
	provider := new(persistenceProvider)
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, true)
	_ = app

	root, err := runtime.OpenSession(context.Background(), SessionOptions{ID: "main-repl"})
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", 4<<20)
	oldID := root.ID()
	runtime.app.Emit(&aop.Event{
		SessionId: root.ID(), TurnId: "turn-1", Emitter: "aiscan",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text(large)}}},
	})
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := root.rotate(context.Background(), SessionCloseResumed, root.ID(), root.MessagesSnapshot(), ""); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if growth := after.Size() - before.Size(); growth > 64<<10 {
		t.Fatalf("continuation appended %d bytes for inherited history", growth)
	}

	events, err := output.ReadJSONL(path)
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
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, true)

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
	_ = runtime.Close(context.Background())
	_ = app.Close(context.Background())
}

func TestClearRotatesToAnEmptyContinuationSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clear.jsonl")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}
	provider := new(persistenceProvider)
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, true)
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
	unsub := runtime.Subscribe(func(event *aop.Event) { events = append(events, event) })
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
	_ = runtime.Close(context.Background())
	_ = app.Close(context.Background())
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
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, true)
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
	unsub := runtime.Subscribe(func(event *aop.Event) { events = append(events, event) })
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
	_ = runtime.Close(context.Background())
	_ = app.Close(context.Background())
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

func TestInteractiveResumeRotatesAndUsesSelectedContext(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "current.jsonl")
	resumePath := filepath.Join(dir, "selected.jsonl")
	writePersistenceSessionForID(t, resumePath, "selected-main")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: currentPath}}
	provider := new(persistenceProvider)
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, true)
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
	_ = runtime.Close(context.Background())
	_ = app.Close(context.Background())
	data, err := ReadHistory(resumePath)
	if err != nil {
		t.Fatal(err)
	}
	if data.SessionID != newID || len(data.Messages) != 2 {
		t.Fatalf("interactive resume JSONL = %#v", data)
	}
	currentEvents, err := output.ReadJSONL(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(currentEvents) == 0 || currentEvents[len(currentEvents)-1].GetSessionEnded().GetReason() != string(SessionCloseResumed) {
		t.Fatalf("current file was not closed before switch: %#v", currentEvents)
	}
}

func TestFreshJSONLOutputRejectsNonEmptyExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateFreshJSONLOutput(&cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}); err == nil {
		t.Fatal("non-empty output file was accepted without --resume")
	}
	if err := validateFreshJSONLOutput(&cfg.Option{AgentOptions: cfg.AgentOptions{Resume: path}}); err != nil {
		t.Fatalf("resume output was rejected: %v", err)
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

func newPersistenceRuntime(t *testing.T, option *cfg.Option, llm *persistenceProvider) (*apppkg.App, *AgentRuntime) {
	return newPersistenceRuntimeWithMode(t, option, llm, false)
}

func newPersistenceRuntimeWithMode(t *testing.T, option *cfg.Option, llm *persistenceProvider, interactive bool) (*apppkg.App, *AgentRuntime) {
	t.Helper()
	app := apppkg.New(apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()}, nil, nil)

	appSet := extensiontest.Set(t, extension.Entry{ID: "app", Extension: app})
	if err := appSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.SetProvider(llm, agent.ProviderConfig{Provider: llm.Name(), Model: "test-model", MaxTokens: 128, ContextWindow: 128000})
	primary := "task"
	if interactive {
		primary = "main-repl"
		option.SaveSession = true
	}
	runtime, err := New(app, nil, option, telemetry.NopLogger(), RuntimeConfig{PrimarySessionID: primary, Loop: agent.StandardLoop{}})
	if err != nil {
		_ = appSet.Close(context.Background())
		t.Fatal(err)
	}

	runtimeSet := extensiontest.Set(t, extension.Entry{ID: "runtime", Extension: runtime})
	if err := runtimeSet.Load(t.Context()); err != nil {
		_ = appSet.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtimeSet.Close(context.Background())
		_ = appSet.Close(context.Background())
	})
	return app, runtime
}

func runResumedTurn(t *testing.T, runtime *AgentRuntime, prompt string) {
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
		{Id: "e-1", EmittedAt: timestamp, SessionId: sessionID, Emitter: "aiscan", Seq: 1, Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: "test-model"}}},
		{Id: "e-2", EmittedAt: timestamp, SessionId: sessionID, TurnId: "old-turn", Emitter: "aiscan", Seq: 2, Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("old user")}}}},
		{Id: "e-3", EmittedAt: timestamp, SessionId: sessionID, TurnId: "old-turn", Emitter: "aiscan", Seq: 3, Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-2", Role: "assistant", Content: []*aop.Content{aop.Text("old assistant")}}}},
		{Id: "e-4", EmittedAt: timestamp, SessionId: sessionID, Emitter: "aiscan", Seq: 4, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: "completed"}}},
	}
	_ = types.SetSessionHistory(events[0], &types.SessionHistory{Mode: types.SessionHistory_MODE_INHERIT})
	bus := eventbus.New[*aop.Event]()
	writer, err := output.NewJSONLRecorder(bus, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadOutputRecorder(t, writer); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		bus.Emit(event)
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

func TestRuntimesBorrowOneAppEventSequenceAndRecorder(t *testing.T) {
	a := apppkg.New(apppkg.Config{
		SkipEngines: true, RecordFile: filepath.Join(t.TempDir(), "shared.jsonl"),
	}, nil, nil)

	aSet := extensiontest.Set(t, extension.Entry{ID: "a", Extension: a})
	if err := aSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer aSet.Close(context.Background())
	var mu sync.Mutex
	var events []*aop.Event
	unsubscribe := a.EventBus.Subscribe(func(event *aop.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})
	defer unsubscribe.Cancel()
	var runtimes []*AgentRuntime
	for range 2 {
		rt, err := New(a, nil, &cfg.Option{}, telemetry.NopLogger(), RuntimeConfig{Loop: agent.StandardLoop{}})
		if err != nil {
			t.Fatal(err)
		}

		rtSet := extensiontest.Set(t, extension.Entry{ID: "rt", Extension: rt})
		if err := rtSet.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer rtSet.Close(context.Background())
		runtimes = append(runtimes, rt)
		if _, err := rt.OpenSession(context.Background(), SessionOptions{ID: "shared"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = runtimes[0].Close(context.Background())
	session, err := runtimes[1].EnsureSession(SessionOptions{ID: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Command(context.Background(), "/status"); err != nil {
		t.Fatalf("closing sibling runtime broke borrowed App: %v", err)
	}
	_ = runtimes[1].Close(context.Background())
	if a.Recorder == nil {
		t.Fatal("borrower closed application recorder")
	}
	last := &aop.Event{SessionId: "shared", Id: "after-runtimes"}
	a.Emit(last)
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
