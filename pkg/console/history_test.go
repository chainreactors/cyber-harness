package console

import (
	"context"
	"github.com/chainreactors/cyber/cmd/harness"
	"github.com/chainreactors/cyber/core/extension"
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
	apppkg "github.com/chainreactors/cyber/pkg/app"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	"github.com/chainreactors/cyber/pkg/types"
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

func loadConsoleApplication(t *testing.T, ctx context.Context, application *apppkg.App) *extension.Set {
	return harness.AppLoad(t, ctx, application)
}

func (*consoleProvider) Name() string { return "console-test" }
func (p *consoleProvider) ChatCompletion(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}}, Usage: p.usage,
	}, nil
}

func newConsoleRuntime(t *testing.T, provider agent.Provider) *agentsession.Runtime {
	t.Helper()
	a := newTestApp(t, telemetry.NopLogger(), apppkg.Dependencies{})

	aSet := loadConsoleApplication(t, t.Context(), a)
	a.SetProvider(provider, agent.ProviderConfig{Model: "test"})
	t.Cleanup(func() { _ = aSet.Close(context.Background()) })
	rt, err := sessionext.New(agentsession.Config{Application: a, Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Loop: agent.StandardLoop{}})
	if err != nil {
		t.Fatal(err)
	}

	rtSet := harness.Set(t, rt)
	if err := rtSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rtSet.Close(context.Background()) })
	return rt.Runtime()
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
	recorder, err := telemetryext.New(stream, telemetryext.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := loadOutputRecorder(t, recorder); err != nil {
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
	rt := newConsoleRuntime(t, provider)
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
