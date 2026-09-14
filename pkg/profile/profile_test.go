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

type lifecycleProbe struct{ loaded, closed bool }

func (p *lifecycleProbe) Load(*extension.Scope) error { p.loaded = true; return nil }
func (p *lifecycleProbe) Close(context.Context) error { p.closed = true; return nil }

type retryCloseProbe struct{ attempts int }

func (*retryCloseProbe) Load(*extension.Scope) error { return nil }
func (p *retryCloseProbe) Close(context.Context) error {
	p.attempts++
	if p.attempts == 1 {
		return extension.ErrCloseIncomplete
	}
	return nil
}

func TestAssemblyPublishesOnlyLoadedGraph(t *testing.T) {
	probe := &lifecycleProbe{}
	assembly, err := Assemble(extension.Entry{ID: "probe", Extension: probe})
	if err != nil {
		t.Fatal(err)
	}
	if assembly.Available() {
		t.Fatal("unloaded assembly was published")
	}
	if err := assembly.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !assembly.Available() || !probe.loaded {
		t.Fatal("loaded assembly was not published")
	}
	if err := assembly.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if assembly.Available() || !probe.closed {
		t.Fatal("closed assembly remained published")
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
		t.Fatalf("first close = %v, want incomplete", err)
	}
	if assembly.Available() {
		t.Fatal("closing assembly remained published")
	}
	if err := assembly.Close(t.Context()); err != nil {
		t.Fatalf("retry close: %v", err)
	}
	if probe.attempts != 2 {
		t.Fatalf("close attempts = %d, want 2", probe.attempts)
	}
}

func TestIsNilRejectsTypedNil(t *testing.T) {
	var value *applicationProbe
	if !IsNil(value) {
		t.Fatal("typed nil application was accepted")
	}
}

type applicationProbe struct{}

func (*applicationProbe) Load(context.Context) error                         { return nil }
func (*applicationProbe) Close(context.Context) error                        { return nil }
func (*applicationProbe) App() (*apppkg.App, error)                          { return nil, nil }
func (*applicationProbe) Runtime() (*sessionext.Manager, error)              { return nil, nil }
func (*applicationProbe) RegisterResourceNamespaces(*aop.NamespaceMux) error { return nil }
