package aiscan

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/agent"
	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	observeext "github.com/chainreactors/cyber/pkg/exts/observe"
)

// Every capability a profile publishes is resolved by type at load, not by the
// compiler. A shape that nobody loads in a test is a shape whose wiring nobody
// checks -- so each one CI cares about is loaded and closed here.
func TestEveryProfileShapeLoadsAndCloses(t *testing.T) {
	for _, test := range []struct {
		name  string
		build func(t *testing.T) config
	}{
		{
			name:  "application only",
			build: func(*testing.T) config { return minimalConfig(nil) },
		},
		{
			name:  "with a session runtime",
			build: func(*testing.T) config { return minimalConfig(&agentsession.Config{}) },
		},
		{
			name: "with the scan engines",
			build: func(*testing.T) config {
				config := minimalConfig(&agentsession.Config{})
				config.Base.SkipEngines = false
				return config
			},
		},
		{
			name: "with event output",
			build: func(t *testing.T) config {
				config := minimalConfig(&agentsession.Config{})
				config.Output = t.TempDir() + "/events.jsonl"
				return config
			},
		},
		{
			name: "with observation",
			build: func(*testing.T) config {
				config := minimalConfig(&agentsession.Config{})
				config.Observe = []observeext.Kind{observeext.Tools}
				return config
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile, err := newProfile(test.build(t))
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			if err := profile.Load(t.Context()); err != nil {
				t.Fatalf("load: %v", err)
			}
			if err := profile.Close(context.Background()); err != nil {
				t.Fatalf("close: %v", err)
			}
		})
	}
}

// The composition root reads what the graph assembled, rather than holding the
// parts it threaded in.
func TestLoadedProfilePublishesItsApplicationAndRuntime(t *testing.T) {
	profile, err := newProfile(minimalConfig(&agentsession.Config{Loop: agent.StandardLoop{}}))
	if err != nil {
		t.Fatal(err)
	}
	if application, err := profile.State(); err == nil && application != nil {
		t.Fatal("the application existed before the graph loaded")
	}
	if err := profile.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = profile.Close(context.Background()) })

	if _, err := profile.State(); err != nil {
		t.Fatal(err)
	}
	runtime, err := profile.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil {
		t.Fatal("the session runtime was not published")
	}
	// The runtime borrowed these; nothing threaded them in.
	if runtime.CommandRegistry() == nil || runtime.Tools() == nil || runtime.Bash() == nil || runtime.Skills() == nil {
		t.Error("the session runtime is missing capabilities its extensions published")
	}
}

var _ = cfg.Option{}
var _ = telemetry.NopLogger
