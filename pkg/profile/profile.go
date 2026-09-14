// Package profile is the host-facing view of one AIScan extension graph.
// Product composition remains in the executable; this package only couples the
// loaded graph to the capabilities required by reusable hosts.
package profile

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
)

// Request contains host-selected inputs to a product composition root. It has
// no extension selection or resource lookup fields; those decisions belong to
// the executable implementing Factory.
type Request struct {
	Option   *cfg.Option
	Features apppkg.RuntimeFeatures
	Runtime  *agentext.Config
	Logger   telemetry.Logger
}

// Factory constructs an unpublished product graph. The caller owns Load and
// Close, including cleanup of a partially loaded candidate.
type Factory func(Request) (*Profile, error)

func (f Factory) Build(request Request) (*Profile, error) {
	if f == nil {
		return nil, fmt.Errorf("profile factory is required")
	}
	value, err := f(request)
	if err == nil && value == nil {
		return nil, fmt.Errorf("profile factory returned nil")
	}
	return value, err
}

// Config binds a graph to the capabilities published by that graph. Entries
// are constructed by the executable composition root; New only validates and
// seals their dependency order.
type Config struct {
	Entries                    []extension.Entry
	App                        *apppkg.App
	Runtime                    *agentext.Runtime
	RegisterResourceNamespaces func(*aop.NamespaceMux) error
}

// Profile owns one fixed extension graph and publishes its host capabilities
// only while the complete graph is active. It adds no lifecycle state of its
// own; core/extension.Set is the sole state machine.
type Profile struct {
	extensions                 *extension.Set
	app                        *apppkg.App
	runtime                    *agentext.Runtime
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
		runtime:                    config.Runtime,
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

func (p *Profile) Runtime() (*agentext.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil, fmt.Errorf("profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("profile has no Agent runtime")
	}
	return p.runtime, nil
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
