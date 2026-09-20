package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/harness"
)

type helloTool struct{}

func (helloTool) Name() string        { return "hello" }
func (helloTool) Description() string { return "say hello" }
func (helloTool) Definition() *coretool.Definition {
	return coretool.Def("hello", "say hello", struct{}{})
}
func (helloTool) Execute(context.Context, string) (*coretool.Result, error) {
	return coretool.TextResult("hello"), nil
}

func toolExtension() extension.Extension {
	return extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add[coretool.Tool](scope, helloTool{})
	}}
}

func TestToolHarnessLoadsCustomCapabilities(t *testing.T) {
	h, err := harness.New(harness.Config{
		Base:       harness.BaseConfig{Directory: t.TempDir()},
		Extensions: []extension.Extension{toolExtension()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close(context.Background()) }()
	if err := h.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	executor, err := h.Tools()
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteTool(t.Context(), "hello", "{}")
	if err != nil || coretool.ResultText(result) != "hello" {
		t.Fatalf("tool result = %q, err = %v", coretool.ResultText(result), err)
	}
	if _, err := h.Runtime(); err == nil {
		t.Fatal("tool-only harness unexpectedly created a session runtime")
	}
}

type providerStub struct{}

func (providerStub) Name() string { return "stub" }
func (providerStub) ChatCompletion(ctx context.Context, _ *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &provider.ChatCompletionResponse{Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "ok"), FinishReason: "stop"}}}, nil
}

func TestAgentHarnessDefaultsToStandardLoop(t *testing.T) {
	h, err := harness.New(harness.Config{
		Base:    harness.BaseConfig{Directory: t.TempDir(), Provider: provider.StartupConfig{Mode: provider.StartupDisabled}},
		Session: &agentsession.Config{},
		Extensions: []extension.Extension{extension.Func{LoadFunc: func(scope *extension.Scope) error {
			state, err := extension.Use[*provider.State](scope)
			if err == nil {
				state.Set(providerStub{}, provider.ProviderConfig{Model: "stub"})
			}
			return err
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close(context.Background()) }()
	if err := h.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, err := h.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	session, err := runtime.OpenSession(t.Context(), agentsession.SessionOptions{ID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := session.Run(t.Context(), agentsession.RunInput{Message: agent.TextInput("hello")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := turn.Wait()
	if err != nil || result.Output != "ok" {
		t.Fatalf("turn = %#v, err = %v", result, err)
	}
	if err := runtime.CloseSession(context.Background(), session.ID(), agentsession.SessionCloseCompleted); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
