// Package profile defines the lifecycle boundary shared by Cyber hosts.
// Product-specific composition and capabilities belong to the executable that
// implements Application and owns its extension.Set; this package validates
// product factories without adding another lifecycle wrapper.
package profile

import (
	"context"
	"fmt"
	"reflect"

	"github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	agentext "github.com/chainreactors/cyber/pkg/exts/session"
)

// Application is the complete capability surface published by a product
// composition root. Load must publish nothing until the whole graph is active.
type Application interface {
	Load(context.Context) error
	Close(context.Context) error
	App() (*apppkg.App, error)
	Runtime() (*agentext.Runtime, error)
	RegisterResourceNamespaces(*aop.NamespaceMux) error
	AgentStatus() *aop.AgentStatus
	Capabilities() []string
	// ConsoleBindings publishes optional presentation contributions.
	ConsoleBindings() *consoleapi.Bindings
}

// Request contains host-selected inputs. Extension selection and resource
// construction remain decisions of the product Factory.
type Request struct {
	Option   *cfg.Option
	Features apppkg.RuntimeFeatures
	Runtime  *agentext.Config
	Logger   telemetry.Logger
}

// Factory constructs an unpublished product graph. The caller owns every
// non-nil result, including cleanup when construction or loading fails.
type Factory func(Request) (Application, error)

func (f Factory) Build(request Request) (Application, error) {
	if f == nil {
		return nil, fmt.Errorf("profile factory is required")
	}
	value, err := f(request)
	if IsNil(value) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("profile factory returned nil")
	}
	return value, err
}

// IsNil recognizes nil interface values and typed nil implementations.
func IsNil(value Application) bool {
	if value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
