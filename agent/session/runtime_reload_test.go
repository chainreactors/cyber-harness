package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
)

func TestRuntimeProviderReloadPreservesSessionHistory(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	session, err := rt.OpenSession(t.Context(), SessionOptions{
		ID:       "provider-reload",
		Messages: []*aop.Message{{Role: "user", Content: []*aop.Content{aop.Text("keep this context")}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	rt.config.Providers.Set(&runtimeSemanticProvider{}, provider.ProviderConfig{Provider: "test", Model: "next-model"})
	if session.Model() != "next-model" {
		t.Fatalf("session model = %q, want next-model", session.Model())
	}
	history := session.MessagesSnapshot()
	if len(history) != 1 || history[0].Content[0].GetText().GetText() != "keep this context" {
		t.Fatalf("provider reload changed session history: %v", history)
	}
}

func TestProviderReloadFollowsTurnBoundaryAndPreservesExplicitModels(t *testing.T) {
	rt := newBareRuntime(t, nil, &runtimeSemanticProvider{})
	old, _ := rt.config.Providers.Current()
	rt.config.Providers.Set(old, provider.ProviderConfig{Provider: "openai", APIKey: "fixture", Model: "model-a", MaxTokens: 128, ContextWindow: 4096})
	started, release := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	rt.agentConfig.Loop = taskLoop(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		config.Inbox.Drain()
		if config.Model == "model-a" {
			startOnce.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &agent.Result{Output: config.Model, Messages: config.Messages, Stop: agenthooks.StopReasonCompleted}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conversation, err := rt.OpenSession(ctx, SessionOptions{ID: "inherited", Messages: []*aop.Message{provider.TextMessage("user", "keep my HAR")}})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := rt.OpenSession(ctx, SessionOptions{ID: "pinned"})
	if err != nil {
		t.Fatal(err)
	}
	if err := pinned.SetModel("local-model"); err != nil {
		t.Fatal(err)
	}
	childConfig := rt.agentConfig.WithModel("child-model")
	child, err := rt.OpenSession(ctx, SessionOptions{ID: "child", ParentSessionID: conversation.ID(), Config: &childConfig})
	if err != nil {
		t.Fatal(err)
	}
	first, err := conversation.Run(ctx, RunInput{TurnID: "active", Message: agent.TextInput("start")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	queued, err := conversation.Run(ctx, RunInput{TurnID: "queued", Message: agent.TextInput("continue")})
	if err != nil {
		t.Fatal(err)
	}
	rt.config.Providers.Set(&runtimeSemanticProvider{}, provider.ProviderConfig{Provider: "openai", APIKey: "fixture", Model: "model-b", MaxTokens: 256, ContextWindow: 8192})
	releaseOnce.Do(func() { close(release) })
	if result, err := first.Wait(); err != nil || result.Output != "model-a" {
		t.Fatalf("active turn changed provider: %v %v", result, err)
	}
	if result, err := queued.Wait(); err != nil || result.Output != "model-b" {
		t.Fatalf("queued turn did not read the latest provider: %v %v", result, err)
	}
	if pinned.Model() != "local-model" || child.Model() != "child-model" || conversation.ContextWindow() != 8192 {
		t.Fatal("global reload overwrote an explicit model or left inherited limits stale")
	}
	if _, err := conversation.Command(ctx, "/status"); err != nil {
		t.Fatal(err)
	}
	if messages := conversation.MessagesSnapshot(); len(messages) != 1 || !strings.Contains(provider.MessageText(messages[0]), "keep my HAR") {
		t.Fatalf("turn boundary lost history: %v", messages)
	}
	for _, session := range []*Session{conversation, pinned} {
		if _, err := session.Command(ctx, "/clear"); err != nil {
			t.Fatal(err)
		}
	}
	rt.config.Providers.Set(&runtimeSemanticProvider{}, provider.ProviderConfig{Model: "model-c", ContextWindow: 16384})
	if conversation.Model() != "model-c" || conversation.ContextWindow() != 16384 || pinned.Model() != "local-model" || child.Model() != "child-model" {
		t.Fatal("session rotation lost provider inheritance or its explicit model")
	}
}
