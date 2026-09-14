package provider

import (
	"context"
	"fmt"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
)

type Extension struct {
	release func()
	state   *provider.State
	config  provider.StartupConfig
	logger  telemetry.Logger
}

func New(state *provider.State, config provider.StartupConfig, logger telemetry.Logger) (*Extension, error) {
	if state == nil {
		return nil, fmt.Errorf("provider state is required")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	config.Fallbacks = append([]provider.ProviderConfig(nil), config.Fallbacks...)
	return &Extension{state: state, config: config, logger: logger}, nil
}
func (e *Extension) Load(scope *extension.Scope) error {
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
