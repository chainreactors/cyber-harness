// Package provider adapts the LLM provider State to Extension lifecycle. It is
// unrelated to declaration aggregation; CLI and Config register through typed
// resource Points directly.
package provider

import (
	"context"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
)

type Extension struct {
	release func()
	state   *provider.State
	config  provider.StartupConfig
	logger  telemetry.Logger
}

func New(config provider.StartupConfig, logger telemetry.Logger) *Extension {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	config.Fallbacks = append([]provider.ProviderConfig(nil), config.Fallbacks...)
	return &Extension{config: config, logger: logger}
}

// Load initializes the provider state the application owns. The state lives on
// App rather than here because the console and the session runtime read it
// directly; this extension only drives its startup and shutdown.
func (e *Extension) Load(scope *extension.Scope) error {
	application, err := extension.Use[*apppkg.App](scope)
	if err != nil {
		return err
	}
	e.state = &application.Providers
	release, err := provider.Initialize(scope.Init(), e.state, e.config, e.logger)
	e.release = release
	return err
}
func (e *Extension) Close(context.Context) error {
	if e.release != nil {
		e.release()
		e.release = nil
	}
	return nil
}
