package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type persistenceProvider struct {
	requests []*provider.ChatCompletionRequest
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
	runtime.Close()
	app.Close()

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
		runtime.Close()
		app.Close()

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
		data, err := loadResumeState(resumePath)
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
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, REPLEphemeral)
	_ = app

	root, err := runtime.OpenSession(context.Background(), SessionOptions{ID: MainREPLName})
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", 4<<20)
	oldID := root.ID()
	runtime.sessionEvents.Emit(&aop.Event{
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

	data, err := loadResumeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Messages) != 1 || data.Messages[0].Content[0].GetText().GetText() != large {
		t.Fatalf("resumed inherited history = %d messages, want the original large message", len(data.Messages))
	}
}

func TestREPLResumeLoadsMainSessionContext(t *testing.T) {
	resumePath := filepath.Join(t.TempDir(), "repl-resume.jsonl")
	writePersistenceSessionForID(t, resumePath, MainREPLName)
	option := &cfg.Option{}
	option.Resume = resumePath
	provider := new(persistenceProvider)
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, REPLEphemeral)

	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: MainREPLName})
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
	if err := runtime.CloseSession(context.Background(), MainREPLName, SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	app.Close()
}

func TestClearRotatesToAnEmptyContinuationSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clear.jsonl")
	option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFile: path}}
	provider := new(persistenceProvider)
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, REPLEphemeral)
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: MainREPLName})
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
	unsub()
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

	if err := runtime.CloseSession(context.Background(), MainREPLName, SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	app.Close()
	data, err := loadResumeState(path)
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
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, REPLEphemeral)
	runtime.config.Compaction = agent.CompactionSettings{KeepRecentTokens: 20, ReserveTokens: 64}
	long := strings.Repeat("history ", 120)
	messages := []*aop.Message{
		agent.TextMessage("user", long+"one"),
		agent.TextMessage("assistant", long+"two"),
		agent.TextMessage("user", long+"three"),
		agent.TextMessage("assistant", "recent answer"),
	}
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: MainREPLName, Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	oldID := session.ID()
	var events []*aop.Event
	unsub := runtime.Subscribe(func(event *aop.Event) { events = append(events, event) })
	if _, err := session.Command(context.Background(), "/compact focus on findings"); err != nil {
		unsub()
		t.Fatalf("/compact: %v", err)
	}
	unsub()
	newID := session.ID()
	if newID == oldID {
		t.Fatal("compact did not rotate the session")
	}
	compacted := session.MessagesSnapshot()
	if len(compacted) == 0 || len(compacted) >= len(messages) {
		t.Fatalf("compacted messages = %d, original = %d", len(compacted), len(messages))
	}
	assertRotationEvents(t, events, oldID, newID, string(SessionCloseCompacted))

	if err := runtime.CloseSession(context.Background(), MainREPLName, SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	app.Close()
	data, err := loadResumeState(path)
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
	app, runtime := newPersistenceRuntimeWithMode(t, option, provider, REPLEphemeral)
	session, err := runtime.OpenSession(context.Background(), SessionOptions{ID: MainREPLName})
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

	if err := runtime.CloseSession(context.Background(), MainREPLName, SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	app.Close()
	data, err := loadResumeState(resumePath)
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

func newPersistenceRuntime(t *testing.T, option *cfg.Option, llm *persistenceProvider) (*App, *AgentRuntime) {
	return newPersistenceRuntimeWithMode(t, option, llm, REPLDisabled)
}

func newPersistenceRuntimeWithMode(t *testing.T, option *cfg.Option, llm *persistenceProvider, replMode REPLMode) (*App, *AgentRuntime) {
	t.Helper()
	app, err := NewApp(context.Background(), ApplicationConfig{SkipEngines: true, Logger: telemetry.NopLogger()})
	if err != nil {
		t.Fatal(err)
	}
	app.Provider = llm
	app.ProviderConfig = agent.ProviderConfig{Provider: llm.Name(), Model: "test-model", MaxTokens: 128, ContextWindow: 128000}
	runtime, err := NewAgentRuntime(context.Background(), option, telemetry.NopLogger(), &RuntimeConfig{ExistingApp: app, REPLMode: replMode})
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		runtime.Close()
		app.Close()
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
	for _, event := range events {
		bus.Emit(event)
	}
	if err := writer.Close(); err != nil {
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
