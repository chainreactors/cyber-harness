package ioa

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	service "github.com/chainreactors/aiscan/tools/ioa"
)

// Extension adapts the IOA service to the common Set lifecycle.
type Extension struct {
	resource         *service.Resource
	commands         *commands.Registry
	registerCommands bool
}

func New(config service.Config, registry *commands.Registry, logger telemetry.Logger) (*Extension, error) {
	if config.RegisterCommands && registry == nil {
		return nil, fmt.Errorf("IOA command registration requires a command registry")
	}
	value := service.New(config, logger)
	return &Extension{resource: value, commands: registry, registerCommands: config.RegisterCommands}, nil
}

func (e *Extension) Runtime() *service.Runtime {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Runtime
}

func (e *Extension) Load(scope *extension.Scope) error {
	if err := e.resource.Start(scope.Init()); err != nil {
		return err
	}
	if e.registerCommands {
		values := e.resource.Commands()
		if len(values) > 0 {
			return e.commands.Register(scope, "ioa", values...)
		}
	}
	return nil
}
func (e *Extension) Close(ctx context.Context) error {
	return e.resource.Close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
