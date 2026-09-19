package console

import (
	"context"
	"github.com/chainreactors/cyber/pkg/apptest"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	"github.com/chainreactors/cyber/pkg/hosttest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/types"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
)

func TestListSavedSessionsOnlyReadsJSONL(t *testing.T) {
	dir := t.TempDir()
	writeSessionEvents(t, filepath.Join(dir, "session.jsonl"), []*aop.Event{
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("hello")}}}}),
	})
	if err := os.WriteFile(filepath.Join(dir, "unsupported.json"), []byte(`{"messages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := listSavedSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || filepath.Base(sessions[0].Path) != "session.jsonl" {
		t.Fatalf("sessions = %#v", sessions)
	}
}

type consoleProvider struct{ usage *aop.TokenUsage }

func (*consoleProvider) Name() string { return "console-test" }
func (p *consoleProvider) ChatCompletion(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}}, Usage: p.usage,
	}, nil
}

func newConsoleRuntime(t *testing.T, provider agent.Provider) (*agentsession.Runtime, *apppkg.State) {
	t.Helper()
	a := apptest.NewState(t, telemetry.NopLogger(), nil)
	// The runtime reads provider state while loading, so the provider is set
	// first. One graph: the session extension borrows the same capabilities a
	// profile publishes, so the test publishes them once and mounts it alongside.
	a.SetProvider(provider, agent.ProviderConfig{Model: "test"})
	rt := sessionext.New(agentsession.Config{Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Loop: agent.StandardLoop{}})
	set := hosttest.Set(t, append(apptest.Entries(t, a), promptext.New(), loopext.New(agent.StandardLoop{}), rt)...)
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return rt.Runtime(), a
}

func sessionTestEvent(id string, event *aop.Event) *aop.Event {
	event.SessionId = id
	if event.GetSessionStarted() != nil {
		_ = types.SetSessionHistory(event, &types.SessionHistory{Mode: types.SessionHistory_MODE_INHERIT})
	}
	return event
}

func writeSessionEvents(t *testing.T, path string, events []*aop.Event) {
	t.Helper()
	stream := coreevents.New()
	recorder, err := telemetryext.New(telemetryext.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := loadOutputRecorder(t, stream, recorder); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		stream.Publish(event)
	}
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConsoleRuntimeAdapterPreservesTotalContextTokens(t *testing.T) {
	provider := &consoleProvider{usage: provider.TokenUsage(8192, 0, 8200, 0, 0)}
	rt, _ := newConsoleRuntime(t, provider)
	session, err := rt.OpenSession(context.Background(), agentsession.SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}

	run, err := session.Run(context.Background(), agentsession.RunInput{Content: []*aop.Content{aop.Text("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextTokens != 8200 {
		t.Fatalf("context tokens = %d, want 8200", result.ContextTokens)
	}
}
