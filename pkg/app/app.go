package app

import (
	"context"
	"fmt"
	"os"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/scanner/engines"
	"github.com/chainreactors/aiscan/pkg/scanner/resources"
	"github.com/chainreactors/aiscan/pkg/scanner/scan"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tool"
	"github.com/chainreactors/aiscan/skills"
	acpclient "github.com/chainreactors/ioa/client"
)

type Config struct {
	Provider ProviderConfig
	Vision   ProviderConfig
	Scanner  ScannerConfig
	Tools    ToolConfig
	ACP      *ACPConfig
	Logger   telemetry.Logger
}

type ProviderConfig struct {
	Enabled  bool
	Config   provider.ProviderConfig
	Optional bool
}

type ScannerConfig struct {
	CyberhubURL         string
	CyberhubKey         string
	CyberhubMode        string
	VerificationEnabled bool
	VerifyMinPriority   string
	VerifyTimeout       int
	VerifySystemPrompt  string
}

type ToolConfig struct {
	Enabled       bool
	BashTimeout   int
	VisionEnabled bool
}

type ACPConfig struct {
	URL           string
	NodeID        string
	NodeName      string
	RegisterTools bool
	AutoRegister  bool
	NodeMeta      map[string]any
}

type App struct {
	Provider         provider.Provider
	ProviderConfig   provider.ProviderConfig
	VisionConfig     provider.ProviderConfig
	Commands         *command.CommandRegistry
	Engines          *engines.Set
	Tools            *tool.ToolRegistry
	Skills           *skills.Store
	SkillDiagnostics []skills.Diagnostic
	ACPClient        acpclient.API
	ACPStreamClient  acpclient.StreamAPI
}

func New(ctx context.Context, cfg Config) (*App, error) {
	app := &App{}
	logger := cfg.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}

	store, diagnostics := skills.LoadEmbeddedStore()
	app.Skills = store
	app.SkillDiagnostics = diagnostics

	if cfg.Provider.Enabled {
		llmProvider, resolved, err := initProvider(cfg.Provider.Config, logger)
		if err != nil {
			if !cfg.Provider.Optional {
				return nil, err
			}
			logger.Debugf("provider not configured: %s", err)
		} else {
			app.Provider = llmProvider
			app.ProviderConfig = *resolved
		}
	}

	app.Engines = initEngines(ctx, cfg.Scanner, logger)
	app.Commands = initCommandRegistry(app.Engines, cfg.Scanner, app.Provider, app.ProviderConfig.Model, logger)

	var visionCfg *provider.ProviderConfig
	if cfg.Tools.Enabled && cfg.Tools.VisionEnabled && cfg.Vision.Enabled {
		resolved, err := provider.Resolve(&cfg.Vision.Config)
		if err != nil {
			if !cfg.Vision.Optional {
				return nil, fmt.Errorf("vision provider: %w", err)
			}
			logger.Debugf("vision provider not configured: %s", err)
		} else {
			app.VisionConfig = *resolved
			visionCfg = &app.VisionConfig
		}
	} else if cfg.Tools.Enabled && cfg.Tools.VisionEnabled && app.Provider != nil {
		visionCfg = &app.ProviderConfig
	}

	if cfg.Tools.Enabled {
		app.Tools = initToolRegistry(cfg.Tools, app.Skills, app.Commands, app.Engines, visionCfg)
	}

	if cfg.ACP != nil {
		if err := app.InitACP(ctx, *cfg.ACP); err != nil {
			app.Close()
			return nil, err
		}
	}

	return app, nil
}

func (a *App) Close() {
	if a == nil {
		return
	}
	if a.Tools != nil {
		for _, t := range a.Tools.All() {
			if closer, ok := t.(interface{ Close() }); ok {
				closer.Close()
			}
		}
	}
	if a.Engines != nil {
		a.Engines.Close()
	}
}

func initProvider(cfg provider.ProviderConfig, logger telemetry.Logger) (provider.Provider, *provider.ProviderConfig, error) {
	resolved, err := provider.Resolve(&cfg)
	if err != nil {
		return nil, nil, err
	}
	logger.Infof("provider init provider=%s model=%s", resolved.Provider, resolved.Model)
	llmProvider, err := provider.NewProviderFromResolved(resolved)
	if err != nil {
		return nil, nil, err
	}
	return llmProvider, resolved, nil
}

