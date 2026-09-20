// Package profile defines the lifecycle boundary shared by Cyber hosts.
// Composition belongs to the concrete distribution that implements Profile
// and owns its extension.Set.
package profile

import (
	"context"

	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
)

// Profile is the complete runtime surface published by a composition root.
// Load must publish nothing until the whole graph is active.
type Profile interface {
	Load(context.Context) error
	Close(context.Context) error
	State() (*apppkg.State, error)
	Runtime() (*agentsession.Runtime, error)
	RegisterNamespaces(*aop.NamespaceMux) error
	AgentStatus() *aop.AgentStatus
	// ConsoleBindings publishes optional presentation contributions.
	ConsoleBindings() *consoleapi.Bindings
}

// Request contains host-selected inputs. Extension selection and resource
// construction remain decisions of the concrete distribution constructor.
type Request struct {
	Option       *cfg.Option
	ProviderMode ProviderMode
	Session      *agentsession.Config
	Logger       telemetry.Logger
}

// ProviderMode is the host's provider requirement for one profile graph and the
// mode consumed directly by provider startup.
type ProviderMode = provider.StartupMode

const (
	ProviderDisabled = provider.StartupDisabled
	ProviderRequired = provider.StartupRequired
	ProviderOptional = provider.StartupOptional
)
