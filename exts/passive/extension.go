package passive

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/tools/files"
	"github.com/chainreactors/cyber/tools/passive"
)

//go:embed passive.md
var docs embed.FS

type Extension struct {
	options passive.Config
	files   *files.Files
}

func New(options passive.Config) *Extension { return &Extension{options: options} }
func (e *Extension) Load(scope *extension.Scope) error {
	logger, err := extension.Use[telemetry.Logger](scope)
	if err != nil {
		return err
	}
	filesCap, err := extension.Use[*files.Files](scope)
	if err != nil {
		return err
	}
	if err = filesCap.Mount("cyber://skills/passive/", docs); err != nil {
		return err
	}
	e.files = filesCap
	backend := passive.NewUncover(e.options)
	logger.Infof("%s", telemetry.StartupOK("uncover", strings.Join(backend.Sources(), ",")))
	if err = extension.Add(scope, passive.NewCommand(backend, logger)); err != nil {
		return fmt.Errorf("register passive: %w", err)
	}
	return nil
}
func (e *Extension) Close(ctx context.Context) error {
	if e.files == nil {
		return nil
	}
	err := e.files.Unmount(ctx, "cyber://skills/passive/")
	if err == nil {
		e.files = nil
	}
	return err
}
