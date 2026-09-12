// Package aiscan is the AIScan composition root. It constructs a fixed extension
// graph; edition capabilities select the application-owned tool extensions.
package aiscan

import (
	"context"
	"fmt"
	"os"
	"sync"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
	proxytool "github.com/chainreactors/aiscan/tools/proxy"
)

const (
	RecorderID    = "aiscan.recorder"
	FileAuditID   = "aiscan.file-audit"
	ProxyID       = "aiscan.proxy"
	ApplicationID = "aiscan.application"
	IOAID         = "aiscan.ioa"
	RuntimeID     = "aiscan.runtime"
)

type Config struct {
	Option      *cfg.Option
	Application apppkg.Config
	IOA         *ioatools.Config
	// Runtime nil creates the application-only profile used by the Web service.
	Runtime *runtimepkg.RuntimeConfig
	Logger  telemetry.Logger
}

func FromOption(option *cfg.Option, features apppkg.RuntimeFeatures, runtimeConfig *runtimepkg.RuntimeConfig, logger telemetry.Logger) Config {
	application := apppkg.AppConfig(option, features, logger)
	return Config{
		Option: option, Application: application,
		IOA: ioatools.ConfigFromOption(option), Runtime: cloneRuntimeConfig(runtimeConfig), Logger: logger,
	}
}

// Profile owns one fixed graph through Set. The app and runtime references are
// borrowed public entry points; Set alone owns their Load/Close ordering.
type Profile struct {
	set    *extension.Set
	access sync.RWMutex
	// Publication state prevents exposing a partially loaded or closing graph.
	// It does not track individual instance lifetimes; Set owns those states.
	active  bool
	closing bool
	app     *apppkg.App
	runtime *runtimepkg.AgentRuntime
}

func New(config Config) (*Profile, error) {
	if config.Option == nil {
		return nil, fmt.Errorf("aiscan profile option is required")
	}
	if config.Logger == nil {
		config.Logger = telemetry.NopLogger()
	}
	config.Runtime = cloneRuntimeConfig(config.Runtime)
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve AIScan working directory: %w", err)
	}
	capture := config.Application.Tools.MitmCapture == nil || *config.Application.Tools.MitmCapture
	proxyHub, err := proxytool.NewHub(workDir, config.Application.Scanner.Proxy, capture, config.Application.Tools.TrafficStorage)
	if err != nil {
		return nil, fmt.Errorf("construct proxy infrastructure: %w", err)
	}
	audit := fileaudit.New()
	application := apppkg.New(config.Application, audit, proxyHub)
	recorder, err := output.NewJSONLRecorder(application.EventBus, config.Application.RecordFile)
	if err != nil {
		return nil, err
	}
	application.Recorder = recorder

	entries := []extension.Entry{
		{ID: RecorderID, Extension: recorder},
		{ID: FileAuditID, Extension: audit},
		{ID: ProxyID, Extension: proxyHub},
		{ID: ApplicationID, DependsOn: []string{FileAuditID, ProxyID, RecorderID}, Extension: application},
	}
	var ioa *ioatools.Extension
	if config.IOA != nil {
		ioa = ioatools.New(*config.IOA, application.Commands, config.Logger)
		entries = append(entries, extension.Entry{
			ID: IOAID, DependsOn: []string{ApplicationID},
			Extension: ioa,
		})
	}
	var run *runtimepkg.AgentRuntime
	if config.Runtime != nil {
		run, err = runtimepkg.New(application, ioa, config.Option, config.Logger, *config.Runtime)
		if err != nil {
			return nil, fmt.Errorf("construct AIScan runtime: %w", err)
		}
		dependencies := []string{ApplicationID}
		if ioa != nil {
			dependencies = append(dependencies, IOAID)
		}
		entries = append(entries, extension.Entry{
			ID: RuntimeID, DependsOn: dependencies,
			Extension: run,
		})
	}
	set, err := extension.New(entries...)
	if err != nil {
		return nil, err
	}
	return &Profile{set: set, app: application, runtime: run}, nil
}

func cloneRuntimeConfig(config *runtimepkg.RuntimeConfig) *runtimepkg.RuntimeConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	if config.PromptConfig != nil {
		prompt := *config.PromptConfig
		prompt.LoadedSkills = append([]runtimepkg.LoadedSkill(nil), config.PromptConfig.LoadedSkills...)
		cloned.PromptConfig = &prompt
	}
	return &cloned
}

func (p *Profile) Load(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("aiscan profile is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.set.Load(ctx); err != nil {
		return err
	}
	p.access.Lock()
	if !p.closing {
		p.active = true
	}
	p.access.Unlock()
	return nil
}

func (p *Profile) App() (*apppkg.App, error) {
	if p == nil {
		return nil, fmt.Errorf("aiscan profile is required")
	}
	p.access.RLock()
	defer p.access.RUnlock()
	if !p.active || p.closing || p.app == nil {
		return nil, fmt.Errorf("aiscan profile is not active")
	}
	return p.app, nil
}

func (p *Profile) Runtime() (*runtimepkg.AgentRuntime, error) {
	if p == nil {
		return nil, fmt.Errorf("aiscan profile is required")
	}
	p.access.RLock()
	defer p.access.RUnlock()
	if !p.active || p.closing {
		return nil, fmt.Errorf("aiscan profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("aiscan profile has no Agent runtime")
	}
	return p.runtime, nil
}

func (p *Profile) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.access.Lock()
	p.closing = true
	p.active = false
	p.access.Unlock()
	return p.set.Close(ctx)
}
