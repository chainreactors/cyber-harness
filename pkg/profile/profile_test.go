package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/extension"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
)

type retryCloseProbe struct{ attempts int }

func (*retryCloseProbe) Load(*extension.Scope) error { return nil }
func (p *retryCloseProbe) Close(context.Context) error {
	p.attempts++
	if p.attempts == 1 {
		return extension.ErrCloseIncomplete
	}
	return nil
}

func TestProfilePublishesCapabilitiesOnlyWhileActive(t *testing.T) {
	application := apppkg.New(apppkg.Config{SkipEngines: true}, apppkg.Dependencies{})
	runtime := new(sessionext.Runtime)
	namespaceCalls := 0
	value, err := New(Config{
		Entries:  []extension.Entry{{ID: "application", Extension: application}},
		App:      application.App,
		Sessions: runtime,
		RegisterResourceNamespaces: func(*aop.NamespaceMux) error {
			namespaceCalls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := value.App(); err == nil {
		t.Fatal("unloaded profile published its application")
	}
	if _, err := value.Sessions(); err == nil {
		t.Fatal("unloaded profile published its session runtime")
	}
	if err := value.RegisterResourceNamespaces(aop.NewNamespaceMux(t.Context())); err == nil {
		t.Fatal("unloaded profile published its resource namespaces")
	}
	if namespaceCalls != 0 {
		t.Fatalf("namespace callback ran %d times before load", namespaceCalls)
	}

	if err := value.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := value.App(); err != nil || got != application.App {
		t.Fatalf("App() = %p, %v; want %p", got, err, application.App)
	}
	if got, err := value.Sessions(); err != nil || got != runtime {
		t.Fatalf("Sessions() = %p, %v; want %p", got, err, runtime)
	}
	if err := value.RegisterResourceNamespaces(aop.NewNamespaceMux(t.Context())); err != nil {
		t.Fatal(err)
	}
	if namespaceCalls != 1 {
		t.Fatalf("namespace callback ran %d times, want 1", namespaceCalls)
	}
	if err := value.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := value.App(); err == nil {
		t.Fatal("closed profile retained its application publication")
	}
	if _, err := value.Sessions(); err == nil {
		t.Fatal("closed profile retained its session runtime publication")
	}
	if !application.App.Closed() {
		t.Fatal("profile did not close its application resource")
	}
}

func TestProfileKeepsIncompleteCloseRetryableAndUnpublished(t *testing.T) {
	application := apppkg.New(apppkg.Config{SkipEngines: true}, apppkg.Dependencies{})
	probe := &retryCloseProbe{}
	value, err := New(Config{
		Entries: []extension.Entry{
			{ID: "application", Extension: application},
			{ID: "probe", DependsOn: []string{"application"}, Extension: probe},
		},
		App: application.App,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := value.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := value.Close(t.Context()); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("first Close() = %v, want incomplete cleanup", err)
	}
	if _, err := value.App(); err == nil {
		t.Fatal("closing profile remained published")
	}
	if err := value.Close(t.Context()); err != nil {
		t.Fatalf("retry Close(): %v", err)
	}
	if probe.attempts != 2 {
		t.Fatalf("close attempts = %d, want 2", probe.attempts)
	}
}

func TestFactoryRejectsMissingImplementationsAndResults(t *testing.T) {
	var factory Factory
	if _, err := factory.Build(Request{}); err == nil {
		t.Fatal("nil factory was accepted")
	}
	factory = func(Request) (*Profile, error) { return nil, nil }
	if _, err := factory.Build(Request{}); err == nil {
		t.Fatal("nil factory result was accepted")
	}
}

func TestNewRequiresApplicationCapability(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("profile without an application was accepted")
	}
}
