// Package profile publishes one fully assembled AIScan extension graph to
// reusable hosts. Product composition remains in the executable.
package profile

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
)

// Request contains host-selected inputs to the product composition root.
type Request struct {
	Option   *cfg.Option
	Features apppkg.RuntimeFeatures
	Session  *sessionext.Config
	Logger   telemetry.Logger
}

// Factory constructs an unpublished Profile. The caller owns every non-nil
// result, including cleanup after a construction or loading error.
type Factory func(Request) (*Profile, error)

func (f Factory) Build(request Request) (*Profile, error) {
	if f == nil {
		return nil, fmt.Errorf("profile factory is required")
	}
	value, err := f(request)
	if value == nil && err == nil {
		return nil, fmt.Errorf("profile factory returned nil")
	}
	return value, err
}

// Config binds host capabilities to the same graph that owns them. Entries
// are declared by the executable; New only validates and seals their order.
type Config struct {
	Entries                    []extension.Entry
	App                        *apppkg.App
	Sessions                   *sessionext.Runtime
	RegisterResourceNamespaces func(*aop.NamespaceMux) error
}

// Profile adds no lifecycle state. extension.Set is the sole activation,
// publication and shutdown authority for all retained capabilities.
type Profile struct {
	extensions                 *extension.Set
	app                        *apppkg.App
	sessions                   *sessionext.Runtime
	registerResourceNamespaces func(*aop.NamespaceMux) error
}

func New(config Config) (*Profile, error) {
	if config.App == nil {
		return nil, fmt.Errorf("profile application is required")
	}
	set, err := extension.New(config.Entries...)
	if err != nil {
		return nil, err
	}
	return &Profile{
		extensions:                 set,
		app:                        config.App,
		sessions:                   config.Sessions,
		registerResourceNamespaces: config.RegisterResourceNamespaces,
	}, nil
}

func (p *Profile) Load(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return fmt.Errorf("profile is required")
	}
	return p.extensions.Load(ctx)
}

func (p *Profile) App() (*apppkg.App, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.app == nil {
		return nil, fmt.Errorf("profile is not active")
	}
	return p.app, nil
}

func (p *Profile) Sessions() (*sessionext.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil, fmt.Errorf("profile is not active")
	}
	if p.sessions == nil {
		return nil, fmt.Errorf("profile has no session runtime")
	}
	return p.sessions, nil
}

func (p *Profile) RegisterResourceNamespaces(mux *aop.NamespaceMux) error {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return fmt.Errorf("profile is not active")
	}
	if p.registerResourceNamespaces == nil {
		return nil
	}
	return p.registerResourceNamespaces(mux)
}

func (p *Profile) Close(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return nil
	}
	return p.extensions.Close(ctx)
}