func initEngines(ctx context.Context, cfg ScannerConfig, logger telemetry.Logger) *engines.Set {
	engineSet, err := engines.InitWithOptions(ctx, resources.Options{
		CyberhubURL: cfg.CyberhubURL,
		APIKey:      cfg.CyberhubKey,
		Mode:        cfg.CyberhubMode,
	}, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		return nil
	}
	return engineSet
}

func initCommandRegistry(engineSet *engines.Set, cfg ScannerConfig, llmProvider provider.Provider, model string, logger telemetry.Logger) *command.CommandRegistry {
	cmdReg := command.NewRegistry()

	var scanOpts []any
	if cfg.VerificationEnabled && llmProvider != nil {
		p := llmProvider
		scanOpts = append(scanOpts, scan.WithVerifyFunc(func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error) {
			return agent.Run(ctx, prompt, tool.NewToolRegistry(),
				agent.WithProvider(p),
				agent.WithModel(model),
				agent.WithMaxTokens(maxTokens),
				agent.WithSystemPrompt(systemPrompt),
				agent.WithLogger(telemetry.NopLogger()),
			)
		}))
		scanOpts = append(scanOpts, scan.WithVerificationConfig(scanVerificationConfig(cfg, model)))
	}
	scanOpts = append(scanOpts, scan.WithLogger(logger))

	deps := &command.Deps{
		EngineSet: engineSet,
		ScanOpts:  scanOpts,
		Logger:    logger,
		Model:     model,
	}
	if engineSet != nil {
		deps.Resources = engineSet.Resources
	}

	for _, gc := range command.BuildAll(deps) {
		cmdReg.Register(gc.Cmd, gc.Group)
	}

	logger.Infof("commands=%s", fmt.Sprintf("%v", cmdReg.Names()))
	return cmdReg
}

func initToolRegistry(cfg ToolConfig, skillStore *skills.Store, cmdReg *command.CommandRegistry, engineSet *engines.Set, providerCfg *provider.ProviderConfig) *tool.ToolRegistry {
	workDir, _ := os.Getwd()
	timeout := cfg.BashTimeout
	if timeout <= 0 {
		timeout = 300
	}
	toolReg := tool.NewToolRegistry()
	toolReg.Register(tool.NewReadTool(workDir, skillStore))
	toolReg.Register(tool.NewWriteTool(workDir))
	toolReg.Register(tool.NewGlobTool(workDir))
	toolReg.Register(tool.NewBashTool(workDir, timeout, cmdReg))
	toolReg.Register(tool.NewWebSearchTool())
	toolReg.Register(tool.NewWebFetchTool())
	if engineSet != nil && engineSet.Resources != nil {
		toolReg.Register(tool.NewCyberhubSearchTool(engineSet.Resources))
	}
	if cfg.VisionEnabled && providerCfg != nil && providerCfg.BaseURL != "" {
		toolReg.Register(tool.NewVisionTool(providerCfg))
	}
	return toolReg
}

func (a *App) InitACP(ctx context.Context, cfg ACPConfig) error {
	client, err := newACPClient(cfg)
	if err != nil {
		return err
	}
	a.ACPClient = client
	if streamClient, ok := client.(acpclient.StreamAPI); ok {
		a.ACPStreamClient = streamClient
	}
	if cfg.RegisterTools && a.Commands != nil {
		deps := &command.Deps{
			ACPClient: client,
			NodeName:  cfg.NodeName,
			NodeMeta:  cfg.NodeMeta,
		}
		for _, cmd := range command.BuildGroup("ioa", deps) {
			a.Commands.Register(cmd, "ioa")
		}
	}
	if cfg.AutoRegister && client != nil && client.NodeID() == "" {
		_, err := client.RegisterNode(ctx, cfg.NodeName, cfg.NodeMeta)
		if err != nil {
			return err
		}
	}
	return nil
}

func newACPClient(cfg ACPConfig) (acpclient.API, error) {
	if cfg.URL == "" {
		return nil, nil
	}
	return acpclient.NewClient(cfg.URL, cfg.NodeID)
}

func scanVerificationConfig(cfg ScannerConfig, model string) scan.VerificationConfig {
	timeout := cfg.VerifyTimeout
	if timeout <= 0 {
		timeout = 120
	}
	return scan.VerificationConfig{
		Model:        model,
		Enable:       cfg.VerificationEnabled,
		MinPriority:  cfg.VerifyMinPriority,
		Timeout:      timeout,
		SystemPrompt: cfg.VerifySystemPrompt,
	}
}
