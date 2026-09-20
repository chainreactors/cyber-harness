// Package client installs IOA connections and optional collaboration.
package client

import (
	"context"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	service "github.com/chainreactors/cyber/tools/ioa"
)

// Extension is the sole owner of an IOA connection.
type Extension struct {
	config   service.Config
	resource *service.Resource
}

func New(config service.Config) *Extension {
	return &Extension{config: config}
}
func (e *Extension) Service() *service.Service {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Service
}
func (e *Extension) Load(scope *extension.Scope) error {
	logger, err := extension.Use[*telemetry.LoggerRef](scope)
	if err != nil {
		return err
	}
	e.resource = service.New(e.config, logger)
	if err := e.resource.Start(scope.Init()); err != nil {
		return err
	}
	if err := extension.Provide(scope, e.resource.Service); err != nil {
		return err
	}
	if e.config.RegisterCommands {
		return extension.Add(scope, e.resource.Service.Commands()...)
	}
	return nil
}
func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
