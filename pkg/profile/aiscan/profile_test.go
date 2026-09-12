package aiscan_test

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/agent"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	profile "github.com/chainreactors/aiscan/pkg/profile/aiscan"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
)

func minimalConfig(runtime *runtimepkg.RuntimeConfig) profile.Config {
	if runtime != nil {
		runtime.Loop = agent.StandardLoop{}
	}
	return profile.Config{
		Option: &cfg.Option{}, Logger: telemetry.NopLogger(), Runtime: runtime,
		Application: apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()},
	}
}

func TestApplicationOnlyProfile(t *testing.T) {
	p, err := profile.New(minimalConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.App(); err == nil {
		t.Fatal("App available before Load")
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.App(); err != nil {
		t.Fatal(err)
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
}

func TestRuntimeBorrowsProfileApplication(t *testing.T) {
	p, err := profile.New(minimalConfig(&runtimepkg.RuntimeConfig{}))
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
		t.Fatal("Runtime did not borrow profile application")
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadContextDoesNotOwnProductLifetime(t *testing.T) {
	p, err := profile.New(minimalConfig(&runtimepkg.RuntimeConfig{}))
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
