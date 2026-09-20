// Package provider adapts the LLM provider State to Extension lifecycle. It is
// unrelated to declaration aggregation; CLI and Config register through typed
// resource Points directly.
package provider

import (
	"context"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
)

type Extension struct {
	release func()
	state   *provider.State
	config  provider.StartupConfig
}

func New(config provider.StartupConfig) *Extension {
	config.Fallbacks = append([]provider.ProviderConfig(nil), config.Fallbacks...)
	return &Extension{config: config}
}

// Load owns and publishes the provider state for this installation.
func (e *Extension) Load(scope *extension.Scope) error {
	e.state = &provider.State{}
	logger, err := extension.Use[telemetry.Logger](scope)
	if err != nil {
		return err
	}
	release, err := provider.Initialize(scope.Init(), e.state, e.config, logger)
	e.release = release
	if err != nil {
		return err
	}
	return extension.Provide[*provider.State](scope, e.state)
}
func (e *Extension) Close(context.Context) error {
	if e.release != nil {
		e.release()
		e.release = nil
	}
	return nil
}
