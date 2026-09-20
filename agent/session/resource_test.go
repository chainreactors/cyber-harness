package session

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
)

func TestResourceRequiresExplicitCapabilities(t *testing.T) {
	f := apptest.NewFixture(t, nil, nil)
	apptest.Load(t, t.Context(), f)
	valid := Config{Loop: agent.NoLoop(), Providers: f.Providers, Events: f.Stream, Hooks: f.Hooks, Tools: f.Tools, CommandRegistry: f.Commands, Skills: f.Skills}
	cases := []struct {
		name   string
		remove func(*Config)
	}{
		{"loop", func(c *Config) { c.Loop = nil }}, {"providers", func(c *Config) { c.Providers = nil }},
		{"events", func(c *Config) { c.Events = nil }}, {"hooks", func(c *Config) { c.Hooks = nil }},
		{"tools", func(c *Config) { c.Tools = nil }}, {"commands", func(c *Config) { c.CommandRegistry = nil }},
		{"skills", func(c *Config) { c.Skills = nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			c := valid
			test.remove(&c)
			resource, err := NewResource(c)
			if resource != nil || err == nil || !strings.Contains(err.Error(), test.name) {
				t.Fatalf("missing capability: %v %v", resource, err)
			}
		})
	}
	resource, err := NewResource(valid)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Runtime().Active() {
		t.Fatal("constructor activated runtime")
	}
	if err := resource.Start(t.Context(), t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	current, err := resource.Runtime().OpenSession(t.Context(), SessionOptions{ID: "missing-adapters"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.Command(t.Context(), "!echo test"); err == nil {
		t.Fatal("uninstalled shell executed")
	}
	if _, err := current.Resume(t.Context(), "missing.jsonl"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("uninstalled history: %v", err)
	}
	if err := resource.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if resource.Runtime().Active() {
		t.Fatal("closed runtime reports active")
	}
	valid.Resume = "missing.jsonl"
	resource, err = NewResource(valid)
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	if err := resource.Start(t.Context(), t.Context()); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("resume without history: %v", err)
	}
}

func TestLoggerRetargetReachesExistingSessionsAndDerivedAgents(t *testing.T) {
	resource, err := newUnitResource(t, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.Start(t.Context(), t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resource.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	runtime := resource.Runtime()
	first, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "before"})
	if err != nil {
		t.Fatal(err)
	}
	child := first.state.agent.Derive()
	var logs bytes.Buffer
	runtime.Logger.SetOutput(&logs)
	first.state.agent.Cfg.Logger.Infof("existing session")
	child.Cfg.Logger.Infof("existing child")
	if _, err := first.state.scheduler.Add(agent.LoopEntry{Name: "existing-scheduler", Prompt: "wait", Interval: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if err := first.state.scheduler.Remove("existing-scheduler"); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "after"})
	if err != nil {
		t.Fatal(err)
	}
	second.state.agent.Cfg.Logger.Infof("new session")
	for _, marker := range []string{"existing session", "existing child", "loop=existing-scheduler", "new session"} {
		if !strings.Contains(logs.String(), marker) {
			t.Errorf("missing %q in logs: %s", marker, logs.String())
		}
	}
	before := logs.Len()
	runtime.Logger.SetOutput(nil)
	child.Cfg.Logger.Infof("discarded")
	if logs.Len() != before {
		t.Fatal("old destination still receives logs")
	}
}
