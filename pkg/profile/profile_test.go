package profile

import (
	"context"
	"errors"
	"testing"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
)

type applicationProbe struct{}

func (*applicationProbe) Load(context.Context) error                 { return nil }
func (*applicationProbe) Close(context.Context) error                { return nil }
func (*applicationProbe) App() (*apppkg.App, error)                  { return nil, nil }
func (*applicationProbe) Runtime() (*agentsession.Runtime, error)    { return nil, nil }
func (*applicationProbe) RegisterNamespaces(*aop.NamespaceMux) error { return nil }

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
	want := errors.New("construction failed")
	factory = func(Request) (Application, error) {
		var value *applicationProbe
		return value, want
	}
	value, err := factory.Build(Request{})
	if value != nil || !errors.Is(err, want) {
		t.Fatalf("typed nil failure = %#v, %v; want nil, %v", value, err, want)
	}
}

func TestFactoryRejectsUnknownProviderMode(t *testing.T) {
	factory := Factory(func(Request) (Application, error) { return &applicationProbe{}, nil })
	if _, err := factory.Build(Request{ProviderMode: ProviderMode(99)}); err == nil {
		t.Fatal("unknown provider mode was accepted")
	}
}

func (*applicationProbe) AgentStatus() *aop.AgentStatus { return &aop.AgentStatus{} }

func (*applicationProbe) ConsoleBindings() *consoleapi.Bindings { return nil }
