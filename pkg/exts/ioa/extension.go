package ioa

import (
	"context"
	"errors"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	service "github.com/chainreactors/aiscan/tools/ioa"
)

// Extension adapts the IOA service to the common Set lifecycle.
type Extension struct {
	*service.Service
}

func New(config service.Config, commands *commands.Registry, logger telemetry.Logger) *Extension {
	return &Extension{Service: service.New(config, commands, logger)}
}

func (e *Extension) Load(scope *extension.Context) error { return e.Service.Start(scope.Init()) }
func (e *Extension) Close(ctx context.Context) error {
	err := e.Service.Close(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(extension.ErrCloseIncomplete, err)
	}
	return err
}

var _ extension.Extension = (*Extension)(nil)
