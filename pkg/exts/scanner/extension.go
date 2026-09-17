package scanner

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/resources"
	"github.com/chainreactors/cyber/tools/scan/engine"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	"github.com/chainreactors/sdk/pkg/association"
)

// Config contains only scanner inputs selected by the Profile.
type Config struct {
	Resources resources.Options
	Recon     engine.ReconOptions
}

type Extension struct {
	application *app.App
	config      Config
	loop        agent.Loop
	workDir     string
	logger      telemetry.Logger
	engines     *engine.Set
}

func New(config Config, loop agent.Loop, workDir string, logger telemetry.Logger) *Extension {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Extension{config: config, loop: loop, workDir: workDir, logger: logger}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || scope == nil {
		return fmt.Errorf("scanner extension is not configured")
	}
	application, err := extension.Use[*app.App](scope)
	if err != nil {
		return err
	}
	endpoint, err := extension.Use[egress.Endpoint](scope)
	if err != nil {
		return err
	}
	tools, err := extension.Use[tool.Executor](scope)
	if err != nil {
		return err
	}
	commandRegistry, err := extension.Use[commands.Executor](scope)
	if err != nil {
		return err
	}
	bash, err := extension.Use[*terminaltool.BashTool](scope)
	if err != nil {
		return err
	}
	store, err := extension.Use[*skills.Store](scope)
	if err != nil {
		return err
	}
	e.application = application
	proxyURL := endpoint.ProxyURL()
	if proxyURL == "" {
		proxyURL = e.config.Resources.Proxy
	}
	e.engines = initEngines(scope.Init(), e.config, e.logger)
	values, err := buildScannerCommands(borrowed{
		application: application, tools: tools, commands: commandRegistry, bash: bash, skills: store,
	}, e.engines, e.config, e.loop, e.workDir, proxyURL, e.logger)
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
