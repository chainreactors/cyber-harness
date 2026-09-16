// Package profile defines the lifecycle boundary shared by Cyber hosts.
// Product-specific composition belongs to the executable that implements
// Application and owns its extension.Set; this package validates product
// factories without adding another lifecycle wrapper.
package profile

import (
	"context"
	"fmt"
	"reflect"

	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
)

// Application is the complete runtime surface published by a product
// composition root. Load must publish nothing until the whole graph is active.
type Application interface {
	Load(context.Context) error
	Close(context.Context) error
	App() (*apppkg.App, error)
	Runtime() (*agentsession.Runtime, error)
	RegisterNamespaces(*aop.NamespaceMux) error
	AgentStatus() *aop.AgentStatus
	// ConsoleBindings publishes optional presentation contributions.
	ConsoleBindings() *consoleapi.Bindings
}

// Request contains host-selected inputs. Extension selection and resource
// construction remain decisions of the product Factory.
type Request struct {
	Option       *cfg.Option
	ProviderMode ProviderMode
	Runtime      *agentsession.Config
	Logger       telemetry.Logger
}

// ProviderMode is the host's provider requirement for one product graph and
// the mode consumed directly by provider startup.
type ProviderMode = provider.StartupMode

const (
	ProviderDisabled = provider.StartupDisabled
	ProviderRequired = provider.StartupRequired
	ProviderOptional = provider.StartupOptional
)

// Factory constructs an unpublished product graph. The caller owns every
// non-nil result, including cleanup when construction or loading fails.
type Factory func(Request) (Application, error)

func (f Factory) Build(request Request) (Application, error) {
	switch request.ProviderMode {
	case ProviderDisabled, ProviderRequired, ProviderOptional:
	default:
		return nil, fmt.Errorf("invalid provider mode %d", request.ProviderMode)
	}
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
