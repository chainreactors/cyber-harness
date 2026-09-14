package scanner

import (
	"context"
	"fmt"
	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	"sync"
)

type Extension struct {
	mu          sync.Mutex
	commands    *commands.Registry
	application func() *app.App
	appConfig   app.Config
	loop        agent.Loop
	workDir     string
	proxyURL    string
	logger      telemetry.Logger
	engines     *engine.Set
	ready       chan struct{}
	readyOnce   sync.Once
	err         error
	initialized bool
}

func New(application func() *app.App, commands *commands.Registry, config app.Config, loop agent.Loop, workDir, proxyURL string, logger telemetry.Logger) *Extension {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Extension{application: application, commands: commands, appConfig: config, loop: loop, workDir: workDir, proxyURL: proxyURL, logger: logger, ready: make(chan struct{})}
}

func (e *Extension) Load(scope *extension.Scope) (err error) {
	if e == nil || e.application == nil || e.commands == nil || scope == nil {
		return fmt.Errorf("scanner extension is not configured")
	}
	defer func() {
		e.mu.Lock()
		e.err = err
		e.mu.Unlock()
		e.readyOnce.Do(func() { close(e.ready) })
	}()
	e.engines = initEngines(scope.Init(), e.appConfig.Scanner, e.logger)
	e.mu.Lock()
	e.initialized = e.engines != nil
	e.mu.Unlock()
	values, err := buildScannerCommands(e.application(), e.engines, e.appConfig, e.loop, e.workDir, e.proxyURL, e.logger)
	if err != nil || len(values) == 0 {
		return err
	}
	return e.commands.Register("scanner", "scanner", values...)
}

func (e *Extension) Close(context.Context) error {
	if e == nil {
		return nil
	}
	if e.engines != nil {
		e.engines.Close()
		e.engines = nil
	}
	e.mu.Lock()
	e.initialized = false
	e.mu.Unlock()
	return nil
}

func (e *Extension) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-e.ready:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Extension) State() string {
	select {
	case <-e.ready:
		e.mu.Lock()
		initialized := e.initialized
		e.mu.Unlock()
		if !initialized {
			return "failed"
		}
	default:
		return "loading"
	}
	if len(e.commands.GroupNames("scanner")) > 0 {
		return "ready"
	}
	return "degraded"
}

func initEngines(ctx context.Context, config app.ScannerConfig, logger telemetry.Logger) *engine.Set {
	engines, err := engine.InitWithOptions(ctx, resources.Options{
		CyberhubURL: config.CyberhubURL,
		APIKey:      config.CyberhubKey,
		Mode:        config.CyberhubMode,
		Proxy:       config.Proxy,
	}, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		return nil
	}
	engines.SetupUncover(engine.ReconOptions{
		FofaKey:      config.FofaKey,
		HunterAPIKey: config.HunterAPIKey,
		IngressProxy: config.ReconProxy,
		Limit:        config.ReconLimit,
		Credentials:  config.UncoverCredentials,
	}, logger)
	return engines
}

var _ extension.Extension = (*Extension)(nil)
var _ app.Scanner = (*Extension)(nil)
