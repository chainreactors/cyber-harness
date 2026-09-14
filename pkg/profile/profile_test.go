package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/aiscan/aop"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
	consoleapi "github.com/chainreactors/aiscan/pkg/console/api"
)

type applicationProbe struct{}

func (*applicationProbe) Load(context.Context) error                         { return nil }
func (*applicationProbe) Close(context.Context) error                        { return nil }
func (*applicationProbe) App() (*apppkg.App, error)                          { return nil, nil }
func (*applicationProbe) Runtime() (*agentext.Runtime, error)                { return nil, nil }
func (*applicationProbe) RegisterResourceNamespaces(*aop.NamespaceMux) error { return nil }

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

func (*applicationProbe) AgentStatus() *aop.AgentStatus { return &aop.AgentStatus{} }

func (*applicationProbe) ConsoleBindings() *consoleapi.Bindings { return nil }

func (*applicationProbe) Capabilities() []string {return nil}
