package console

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
	"github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
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

func newConsoleRuntime(t *testing.T, provider agent.Provider) *runtimepkg.AgentRuntime {
	t.Helper()
	a := &apppkg.App{Commands: commands.NewRegistry(), Skills: &skills.Store{}, EventBus: eventbus.New[*aop.Event]()}
	a.SetProvider(provider, agent.ProviderConfig{Model: "test"})
	t.Cleanup(a.Close)
	rt, err := runtimepkg.New(context.Background(), &cfg.Option{}, telemetry.NopLogger(), &runtimepkg.RuntimeConfig{ExistingApp: a})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	return rt
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
	a := &apppkg.App{EventBus: eventbus.New[*aop.Event]()}
	recorder, err := output.NewJSONLRecorder(a.EventBus, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		a.Emit(event)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConsoleRuntimeAdapterPreservesTotalContextTokens(t *testing.T) {
	provider := &consoleProvider{usage: provider.TokenUsage(8192, 0, 8200, 0, 0)}
	rt := newConsoleRuntime(t, provider)
	session, err := rt.OpenSession(context.Background(), runtimepkg.SessionOptions{ID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := consoleAppInfoForSession(rt, session).Run(context.Background(), "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextTokens != 8200 {
		t.Fatalf("context tokens = %d, want 8200", result.ContextTokens)
	}
}
