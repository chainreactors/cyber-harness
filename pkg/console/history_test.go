package console

import (
	"context"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/internal/applicationtest"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	agentext "github.com/chainreactors/aiscan/pkg/exts/session"
	eventoutput "github.com/chainreactors/aiscan/pkg/exts/eventoutput"
	"github.com/chainreactors/aiscan/pkg/types"
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

func loadConsoleApplication(t *testing.T, ctx context.Context, application *apppkg.Resource) *extension.Set {
	return applicationtest.Load(t, ctx, application)
}

func (*consoleProvider) Name() string { return "console-test" }
func (p *consoleProvider) ChatCompletion(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}}, Usage: p.usage,
	}, nil
}

func newConsoleRuntime(t *testing.T, provider agent.Provider) *agentext.Runtime {
	t.Helper()
	a := newTestApp(t, apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()}, apppkg.AppServices{})

	aSet := loadConsoleApplication(t, t.Context(), a)
	a.App.SetProvider(provider, agent.ProviderConfig{Model: "test"})
	t.Cleanup(func() { _ = aSet.Close(context.Background()) })
	rt, err := agentext.New(agentext.Config{Application: a.App, Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Loop: agent.StandardLoop{}})
	if err != nil {
		t.Fatal(err)
	}

	rtSet := extensiontest.Set(t, extension.Entry{ID: "rt", Extension: rt})
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
	recorder, err := eventoutput.New(stream, eventoutput.Options{Path: path})
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
	session, err := rt.OpenSession(context.Background(), agentext.SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}

	run, err := session.Run(context.Background(), agentext.RunInput{Content: []*aop.Content{aop.Text("hello")}})
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
