// Package profile defines the lifecycle boundary shared by AIScan hosts.
// Product-specific composition and capabilities belong to the executable that
// implements Application; this package only validates factories and assembles
// an extension graph.
package profile

import (
	"context"
	"fmt"
	"reflect"

	"github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
)

// Application is the complete capability surface published by a product
// composition root. Load must publish nothing until the whole graph is active.
type Application interface {
	Load(context.Context) error
	Close(context.Context) error
	App() (*apppkg.App, error)
	Runtime() (*agentext.Runtime, error)
	RegisterResourceNamespaces(*aop.NamespaceMux) error
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
	if err == nil && IsNil(value) {
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

// Assembly owns one fixed extension graph. core/extension.Set remains the only
// lifecycle state machine; Assembly deliberately exposes no product resources.
type Assembly struct {
	extensions *extension.Set
}

func Assemble(entries ...extension.Entry) (*Assembly, error) {
	set, err := extension.New(entries...)
	if err != nil {
		return nil, err
	}
	return &Assembly{extensions: set}, nil
}

func (a *Assembly) Load(ctx context.Context) error {
	if a == nil || a.extensions == nil {
		return fmt.Errorf("profile assembly is required")
	}
	return a.extensions.Load(ctx)
}

// Available reports whether the complete graph has been loaded and published.
func (a *Assembly) Available() bool {
	return a != nil && a.extensions != nil && a.extensions.Active()
}

func (a *Assembly) Close(ctx context.Context) error {
	if a == nil || a.extensions == nil {
		return nil
	}
	return a.extensions.Close(ctx)
}
