package scanner

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resources"
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/sdk/pkg/association"
)

// Config contains only scanner inputs selected by the product profile.
type Config struct {
	Resources resources.Options
	Recon     engine.ReconOptions
}

type Extension struct {
	application *app.App
	config      Config
	loop        agent.Loop
	workDir     string
	proxyURL    string
	logger      telemetry.Logger
	engines     *engine.Set
}

func New(application *app.App, config Config, loop agent.Loop, workDir, proxyURL string, logger telemetry.Logger) *Extension {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Extension{application: application, config: config, loop: loop, workDir: workDir, proxyURL: proxyURL, logger: logger}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.application == nil || e.application.Commands == nil || scope == nil {
		return fmt.Errorf("scanner extension is not configured")
	}
	e.engines = initEngines(scope.Init(), e.config, e.logger)
	values, err := buildScannerCommands(e.application, e.engines, e.config, e.loop, e.workDir, e.proxyURL, e.logger)
	if err != nil || len(values) == 0 {
		return err
	}
	return extension.Add(scope, values...)
}

func (e *Extension) Close(context.Context) error {
	if e == nil {
		return nil
	}
	if e.engines != nil {
		e.engines.Close()
		e.engines = nil
	}
	return nil
}

func initEngines(ctx context.Context, config Config, logger telemetry.Logger) *engine.Set {
	engines, err := engine.InitWithOptions(ctx, config.Resources, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		return nil
	}
	engines.SetupUncover(config.Recon, logger)
	return engines
}

var _ extension.Extension = (*Extension)(nil)

// Index is borrowed by search after scanner initialization; its registry must
// drain before this extension closes the owning engines.
func (e *Extension) Index() *association.Index {
	if e == nil || e.engines == nil {
		return nil
	}
	return e.engines.Index
}
