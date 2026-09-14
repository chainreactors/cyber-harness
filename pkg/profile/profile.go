// Package profile defines the lifecycle boundary shared by composition roots.
//
// A Profile is a published extension graph. Product-specific code declares the
// graph at its executable entrypoint; this package only provides atomic
// assembly, publication, and shutdown semantics.
package profile

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
)

// Application is the explicit surface required by application hosts. It keeps
// Web, Runner, and transport packages independent of a concrete product graph.
type Application interface {
	Load(context.Context) error
	Close(context.Context) error
	App() (*apppkg.App, error)
	Runtime() (*sessionext.Manager, error)
	RegisterResourceNamespaces(*aop.NamespaceMux) error
}

// Request contains host-selected inputs to a product composition root. It has
// no extension selection or resource lookup fields; those decisions belong to
// the executable that implements Factory.
type Request struct {
	Option   *cfg.Option
	Features apppkg.RuntimeFeatures
	Runtime  *sessionext.Config
	Logger   telemetry.Logger
}

// Factory constructs an unpublished product graph. The caller owns Load and
// Close, including cleanup of a partially loaded candidate.
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

// IsNil rejects both nil interfaces and interfaces containing typed nil
// pointers. Composition roots use it before publishing a candidate.
func IsNil(value Application) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

// Assembly owns one extension.Set and publishes it atomically after every
// entry has loaded. It deliberately exposes no lookup API: concrete profiles
// retain direct references to the resources they publish.
type Assembly struct {
	set *extension.Set

	mu      sync.RWMutex
	active  bool
	closing bool
}

func Assemble(entries ...extension.Entry) (*Assembly, error) {
	set, err := extension.New(entries...)
	if err != nil {
		return nil, err
	}
	return &Assembly{set: set}, nil
}

func (a *Assembly) Load(ctx context.Context) error {
	if a == nil || a.set == nil {
		return fmt.Errorf("profile assembly is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.RLock()
	closing := a.closing
	a.mu.RUnlock()
	if closing {
		return extension.ErrCloseIncomplete
	}
	if err := a.set.Load(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return extension.ErrCloseIncomplete
	}
	a.active = true
	a.mu.Unlock()
	return nil
}

// Available reports whether the complete graph is published and not closing.
func (a *Assembly) Available() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.active && !a.closing
}

func (a *Assembly) Close(ctx context.Context) error {
	if a == nil || a.set == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	a.closing = true
	a.active = false
	a.mu.Unlock()
	return a.set.Close(ctx)
}
