package scanner

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resources"
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/sdk/pkg/association"
	"path/filepath"
	"sync"
)

// Config contains only scanner inputs selected by the product profile.
type Config struct {
	DataDir      string
	Scanner      app.ScannerConfig
	Capabilities capability.Catalog
}

type Extension struct {
	mu          sync.Mutex
	commands    commands.Runtime
	application func() *app.App
	config      Config
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

func New(application func() *app.App, commands commands.Runtime, config Config, loop agent.Loop, workDir, proxyURL string, logger telemetry.Logger) *Extension {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Extension{application: application, commands: commands, config: config, loop: loop, workDir: workDir, proxyURL: proxyURL, logger: logger, ready: make(chan struct{})}
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
	e.engines = initEngines(scope.Init(), e.config.DataDir, e.config.Scanner, e.logger)
	e.mu.Lock()
	e.initialized = e.engines != nil
	e.mu.Unlock()
	application := e.application()
	if application == nil {
		return fmt.Errorf("scanner application is required")
	}
	values, err := buildScannerCommands(application, e.engines, e.config, e.loop, e.workDir, e.proxyURL, e.logger)
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

func initEngines(ctx context.Context, dataDir string, config app.ScannerConfig, logger telemetry.Logger) *engine.Set {
	engines, err := engine.InitWithOptions(ctx, resources.Options{
		CyberhubURL: config.CyberhubURL,
		CacheDir:    filepath.Join(dataDir, "cache"),
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

// Index is borrowed by search after scanner initialization; its registry must
// drain before this extension closes the owning engines.
func (e *Extension) Index() *association.Index {
	if e == nil || e.engines == nil {
		return nil
	}
	return e.engines.Index
}
