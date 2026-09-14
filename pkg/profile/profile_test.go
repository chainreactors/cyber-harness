package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/extension"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
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

type applicationProbe struct{}

func (*applicationProbe) Load(context.Context) error                         { return nil }
func (*applicationProbe) Close(context.Context) error                        { return nil }
func (*applicationProbe) App() (*apppkg.App, error)                          { return nil, nil }
func (*applicationProbe) Runtime() (*agentext.Runtime, error)                { return nil, nil }
func (*applicationProbe) RegisterResourceNamespaces(*aop.NamespaceMux) error { return nil }

func TestAssemblyPublishesOnlyCompleteGraph(t *testing.T) {
	application := apppkg.New(apppkg.Config{SkipEngines: true}, apppkg.Dependencies{})
	assembly, err := Assemble(extension.Entry{ID: "application", Extension: application})
	if err != nil {
		t.Fatal(err)
	}
	if assembly.Available() {
		t.Fatal("unloaded assembly is available")
	}
	if err := assembly.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !assembly.Available() {
		t.Fatal("loaded assembly is unavailable")
	}
	if err := assembly.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if assembly.Available() {
		t.Fatal("closed assembly remained available")
	}
	if !application.App.Closed() {
		t.Fatal("assembly did not close its application resource")
	}
}

func TestAssemblyKeepsIncompleteCloseRetryable(t *testing.T) {
	probe := &retryCloseProbe{}
	assembly, err := Assemble(extension.Entry{ID: "probe", Extension: probe})
	if err != nil {
		t.Fatal(err)
	}
	if err := assembly.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := assembly.Close(t.Context()); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("first Close() = %v, want incomplete cleanup", err)
	}
	if assembly.Available() {
		t.Fatal("closing assembly remained available")
	}
	if err := assembly.Close(t.Context()); err != nil {
		t.Fatalf("retry Close(): %v", err)
	}
	if probe.attempts != 2 {
		t.Fatalf("close attempts = %d, want 2", probe.attempts)
	}
}

func TestFactoryRejectsNilImplementations(t *testing.T) {
	var factory Factory
	if _, err := factory.Build(Request{}); err == nil {
		t.Fatal("nil factory was accepted")
	}
	factory = func(Request) (Application, error) { return nil, nil }
	if _, err := factory.Build(Request{}); err == nil {
		t.Fatal("nil factory result was accepted")
	}
	factory = func(Request) (Application, error) {
		var value *applicationProbe
		return value, nil
	}
	if _, err := factory.Build(Request{}); err == nil {
		t.Fatal("typed nil factory result was accepted")
	}
}
