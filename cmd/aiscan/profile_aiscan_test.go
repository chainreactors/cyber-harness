package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
)

type profileLoop func(context.Context, agent.Config) (*agent.Result, error)

func (f profileLoop) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	return f(ctx, config)
}

// The selected test Loop never calls this provider or any tool.
type inertProvider struct{}

func (inertProvider) Name() string { return "inert" }
func (inertProvider) ChatCompletion(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return nil, errors.New("unexpected model request")
}

func TestProfileOwnsAgentLifecycleAndRetainsResourcesDuringClose(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	runtimes := make(chan *agentext.Runtime, 2)
	var unblock sync.Once
	config := minimalConfig(&agentext.Config{})
	config.Runtime.Loop = profileLoop(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		managed, ok := config.Loop.(*agentext.Runtime)
		if !ok {
			return nil, errors.New("run bypassed Agent extension")
		}
		if config.Inbox != nil {
			config.Inbox.Drain()
		}
		runtimes <- managed
		if config.SessionID == "second" {
			return &agent.Result{Stop: agent.StopReasonCompleted}, nil
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	})
	p, err := newAIScanProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblock.Do(func() { close(release) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := p.Close(ctx); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, _ := p.Runtime()
	app, _ := p.App()
	runtime.SetProvider(inertProvider{}, agent.ProviderConfig{})
	first, err := runtime.EnsureSession(agentext.SessionOptions{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.EnsureSession(agentext.SessionOptions{ID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	secondRun, err := second.Run(t.Context(), agentext.RunInput{Message: agent.TextInput("local lifecycle probe")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondRun.Wait(); err != nil {
		t.Fatal(err)
	}
	managed := <-runtimes
	if _, ok := config.Runtime.Loop.(profileLoop); !ok {
		t.Fatal("profile mutated caller-owned loop selection")
	}
	run, err := first.Run(t.Context(), agentext.RunInput{Message: agent.TextInput("local lifecycle test")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("selected loop did not start")
	}
	if <-runtimes != managed {
		t.Fatal("sessions do not use the same installed Agent runtime")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := p.Close(deadline); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("profile close while run is draining: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("profile shutdown did not cancel the run")
	}
	if app.Closed() {
		t.Fatal("profile released App before execution completed")
	}
	if _, err := second.Run(t.Context(), agentext.RunInput{Message: agent.TextInput("too late")}); err == nil {
		t.Fatal("manager admitted work after shutdown began")
	}
	unblock.Do(func() { close(release) })
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run: %v", err)
	}
	if err := p.Close(t.Context()); err != nil || !app.Closed() {
		t.Fatalf("profile close retry: %v, app closed=%v", err, app.Closed())
	}
	if _, err := managed.Run(t.Context(), agent.Config{}); !errors.Is(err, agentext.ErrUnavailable) {
		t.Fatalf("agent runtime remained active after profile close: %v", err)
	}
}

func TestSessionProfileCanOmitAgentLifecycle(t *testing.T) {
	config := minimalConfig(nil)
	config.Runtime = &agentext.Config{} // Sessions are selected, reasoning is not.
	p, err := newAIScanProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, _ := p.Runtime()
	runtime.SetProvider(inertProvider{}, agent.ProviderConfig{})
	session, err := runtime.EnsureSession(agentext.SessionOptions{ID: "history-only"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(t.Context(), agentext.RunInput{Message: agent.TextInput("no reasoning selected")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err == nil || !strings.Contains(err.Error(), "agent loop is not configured") {
		t.Fatalf("run without Agent extension: %v", err)
	}
	if _, err := session.Command(t.Context(), "/status"); err != nil {
		t.Fatalf("session control depends on Agent extension: %v", err)
	}
}

func minimalConfig(runtime *agentext.Config) aiscanProfileConfig {
	if runtime != nil {
		runtime.Loop = agent.StandardLoop{}
	}
	return aiscanProfileConfig{
		Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Runtime: runtime,
		Application: apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()},
	}
}

func TestApplicationOnlyProfile(t *testing.T) {
	p, err := newAIScanProfile(minimalConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.App(); err == nil {
		t.Fatal("App available before Load")
	}
	if err := p.RegisterResourceNamespaces(aop.NewNamespaceMux(t.Context())); err == nil {
		t.Fatal("resource namespaces available before Load")
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.App(); err != nil {
		t.Fatal(err)
	}
	mux := aop.NewNamespaceMux(t.Context())
	if err := p.RegisterResourceNamespaces(mux); err != nil {
		t.Fatal("resource namespaces unavailable after Load")
	}
	if _, err := p.Runtime(); err == nil {
		t.Fatal("application-only profile returned Runtime")
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.App(); err == nil {
		t.Fatal("App available after Close")
	}
	if err := p.RegisterResourceNamespaces(aop.NewNamespaceMux(t.Context())); err == nil {
		t.Fatal("resource namespaces available after Close")
	}
}

func TestRuntimeUsesProfileApplicationWithoutOwningIt(t *testing.T) {
	p, err := newAIScanProfile(minimalConfig(&agentext.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	application, err := p.App()
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if run.App() != application {
		t.Fatal("Runtime did not use the profile application")
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadContextDoesNotOwnProductLifetime(t *testing.T) {
	p, err := newAIScanProfile(minimalConfig(&agentext.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	if err := p.Load(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	run, err := p.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Context().Err(); err != nil {
		t.Fatalf("startup context canceled product lifetime: %v", err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFromOptionOwnsEventOutputSelection(t *testing.T) {
	base := &cfg.Option{}
	if got := profileConfigFromOption(base, apppkg.RuntimeFeatures{}, nil, telemetry.NopLogger()).Output; got != "" {
		t.Fatalf("one-shot output = %q", got)
	}

	base.Resume = "source.jsonl"
	if got := profileConfigFromOption(base, apppkg.RuntimeFeatures{}, nil, telemetry.NopLogger()).Output; got != "" {
		t.Fatalf("resume selected output %q", got)
	}

	base.OutputFile = "explicit.jsonl"
	if got := profileConfigFromOption(base, apppkg.RuntimeFeatures{}, nil, telemetry.NopLogger()).Output; got != "explicit.jsonl" {
		t.Fatalf("explicit output = %q", got)
	}
}
