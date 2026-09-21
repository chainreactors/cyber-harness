package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	protobuf "google.golang.org/protobuf/proto"
)

type profileLoop func(context.Context, agent.Config) (*agent.Result, error)

func (f profileLoop) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	return f(ctx, config)
}

func TestNewRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name    string
		request profilepkg.Request
		want    string
	}{
		{name: "nil option", request: profilepkg.Request{}, want: "option is required"},
		{name: "invalid provider mode", request: profilepkg.Request{ProviderMode: provider.StartupMode(99)}, want: "invalid provider mode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newAIScanProfile(test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("newAIScanProfile() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

// The selected test Loop never calls this provider or any tool.
type inertProvider struct{}

func (inertProvider) Name() string { return "inert" }
func (inertProvider) ChatCompletion(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return nil, errors.New("unexpected model request")
}

func TestProfileOwnsAgentLifecycleAndRetainsResourcesDuringClose(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	loops := make(chan *loopext.Loop, 2)
	var unblock sync.Once
	config := minimalConfig(&agentsession.Config{})
	config.Session.Loop = profileLoop(func(ctx context.Context, config agent.Config) (*agent.Result, error) {
		managed, ok := config.Loop.(*loopext.Loop)
		if !ok {
			return nil, errors.New("run bypassed Agent extension")
		}
		if config.Inbox != nil {
			config.Inbox.Drain()
		}
		loops <- managed
		if config.SessionID == "second" {
			return &agent.Result{Stop: agent.StopReasonCompleted}, nil
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	})
	config.Base.Provider = provider.StartupConfig{Mode: provider.StartupOptional, Config: agent.ProviderConfig{Provider: "openai", Model: "test", APIKey: "test", BaseURL: "http://127.0.0.1:1/v1"}}
	p, err := buildAIScanProfile(config)
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
	first, err := runtime.EnsureSession(agentsession.SessionOptions{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.EnsureSession(agentsession.SessionOptions{ID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	secondRun, err := second.Run(t.Context(), agentsession.RunInput{Message: agent.TextInput("local lifecycle probe")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondRun.Wait(); err != nil {
		t.Fatal(err)
	}
	managed := <-loops
	if _, ok := config.Session.Loop.(profileLoop); !ok {
		t.Fatal("profile mutated caller-owned loop selection")
	}
	run, err := first.Run(t.Context(), agentsession.RunInput{Message: agent.TextInput("local lifecycle test")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("selected loop did not start")
	}
	if <-loops != managed {
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
	if _, err := p.Providers(); err == nil {
		t.Fatal("closing profile still published State")
	}
	if _, err := second.Run(t.Context(), agentsession.RunInput{Message: agent.TextInput("too late")}); err == nil {
		t.Fatal("manager admitted work after shutdown began")
	}
	unblock.Do(func() { close(release) })
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run: %v", err)
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatalf("profile close retry: %v", err)
	}
	if _, err := managed.Run(t.Context(), agent.Config{}); !errors.Is(err, loopext.ErrUnavailable) {
		t.Fatalf("agent runtime remained active after profile close: %v", err)
	}
}

func TestSessionProfileCanOmitAgentLifecycle(t *testing.T) {
	config := minimalConfig(nil)
	config.Session = &agentsession.Config{} // Sessions are selected, reasoning is not.
	config.Base.Provider = provider.StartupConfig{Mode: provider.StartupOptional, Config: agent.ProviderConfig{Provider: "openai", Model: "test", APIKey: "test", BaseURL: "http://127.0.0.1:1/v1"}}
	p, err := buildAIScanProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, _ := p.Runtime()
	session, err := runtime.EnsureSession(agentsession.SessionOptions{ID: "history-only"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.Run(t.Context(), agentsession.RunInput{Message: agent.TextInput("no reasoning selected")})
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

func minimalConfig(runtime *agentsession.Config) config {
	if runtime != nil {
		runtime.Loop = agent.StandardLoop{}
	}
	return config{
		Option: &cfg.Option{}, Session: runtime,
		Base: appConfig{SkipEngines: true, Logger: telemetry.NopLogger()},
	}
}

func TestApplicationOnlyProfile(t *testing.T) {
	p, err := buildAIScanProfile(minimalConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Providers(); err == nil {
		t.Fatal("State available before Load")
	}
	if err := p.RegisterNamespaces(aop.NewNamespaceMux(t.Context())); err == nil {
		t.Fatal("resource namespaces available before Load")
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Providers(); err != nil {
		t.Fatal(err)
	}
	mux := aop.NewNamespaceMux(t.Context())
	if err := mux.Register(&ptypb.ProtocolMessage{}, func(context.Context, *aop.Envelope, protobuf.Message, aop.SendFunc) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.RegisterNamespaces(mux); err != nil {
		t.Fatalf("application profile conflicts with Web-owned namespaces: %v", err)
	}
	if _, err := p.Runtime(); err == nil {
		t.Fatal("application-only profile returned Runtime")
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Providers(); err == nil {
		t.Fatal("State available after Close")
	}
	if err := p.RegisterNamespaces(aop.NewNamespaceMux(t.Context())); err == nil {
		t.Fatal("resource namespaces available after Close")
	}
}

func TestRuntimeUsesProfileApplicationWithoutOwningIt(t *testing.T) {
	p, err := buildAIScanProfile(minimalConfig(&agentsession.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	application, err := p.Providers()
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	// The runtime has no accessor handing the application out. What matters is
	// that both read one provider state, so setting it on one is visible on the
	// other.
	application.Set(inertProvider{}, agent.ProviderConfig{Model: "shared"})
	if _, providerConfig := run.ProviderState(); providerConfig.Model != "shared" {
		t.Fatal("Runtime did not use the profile application")
	}
	mux := aop.NewNamespaceMux(t.Context())
	defer mux.Close(context.Background())
	if err := p.RegisterNamespaces(mux); err != nil {
		t.Fatal(err)
	}
	request := aop.MustWrap("open", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{
		OpenSessionRequest: &aop.OpenSessionRequest{SessionId: "namespace"},
	}})
	var response *aop.Envelope
	handled, err := mux.Dispatch(request, func(value *aop.Envelope) error {
		response = value
		return nil
	})
	if err != nil || !handled || response == nil {
		t.Fatalf("session namespace: handled=%v response=%v err=%v", handled, response, err)
	}
	message, err := aop.Unwrap(response)
	if err != nil || message.(*aop.ProtocolMessage).GetOpenSessionResponse().GetAccepted().GetId() != "namespace" {
		t.Fatalf("session namespace response=%v err=%v", response, err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadContextDoesNotOwnProfileLifetime(t *testing.T) {
	p, err := buildAIScanProfile(minimalConfig(&agentsession.Config{}))
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
		t.Fatalf("startup context canceled profile lifetime: %v", err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFromOptionOwnsEventOutputSelection(t *testing.T) {
	resume := &cfg.Option{}
	resume.Resume = "source.jsonl"
	explicit := &cfg.Option{}
	explicit.OutputFile = "explicit.jsonl"
	tests := []struct {
		name   string
		option *cfg.Option
		want   string
	}{
		{name: "one-shot", option: &cfg.Option{}},
		{name: "resume", option: resume},
		{name: "explicit", option: explicit, want: "explicit.jsonl"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := configFromOption(test.option, profilepkg.ProviderDisabled, nil, telemetry.NopLogger())
			if err != nil {
				t.Fatal(err)
			}
			if config.Output != test.want {
				t.Fatalf("output = %q, want %q", config.Output, test.want)
			}
		})
	}
}

func TestProfileResolvesHostInputWithoutMutatingIt(t *testing.T) {
	option := &cfg.Option{}
	p, err := newAIScanProfile(profilepkg.Request{Option: option, ProviderMode: provider.StartupDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if option.Resolved != nil || option.Extensions != nil {
		t.Fatal("profile construction mutated host input")
	}
}
